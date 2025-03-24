package applog

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sync"
	"testing"
	"time"
)

type testCore struct {
	mu      sync.Mutex
	entries []string
	fields  [][]zap.Field
}

func (tc *testCore) Enabled(_ zapcore.Level) bool    { return true }
func (tc *testCore) With(_ []zap.Field) zapcore.Core { return tc }
func (tc *testCore) Sync() error                     { return nil }

func (tc *testCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if tc.Enabled(ent.Level) {
		return ce.AddCore(ent, tc)
	}
	return ce
}

func (tc *testCore) Write(ent zapcore.Entry, fields []zap.Field) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.entries = append(tc.entries, ent.Message)
	tc.fields = append(tc.fields, fields)
	return nil
}

// slowCore slows down writing (delays Write method) so we can simulate buffer overflow.
type slowCore struct {
	testCore
	delay time.Duration
}

func (sc *slowCore) Write(ent zapcore.Entry, fields []zap.Field) error {
	time.Sleep(sc.delay)
	return sc.testCore.Write(ent, fields)
}

func TestAsyncSinkWrite(t *testing.T) {
	tc := &testCore{}
	sink := newAsyncSink(tc, 10)
	defer sink.Shutdown(100 * time.Millisecond)

	entry := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "hello async",
		Time:    time.Now(),
	}

	err := sink.Write(entry, []zap.Field{zap.String("k", "v")})
	assert.NoError(t, err)

	// Give time for `process` to execute and finish.
	time.Sleep(20 * time.Millisecond)

	tc.mu.Lock()
	defer tc.mu.Unlock()
	assert.Equal(t, 1, len(tc.entries))
	assert.Equal(t, "hello async", tc.entries[0])
	assert.Equal(t, 1, len(tc.fields[0]))
	assert.Equal(t, "k", tc.fields[0][0].Key)
}

func TestAsyncSinkBufferOverflow(t *testing.T) {
	bufferSize := 1
	sc := &slowCore{delay: 100 * time.Millisecond}
	sink := newAsyncSink(sc, bufferSize)
	defer sink.Shutdown(200 * time.Millisecond)

	entry := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "first",
		Time:    time.Now(),
	}

	err := sink.Write(entry, nil)
	assert.NoError(t, err)

	// Second write should return cause overflow error since our buffer
	// had size of 1 element and no more entries could be stored at the same time.
	entry.Message = "second"
	err = sink.Write(entry, nil)
	assert.NotNil(t, err)
	assert.EqualError(t, err, fmt.Sprintf("channel log buffer overflow (capacity: %d)", bufferSize))
}

func TestAsyncSinkEnabled(t *testing.T) {
	tc := &testCore{}
	sink := newAsyncSink(tc, 10)
	defer sink.Shutdown(100 * time.Millisecond)
	assert.True(t, sink.Enabled(zapcore.DebugLevel))
}

func TestAsyncSinkWith(t *testing.T) {
	tc := &testCore{}
	sink := newAsyncSink(tc, 10)
	defer sink.Shutdown(100 * time.Millisecond)

	// Add extra field to `asyncSink` using With.
	newCore := sink.With([]zap.Field{zap.String("a", "1")})
	// Cast result of With to *asyncSink as it should return it in theory.
	typed, ok := newCore.(*asyncSink)
	if !ok {
		t.Fatal("With should return the same asyncSink type but it's not giving us *asyncSink type")
	}
	assert.Equal(t, 1, len(typed.extraFields),
		fmt.Sprintf("expected 1 extraField, got %d", len(typed.extraFields)))

	// Now write another entry using our result from With.
	entry := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "with test",
		Time:    time.Now(),
	}
	err := typed.Write(entry, []zap.Field{zap.String("b", "2")})
	assert.NoError(t, err)

	// Give time for `process` to execute and finish.
	time.Sleep(20 * time.Millisecond)

	tc.mu.Lock()
	defer tc.mu.Unlock()
	assert.Equal(t, 1, len(tc.entries))

	// Make sure fields contains `a` and `b` keys with correct values.
	foundA, foundB := false, false
	for _, field := range tc.fields[0] {
		if field.Key == "a" && field.String == "1" {
			foundA = true
		}
		if field.Key == "b" && field.String == "2" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("Excepted fields 'a:1' and 'b:2', got %+v", tc.fields[0])
	}
}

type levelCore struct {
	minLevel zapcore.Level
}

func (lc *levelCore) Enabled(lvl zapcore.Level) bool {
	return lvl >= lc.minLevel
}

func (lc *levelCore) With(_ []zap.Field) zapcore.Core            { return lc }
func (lc *levelCore) Write(_ zapcore.Entry, _ []zap.Field) error { return nil }
func (lc *levelCore) Sync() error                                { return nil }

func (lc *levelCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if lc.Enabled(ent.Level) {
		return ce.AddCore(ent, lc)
	}
	return nil
}

func TestAsyncSinkCheck(t *testing.T) {
	lc := &levelCore{minLevel: zapcore.InfoLevel}
	sink := newAsyncSink(lc, 10)
	// Give time for `process` to execute and finish.
	time.Sleep(10 * time.Millisecond)
	defer sink.Shutdown(100 * time.Millisecond)

	// Test "Info" level entry, it should give a new core added in `sink.Check` call when level is enabled.
	ceEnabled := new(zapcore.CheckedEntry)
	retEnabled := sink.Check(zapcore.Entry{Level: zapcore.InfoLevel, Message: "enabled test"}, ceEnabled)
	assert.NotNil(t, retEnabled,
		"for entry of level Info expected non nil CheckedEntry in result")

	// Test "Debug" level entry, since we have set only Info level in `levelCore`, now `sink.Check` call
	// should return only original entry since AddCore won't be called.
	ceDisabled := new(zapcore.CheckedEntry)
	retDisabled := sink.Check(zapcore.Entry{Level: zapcore.DebugLevel, Message: "disabled test"}, ceDisabled)
	assert.Equal(t, ceDisabled, retDisabled,
		"for entry of level Debug, Check should return source CheckedEntry in result")
}

func TestAsyncSinkSync(t *testing.T) {
	tc := &testCore{}
	sink := newAsyncSink(tc, 10)
	defer sink.Shutdown(100 * time.Millisecond)
	err := sink.Sync()
	assert.NoError(t, err)
}

func TestAsyncSinkShutdown(t *testing.T) {
	tc := &testCore{}
	sink := newAsyncSink(tc, 10)

	for i := 0; i < 5; i++ {
		entry := zapcore.Entry{
			Level:   zapcore.InfoLevel,
			Message: fmt.Sprintf("msg %d", i),
			Time:    time.Now(),
		}
		err := sink.Write(entry, nil)
		assert.NoError(t, err)
	}

	// Test for deadlock finish.
	done := make(chan struct{})
	go func() {
		sink.Shutdown(200 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Error("Shutdown did not complete in time - possible deadlock")
	}
}
