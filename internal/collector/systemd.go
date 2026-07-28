package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Systemd struct {
	Host   string
	Units  []string
	Runner Runner
}

func (c Systemd) Name() string            { return "systemd" }
func (c Systemd) Interval() time.Duration { return 30 * time.Second }
func (c Systemd) Capability(context.Context) Capability {
	runner := c.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if _, err := runner.LookPath("systemctl"); err != nil {
		return Capability{Name: c.Name(), State: "unavailable", Detail: "systemctl is not installed"}
	}
	return Capability{Name: c.Name(), State: "available", Detail: "configured systemd units via read-only systemctl show"}
}

func (c Systemd) Collect(ctx context.Context) (domain.IngestBatch, error) {
	runner := c.Runner
	if runner == nil {
		runner = ExecRunner{MaxOutput: 512 << 10}
	}
	var batch domain.IngestBatch
	var failures []error
	for _, unit := range c.Units {
		unit = strings.TrimSpace(unit)
		if unit == "" || strings.ContainsAny(unit, " \t\r\n/\\;&|`$") {
			failures = append(failures, fmt.Errorf("invalid systemd unit %q", unit))
			continue
		}
		result, err := runner.Run(ctx, "systemctl", "show", unit, "--no-pager",
			"--property=Id,Description,LoadState,ActiveState,SubState,NRestarts")
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", unit, err))
			continue
		}
		appendSystemd(&batch, c.Host, unit, string(result.Stdout))
	}
	return batch, errors.Join(failures...)
}

func appendSystemd(batch *domain.IngestBatch, host, unit, raw string) {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	assetID := "systemd:" + shortHash(host+":"+unit)
	batch.Assets = append(batch.Assets, domain.IngestAsset{
		StableID: assetID, Kind: "service", Name: firstNonEmpty(values["Description"], unit),
		Metadata: map[string]any{
			"unit": unit, "loadState": values["LoadState"],
			"activeState": values["ActiveState"], "subState": values["SubState"],
		},
	})
	now := time.Now().UTC()
	active := 0.0
	if values["ActiveState"] == "active" {
		active = 1
	}
	state := values["ActiveState"] + "/" + values["SubState"]
	stateID := "systemd:" + shortHash(assetID+":state")
	batch.Sensors = append(batch.Sensors, domain.IngestSensor{
		StableID: stateID, AssetStableID: assetID, Source: "systemd",
		SourcePath: unit + "/ActiveState", RawName: "ActiveState",
		Category: "health", Unit: "1", Scale: 1, IntervalSeconds: 30, Quality: "good",
	})
	batch.Samples = append(batch.Samples, domain.IngestSample{
		SensorStableID: stateID, ObservedAt: now, Value: &active,
		TextValue: &state, Source: "systemd", Quality: "good",
	})
	if restarts, err := strconv.ParseFloat(values["NRestarts"], 64); err == nil {
		restartID := "systemd:" + shortHash(assetID+":restarts")
		batch.Sensors = append(batch.Sensors, domain.IngestSensor{
			StableID: restartID, AssetStableID: assetID, Source: "systemd",
			SourcePath: unit + "/NRestarts", RawName: "NRestarts",
			Category: "error_count", Unit: "1", Scale: 1, Monotonic: true,
			IntervalSeconds: 30, Quality: "good",
		})
		batch.Samples = append(batch.Samples, domain.IngestSample{
			SensorStableID: restartID, ObservedAt: now, Value: &restarts,
			Source: "systemd", Quality: "good",
		})
	}
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
