// Package nut implements the read-only Network UPS Tools upsd text protocol.
package nut

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Client struct {
	Address  string
	Username string
	Password string
	Timeout  time.Duration
}

func (c Client) Collect(ctx context.Context) (domain.IngestBatch, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return domain.IngestBatch{}, fmt.Errorf("connect to upsd: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(timeout))
	reader := bufio.NewReaderSize(connection, 256<<10)
	if c.Username != "" {
		if _, err := fmt.Fprintf(connection, "USERNAME %s\nPASSWORD %s\n", quote(c.Username), quote(c.Password)); err != nil {
			return domain.IngestBatch{}, err
		}
		if err := expectOK(reader); err != nil {
			return domain.IngestBatch{}, fmt.Errorf("NUT username: %w", err)
		}
		if err := expectOK(reader); err != nil {
			return domain.IngestBatch{}, fmt.Errorf("NUT password: %w", err)
		}
	}
	if _, err := fmt.Fprintln(connection, "LIST UPS"); err != nil {
		return domain.IngestBatch{}, err
	}
	upsList, err := readList(reader, "UPS")
	if err != nil {
		return domain.IngestBatch{}, err
	}
	var batch domain.IngestBatch
	for _, row := range upsList {
		if len(row) < 3 {
			continue
		}
		name := row[1]
		if _, err := fmt.Fprintf(connection, "LIST VAR %s\n", quote(name)); err != nil {
			return batch, err
		}
		variables, err := readList(reader, "VAR")
		if err != nil {
			return batch, fmt.Errorf("list variables for %s: %w", name, err)
		}
		appendUPS(&batch, c.Address, name, row[2], variables)
	}
	return batch, nil
}

func appendUPS(batch *domain.IngestBatch, address, name, description string, rows [][]string) {
	values := make(map[string]string)
	for _, row := range rows {
		if len(row) >= 4 {
			values[row[2]] = row[3]
		}
	}
	identity := first(values["device.serial"], values["ups.serial"], name)
	assetID := "nut:" + fingerprint(address+":"+identity)
	batch.Assets = append(batch.Assets, domain.IngestAsset{
		StableID: assetID, Kind: "ups", Name: first(values["ups.model"], description, name),
		Vendor: values["ups.mfr"], Model: values["ups.model"],
		Metadata: map[string]any{"nutName": name, "serial": identity, "status": values["ups.status"], "address": address},
	})
	now := time.Now().UTC()
	for key, raw := range values {
		category, unit, monotonic, supported := metric(key)
		if !supported {
			continue
		}
		sensorID := "nut:" + fingerprint(assetID+":"+key)
		sensor := domain.IngestSensor{
			StableID: sensorID, AssetStableID: assetID, Source: "nut",
			SourcePath: name + "/" + key, RawName: key, Category: category, Unit: unit,
			Scale: 1, Monotonic: monotonic, IntervalSeconds: 30, Quality: "good",
		}
		sample := domain.IngestSample{SensorStableID: sensorID, ObservedAt: now, Source: "nut", Quality: "good"}
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			sample.Value = &value
		} else {
			sample.TextValue = &raw
		}
		batch.Sensors = append(batch.Sensors, sensor)
		batch.Samples = append(batch.Samples, sample)
	}
}

func metric(key string) (category, unit string, monotonic, supported bool) {
	switch {
	case key == "battery.charge", key == "ups.load":
		return "capacity", "%", false, true
	case key == "battery.runtime":
		return "time", "s", false, true
	case strings.HasSuffix(key, ".voltage"):
		return "voltage", "V", false, true
	case strings.HasSuffix(key, ".frequency"):
		return "frequency", "Hz", false, true
	case strings.HasSuffix(key, ".current"):
		return "current", "A", false, true
	case strings.HasSuffix(key, ".power"):
		return "power", "W", false, true
	case strings.Contains(key, "temperature"):
		return "temperature", "Cel", false, true
	case key == "ups.status", strings.HasSuffix(key, ".status"):
		return "health", "1", false, true
	default:
		return "", "", false, false
	}
}

func readList(reader *bufio.Reader, kind string) ([][]string, error) {
	begin, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	beginFields, err := split(begin)
	if err != nil || len(beginFields) < 3 || beginFields[0] != "BEGIN" || beginFields[1] != "LIST" || beginFields[2] != kind {
		return nil, fmt.Errorf("unexpected list response %q", strings.TrimSpace(begin))
	}
	var result [][]string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		fields, err := split(line)
		if err != nil {
			return nil, err
		}
		if len(fields) >= 3 && fields[0] == "END" && fields[1] == "LIST" && fields[2] == kind {
			return result, nil
		}
		if len(fields) > 0 && fields[0] == "ERR" {
			return nil, errors.New(strings.Join(fields, " "))
		}
		result = append(result, fields)
	}
}

func expectOK(reader *bufio.Reader) error {
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "OK" {
		return errors.New(strings.TrimSpace(line))
	}
	return nil
}

func split(line string) ([]string, error) {
	var result []string
	var current strings.Builder
	quoted, escaped := false, false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for _, char := range strings.TrimSpace(line) {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if (char == ' ' || char == '\t') && !quoted {
			flush()
			continue
		}
		current.WriteRune(char)
	}
	if quoted || escaped {
		return nil, errors.New("invalid quoted NUT response")
	}
	flush()
	return result, nil
}

func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
