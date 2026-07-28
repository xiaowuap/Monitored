// Package ipmi implements read-only IPMI 2.0/RMCP+ polling through ipmitool.
// Credentials are passed through the child environment and never appear in
// argv, logs, profiles, or process listings.
package ipmi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Client struct {
	Host       string
	Port       uint16
	Username   string
	Password   string
	Executable string
}

func (c Client) Collect(ctx context.Context) (domain.IngestBatch, error) {
	if c.Host == "" || c.Username == "" || c.Password == "" {
		return domain.IngestBatch{}, errors.New("IPMI host, username, and password are required")
	}
	executable := c.Executable
	if executable == "" {
		executable = "ipmitool"
	}
	arguments := []string{"-I", "lanplus", "-H", c.Host, "-U", c.Username, "-E"}
	if c.Port != 0 && c.Port != 623 {
		arguments = append(arguments, "-p", strconv.Itoa(int(c.Port)))
	}
	sdr, err := run(ctx, executable, c.Password, append(arguments, "sdr", "elist")...)
	if err != nil {
		return domain.IngestBatch{}, fmt.Errorf("read IPMI SDR: %w", err)
	}
	sel, selErr := run(ctx, executable, c.Password, append(arguments, "sel", "elist")...)
	return mapReadings(c.Host, sdr, sel, selErr), nil
}

func run(ctx context.Context, executable, password string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "IPMI_PASSWORD=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "IPMI_PASSWORD="+password)
	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = 2<<20, 128<<10
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func mapReadings(host, sdr, sel string, selErr error) domain.IngestBatch {
	assetID := "ipmi:" + fingerprint(host)
	metadata := map[string]any{"endpoint": host, "transport": "ipmi-lanplus"}
	if selErr == nil {
		lines := nonEmptyLines(sel)
		metadata["selEntryCount"] = len(lines)
		if len(lines) > 0 {
			metadata["selLatest"] = lines[len(lines)-1]
		}
	} else {
		metadata["selStatus"] = "unavailable"
	}
	batch := domain.IngestBatch{Assets: []domain.IngestAsset{{
		StableID: assetID, Kind: "bmc", Name: "IPMI · " + host, Metadata: metadata,
	}}}
	now := time.Now().UTC()
	for index, line := range nonEmptyLines(sdr) {
		fields := strings.Split(line, "|")
		if len(fields) < 2 {
			continue
		}
		for position := range fields {
			fields[position] = strings.TrimSpace(fields[position])
		}
		name := fields[0]
		value, category, unit, ok := reading(fields[1])
		if !ok {
			continue
		}
		key := name + ":" + strconv.Itoa(index)
		sensorID := "ipmi:" + fingerprint(assetID+":"+key)
		quality := "good"
		if len(fields) > 3 && strings.Contains(strings.ToLower(fields[len(fields)-1]), "critical") {
			quality = "error"
		}
		batch.Sensors = append(batch.Sensors, domain.IngestSensor{
			StableID: sensorID, AssetStableID: assetID, Source: "ipmi",
			SourcePath: "sdr/" + key, RawName: name, Category: category, Unit: unit,
			Scale: 1, IntervalSeconds: 30, Quality: quality,
			Metadata: map[string]any{"raw": line},
		})
		batch.Samples = append(batch.Samples, domain.IngestSample{
			SensorStableID: sensorID, ObservedAt: now, Value: &value,
			Source: "ipmi", Quality: quality,
		})
	}
	return batch
}

func reading(value string) (float64, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0, "", "", false
	}
	numeric, err := strconv.ParseFloat(fields[0], 64)
	if err != nil && strings.HasPrefix(strings.ToLower(fields[0]), "0x") {
		integer, parseErr := strconv.ParseUint(fields[0][2:], 16, 64)
		numeric, err = float64(integer), parseErr
	}
	if err != nil {
		return 0, "", "", false
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "degree") || strings.Contains(lower, " c"):
		return numeric, "temperature", "Cel", true
	case strings.Contains(lower, "rpm"):
		return numeric, "fan", "rpm", true
	case strings.Contains(lower, "volt"):
		return numeric, "voltage", "V", true
	case strings.Contains(lower, "amp"):
		return numeric, "current", "A", true
	case strings.Contains(lower, "watt"):
		return numeric, "power", "W", true
	default:
		return numeric, "unknown", "1", true
	}
}

func nonEmptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, strings.TrimSpace(line))
		}
	}
	return result
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	count := len(value)
	if b.Len() < b.limit {
		_, _ = b.Buffer.Write(value[:min(len(value), b.limit-b.Len())])
	}
	return count, nil
}
