package collector

import (
	"context"
	"encoding/csv"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type IPMI struct {
	Host   string
	Runner Runner
}

func (c IPMI) Name() string            { return "ipmi-local" }
func (c IPMI) Interval() time.Duration { return 30 * time.Second }
func (c IPMI) Capability(context.Context) Capability {
	if _, err := c.runner().LookPath("ipmitool"); err != nil {
		return Capability{Name: c.Name(), State: "enhanceable", Detail: "Local IPMI unavailable", Enhancement: "Install ipmitool or configure Redfish"}
	}
	return Capability{Name: c.Name(), State: "available", Detail: "Read-only local SDR sensor inventory"}
}

func (c IPMI) Collect(ctx context.Context) (domain.IngestBatch, error) {
	result, err := c.runner().Run(ctx, "ipmitool", "sensor", "list", "-c")
	if err != nil && len(result.Stdout) == 0 {
		return domain.IngestBatch{AgentID: c.Host}, err
	}
	return parseIPMISensors(result.Stdout, c.Host), nil
}

func (c IPMI) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

func parseIPMISensors(body []byte, host string) domain.IngestBatch {
	hostID := "host:" + stableHash(host)
	bmcID := "bmc:" + stableHash(host)
	batch := domain.IngestBatch{
		AgentID: host,
		Assets: []domain.IngestAsset{{
			StableID:       bmcID,
			ParentStableID: hostID,
			Kind:           "bmc",
			Name:           host + " BMC",
			Metadata:       map[string]any{"protocol": "IPMI 2.0", "mode": "read-only"},
		}},
	}
	reader := csv.NewReader(strings.NewReader(string(body)))
	reader.FieldsPerRecord = -1
	records, _ := reader.ReadAll()
	now := time.Now().UTC()
	for index, record := range records {
		if len(record) < 4 {
			continue
		}
		name := strings.TrimSpace(record[0])
		raw := strings.TrimSpace(record[1])
		unitRaw := strings.TrimSpace(record[2])
		status := strings.TrimSpace(record[3])
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		category, unit, scale := ipmiSemantics(unitRaw)
		value *= scale
		appendNumeric(&batch, bmcID, "ipmi", "sdr/"+strconv.Itoa(index),
			"sdr/"+stableHash(name), name, category, unit, false, 30, value, now,
			map[string]any{"status": status, "originalUnit": unitRaw})
	}
	return batch
}

func ipmiSemantics(unit string) (category, normalized string, scale float64) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "degrees c", "degree c", "c":
		return "temperature", "Cel", 1
	case "rpm":
		return "fan", "rpm", 1
	case "volts", "v":
		return "voltage", "V", 1
	case "amps", "a":
		return "current", "A", 1
	case "watts", "w":
		return "power", "W", 1
	case "percent", "%":
		return "utilization", "%", 1
	default:
		return "unknown", "1", 1
	}
}
