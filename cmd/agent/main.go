package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/action"
	"github.com/monitored-io/monitored/internal/adapter/truenas"
	"github.com/monitored-io/monitored/internal/agent"
	"github.com/monitored-io/monitored/internal/collector"
	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/domain"
)

const version = "0.2.0-dev"

type sender interface {
	Send(context.Context, domain.IngestBatch) error
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.AgentFromEnvironment()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	registry := collectors(cfg)
	var transport sender
	var grpcTransport *agent.GRPCClient
	actionExecutor := action.Default()
	actionExecutor.AllowRoot = cfg.AllowRootActions
	taskHandler := agent.TaskHandler{
		Collectors: registry,
		Actions:    actionExecutor,
		Publish: func(publishContext context.Context, batch domain.IngestBatch) error {
			if grpcTransport == nil {
				return nil
			}
			batch.AgentID = cfg.Name
			return grpcTransport.Send(publishContext, batch)
		},
	}
	if strings.EqualFold(cfg.Protocol, "grpc") {
		grpcTransport = agent.NewGRPCClient(agent.GRPCConfig{
			Target: cfg.GRPCTarget, AgentID: cfg.Name, StableHostID: stableHostID(cfg),
			Hostname: cfg.Name, Version: version, CertFile: cfg.TLSCertFile,
			KeyFile: cfg.TLSKeyFile, CAFile: cfg.TLSCAFile, ServerName: cfg.TLSServerName,
			Insecure: cfg.GRPCInsecure, Collectors: registry.Names(), Tools: installedTools(),
			Metadata:    map[string]string{"wal": "bounded-24h-1GiB", "transport": "grpc"},
			TaskHandler: taskHandler.Handle,
		})
		if err := grpcTransport.Connect(ctx); err != nil {
			logger.Warn("initial gRPC connection unavailable; WAL will buffer telemetry", "error", err)
		}
		defer grpcTransport.Close()
		transport = grpcTransport
	} else {
		transport = agent.NewClient(cfg.ServerURL, cfg.Token)
		logger.Warn("REST Agent transport enabled; use gRPC with mTLS for remote production agents")
	}
	wal := agent.NewWAL(cfg.WALPath)
	lastRun := make(map[string]time.Time)

	send := func(batch domain.IngestBatch) error {
		if grpcTransport != nil {
			// The stream is tied to the process context so it remains a true
			// outbound long-lived connection.
			if err := grpcTransport.Connect(ctx); err != nil {
				return err
			}
			return grpcTransport.Send(ctx, batch)
		}
		sendContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return transport.Send(sendContext, batch)
	}
	collectAndSend := func(force bool) {
		if replayed, err := wal.Replay(send); err != nil {
			logger.Warn("WAL replay pending", "replayed", replayed, "error", err)
		} else if replayed > 0 {
			logger.Info("WAL replay completed", "batches", replayed)
		}
		now := time.Now()
		var batch domain.IngestBatch
		for _, item := range registry.All() {
			if !force && now.Sub(lastRun[item.Name()]) < item.Interval() {
				continue
			}
			collectContext, cancel := context.WithTimeout(ctx, min(item.Interval(), 5*time.Minute))
			value, err := item.Collect(collectContext)
			cancel()
			lastRun[item.Name()] = now
			if err != nil {
				logger.Warn("collector failed", "collector", item.Name(), "error", err)
				continue
			}
			batch = collector.Merge(batch, value)
		}
		if batch.AgentID == "" {
			batch.AgentID = cfg.Name
		}
		if len(batch.Assets)+len(batch.Sensors)+len(batch.Samples) > 0 {
			if err := send(batch); err != nil {
				if walErr := wal.Append(batch); walErr != nil {
					logger.Error("persist telemetry to WAL", "sendError", err, "walError", walErr)
					return
				}
				bytes, oldest, _ := wal.Stats()
				logger.Warn("Master unavailable; telemetry queued", "error", err, "walBytes", bytes, "oldest", oldest)
			} else {
				logger.Info("telemetry sent", "assets", len(batch.Assets), "sensors", len(batch.Sensors), "samples", len(batch.Samples))
			}
		}
		if grpcTransport != nil {
			bytes, oldest, _ := wal.Stats()
			_ = grpcTransport.Heartbeat(bytes, oldest)
			if gap, err := wal.Gap(); err == nil && !gap.From.IsZero() {
				if err := grpcTransport.ReportGap(gap.From, gap.To, gap.Reason); err == nil {
					_ = wal.ClearGap()
				}
			}
		}
	}

	collectAndSend(true)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectAndSend(false)
		}
	}
}

func collectors(cfg config.Agent) *collector.Registry {
	values := []collector.Scheduled{
		collector.SMART{
			SysRoot: cfg.SysRoot, DeviceRoot: cfg.DeviceRoot, Host: cfg.Name,
			PollInterval: cfg.SMARTInterval,
		},
		collector.ZFS{Host: cfg.Name, SysRoot: cfg.SysRoot},
		collector.NVML{Host: cfg.Name, Fallback: collector.NVIDIA{Host: cfg.Name}},
		collector.Mellanox{Host: cfg.Name},
		collector.IPMI{Host: cfg.Name},
	}
	if runtime.GOOS == "freebsd" {
		values = append(values, collector.FreeBSD{Host: cfg.Name})
	} else {
		values = append(values,
			collector.HWMonScheduled{HWMon: collector.HWMon{SysRoot: cfg.SysRoot, Host: cfg.Name}},
			collector.LinuxInventory{SysRoot: cfg.SysRoot, Host: cfg.Name},
		)
	}
	if strings.TrimSpace(cfg.TrueNASEndpoint) != "" {
		values = append(values, trueNASAPIScheduled{client: &truenas.Client{
			Endpoint: cfg.TrueNASEndpoint, Token: cfg.TrueNASToken,
			AgentID: cfg.Name, HostIdentity: stableHostID(cfg),
		}})
	}
	if len(cfg.SystemdUnits) > 0 {
		values = append(values, collector.Systemd{Host: cfg.Name, Units: cfg.SystemdUnits})
	}
	return collector.NewRegistry(values...)
}

type trueNASAPIScheduled struct {
	client *truenas.Client
}

func (c trueNASAPIScheduled) Name() string            { return "truenas-scale-api" }
func (c trueNASAPIScheduled) Interval() time.Duration { return 5 * time.Minute }
func (c trueNASAPIScheduled) Capability(context.Context) collector.Capability {
	return collector.Capability{
		Name: c.Name(), State: "available",
		Detail: "TrueNAS SCALE system, pool, disk and cached disk-temperature JSON-RPC",
	}
}
func (c trueNASAPIScheduled) Collect(ctx context.Context) (domain.IngestBatch, error) {
	return c.client.Collect(ctx)
}

func stableHostID(cfg config.Agent) string {
	for _, path := range []string{cfg.SysRoot + "/etc/machine-id", cfg.SysRoot + "/host/etc/machine-id", "/etc/machine-id"} {
		if value, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(value)) != "" {
			return strings.TrimSpace(string(value))
		}
	}
	return cfg.Name
}

func installedTools() []string {
	var result []string
	for _, name := range []string{"smartctl", "zpool", "nvidia-smi", "dcgmi", "mget_temp", "mlxlink", "ipmitool", "ethtool", "devlink", "systemctl"} {
		if _, err := exec.LookPath(name); err == nil {
			result = append(result, name)
		}
	}
	return result
}
