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

var freeBSDTemperature = regexp.MustCompile(`^dev\.cpu\.([0-9]+)\.temperature:\s*([0-9.]+)C`)

type FreeBSD struct {
	Host   string
	Runner Runner
}

func (c FreeBSD) Name() string            { return "freebsd" }
func (c FreeBSD) Interval() time.Duration { return 30 * time.Second }
func (c FreeBSD) Capability(context.Context) Capability {
	if _, err := c.runner().LookPath("sysctl"); err != nil {
		return Capability{Name: c.Name(), State: "unavailable", Detail: "FreeBSD sysctl unavailable"}
	}
	enhancement := ""
	if _, err := c.runner().LookPath("smartctl"); err != nil {
		enhancement = "Install smartmontools for disk health"
	}
	return Capability{Name: c.Name(), State: "available", Detail: "FreeBSD sysctl/devstat baseline", Enhancement: enhancement}
}

func (c FreeBSD) Collect(ctx context.Context) (domain.IngestBatch, error) {
	result, err := c.runner().Run(ctx, "sysctl", "-a")
	if err != nil && len(result.Stdout) == 0 {
		return domain.IngestBatch{AgentID: c.Host}, err
	}
	return parseFreeBSDSysctl(result.Stdout, c.Host), nil
}

func (c FreeBSD) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

func parseFreeBSDSysctl(body []byte, host string) domain.IngestBatch {
	hostID := "host:" + stableHash(host)
	batch := domain.IngestBatch{
		AgentID: host,
		Assets: []domain.IngestAsset{{
			StableID: hostID,
			Kind:     "host",
			Name:     host,
			Metadata: map[string]any{"os": "freebsd"},
		}},
	}
	now := time.Now().UTC()
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if match := freeBSDTemperature.FindStringSubmatch(line); len(match) == 3 {
			value, err := strconv.ParseFloat(match[2], 64)
			if err != nil {
				continue
			}
			appendNumeric(&batch, hostID, "freebsd-sysctl", "dev.cpu."+match[1]+".temperature",
				"cpu/"+match[1]+"/temperature", "CPU "+match[1]+" temperature",
				"temperature", "Cel", false, 30, value, now, nil)
			continue
		}
		if strings.HasPrefix(line, "hw.model:") {
			batch.Assets[0].Model = strings.TrimSpace(strings.TrimPrefix(line, "hw.model:"))
		}
	}
	return batch
}
