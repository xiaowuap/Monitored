package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.WorkerFromEnvironment()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	repository, err := store.Open(connectContext, cfg.DatabaseURL)
	cancel()
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer repository.Close()

	run := func() {
		maintenanceContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := repository.RunMaintenance(maintenanceContext); err != nil {
			logger.Error("metric maintenance", "error", err)
			return
		}
		logger.Info("metric maintenance completed")
	}

	run()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
