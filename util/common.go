package util

import (
	"context"
	"faf-pioneer/applog"
	"fmt"
	"go.uber.org/zap"
)

func PtrValueOrDef[T any](value *T, def T) T {
	if value == nil {
		return def
	}
	return *value
}

func WrapAppContextCancelExitMessage(ctx context.Context, appName string) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		applog.Info(fmt.Sprintf("%s exited; context cancelled", appName), zap.Error(ctxErr))
		return
	}

	applog.Info(fmt.Sprintf("%s exited", appName))
}
