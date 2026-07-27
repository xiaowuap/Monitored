package collector

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

var hwmonInputPattern = regexp.MustCompile(`^([a-zA-Z]+)([0-9]+)_input$`)

type HWMon struct {
	SysRoot string
	Host    string
}

func (c HWMon) Collect() (domain.IngestBatch, error) {
	machineID := firstNonEmpty(
		readTrimmed(filepath.Join(c.SysRoot, "etc/machine-id")),
		readTrimmed(filepath.Join(c.SysRoot, "host/etc/machine-id")),
		c.Host,
	)
	hostStableID := "host:" + stableHash(machineID)
	batch := domain.IngestBatch{
		AgentID: c.Host,
		Assets: []domain.IngestAsset{{
			StableID: hostStableID,
			Kind:     "host",
			Name:     c.Host,
			Metadata: map[string]any{
				"os":           runtime.GOOS,
				"architecture": runtime.GOARCH,
				"machineId":    machineID,
				"collector":    "linux-hwmon",
			},
		}},
	}

	hwmonRoot := filepath.Join(c.SysRoot, "sys/class/hwmon")
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return batch, nil
		}
		return batch, fmt.Errorf("read hwmon root: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	now := time.Now().UTC()
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(hwmonRoot, entry.Name())
		chip := firstNonEmpty(readTrimmed(filepath.Join(path, "name")), entry.Name())
		devicePath, _ := filepath.EvalSymlinks(filepath.Join(path, "device"))
		deviceIdentity := strings.TrimPrefix(devicePath, c.SysRoot)
		if deviceIdentity == "" {
			deviceIdentity = path
		}
		files, readErr := os.ReadDir(path)
		if readErr != nil {
			continue
		}
		for _, file := range files {
			match := hwmonInputPattern.FindStringSubmatch(file.Name())
			if len(match) != 3 {
				continue
			}
			raw := readTrimmed(filepath.Join(path, file.Name()))
			value, parseErr := strconv.ParseFloat(raw, 64)
			if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			prefix := strings.ToLower(match[1])
			channel := prefix + match[2]
			category, unit, scale := normalizeHWMonKind(prefix)
			label := readTrimmed(filepath.Join(path, channel+"_label"))
			rawName := chip + "/" + channel
			if label != "" {
				rawName += " (" + label + ")"
			}
			sourcePath := strings.TrimPrefix(filepath.Join(path, file.Name()), c.SysRoot)
			stableID := "hwmon:" + stableHash(machineID+"|"+deviceIdentity+"|"+chip+"|"+channel)
			normalizedValue := value * scale
			batch.Sensors = append(batch.Sensors, domain.IngestSensor{
				StableID:        stableID,
				AssetStableID:   hostStableID,
				Source:          "agent",
				SourcePath:      sourcePath,
				RawName:         rawName,
				Category:        category,
				Unit:            unit,
				Scale:           scale,
				IntervalSeconds: 10,
				Quality:         "good",
				Metadata: map[string]any{
					"chip":          chip,
					"channel":       channel,
					"kernelHwmon":   entry.Name(),
					"devicePath":    deviceIdentity,
					"originalValue": raw,
					"originalLabel": label,
					"normalizer":    "hwmon-sysfs-v1",
				},
			})
			batch.Samples = append(batch.Samples, domain.IngestSample{
				SensorStableID: stableID,
				ObservedAt:     now,
				Value:          &normalizedValue,
				Source:         "agent",
				Quality:        "good",
			})
		}
	}
	return batch, nil
}

func normalizeHWMonKind(prefix string) (category, unit string, scale float64) {
	switch prefix {
	case "temp":
		return "temperature", "Cel", 0.001
	case "fan":
		return "fan", "rpm", 1
	case "in":
		return "voltage", "V", 0.001
	case "curr":
		return "current", "A", 0.001
	case "power":
		return "power", "W", 0.000001
	case "energy":
		return "energy", "J", 0.000001
	case "freq":
		return "frequency", "Hz", 1
	case "humidity":
		return "utilization", "%", 0.001
	case "pwm":
		return "utilization", "%", 100.0 / 255.0
	case "intrusion":
		return "health_status", "1", 1
	default:
		return "unknown", "1", 1
	}
}

func readTrimmed(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}

func stableHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}
