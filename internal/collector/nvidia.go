package collector

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type NVIDIA struct {
	Host   string
	Runner Runner
}

func (c NVIDIA) Name() string            { return "nvidia" }
func (c NVIDIA) Interval() time.Duration { return 10 * time.Second }
func (c NVIDIA) Capability(context.Context) Capability {
	if _, err := c.runner().LookPath("nvidia-smi"); err != nil {
		return Capability{Name: c.Name(), State: "unavailable", Detail: "NVML/nvidia-smi unavailable"}
	}
	enhancement := ""
	if _, err := c.runner().LookPath("dcgmi"); err == nil {
		enhancement = "DCGM detected for health and profiling augmentation"
	}
	return Capability{Name: c.Name(), State: "available", Detail: "NVML-backed nvidia-smi device queries", Enhancement: enhancement}
}

func (c NVIDIA) Collect(ctx context.Context) (domain.IngestBatch, error) {
	fields := []string{
		"uuid", "name", "pci.bus_id", "temperature.gpu", "temperature.memory",
		"fan.speed", "power.draw", "power.limit", "utilization.gpu",
		"utilization.memory", "memory.used", "memory.total", "clocks.sm",
		"clocks.mem", "pcie.link.gen.current", "pcie.link.width.current",
		"ecc.errors.uncorrected.volatile.total",
	}
	result, err := c.runner().Run(ctx, "nvidia-smi",
		"--query-gpu="+strings.Join(fields, ","),
		"--format=csv,noheader,nounits")
	if err != nil && len(result.Stdout) == 0 {
		return domain.IngestBatch{AgentID: c.Host}, err
	}
	return parseNvidiaSMI(result.Stdout, fields, c.Host)
}

func (c NVIDIA) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

func parseNvidiaSMI(body []byte, fields []string, host string) (domain.IngestBatch, error) {
	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return domain.IngestBatch{}, fmt.Errorf("parse nvidia-smi CSV: %w", err)
	}
	hostID := "host:" + stableHash(host)
	batch := domain.IngestBatch{AgentID: host}
	now := time.Now().UTC()
	for _, record := range records {
		if len(record) != len(fields) {
			continue
		}
		values := make(map[string]string, len(fields))
		for index, field := range fields {
			values[field] = strings.TrimSpace(record[index])
		}
		uuid := values["uuid"]
		if unavailable(uuid) {
			continue
		}
		assetID := "gpu:" + uuid
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "gpu",
			Name:           values["name"],
			Vendor:         "NVIDIA",
			Model:          values["name"],
			Metadata: map[string]any{
				"uuid":       uuid,
				"pciAddress": values["pci.bus_id"],
				"collector":  "nvml-via-nvidia-smi",
			},
		})
		for field, definition := range nvidiaFieldDefinitions {
			raw := values[field]
			if unavailable(raw) {
				continue
			}
			value, parseErr := strconv.ParseFloat(raw, 64)
			if parseErr != nil {
				continue
			}
			if definition.scale != 0 {
				value *= definition.scale
			}
			appendNumeric(&batch, assetID, "nvml", uuid+"/"+field, field,
				definition.label, definition.category, definition.unit,
				definition.monotonic, 10, value, now,
				map[string]any{"field": field, "availability": "supported"})
		}
	}
	return batch, nil
}

var nvidiaFieldDefinitions = map[string]struct {
	label, category, unit string
	scale                 float64
	monotonic             bool
}{
	"temperature.gpu":                       {"GPU core temperature", "temperature", "Cel", 1, false},
	"temperature.memory":                    {"GPU memory temperature", "temperature", "Cel", 1, false},
	"fan.speed":                             {"Fan speed", "fan", "%", 1, false},
	"power.draw":                            {"Power draw", "power", "W", 1, false},
	"power.limit":                           {"Power limit", "power", "W", 1, false},
	"utilization.gpu":                       {"GPU utilization", "utilization", "%", 1, false},
	"utilization.memory":                    {"Memory utilization", "utilization", "%", 1, false},
	"memory.used":                           {"Memory used", "capacity", "By", 1024 * 1024, false},
	"memory.total":                          {"Memory total", "capacity", "By", 1024 * 1024, false},
	"clocks.sm":                             {"SM clock", "frequency", "MHz", 1, false},
	"clocks.mem":                            {"Memory clock", "frequency", "MHz", 1, false},
	"pcie.link.gen.current":                 {"PCIe generation", "link_status", "1", 1, false},
	"pcie.link.width.current":               {"PCIe width", "link_status", "1", 1, false},
	"ecc.errors.uncorrected.volatile.total": {"Uncorrected ECC errors", "error_count", "1", 1, true},
}

func unavailable(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized == "" || normalized == "n/a" || normalized == "[not supported]" ||
		normalized == "not supported" || normalized == "unknown"
}
