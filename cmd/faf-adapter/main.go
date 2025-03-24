package main

import (
	"context"
	"faf-pioneer/adapter"
	"faf-pioneer/applog"
	"faf-pioneer/launcher"
	"faf-pioneer/util"
	"go.uber.org/zap"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	defer cancel()

	info := launcher.NewInfoFromFlags()
	applog.Initialize(info.UserId, info.GameId, info.LogLevel)
	defer applog.Shutdown()
	defer util.WrapAppContextCancelExitMessage(ctx, "Adapter")

	if err := info.Validate(); err != nil {
		applog.Fatal("Failed to validate command line arguments", zap.Error(err))
		return
	}

	applog.LogStartup(info)

	adapterInstance := adapter.New(ctx, info)
	if err := adapterInstance.Start(); err != nil {
		applog.Fatal("Failed to start adapter", zap.Error(err))
	}
}
