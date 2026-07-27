package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/agent"
	"github.com/monitored-io/monitored/internal/collector"
	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/domain"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.AgentFromEnvironment()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	hwmon := collector.HWMon{SysRoot: cfg.SysRoot, Host: cfg.Name}
	client := agent.NewClient(cfg.ServerURL, cfg.Token)
	wal := agent.NewWAL(cfg.WALPath)

	send := func(batch domain.IngestBatch) error {
		sendContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return client.Send(sendContext, batch)
	}
	collectAndSend := func() {
		if replayed, err := wal.Replay(send); err != nil {
			logger.Warn("WAL replay pending", "replayed", replayed, "error", err)
		} else if replayed > 0 {
			logger.Info("WAL replay completed", "batches", replayed)
		}
		batch, err := hwmon.Collect()
		if err != nil {
			logger.Error("collect hwmon telemetry", "error", err)
			return
		}
		if err := send(batch); err != nil {
			if walErr := wal.Append(batch); walErr != nil {
				logger.Error("persist telemetry to WAL", "sendError", err, "walError", walErr)
				return
			}
			bytes, oldest, _ := wal.Stats()
			logger.Warn("server unavailable; telemetry queued", "error", err, "walBytes", bytes, "oldest", oldest)
			return
		}
		logger.Info("telemetry sent", "assets", len(batch.Assets), "sensors", len(batch.Sensors), "samples", len(batch.Samples))
	}

	collectAndSend()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectAndSend()
		}
	}
}
