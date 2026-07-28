package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type LinuxInventory struct {
	SysRoot string
	Host    string
	Runner  Runner
}

func (c LinuxInventory) Name() string            { return "linux-inventory" }
func (c LinuxInventory) Interval() time.Duration { return 15 * time.Minute }
func (c LinuxInventory) Capability(context.Context) Capability {
	if _, err := os.Stat(filepath.Join(c.SysRoot, "sys")); err != nil {
		return Capability{Name: c.Name(), State: "unavailable", Detail: err.Error()}
	}
	return Capability{Name: c.Name(), State: "available", Detail: "PCI, USB, block, network, thermal, power_supply, EDAC, DRM, RDMA sysfs and optional devlink"}
}

func (c LinuxInventory) Collect(ctx context.Context) (domain.IngestBatch, error) {
	machineID := firstNonEmpty(
		readTrimmed(filepath.Join(c.SysRoot, "etc/machine-id")),
		readTrimmed(filepath.Join(c.SysRoot, "host/etc/machine-id")),
		c.Host,
	)
	hostID := "host:" + stableHash(machineID)
	now := time.Now().UTC()
	batch := domain.IngestBatch{
		AgentID: c.Host,
		Assets: []domain.IngestAsset{{
			StableID: hostID,
			Kind:     "host",
			Name:     c.Host,
			Metadata: map[string]any{"machineId": machineID, "collector": c.Name()},
		}},
	}
	c.collectPCI(&batch, hostID)
	c.collectUSB(&batch, hostID)
	c.collectBlock(&batch, hostID, now)
	c.collectNetwork(&batch, hostID, now)
	c.collectThermal(&batch, hostID, now)
	c.collectPowerSupply(&batch, hostID, now)
	c.collectEDAC(&batch, hostID, now)
	c.collectDRM(&batch, hostID)
	c.collectRDMA(&batch, hostID, now)
	c.collectDevlink(ctx, &batch, hostID)
	return batch, nil
}

func (c LinuxInventory) collectPCI(batch *domain.IngestBatch, hostID string) {
	root := filepath.Join(c.SysRoot, "sys/bus/pci/devices")
	entries, _ := os.ReadDir(root)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		address := entry.Name()
		path := filepath.Join(root, address)
		vendor := readTrimmed(filepath.Join(path, "vendor"))
		device := readTrimmed(filepath.Join(path, "device"))
		class := readTrimmed(filepath.Join(path, "class"))
		subsystemVendor := readTrimmed(filepath.Join(path, "subsystem_vendor"))
		subsystemDevice := readTrimmed(filepath.Join(path, "subsystem_device"))
		serial := readTrimmed(filepath.Join(path, "serial"))
		identity := firstNonEmpty(serial, address)
		kind := "pci_device"
		switch {
		case strings.HasPrefix(class, "0x03"):
			kind = "gpu"
		case strings.HasPrefix(class, "0x02"):
			kind = "nic"
		case strings.HasPrefix(class, "0x01"):
			kind = "hba"
		}
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       "pci:" + stableHash(identity+"|"+vendor+"|"+device),
			ParentStableID: hostID,
			Kind:           kind,
			Name:           address + " · " + vendor + ":" + device,
			Vendor:         vendor,
			Model:          device,
			Metadata: map[string]any{
				"pciAddress":      address,
				"class":           class,
				"subsystemVendor": subsystemVendor,
				"subsystemDevice": subsystemDevice,
				"serial":          serial,
			},
		})
	}
}

func (c LinuxInventory) collectUSB(batch *domain.IngestBatch, hostID string) {
	root := filepath.Join(c.SysRoot, "sys/bus/usb/devices")
	entries, _ := os.ReadDir(root)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ":") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		vendor := readTrimmed(filepath.Join(path, "idVendor"))
		productID := readTrimmed(filepath.Join(path, "idProduct"))
		if vendor == "" || productID == "" {
			continue
		}
		serial := readTrimmed(filepath.Join(path, "serial"))
		product := readTrimmed(filepath.Join(path, "product"))
		manufacturer := readTrimmed(filepath.Join(path, "manufacturer"))
		identity := firstNonEmpty(serial, entry.Name()+"|"+vendor+"|"+productID)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       "usb:" + stableHash(identity),
			ParentStableID: hostID,
			Kind:           "usb_device",
			Name:           firstNonEmpty(product, entry.Name()+" · "+vendor+":"+productID),
			Vendor:         manufacturer,
			Model:          product,
			Metadata: map[string]any{
				"usbPath": entry.Name(), "vendorId": vendor,
				"productId": productID, "serial": serial,
			},
		})
	}
}

