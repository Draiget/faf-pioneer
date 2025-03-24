package applog

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRemoteLogSender struct {
	mu      sync.Mutex
	entries [][]*LogEntry
}

func (f *fakeRemoteLogSender) WriteLogEntryToRemote(entries []*LogEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, entries)
	return nil
}

func (f *fakeRemoteLogSender) getEntries() [][]*LogEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries
}

type fakeEncoder struct {
	zapcore.Encoder
	fixedSize int
}

func (fe *fakeEncoder) EncodeEntry(_ zapcore.Entry, _ []zap.Field) (*buffer.Buffer, error) {
	b := buffer.NewPool().Get()
	_, _ = b.WriteString(strings.Repeat("x", fe.fixedSize))
	return b, nil
}

func TestRemoteSinkWriteAndFlush(t *testing.T) {
	bufferSize := 100
	fakeSender := &fakeRemoteLogSender{}

	cfg := getEncoderConfig()
	rs := newRemoteSink(fakeSender, bufferSize, cfg)
	const fixedEntrySize = 1024
	rs.encoder = &fakeEncoder{
		Encoder:   zapcore.NewJSONEncoder(cfg),
		fixedSize: fixedEntrySize,
	}

	// Let's write that amount of entries that it will cause a flush.
	flushCount := remoteSinkBatchSizeLimit/fixedEntrySize + 1
	for i := 0; i < flushCount; i++ {
		entry := zapcore.Entry{
			Level:   zapcore.InfoLevel,
			Message: "test message",
			Time:    time.Now(),
		}
		fieldValue := "value_" + strconv.Itoa(i)
		err := rs.Write(&LogEntry{
			Entry:  &entry,
			Fields: []zap.Field{zap.String("index", fieldValue)},
		})
		if err != nil {
			t.Fatalf("failed to write log entry %d: %v", i, err)
		}

		// Give a time for fakeRemoteLogSender to handle `process` goroutine.
		time.Sleep(10 * time.Millisecond)
	}

	// Shutdown remoteSink, which also should call flush.
	rs.Shutdown(2 * time.Second)

	entriesBatches := fakeSender.getEntries()
	if len(entriesBatches) == 0 {
		t.Error("expected at least one batch of log entries, got 0")
	}

	totalEntries := 0
	for batchIdx, batch := range entriesBatches {
		totalEntries += len(batch)
		for entryIdx, logEntry := range batch {
			// Verify we have `index` in fields with correct values.
			found := false
			for _, field := range logEntry.Fields {
				if field.Key == "index" {
					// Make sure it's `value_<x>`.
					if strings.HasPrefix(field.String, "value_") {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("batch %d, entry %d: expected field 'index' not found", batchIdx, entryIdx)
			}
		}
	}

	if totalEntries != flushCount {
		t.Errorf("expected total of %d log entries sent, got %d", flushCount, totalEntries)
	}
}

func TestRemoteSinkBufferOverflow(t *testing.T) {
	bufferSize := 1
	fakeSender := &fakeRemoteLogSender{}
	cfg := zap.NewProductionEncoderConfig()
	rs := newRemoteSink(fakeSender, bufferSize, cfg)

	// Write first entry which should fit to bufferSize with size 1.
	entry := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "first",
		Time:    time.Now(),
	}
	err := rs.Write(&LogEntry{
		Entry:  &entry,
		Fields: []zap.Field{zap.String("key", "value")},
	})
	if err != nil {
		t.Fatalf("failed to write first log entry: %v", err)
	}

	// Write second entry and since we set bufferSize to 1, it should return overflow error.
	err = rs.Write(&LogEntry{
		Entry:  &entry,
		Fields: []zap.Field{zap.String("key", "value2")},
	})
	assert.EqualError(t, err, fmt.Sprintf("remote log buffer overflow (capacity: %d)", bufferSize))

	rs.Shutdown(1 * time.Second)
}

func TestRemoteSinkShutdown(t *testing.T) {
	fakeSender := &fakeRemoteLogSender{}
	cfg := zap.NewProductionEncoderConfig()
	rs := newRemoteSink(fakeSender, 10, cfg)

	entry := zapcore.Entry{
		Level:   zapcore.DebugLevel,
		Message: "shutdown test",
		Time:    time.Now(),
	}
	err := rs.Write(&LogEntry{
		Entry:  &entry,
		Fields: []zap.Field{zap.String("test", "shutdown")},
	})
	assert.NoError(t, err)

	// Test for deadlock finish.
	done := make(chan struct{})
	go func() {
		rs.Shutdown(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2500 * time.Millisecond):
		t.Error("Shutdown did not complete in time - possible deadlock")
	}
}

type blockingWriter struct{}

func (b *blockingWriter) Write(_ []byte) (n int, err error) {
	select {}
}

func (b *blockingWriter) Sync() error {
	return nil
}

func TestStdoutNonBlocking(t *testing.T) {
	// Let's create a writer (just like STDOUT) that will block writing into it,
	// like a situation with a launcher blocking STDOUT for some reason that we saw.
	bw := &blockingWriter{}

	encoderCfg := getEncoderConfig()
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(bw), zap.DebugLevel)
	// Create `asyncSink` with very small buffer to avoid any send latency/lag.
	async := newAsyncSink(core, 1)

	// Track time of the Write method.
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		err := async.Write(
			zapcore.Entry{
				Level:   zapcore.InfoLevel,
				Message: "test non blocking",
				Time:    time.Now(),
			},
			[]zap.Field{zap.String("key", "value")},
		)
		if err != nil {
			t.Errorf("Write returned error: %v", err)
		}

		done <- time.Since(start)
	}()

	select {
	case duration := <-done:
		if duration > 50*time.Millisecond {
			t.Errorf(
				"Write took too long: %v; "+
					"asyncSink should return control when output stream "+
					"(like STDOUT) is unavailable for writing",
				duration)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Write appears to be blocked (deadlock) and didn't completed within 100 ms")
	}

	async.Shutdown(1 * time.Second)
}

