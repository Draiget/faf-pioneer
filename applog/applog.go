package applog

import (
	"context"
	"faf-pioneer/build"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Logger = zap.Logger

type LocalLogger struct {
	logger *zap.Logger
}

func (l LocalLogger) Info(msg string, fields ...zap.Field) {
	l.logger.WithOptions(zap.AddCallerSkip(1)).Info(msg, fields...)
}

func (l LocalLogger) Debug(msg string, fields ...zap.Field) {
	l.logger.WithOptions(zap.AddCallerSkip(1)).Debug(msg, fields...)
}

func (l LocalLogger) Error(msg string, fields ...zap.Field) {
	l.logger.WithOptions(zap.AddCallerSkip(1)).Error(msg, fields...)
}

func (l LocalLogger) Fatal(msg string, fields ...zap.Field) {
	l.logger.WithOptions(zap.AddCallerSkip(1)).Fatal(msg, fields...)
}

func OnlyLocal() LocalLogger {
	return LocalLogger{logger: def}
}

type RemoteLogger interface {
	WriteLogEntryToRemote(entry *LogEntry) error
}

type LogEntry struct {
	Entry  *zapcore.CheckedEntry
	Fields []zap.Field
}

func Info(msg string, fields ...zapcore.Field) {
	entry := def.WithOptions(zap.AddCallerSkip(1)).Check(zap.InfoLevel, msg)
	logToRemote(entry, fields)
}

func Warn(msg string, fields ...zapcore.Field) {
	entry := def.WithOptions(zap.AddCallerSkip(1)).Check(zap.WarnLevel, msg)
	logToRemote(entry, fields)
}

func Debug(msg string, fields ...zapcore.Field) {
	entry := def.WithOptions(zap.AddCallerSkip(1)).Check(zap.DebugLevel, msg)
	logToRemote(entry, fields)
}

func Error(msg string, fields ...zapcore.Field) {
	entry := def.WithOptions(zap.AddCallerSkip(1)).Check(zap.ErrorLevel, msg)
	logToRemote(entry, fields)
}

func Fatal(msg string, fields ...zapcore.Field) {
	entry := def.WithOptions(zap.AddCallerSkip(1)).Check(zap.FatalLevel, msg)
	logToRemote(entry, fields)
}

func logToRemote(entry *zapcore.CheckedEntry, fields []zapcore.Field) {
	entry.Write(fields...)
	if remoteLogger == nil || entry == nil || atomic.LoadInt32(&acceptingLogs) == 0 {
		return
	}

	remoteEntry := &LogEntry{
		Entry:  entry,
		Fields: fields,
	}

	if err := remoteLogger.WriteLogEntryToRemote(remoteEntry); err != nil {
		def.WithOptions(zap.AddCallerSkip(1)).Debug(
			"failed to write into remote log server",
			zap.Error(err),
		)
	}
}

func LogStartup(launchArgs interface{}) {
	buildInfo := build.GetBuildInfo()
	buildCommit := "unknown"
	if buildInfo != nil {
		buildCommit = buildInfo.CommitHash
	}

	Info("Application started",
		zap.String("buildCommit", buildCommit),
		zap.Any("launchArgs", launchArgs),
	)
}

func GetLogger() *Logger {
	return def
}

func SetRemoteLogger(rl RemoteLogger) {
	remoteLogger = rl
}

func FromContext(ctx context.Context) *Logger {
	return def.With(getFields(ctx)...)
}

func Initialize(userId uint, gameId uint64) {
	workdir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current working directory: %v", err)
	}

	logFilename := filepath.Join(
		workdir,
		"logs",
		fmt.Sprintf("game_%d_user_%d.log",
			gameId,
			userId,
		),
	)

	err = os.MkdirAll(filepath.Dir(logFilename), os.ModePerm)
	if err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	logFile, err = os.OpenFile(logFilename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file '%s': %v", logFilename, err)
	}

	l := newLogger(opts...).With(
		zap.Uint("localUserId", userId),
		zap.Uint64("localGameId", gameId))

	setLogger(l)
	atomic.StoreInt32(&acceptingLogs, 1)
}

func Shutdown() {
	var wg sync.WaitGroup
	wg.Add(2)

	// Let our logger sync entries to disk and flush buffers.
	go func() {
		defer wg.Done()
		_ = def.Sync()
	}()

	// Let our logger write more logs for 0.5 second if we have something pending after
	// main context was canceled.
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		atomic.StoreInt32(&acceptingLogs, 0)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Give sync and accepting log setter 3 seconds to finish everything and then shutdown.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}

	if logFile != nil {
		_ = logFile.Close()
	}
}

type logFieldKey struct{}

func getFields(ctx context.Context) []zap.Field {
	fields, ok := ctx.Value(logFieldKey{}).([]zap.Field)
	if !ok {
		return nil
	}
	return fields
}

func mergeFields(ctx context.Context, fields ...zap.Field) []zap.Field {
	current := getFields(ctx)
	result := make([]zap.Field, 0, len(current)+len(fields))
	seen := make(map[string]struct{}, len(current)+len(fields))
	for _, v := range fields {
		seen[v.Key] = struct{}{}
		result = append(result, v)
	}
	for _, v := range current {
		if _, ok := seen[v.Key]; ok {
			continue
		}
		seen[v.Key] = struct{}{}
		result = append(result, v)
	}
	return result
}

func AddFields(ctx context.Context, fields ...zap.Field) context.Context {
	fm := mergeFields(ctx, fields...)
	return context.WithValue(ctx, logFieldKey{}, fm)
}

var (
	opts = []zap.Option{
		zap.AddCaller(),
	}
	def           = newLogger(opts...)
	logFile       *os.File
	remoteLogger  RemoteLogger
	acceptingLogs int32 = 0
)

func newLogger(opts ...zap.Option) *Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "time"
	encoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.UTC().Format(time.RFC3339)) // Ensure UTC
	}

	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

	consoleSyncer := zapcore.AddSync(os.Stdout)
	fileSyncer := zapcore.AddSync(logFile)
	logLevel := zapcore.DebugLevel

	consoleCore := zapcore.NewCore(jsonEncoder, consoleSyncer, logLevel)
	fileCore := zapcore.NewCore(jsonEncoder, fileSyncer, logLevel)

	combinedCore := zapcore.NewTee(consoleCore, fileCore)
	logger := zap.New(combinedCore, opts...)

	defer func(logger *zap.Logger) {
		_ = logger.Sync()
	}(logger)

	return logger
}

func setLogger(l *Logger) {
	def = l
	zap.ReplaceGlobals(def)
}