func (c LinuxInventory) collectBlock(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/class/block")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		path := filepath.Join(root, name)
		if readTrimmed(filepath.Join(path, "partition")) != "" {
			continue
		}
		serial := readTrimmed(filepath.Join(path, "device/serial"))
		wwid := firstNonEmpty(readTrimmed(filepath.Join(path, "wwid")), readTrimmed(filepath.Join(path, "device/wwid")))
		model := readTrimmed(filepath.Join(path, "device/model"))
		vendor := readTrimmed(filepath.Join(path, "device/vendor"))
		identity := firstNonEmpty(serial, wwid, name+"|"+model+"|"+vendor)
		assetID := "disk:" + stableHash(identity)
		sizeSectors, _ := strconv.ParseUint(readTrimmed(filepath.Join(path, "size")), 10, 64)
		rotational := readTrimmed(filepath.Join(path, "queue/rotational")) == "1"
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "disk",
			Name:           name + " · " + firstNonEmpty(model, "block device"),
			Vendor:         vendor,
			Model:          model,
			Metadata: map[string]any{
				"device":     "/dev/" + name,
				"serial":     serial,
				"wwid":       wwid,
				"sizeBytes":  sizeSectors * 512,
				"rotational": rotational,
			},
		})
		stats := strings.Fields(readTrimmed(filepath.Join(path, "stat")))
		if len(stats) < 11 {
			continue
		}
		for _, definition := range []struct {
			offset   int
			key      string
			name     string
			category string
			unit     string
			scale    float64
		}{
			{0, "reads_completed", "Reads completed", "throughput", "1", 1},
			{2, "sectors_read", "Bytes read", "throughput", "By", 512},
			{4, "writes_completed", "Writes completed", "throughput", "1", 1},
			{6, "sectors_written", "Bytes written", "throughput", "By", 512},
			{9, "io_in_progress", "I/O in progress", "utilization", "1", 1},
			{10, "io_time_ms", "I/O time", "time", "ms", 1},
		} {
			value, err := strconv.ParseFloat(stats[definition.offset], 64)
			if err != nil {
				continue
			}
			value *= definition.scale
			appendNumeric(batch, assetID, "sysfs", filepath.Join("/sys/class/block", name, "stat"),
				"block/"+name+"/"+definition.key, definition.name, definition.category,
				definition.unit, true, 30, value, now, map[string]any{"field": definition.offset})
		}
	}
}

func (c LinuxInventory) collectNetwork(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/class/net")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		name := entry.Name()
		if name == "lo" {
			continue
		}
		path := filepath.Join(root, name)
		address := readTrimmed(filepath.Join(path, "address"))
		permanent := readTrimmed(filepath.Join(path, "perm_address"))
		if permanent == "" || permanent == "00:00:00:00:00:00" {
			permanent = address
		}
		ifindex := readTrimmed(filepath.Join(path, "ifindex"))
		assetID := "nic:" + stableHash(firstNonEmpty(permanent, ifindex, name))
		speed, _ := strconv.ParseInt(readTrimmed(filepath.Join(path, "speed")), 10, 64)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "nic",
			Name:           name,
			Metadata: map[string]any{
				"interface": name,
				"mac":       address,
				"permanent": permanent,
				"ifindex":   ifindex,
				"operstate": readTrimmed(filepath.Join(path, "operstate")),
				"speedMbps": speed,
			},
		})
		stats, _ := os.ReadDir(filepath.Join(path, "statistics"))
		for _, stat := range stats {
			raw := readTrimmed(filepath.Join(path, "statistics", stat.Name()))
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			category, unit := networkStatSemantics(stat.Name())
			appendNumeric(batch, assetID, "sysfs", filepath.Join("/sys/class/net", name, "statistics", stat.Name()),
				"net/"+name+"/"+stat.Name(), stat.Name(), category, unit, true, 30, value, now,
				map[string]any{"semanticGroup": category})
		}
	}
}

func (c LinuxInventory) collectThermal(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/class/thermal")
	entries, _ := filepath.Glob(filepath.Join(root, "thermal_zone*"))
	for _, path := range entries {
		value, err := strconv.ParseFloat(readTrimmed(filepath.Join(path, "temp")), 64)
		if err != nil {
			continue
		}
		value /= 1000
		zone := filepath.Base(path)
		label := firstNonEmpty(readTrimmed(filepath.Join(path, "type")), zone)
		appendNumeric(batch, hostID, "sysfs", filepath.Join("/sys/class/thermal", zone, "temp"),
			"thermal/"+zone, label, "temperature", "Cel", false, 10, value, now,
			map[string]any{"zone": zone})
	}
}

