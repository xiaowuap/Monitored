package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/httpapi"
	"github.com/monitored-io/monitored/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.ServerFromEnvironment()

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 45*time.Second)
	repository, err := store.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		cancelStartup()
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	if cfg.AutoMigrate {
		if err := repository.Migrate(startupContext); err != nil {
			cancelStartup()
			repository.Close()
			logger.Error("migrate database", "error", err)
			os.Exit(1)
		}
	}
	cancelStartup()
	defer repository.Close()

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(repository, cfg, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("Monitored server listening", "address", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve HTTP", "error", err)
			os.Exit(1)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("shutdown server", "error", err)
	}
}
