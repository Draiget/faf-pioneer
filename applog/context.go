package applog

import (
	"context"
	"go.uber.org/zap"
)

type logContextFieldKey struct{}

func FromContext(ctx context.Context) *Logger {
	return globalLogger.With(getContextFields(ctx)...)
}

func getContextFields(ctx context.Context) []zap.Field {
	fields, ok := ctx.Value(logContextFieldKey{}).([]zap.Field)
	if !ok {
		return nil
	}
	return fields
}

func mergeContextFields(ctx context.Context, fields ...zap.Field) []zap.Field {
	current := getContextFields(ctx)
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

func AddContextFields(ctx context.Context, fields ...zap.Field) context.Context {
	fm := mergeContextFields(ctx, fields...)
	return context.WithValue(ctx, logContextFieldKey{}, fm)
}