func (c LinuxInventory) collectPowerSupply(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/class/power_supply")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		serial := readTrimmed(filepath.Join(path, "serial_number"))
		model := readTrimmed(filepath.Join(path, "model_name"))
		kind := strings.ToLower(readTrimmed(filepath.Join(path, "type")))
		assetID := "power:" + stableHash(firstNonEmpty(serial, entry.Name()+"|"+model))
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           firstNonEmpty(kind, "power_supply"),
			Name:           entry.Name(),
			Model:          model,
			Metadata: map[string]any{
				"serial": serial,
				"status": readTrimmed(filepath.Join(path, "status")),
			},
		})
		files, _ := os.ReadDir(path)
		for _, file := range files {
			category, unit, scale, ok := powerSupplySemantics(file.Name())
			if !ok {
				continue
			}
			value, err := strconv.ParseFloat(readTrimmed(filepath.Join(path, file.Name())), 64)
			if err != nil {
				continue
			}
			value *= scale
			appendNumeric(batch, assetID, "sysfs", filepath.Join("/sys/class/power_supply", entry.Name(), file.Name()),
				"power_supply/"+entry.Name()+"/"+file.Name(), file.Name(), category, unit, false, 30, value, now, nil)
		}
	}
}

func (c LinuxInventory) collectEDAC(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/devices/system/edac/mc")
	controllers, _ := filepath.Glob(filepath.Join(root, "mc[0-9]*"))
	sort.Strings(controllers)
	for _, path := range controllers {
		name := filepath.Base(path)
		assetID := "edac:" + stableHash(hostID+"|"+name)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "memory_controller",
			Name:           "EDAC " + name,
			Metadata: map[string]any{
				"controller": name,
				"sizeMiB":    readTrimmed(filepath.Join(path, "size_mb")),
			},
		})
		for _, counter := range []string{"ce_count", "ue_count"} {
			value, err := strconv.ParseFloat(readTrimmed(filepath.Join(path, counter)), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			appendNumeric(
				batch, assetID, "edac-sysfs",
				filepath.Join("/sys/devices/system/edac/mc", name, counter),
				"edac/"+name+"/"+counter, counter, "error_count", "1",
				true, 30, value, now, map[string]any{"controller": name},
			)
		}
	}
}

func (c LinuxInventory) collectDRM(batch *domain.IngestBatch, hostID string) {
	root := filepath.Join(c.SysRoot, "sys/class/drm")
	entries, _ := filepath.Glob(filepath.Join(root, "card[0-9]*"))
	sort.Strings(entries)
	for _, path := range entries {
		name := filepath.Base(path)
		if strings.Contains(name, "-") {
			continue
		}
		devicePath := filepath.Join(path, "device")
		vendor := readTrimmed(filepath.Join(devicePath, "vendor"))
		device := readTrimmed(filepath.Join(devicePath, "device"))
		if vendor == "" && device == "" {
			continue
		}
		resolved, _ := filepath.EvalSymlinks(devicePath)
		address := filepath.Base(resolved)
		serial := readTrimmed(filepath.Join(devicePath, "serial"))
		identity := firstNonEmpty(serial, address)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       "pci:" + stableHash(identity+"|"+vendor+"|"+device),
			ParentStableID: hostID,
			Kind:           "gpu",
			Name:           name + " · DRM",
			Vendor:         vendor,
			Model:          device,
			Metadata: map[string]any{
				"drmCard": name, "pciAddress": address, "serial": serial,
			},
		})
	}
}

func (c LinuxInventory) collectDevlink(ctx context.Context, batch *domain.IngestBatch, hostID string) {
	runner := c.Runner
	if runner == nil {
		runner = ExecRunner{MaxOutput: 2 << 20}
	}
	if _, err := runner.LookPath("devlink"); err != nil {
		return
	}
	result, err := runner.Run(ctx, "devlink", "-j", "dev", "info")
	if err != nil {
		return
	}
	var response struct {
		Info map[string]struct {
			Driver       string         `json:"driver"`
			SerialNumber string         `json:"serial_number"`
			Versions     map[string]any `json:"versions"`
		} `json:"info"`
	}
	if json.Unmarshal(result.Stdout, &response) != nil {
		return
	}
	for busName, info := range response.Info {
		parts := strings.SplitN(busName, "/", 2)
		if len(parts) != 2 {
			continue
		}
		address := parts[1]
		devicePath := filepath.Join(c.SysRoot, "sys/bus/pci/devices", address)
		vendor := readTrimmed(filepath.Join(devicePath, "vendor"))
		device := readTrimmed(filepath.Join(devicePath, "device"))
		serial := firstNonEmpty(info.SerialNumber, readTrimmed(filepath.Join(devicePath, "serial")))
		identity := firstNonEmpty(serial, address)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       "pci:" + stableHash(identity+"|"+vendor+"|"+device),
			ParentStableID: hostID,
			Kind:           "nic",
			Name:           busName,
			Vendor:         vendor,
			Model:          device,
			Metadata: map[string]any{
				"devlink": busName, "driver": info.Driver,
				"serial": serial, "versions": info.Versions,
			},
		})
	}
}