func TestAppLogInfoNonBlockingWithTimeout(t *testing.T) {
	oldLogger := globalLogger
	defer setLogger(oldLogger)

	// Let's create a writer (just like STDOUT) that will block writing into it,
	// like a situation with a launcher blocking STDOUT for some reason that we saw.
	bw := &blockingWriter{}

	encoderCfg := zap.NewProductionEncoderConfig()
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(bw), zap.DebugLevel)
	// Create `asyncSink` with very small buffer to avoid any send latency/lag.
	async := newAsyncSink(core, 1)
	newLogger := zap.New(async, zap.AddCaller())
	// Override global logger for app.Info call.
	setLogger(newLogger)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		Info("non blocking test info", zap.String("key", "value"))
		done <- time.Since(start)
	}()

	select {
	case duration := <-done:
		if duration > 50*time.Millisecond {
			t.Errorf(
				"applog.Info took too long: %v; "+
					"asyncSink should return control when output stream "+
					"(like STDOUT) is unavailable for writing",
				duration)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("applog.Info appears to be blocked (deadlock) and didn't completed within 100 ms")
	}
}

type capturingCore struct {
	mu      sync.Mutex
	entries []string
}

func (c *capturingCore) Enabled(lvl zapcore.Level) bool       { return true }
func (c *capturingCore) With(fields []zap.Field) zapcore.Core { return c }
func (c *capturingCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}
func (c *capturingCore) Write(ent zapcore.Entry, fields []zap.Field) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, ent.Message)
	return nil
}
func (c *capturingCore) Sync() error { return nil }

// testRemoteSenderRecursion is a special case of RemoteLogSender, that calls `NoRemote().Info("simulated error")`
// and then saves received entries into storedEntries for future validation.
type testRemoteSenderRecursion struct {
	mu            sync.Mutex
	storedEntries []*LogEntry
}

func (ts *testRemoteSenderRecursion) WriteLogEntryToRemote(entries []*LogEntry) error {
	// Store received entries which should be sent to remote.
	ts.mu.Lock()
	ts.storedEntries = append(ts.storedEntries, entries...)
	ts.mu.Unlock()

	// Now call valid NoRemote() logger Info call to avoid recursion (by adding log entry,
	// that will call adding it again and so on).
	NoRemote().Info("simulated error")
	return nil
}

func (ts *testRemoteSenderRecursion) getStoredEntries() []*LogEntry {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.storedEntries
}

func TestNoRecursiveLogging_Fallback(t *testing.T) {
	// Create two different capture cores:
	// globalCapture - is for regular logger (like STDOUT for example, it should receive "test message").
	// remoteCapture - is for NoRemote() logging, where we should see "simulated error".
	globalCapture := &capturingCore{}
	remoteCapture := &capturingCore{}

	globalLogger = zap.New(globalCapture, zap.AddCaller())
	oldLogger := globalLogger
	defer setLogger(oldLogger)
	setLogger(globalLogger)
	noRemoteLogger = zap.New(remoteCapture, zap.AddCaller())

	ts := &testRemoteSenderRecursion{}
	cfg := zap.NewProductionEncoderConfig()
	rs := newRemoteSink(ts, 10, cfg)
	const fixedEntrySize = 512
	rs.encoder = &fakeEncoder{
		Encoder:   zapcore.NewJSONEncoder(cfg),
		fixedSize: fixedEntrySize,
	}
	remoteSinkInstance = rs

	// Here we're calling `applog.Info` where:
	// - It writes a message to "test message" to globalLogger (with Info level);
	// - logToRemoteSink() call inside Info function will call rs.Write, that within flush() should call a
	//   ts.WriteLogEntryToRemote, that finally will call our `NoRemote().Info("simulated error")`.
	done := make(chan struct{})
	go func() {
		Info("test message", zap.String("foo", "bar"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("applog.Info appears to be blocked (deadlock) and didn't completed within 100 ms")
	}

	// Give some time for remoteSink to finish `flush()` call.
	time.Sleep(20 * time.Millisecond)
	// Shutting down remoteSink.
	rs.Shutdown(100 * time.Millisecond)

	// Verify that testRemoteSenderRecursion (our remoteSink) received exactly one entry with text - "test message".
	stored := ts.getStoredEntries()
	if len(stored) != 1 {
		t.Errorf("Excepted 1 entry in remoteSink, got %d", len(stored))
		return
	}

	// Make sure that entry text and other fields are exactly as we expected.
	assert.Equal(t, stored[0].Entry.Message, "test message")
	assert.Equal(t, stored[0].Entry.Level, zapcore.InfoLevel)

	// Verify that NoRemote() which is our primary logger (like STDOUT)
	// wrote exactly one entry with text - "simulated error".
	remoteCapture.mu.Lock()
	defer remoteCapture.mu.Unlock()
	count := 0
	for _, msg := range remoteCapture.entries {
		if strings.Contains(msg, "simulated error") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Excepted 1 entry in noRemoteLogger with text 'simulated error', got %d instead", count)
		return
	}

	// Double check that globalCapture (our primary logger, like STDOUT) does not have "simulated error" message,
	// to make sure NoRemote() call is working correctly.
	globalCapture.mu.Lock()
	defer globalCapture.mu.Unlock()
	for _, msg := range globalCapture.entries {
		if strings.Contains(msg, "simulated error") {
			t.Error("Entry with message 'simulated error' should not appear in globalLogger")
		}
	}
}
