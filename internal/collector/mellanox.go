package collector

import (
	"bufio"
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

var mgetTemperaturePattern = regexp.MustCompile(`(?i)(?:temperature|temp)[^0-9-]*(-?[0-9]+(?:\.[0-9]+)?)`)

type Mellanox struct {
	Host   string
	Runner Runner
}

func (c Mellanox) Name() string            { return "mellanox-mft" }
func (c Mellanox) Interval() time.Duration { return 30 * time.Second }
func (c Mellanox) Capability(context.Context) Capability {
	if _, err := c.runner().LookPath("mget_temp"); err != nil {
		return Capability{
			Name:        c.Name(),
			State:       "enhanceable",
			Detail:      "RDMA and ethtool counters remain available through sysfs",
			Enhancement: "Install NVIDIA MFT for ASIC temperature and cable diagnostics; MST initialization requires approval",
		}
	}
	return Capability{Name: c.Name(), State: "available", Detail: "Read-only MFT ASIC temperature enhancement"}
}

func (c Mellanox) Collect(ctx context.Context) (domain.IngestBatch, error) {
	result, err := c.runner().Run(ctx, "mget_temp")
	if err != nil && len(result.Stdout) == 0 {
		return domain.IngestBatch{AgentID: c.Host}, err
	}
	return parseMGetTemp(result.Stdout, c.Host), nil
}

func (c Mellanox) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

func parseMGetTemp(body []byte, host string) domain.IngestBatch {
	hostID := "host:" + stableHash(host)
	batch := domain.IngestBatch{AgentID: host}
	now := time.Now().UTC()
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	index := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := mgetTemperaturePattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			continue
		}
		assetID := "mellanox:" + stableHash(host+"|"+strconv.Itoa(index)+"|"+line)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "nic",
			Name:           "NVIDIA networking ASIC",
			Vendor:         "NVIDIA",
			Metadata:       map[string]any{"mftLine": line, "collector": "mget_temp"},
		})
		appendNumeric(&batch, assetID, "mft", "mget_temp/"+strconv.Itoa(index),
			"asic_temperature", "ASIC temperature", "temperature", "Cel",
			false, 30, value, now, map[string]any{"raw": line})
		index++
	}
	return batch
}
