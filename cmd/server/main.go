package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/grpcapi"
	"github.com/monitored-io/monitored/internal/httpapi"
	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		address := os.Getenv("MONITORED_HTTP_ADDR")
		if address == "" {
			address = ":8080"
		}
		_, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			os.Exit(1)
		}
		response, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
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
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), 12)
	if err != nil {
		cancelStartup()
		repository.Close()
		logger.Error("hash bootstrap administrator password", "error", err)
		os.Exit(1)
	}
	if err := repository.EnsureBootstrapUser(startupContext, cfg.AdminEmail, string(passwordHash)); err != nil {
		cancelStartup()
		repository.Close()
		logger.Error("ensure bootstrap administrator", "error", err)
		os.Exit(1)
	}
	cancelStartup()
	defer repository.Close()

	var agentIssuer security.AgentCertificateIssuer
	if cfg.AgentCAKeyFile != "" {
		issuer, issuerErr := security.LoadAgentIssuer(
			cfg.AgentCACertFile,
			cfg.AgentCAKeyFile,
			cfg.AgentCertificateTTL,
		)
		if issuerErr != nil {
			logger.Error("configure Agent certificate issuer", "error", issuerErr)
			os.Exit(1)
		}
		agentIssuer = issuer
		logger.Info("automatic Agent enrollment and certificate rotation enabled", "certificateTTL", cfg.AgentCertificateTTL)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(repository, cfg, logger, agentIssuer),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	var grpcServer interface {
		GracefulStop()
		Stop()
	}
	if cfg.GRPCAddr != "" {
		listener, listenErr := net.Listen("tcp", cfg.GRPCAddr)
		if listenErr != nil {
			logger.Error("listen for Agent gRPC", "error", listenErr)
			os.Exit(1)
		}
		service := grpcapi.New(repository, cfg.GRPCInsecure, agentIssuer)
		rpcServer, createErr := grpcapi.NewServer(service, cfg.GRPCCertFile, cfg.GRPCKeyFile, cfg.GRPCClientCA, cfg.GRPCInsecure)
		if createErr != nil {
			logger.Error("configure Agent gRPC", "error", createErr)
			os.Exit(1)
		}
		grpcServer = rpcServer
		go func() {
			logger.Info("Monitored Agent gRPC listening", "address", cfg.GRPCAddr, "mTLS", !cfg.GRPCInsecure)
			if serveErr := rpcServer.Serve(listener); serveErr != nil {
				logger.Error("serve Agent gRPC", "error", serveErr)
			}
		}()
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
	if grpcServer != nil {
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			grpcServer.Stop()
		}
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("shutdown server", "error", err)
	}
}
