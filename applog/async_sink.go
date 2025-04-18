package applog

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sync"
	"time"
)

type asyncSink struct {
	core        zapcore.Core
	entryChan   chan *LogEntry
	quit        chan struct{}
	wg          *sync.WaitGroup
	extraFields []zap.Field
	once        sync.Once
}

func newAsyncSink(core zapcore.Core, bufferSize int) *asyncSink {
	wg := &sync.WaitGroup{}
	s := &asyncSink{
		core:      core,
		entryChan: make(chan *LogEntry, bufferSize),
		quit:      make(chan struct{}),
		wg:        wg,
	}

	s.wg.Add(1)
	go s.process()
	return s
}

func (s *asyncSink) process() {
	defer s.wg.Done()
	for {
		select {
		case entry := <-s.entryChan:
			if entry.Entry == nil {
				continue
			}

			_ = s.core.Write(*entry.Entry, entry.Fields)
		case <-s.quit:
			for {
				select {
				case entry := <-s.entryChan:
					if entry.Entry == nil {
						continue
					}

					_ = s.core.Write(*entry.Entry, entry.Fields)
				default:
					return
				}
			}
		}
	}
}

func (s *asyncSink) Sync() error {
	return s.core.Sync()
}

func (s *asyncSink) Write(entry zapcore.Entry, fields []zap.Field) error {
	newFields := make([]zap.Field, 0, len(s.extraFields)+len(fields))
	newFields = append(newFields, s.extraFields...)
	newFields = append(newFields, fields...)
	logEntry := &LogEntry{
		Entry:  &entry,
		Fields: newFields,
	}

	select {
	case s.entryChan <- logEntry:
	default:
		return fmt.Errorf("channel log buffer overflow (capacity: %d)", cap(s.entryChan))
	}
	return nil
}

func (s *asyncSink) Enabled(lvl zapcore.Level) bool {
	return s.core.Enabled(lvl)
}

func (s *asyncSink) With(fields []zap.Field) zapcore.Core {
	newFields := make([]zap.Field, 0, len(s.extraFields)+len(fields))
	newFields = append(newFields, s.extraFields...)
	newFields = append(newFields, fields...)
	return &asyncSink{
		core:        s.core.With(fields),
		entryChan:   s.entryChan,
		quit:        s.quit,
		wg:          s.wg,
		extraFields: newFields,
	}
}

func (s *asyncSink) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if s.Enabled(entry.Level) {
		return ce.AddCore(entry, s)
	}
	return ce
}

func (s *asyncSink) Shutdown(timeout time.Duration) {
	s.once.Do(func() {
		close(s.quit)
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
