package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	monitoredv1 "github.com/monitored-io/monitored/gen/monitored/v1"
	"github.com/monitored-io/monitored/internal/action"
	"github.com/monitored-io/monitored/internal/collector"
	"github.com/monitored-io/monitored/internal/discovery"
	"github.com/monitored-io/monitored/internal/domain"
)

type TaskHandler struct {
	Collectors *collector.Registry
	Actions    *action.Executor
	Runner     collector.Runner
	Publish    func(context.Context, domain.IngestBatch) error
}

func (h TaskHandler) Handle(ctx context.Context, envelope *monitoredv1.MasterEnvelope) *monitoredv1.TaskResult {
	switch {
	case envelope.GetDiscovery() != nil:
		task := envelope.GetDiscovery()
		if strings.TrimSpace(task.Cidr) != "" {
			return h.scan(ctx, task)
		}
		batch, errs := h.Collectors.Collect(ctx, task.Collectors)
		status, errorText := "succeeded", joinErrors(errs)
		if len(errs) > 0 && len(batch.Assets) == 0 && len(batch.Sensors) == 0 {
			status = "failed"
		}
		if status == "succeeded" && h.Publish != nil {
			if err := h.Publish(ctx, batch); err != nil {
				errs = append(errs, err)
				status = "failed"
				errorText = joinErrors(errs)
			}
		}
		structured, _ := json.Marshal(map[string]any{
			"assets": len(batch.Assets), "sensors": len(batch.Sensors),
			"samples": len(batch.Samples), "unknown": unknownSensors(batch.Sensors),
			"errors": errorText,
		})
		return &monitoredv1.TaskResult{TaskId: task.TaskId, Status: status, StructuredJson: string(structured), Error: errorText}
	case envelope.GetDiagnostic() != nil:
		return h.diagnose(ctx, envelope.GetDiagnostic())
	case envelope.GetAction() != nil:
		return h.execute(ctx, envelope.GetAction())
	default:
		return nil
	}
}

func (h TaskHandler) scan(ctx context.Context, task *monitoredv1.DiscoveryTask) *monitoredv1.TaskResult {
	request := discovery.Request{
		CIDR: task.Cidr, ConfirmLarge: task.ConfirmLarge,
		RatePerSecond: int(task.RatePerSecond), Concurrency: int(task.Concurrency),
		Ports: make(map[discovery.Protocol][]uint16),
	}
	for _, value := range task.ScanProtocols {
		request.Protocols = append(request.Protocols, discovery.Protocol(value))
	}
	for _, mapping := range task.ScanPorts {
		protocol := discovery.Protocol(mapping.Protocol)
		for _, port := range mapping.Ports {
			if port == 0 || port > 65535 {
				return &monitoredv1.TaskResult{
					TaskId: task.TaskId, Status: "failed",
					Error: "scan port must be between 1 and 65535",
				}
			}
			request.Ports[protocol] = append(request.Ports[protocol], uint16(port))
		}
	}
	observations, err := discovery.Scan(ctx, request)
	status := "succeeded"
	if err != nil {
		status = "failed"
	}
	structured, _ := json.Marshal(map[string]any{
		"assets": 0, "sensors": 0, "unknown": 0,
		"observations": observations,
	})
	result := &monitoredv1.TaskResult{
		TaskId: task.TaskId, Status: status, StructuredJson: string(structured),
	}
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func unknownSensors(sensors []domain.IngestSensor) int {
	count := 0
	for _, sensor := range sensors {
		if sensor.Category == "" || sensor.Category == "unknown" {
			count++
		}
	}
	return count
}

func (h TaskHandler) diagnose(ctx context.Context, task *monitoredv1.DiagnosticTask) *monitoredv1.TaskResult {
	runner := h.Runner
	if runner == nil {
		runner = collector.ExecRunner{MaxOutput: 256 << 10}
	}
	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = time.Minute
	}
	var stdout, stderr []string
	for _, raw := range task.Commands {
		argv, err := action.SplitCommand(raw)
		if err != nil || !readOnlyDiagnostic(argv) {
			return &monitoredv1.TaskResult{
				TaskId: task.TaskId, Status: "failed",
				Error: "diagnostic command is not in the read-only allowlist",
			}
		}
		commandContext, cancel := context.WithTimeout(ctx, timeout)
		result, runErr := runner.Run(commandContext, argv[0], argv[1:]...)
		cancel()
		stdout = append(stdout, string(result.Stdout))
		stderr = append(stderr, string(result.Stderr))
		if runErr != nil {
			return &monitoredv1.TaskResult{
				TaskId: task.TaskId, Status: "failed", StdoutSummary: strings.Join(stdout, "\n"),
				StderrSummary: strings.Join(stderr, "\n"), Error: runErr.Error(),
			}
		}
	}
	return &monitoredv1.TaskResult{
		TaskId: task.TaskId, Status: "succeeded",
		StdoutSummary: strings.Join(stdout, "\n"), StderrSummary: strings.Join(stderr, "\n"),
	}
}

func (h TaskHandler) execute(ctx context.Context, task *monitoredv1.ApprovedAction) *monitoredv1.TaskResult {
	if h.Actions == nil {
		return &monitoredv1.TaskResult{TaskId: task.ActionId, Status: "failed", Error: "action executor is disabled"}
	}
	if strings.TrimSpace(task.OneTimeToken) == "" {
		return &monitoredv1.TaskResult{TaskId: task.ActionId, Status: "failed", Error: "one-time execution token is missing"}
	}
	request := domain.ActionRequest{
		ID: task.ActionId, AssetID: task.AssetId, Command: task.Command,
		WorkingDirectory: task.WorkingDirectory, Identity: task.Identity,
		TimeoutSeconds: int(task.TimeoutSeconds), Risk: task.Risk, ImmutableHash: task.ImmutableHash,
		Environment: task.Environment,
	}
	result, err := h.Actions.Execute(ctx, request)
	status := "succeeded"
	if err != nil {
		status = "failed"
	}
	payload, _ := json.Marshal(map[string]any{
		"exitCode": result.ExitCode, "startedAt": result.StartedAt, "finishedAt": result.FinishedAt,
	})
	response := &monitoredv1.TaskResult{
		TaskId: task.ActionId, Status: status, StructuredJson: string(payload),
		StdoutSummary: result.Stdout, StderrSummary: result.Stderr,
	}
	if err != nil {
		response.Error = err.Error()
	}
	return response
}

func readOnlyDiagnostic(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch argv[0] {
	case "smartctl":
		return len(argv) >= 2 && (argv[1] == "-a" || argv[1] == "-x" || argv[1] == "--all")
	case "zpool":
		return len(argv) >= 2 && (argv[1] == "status" || argv[1] == "list")
	case "nvidia-smi":
		return true
	case "ipmitool":
		return len(argv) >= 3 && ((argv[1] == "sensor" && argv[2] == "list") || (argv[1] == "sdr" && argv[2] == "list"))
	case "ethtool":
		return len(argv) >= 2 && argv[1] != "-s"
	case "sysctl":
		return len(argv) >= 2 && argv[1] != "-w"
	default:
		return false
	}
}

func joinErrors(values []error) string {
	if len(values) == 0 {
		return ""
	}
	return errors.Join(values...).Error()
}