func (c LinuxInventory) collectRDMA(batch *domain.IngestBatch, hostID string, now time.Time) {
	root := filepath.Join(c.SysRoot, "sys/class/infiniband")
	devices, _ := os.ReadDir(root)
	for _, device := range devices {
		devicePath := filepath.Join(root, device.Name())
		guid := firstNonEmpty(readTrimmed(filepath.Join(devicePath, "node_guid")), device.Name())
		assetID := "rdma:" + stableHash(guid)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "nic",
			Name:           device.Name() + " · RDMA",
			Metadata: map[string]any{
				"nodeGuid": guid,
				"firmware": readTrimmed(filepath.Join(devicePath, "fw_ver")),
			},
		})
		ports, _ := os.ReadDir(filepath.Join(devicePath, "ports"))
		for _, port := range ports {
			for _, counterRoot := range []string{"counters", "hw_counters"} {
				counterPath := filepath.Join(devicePath, "ports", port.Name(), counterRoot)
				counters, _ := os.ReadDir(counterPath)
				for _, counter := range counters {
					value, err := strconv.ParseFloat(readTrimmed(filepath.Join(counterPath, counter.Name())), 64)
					if err != nil {
						continue
					}
					category, unit := networkStatSemantics(counter.Name())
					source := filepath.Join("/sys/class/infiniband", device.Name(), "ports", port.Name(), counterRoot, counter.Name())
					appendNumeric(batch, assetID, "rdma-sysfs", source,
						"rdma/"+device.Name()+"/"+port.Name()+"/"+counter.Name(),
						counter.Name(), category, unit, true, 30, value, now,
						map[string]any{"port": port.Name(), "counterSet": counterRoot})
				}
			}
		}
	}
}

func appendNumeric(
	batch *domain.IngestBatch,
	assetID, source, sourcePath, stableSuffix, rawName, category, unit string,
	monotonic bool,
	interval int,
	value float64,
	observedAt time.Time,
	metadata map[string]any,
) {
	stableID := source + ":" + stableHash(assetID+"|"+stableSuffix)
	batch.Sensors = append(batch.Sensors, domain.IngestSensor{
		StableID:        stableID,
		AssetStableID:   assetID,
		Source:          source,
		SourcePath:      sourcePath,
		RawName:         rawName,
		Category:        category,
		Unit:            unit,
		Scale:           1,
		Monotonic:       monotonic,
		IntervalSeconds: interval,
		Quality:         "good",
		Metadata:        metadata,
	})
	batch.Samples = append(batch.Samples, domain.IngestSample{
		SensorStableID: stableID,
		ObservedAt:     observedAt,
		Value:          &value,
		Source:         source,
		Quality:        "good",
	})
}

func networkStatSemantics(name string) (string, string) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "byte") || strings.Contains(lower, "octet") || strings.HasSuffix(lower, "_data"):
		return "throughput", "By"
	case strings.Contains(lower, "error") || strings.Contains(lower, "discard") || strings.Contains(lower, "drop") ||
		strings.Contains(lower, "fault") || strings.Contains(lower, "crc") || strings.Contains(lower, "timeout"):
		return "error_count", "1"
	default:
		return "throughput", "1"
	}
}

func powerSupplySemantics(name string) (category, unit string, scale float64, ok bool) {
	switch name {
	case "voltage_now", "voltage_avg":
		return "voltage", "V", 1e-6, true
	case "current_now", "current_avg":
		return "current", "A", 1e-6, true
	case "power_now", "power_avg":
		return "power", "W", 1e-6, true
	case "energy_now", "energy_full":
		return "energy", "J", 3.6e-3, true
	case "capacity":
		return "capacity", "%", 1, true
	case "temp":
		return "temperature", "Cel", 0.1, true
	default:
		return "", "", 0, false
	}
}

func validateDeviceName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid device name %q", name)
	}
	return nil
}
