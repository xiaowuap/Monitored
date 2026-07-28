package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

const defaultSMARTInterval = 5 * time.Minute

// SMART collects standby-safe ATA, NVMe and SCSI health data. DeviceRoot is
// separate from SysRoot so an agent container can mount host sysfs read-only at
// /host/sys and explicitly mapped host device nodes at /host/dev.
type SMART struct {
	SysRoot      string
	DeviceRoot   string
	Host         string
	PollInterval time.Duration
	Runner       Runner
}

func (c SMART) Name() string { return "smart-nvme" }
func (c SMART) Interval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return defaultSMARTInterval
}
func (c SMART) Capability(context.Context) Capability {
	runner := c.runner()
	if _, err := runner.LookPath("smartctl"); err != nil {
		return Capability{Name: c.Name(), State: "enhanceable", Detail: "smartctl unavailable", Enhancement: "Install smartmontools"}
	}
	return Capability{
		Name: c.Name(), State: "available",
		Detail: "standby-safe ATA/NVMe/SCSI SMART JSON with scan and sysfs discovery",
	}
}

func (c SMART) Collect(ctx context.Context) (domain.IngestBatch, error) {
	machineID := firstNonEmpty(
		readTrimmed(filepath.Join(c.SysRoot, "etc/machine-id")),
		readTrimmed(filepath.Join(c.SysRoot, "host/etc/machine-id")),
		c.Host,
	)
	hostID := "host:" + stableHash(machineID)
	batch := domain.IngestBatch{AgentID: c.Host}
	if _, err := c.runner().LookPath("smartctl"); err != nil {
		return batch, fmt.Errorf("smartctl unavailable: %w", err)
	}

	devices := c.discoverDevices(ctx)
	var collectionErrors []error
	for _, device := range devices {
		arguments := []string{"-a", "-j", "-n", "standby,0"}
		if device.Type != "" && device.Type != "auto" {
			arguments = append(arguments, "-d", device.Type)
		}
		arguments = append(arguments, c.devicePath(device.Name))
		result, runErr := c.runner().Run(ctx, "smartctl", arguments...)
		if len(result.Stdout) == 0 {
			if runErr != nil {
				collectionErrors = append(collectionErrors, fmt.Errorf("%s: %w", device.Name, runErr))
			}
			continue
		}
		deviceBatch, parseErr := parseSMARTDeviceJSON(
			result.Stdout, device, hostID, c.Host, int(c.Interval().Seconds()),
		)
		if parseErr != nil {
			collectionErrors = append(collectionErrors, fmt.Errorf("%s: %w", device.Name, parseErr))
			continue
		}
		batch = Merge(batch, deviceBatch)
	}
	if len(batch.Assets) == 0 && len(collectionErrors) > 0 {
		return batch, errors.Join(collectionErrors...)
	}
	return batch, nil
}

func (c SMART) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

type smartDevice struct {
	Name     string `json:"name"`
	InfoName string `json:"info_name"`
	Type     string `json:"type"`
	Protocol string `json:"protocol"`
	Serial   string `json:"-"`
	WWID     string `json:"-"`
	Model    string `json:"-"`
	Vendor   string `json:"-"`
}

type smartScanPayload struct {
	Devices []smartDevice `json:"devices"`
}

func (c SMART) discoverDevices(ctx context.Context) []smartDevice {
	var result []smartDevice
	scannedNames := make(map[string]struct{})
	scan, _ := c.runner().Run(ctx, "smartctl", "--scan", "-j")
	if len(scan.Stdout) > 0 {
		var payload smartScanPayload
		if json.Unmarshal(scan.Stdout, &payload) == nil {
			for _, device := range payload.Devices {
				if validSMARTDevicePath(device.Name) {
					result = append(result, device)
					scannedNames[device.Name] = struct{}{}
				}
			}
		}
	}

	// smartctl scan can be empty in a restricted container. Host sysfs remains
	// a reliable fallback when the required device nodes were explicitly mapped.
	entries, _ := os.ReadDir(filepath.Join(c.SysRoot, "sys/class/block"))
	for _, entry := range entries {
		name := entry.Name()
		if skipBlockDevice(name) || validateDeviceName(name) != nil {
			continue
		}
		if readTrimmed(filepath.Join(c.SysRoot, "sys/class/block", name, "partition")) != "" {
			continue
		}
		path := "/dev/" + name
		if _, ok := scannedNames[path]; !ok {
			result = append(result, smartDevice{Name: path})
		}
	}

	seen := make(map[string]struct{}, len(result))
	deduplicated := make([]smartDevice, 0, len(result))
	for _, device := range result {
		key := device.Name + "\x00" + device.Type
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, c.enrichDevice(device))
	}
	return deduplicated
}

func (c SMART) enrichDevice(device smartDevice) smartDevice {
	name := filepath.Base(device.Name)
	if validateDeviceName(name) != nil {
		return device
	}
	root := filepath.Join(c.SysRoot, "sys/class/block", name)
	device.Serial = readTrimmed(filepath.Join(root, "device/serial"))
	device.WWID = firstNonEmptyValue(
		readTrimmed(filepath.Join(root, "wwid")),
		readTrimmed(filepath.Join(root, "device/wwid")),
	)
	device.Model = readTrimmed(filepath.Join(root, "device/model"))
	device.Vendor = readTrimmed(filepath.Join(root, "device/vendor"))
	return device
}

func validSMARTDevicePath(value string) bool {
	if !strings.HasPrefix(value, "/dev/") || filepath.Clean(value) != value {
		return false
	}
	relative := strings.TrimPrefix(value, "/dev/")
	return relative != "" && relative != "." && !strings.ContainsAny(relative, "\x00\r\n\t ")
}

func skipBlockDevice(name string) bool {
	for _, prefix := range []string{"loop", "ram", "dm-", "md", "zram", "nbd", "rbd", "sr", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (c SMART) devicePath(scanned string) string {
	root := strings.TrimSpace(c.DeviceRoot)
	if root == "" {
		root = "/dev"
	}
	relative := strings.TrimPrefix(scanned, "/dev/")
	return filepath.Join(root, filepath.FromSlash(relative))
}

type smartPayload struct {
	Device struct {
		Name     string `json:"name"`
		InfoName string `json:"info_name"`
		Type     string `json:"type"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelFamily     string          `json:"model_family"`
	ModelName       string          `json:"model_name"`
	SerialNumber    string          `json:"serial_number"`
	FirmwareVersion string          `json:"firmware_version"`
	SCSIVendor      string          `json:"scsi_vendor"`
	SCSIModelName   string          `json:"scsi_model_name"`
	SCSIProduct     string          `json:"scsi_product"`
	SCSIRevision    string          `json:"scsi_revision"`
	LogicalUnitID   json.RawMessage `json:"logical_unit_id"`
	WWN             struct {
		NAA int    `json:"naa"`
		OUI int    `json:"oui"`
		ID  uint64 `json:"id"`
	} `json:"wwn"`
	UserCapacity struct {
		Bytes *float64 `json:"bytes"`
	} `json:"user_capacity"`
	RotationRate *float64 `json:"rotation_rate"`
	FormFactor   struct {
		Name string `json:"name"`
	} `json:"form_factor"`
	SmartSupport struct {
		Available *bool `json:"available"`
		Enabled   *bool `json:"enabled"`
	} `json:"smart_support"`
	SmartStatus struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	PowerMode   string `json:"power_mode"`
	PowerOnTime struct {
		Hours   *float64 `json:"hours"`
		Minutes *float64 `json:"minutes"`
	} `json:"power_on_time"`
	PowerCycleCount *float64         `json:"power_cycle_count"`
	Temperature     smartTemperature `json:"temperature"`
	ATASCTStatus    struct {
		Temperature smartTemperature `json:"temperature"`
	} `json:"ata_sct_status"`
	NVMeSmartHealthInformationLog nvmeHealth `json:"nvme_smart_health_information_log"`
	ATASmartAttributes            struct {
		Table []ataAttribute `json:"table"`
	} `json:"ata_smart_attributes"`
	ATASmartErrorLog struct {
		Summary struct {
			Count *float64 `json:"count"`
		} `json:"summary"`
	} `json:"ata_smart_error_log"`
	ATASmartSelfTestLog struct {
		Standard struct {
			Table []smartSelfTest `json:"table"`
		} `json:"standard"`
		Extended struct {
			Table []smartSelfTest `json:"table"`
		} `json:"extended"`
	} `json:"ata_smart_self_test_log"`
	NVMeSelfTestLog struct {
		Table []smartSelfTest `json:"table"`
	} `json:"nvme_self_test_log"`
	SCSITemperature struct {
		CurrentDriveTemperature *float64 `json:"current"`
		DriveTripTemperature    *float64 `json:"drive_trip"`
	} `json:"scsi_temperature"`
	SCSIGrownDefectList       *float64            `json:"scsi_grown_defect_list"`
	SCSIErrorCounterLog       scsiErrorCounterLog `json:"scsi_error_counter_log"`
	SCSIStartStopCycleCounter struct {
		AccumulatedStartStopCycles                 *float64 `json:"accumulated_start_stop_cycles"`
		AccumulatedLoadUnloadCycles                *float64 `json:"accumulated_load_unload_cycles"`
		SpecifiedCycleCountOverDeviceLifetime      *float64 `json:"specified_cycle_count_over_device_lifetime"`
		SpecifiedLoadUnloadCountOverDeviceLifetime *float64 `json:"specified_load_unload_count_over_device_lifetime"`
	} `json:"scsi_start_stop_cycle_counter"`
	Smartctl struct {
		ExitStatus *float64 `json:"exit_status"`
	} `json:"smartctl"`
}

type smartTemperature struct {
	Current       *float64 `json:"current"`
	PowerCycleMin *float64 `json:"power_cycle_min"`
	PowerCycleMax *float64 `json:"power_cycle_max"`
	LifetimeMin   *float64 `json:"lifetime_min"`
	LifetimeMax   *float64 `json:"lifetime_max"`
	OpLimitMin    *float64 `json:"op_limit_min"`
	OpLimitMax    *float64 `json:"op_limit_max"`
	DriveTrip     *float64 `json:"drive_trip"`
}

type nvmeHealth struct {
	CriticalWarning         *float64   `json:"critical_warning"`
	Temperature             *float64   `json:"temperature"`
	AvailableSpare          *float64   `json:"available_spare"`
	AvailableSpareThreshold *float64   `json:"available_spare_threshold"`
	PercentageUsed          *float64   `json:"percentage_used"`
	DataUnitsRead           *float64   `json:"data_units_read"`
	DataUnitsWritten        *float64   `json:"data_units_written"`
	HostReads               *float64   `json:"host_reads"`
	HostWrites              *float64   `json:"host_writes"`
	ControllerBusyTime      *float64   `json:"controller_busy_time"`
	PowerCycles             *float64   `json:"power_cycles"`
	PowerOnHours            *float64   `json:"power_on_hours"`
	UnsafeShutdowns         *float64   `json:"unsafe_shutdowns"`
	MediaErrors             *float64   `json:"media_errors"`
	NumErrLogEntries        *float64   `json:"num_err_log_entries"`
	WarningTempTime         *float64   `json:"warning_temp_time"`
	CriticalCompTime        *float64   `json:"critical_comp_time"`
	ThermalMgmtT1TransCount *float64   `json:"thermal_mgmt_t1_trans_count"`
	ThermalMgmtT2TransCount *float64   `json:"thermal_mgmt_t2_trans_count"`
	ThermalMgmtT1TotalTime  *float64   `json:"thermal_mgmt_t1_total_time"`
	ThermalMgmtT2TotalTime  *float64   `json:"thermal_mgmt_t2_total_time"`
	TemperatureSensors      []*float64 `json:"temperature_sensors"`
}

type ataAttribute struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Value      *int   `json:"value"`
	Worst      *int   `json:"worst"`
	Thresh     *int   `json:"thresh"`
	WhenFailed string `json:"when_failed"`
	Flags      struct {
		String string `json:"string"`
	} `json:"flags"`
	Raw struct {
		Value  *float64 `json:"value"`
		String string   `json:"string"`
	} `json:"raw"`
}

type smartSelfTest struct {
	Num           int      `json:"num"`
	Description   string   `json:"description"`
	Status        string   `json:"status"`
	StatusVerbose string   `json:"status_verbose"`
	Remaining     *float64 `json:"remaining_percent"`
	LifetimeHours *float64 `json:"lifetime_hours"`
}

type scsiErrorCounterLog struct {
	Read   scsiErrorCounters `json:"read"`
	Write  scsiErrorCounters `json:"write"`
	Verify scsiErrorCounters `json:"verify"`
}

type scsiErrorCounters struct {
	ErrorsCorrectedByECCFast         *float64       `json:"errors_corrected_by_eccfast"`
	ErrorsCorrectedByECCDelayed      *float64       `json:"errors_corrected_by_eccdelayed"`
	ErrorsCorrectedByRereadsRewrites *float64       `json:"errors_corrected_by_rereads_rewrites"`
	TotalErrorsCorrected             *float64       `json:"total_errors_corrected"`
	CorrectionAlgorithmInvocations   *float64       `json:"correction_algorithm_invocations"`
	GigabytesProcessed               *flexibleFloat `json:"gigabytes_processed"`
	TotalUncorrectedErrors           *float64       `json:"total_uncorrected_errors"`
}

type flexibleFloat float64

func (value *flexibleFloat) UnmarshalJSON(raw []byte) error {
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		*value = flexibleFloat(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return err
	}
	*value = flexibleFloat(parsed)
	return nil
}

// parseSMARTJSON is retained for fixtures and callers that already discovered a
// simple /dev/<name> device through sysfs.
func parseSMARTJSON(body []byte, device, hostID, agentID string) (domain.IngestBatch, error) {
	return parseSMARTDeviceJSON(body, smartDevice{Name: "/dev/" + device}, hostID, agentID, int(defaultSMARTInterval.Seconds()))
}

func parseSMARTDeviceJSON(
	body []byte,
	discovered smartDevice,
	hostID, agentID string,
	intervalSeconds int,
) (domain.IngestBatch, error) {
	var payload smartPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.IngestBatch{}, fmt.Errorf("parse smartctl JSON: %w", err)
	}
	if intervalSeconds <= 0 {
		intervalSeconds = int(defaultSMARTInterval.Seconds())
	}
	devicePath := firstNonEmpty(payload.Device.Name, discovered.Name)
	deviceType := firstNonEmpty(payload.Device.Type, discovered.Type)
	protocol := firstNonEmpty(payload.Device.Protocol, discovered.Protocol)
	deviceLabel := strings.TrimPrefix(devicePath, "/dev/")
	if deviceLabel == "" || deviceLabel == "unknown" {
		deviceLabel = firstNonEmpty(discovered.InfoName, "storage device")
	}
	wwn := firstNonEmptyValue(formatWWN(payload.WWN.NAA, payload.WWN.OUI, payload.WWN.ID), discovered.WWID)
	serial := firstNonEmptyValue(payload.SerialNumber, discovered.Serial)
	logicalUnitID := jsonScalarString(payload.LogicalUnitID)
	identity := firstNonEmpty(
		serial,
		wwn,
		logicalUnitID,
		devicePath+"|"+deviceType+"|"+payload.ModelName+"|"+payload.SCSIProduct,
	)
	assetID := "disk:" + stableHash(identity)
	model := firstNonEmpty(
		payload.ModelName, payload.SCSIModelName, payload.SCSIProduct,
		payload.ModelFamily, discovered.Model,
	)
	metadata := map[string]any{
		"device":        devicePath,
		"deviceType":    deviceType,
		"protocol":      protocol,
		"serial":        serial,
		"wwn":           wwn,
		"logicalUnitId": logicalUnitID,
		"firmware":      payload.FirmwareVersion,
		"modelFamily":   payload.ModelFamily,
		"formFactor":    payload.FormFactor.Name,
		"powerMode":     payload.PowerMode,
		"sleepPolicy":   "never-wake",
		"collector":     "smartctl-json",
	}
	putOptional(metadata, "capacityBytes", payload.UserCapacity.Bytes)
	putOptional(metadata, "rotationRate", payload.RotationRate)
	putOptional(metadata, "smartAvailable", payload.SmartSupport.Available)
	putOptional(metadata, "smartEnabled", payload.SmartSupport.Enabled)
	putOptional(metadata, "smartPassed", payload.SmartStatus.Passed)
	putOptional(metadata, "smartctlExitStatus", payload.Smartctl.ExitStatus)

	batch := domain.IngestBatch{
		AgentID: agentID,
		Assets: []domain.IngestAsset{{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "disk",
			Name:           deviceLabel + " · " + firstNonEmpty(model, "storage device"),
			Vendor:         firstNonEmptyValue(payload.SCSIVendor, discovered.Vendor),
			Model:          model,
			Metadata:       metadata,
		}},
	}
	now := time.Now().UTC()
	emit := func(key, label, category, unit string, value *float64, monotonic bool, extra map[string]any) {
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
			return
		}
		appendNumeric(
			&batch, assetID, "smart", devicePath+"/"+key, key, label,
			category, unit, monotonic, intervalSeconds, *value, now, extra,
		)
	}

	temperature := firstNumber(
		payload.NVMeSmartHealthInformationLog.Temperature,
		payload.Temperature.Current,
		payload.SCSITemperature.CurrentDriveTemperature,
		payload.ATASCTStatus.Temperature.Current,
	)
	tempMetadata := map[string]any{}
	putOptional(tempMetadata, "warningC", payload.Temperature.OpLimitMax)
	putOptional(tempMetadata, "criticalC", firstNumber(payload.Temperature.DriveTrip, payload.SCSITemperature.DriveTripTemperature))
	emit("temperature", "Temperature", "temperature", "Cel", temperature, false, tempMetadata)
	for _, definition := range []struct {
		key   string
		label string
		value *float64
	}{
		{"temperature/power_cycle_min", "Power-cycle minimum temperature", payload.Temperature.PowerCycleMin},
		{"temperature/power_cycle_max", "Power-cycle maximum temperature", payload.Temperature.PowerCycleMax},
		{"temperature/lifetime_min", "Lifetime minimum temperature", payload.Temperature.LifetimeMin},
		{"temperature/lifetime_max", "Lifetime maximum temperature", payload.Temperature.LifetimeMax},
	} {
		emit(definition.key, definition.label, "temperature", "Cel", definition.value, false, nil)
	}
	for index, value := range payload.NVMeSmartHealthInformationLog.TemperatureSensors {
		if value == nil || *value == 0 {
			continue
		}
		key := fmt.Sprintf("temperature/sensor_%d", index+1)
		emit(key, fmt.Sprintf("NVMe temperature sensor %d", index+1), "temperature", "Cel", value, false, map[string]any{"protocol": "nvme"})
	}

	if payload.SmartStatus.Passed != nil {
		value := boolFloat(*payload.SmartStatus.Passed)
		emit("health", "SMART health", "health_status", "1", &value, false, nil)
	}
	emit("smartctl_exit_status", "smartctl exit status", "health_status", "1", payload.Smartctl.ExitStatus, false, map[string]any{"encoding": "smartctl-bitmask"})
	emit("power_on_hours", "Power-on hours", "time", "h", firstNumber(payload.PowerOnTime.Hours, payload.NVMeSmartHealthInformationLog.PowerOnHours), true, nil)
	emit("power_cycles", "Power cycles", "health_status", "1", firstNumber(payload.PowerCycleCount, payload.NVMeSmartHealthInformationLog.PowerCycles), true, nil)

	nvme := payload.NVMeSmartHealthInformationLog
	for _, metric := range []struct {
		key, label, category, unit string
		value                      *float64
		monotonic                  bool
	}{
		{"nvme/critical_warning", "Critical warning", "health_status", "1", nvme.CriticalWarning, false},
		{"nvme/available_spare", "Available spare", "capacity", "%", nvme.AvailableSpare, false},
		{"nvme/available_spare_threshold", "Available spare threshold", "capacity", "%", nvme.AvailableSpareThreshold, false},
		{"nvme/percentage_used", "Percentage used", "utilization", "%", nvme.PercentageUsed, false},
		{"nvme/data_units_read", "Data units read", "throughput", "1", nvme.DataUnitsRead, true},
		{"nvme/data_units_written", "Data units written", "throughput", "1", nvme.DataUnitsWritten, true},
		{"nvme/host_reads", "Host read commands", "throughput", "1", nvme.HostReads, true},
		{"nvme/host_writes", "Host write commands", "throughput", "1", nvme.HostWrites, true},
		{"nvme/controller_busy_minutes", "Controller busy time", "time", "min", nvme.ControllerBusyTime, true},
		{"nvme/unsafe_shutdowns", "Unsafe shutdowns", "error_count", "1", nvme.UnsafeShutdowns, true},
		{"nvme/media_errors", "Media errors", "error_count", "1", nvme.MediaErrors, true},
		{"nvme/error_log_entries", "Error log entries", "error_count", "1", nvme.NumErrLogEntries, true},
		{"nvme/warning_temperature_minutes", "Warning temperature time", "time", "min", nvme.WarningTempTime, true},
		{"nvme/critical_temperature_minutes", "Critical temperature time", "time", "min", nvme.CriticalCompTime, true},
		{"nvme/thermal_t1_transitions", "Thermal management T1 transitions", "health_status", "1", nvme.ThermalMgmtT1TransCount, true},
		{"nvme/thermal_t2_transitions", "Thermal management T2 transitions", "health_status", "1", nvme.ThermalMgmtT2TransCount, true},
		{"nvme/thermal_t1_seconds", "Thermal management T1 time", "time", "s", nvme.ThermalMgmtT1TotalTime, true},
		{"nvme/thermal_t2_seconds", "Thermal management T2 time", "time", "s", nvme.ThermalMgmtT2TotalTime, true},
	} {
		emit(metric.key, metric.label, metric.category, metric.unit, metric.value, metric.monotonic, map[string]any{"protocol": "nvme"})
	}
	if nvme.DataUnitsRead != nil {
		value := *nvme.DataUnitsRead * 512000
		emit("nvme/bytes_read", "Bytes read", "throughput", "By", &value, true, map[string]any{"derivedFrom": "data_units_read"})
	}
	if nvme.DataUnitsWritten != nil {
		value := *nvme.DataUnitsWritten * 512000
		emit("nvme/bytes_written", "Bytes written", "throughput", "By", &value, true, map[string]any{"derivedFrom": "data_units_written"})
	}

	for _, attribute := range payload.ATASmartAttributes.Table {
		if attribute.Raw.Value == nil {
			continue
		}
		category, unit, monotonic := ataAttributeSemantics(attribute.ID, attribute.Name)
		attributeValue := ataAttributeValue(attribute, category)
		key := fmt.Sprintf("ata/attribute/%d", attribute.ID)
		attributeMetadata := map[string]any{
			"attributeId": attribute.ID,
			"rawString":   attribute.Raw.String,
			"flags":       attribute.Flags.String,
			"whenFailed":  attribute.WhenFailed,
		}
		putOptional(attributeMetadata, "normalized", attribute.Value)
		putOptional(attributeMetadata, "worst", attribute.Worst)
		putOptional(attributeMetadata, "threshold", attribute.Thresh)
		emit(key, firstNonEmpty(attribute.Name, fmt.Sprintf("ATA attribute %d", attribute.ID)), category, unit, attributeValue, monotonic, attributeMetadata)
	}
	emit("ata/error_log_count", "ATA SMART error log count", "error_count", "1", payload.ATASmartErrorLog.Summary.Count, true, map[string]any{"protocol": "ata"})
	appendSelfTests(&batch, assetID, devicePath, payload.ATASmartSelfTestLog.Standard.Table, "ata/standard", intervalSeconds, now)
	appendSelfTests(&batch, assetID, devicePath, payload.ATASmartSelfTestLog.Extended.Table, "ata/extended", intervalSeconds, now)
	appendSelfTests(&batch, assetID, devicePath, payload.NVMeSelfTestLog.Table, "nvme", intervalSeconds, now)

	emit("scsi/grown_defect_list", "SCSI grown defect list", "error_count", "1", payload.SCSIGrownDefectList, true, map[string]any{"protocol": "scsi"})
	appendSCSICounters(emit, "read", payload.SCSIErrorCounterLog.Read)
	appendSCSICounters(emit, "write", payload.SCSIErrorCounterLog.Write)
	appendSCSICounters(emit, "verify", payload.SCSIErrorCounterLog.Verify)
	for _, metric := range []struct {
		key, label string
		value      *float64
	}{
		{"start_stop_cycles", "SCSI accumulated start/stop cycles", payload.SCSIStartStopCycleCounter.AccumulatedStartStopCycles},
		{"load_unload_cycles", "SCSI accumulated load/unload cycles", payload.SCSIStartStopCycleCounter.AccumulatedLoadUnloadCycles},
		{"lifetime_start_stop_cycles", "SCSI specified lifetime start/stop cycles", payload.SCSIStartStopCycleCounter.SpecifiedCycleCountOverDeviceLifetime},
		{"lifetime_load_unload_cycles", "SCSI specified lifetime load/unload cycles", payload.SCSIStartStopCycleCounter.SpecifiedLoadUnloadCountOverDeviceLifetime},
	} {
		emit("scsi/"+metric.key, metric.label, "health_status", "1", metric.value, true, map[string]any{"protocol": "scsi"})
	}
	return batch, nil
}

func appendSelfTests(
	batch *domain.IngestBatch,
	assetID, devicePath string,
	tests []smartSelfTest,
	kind string,
	interval int,
	at time.Time,
) {
	for _, test := range tests {
		key := fmt.Sprintf("self_test/%s/%d", kind, test.Num)
		text := firstNonEmpty(test.StatusVerbose, test.Status, test.Description)
		stableID := "smart:" + stableHash(assetID+"|"+key)
		metadata := map[string]any{"testNumber": test.Num, "description": test.Description, "status": test.Status}
		putOptional(metadata, "remainingPercent", test.Remaining)
		putOptional(metadata, "lifetimeHours", test.LifetimeHours)
		batch.Sensors = append(batch.Sensors, domain.IngestSensor{
			StableID: stableID, AssetStableID: assetID, Source: "smart",
			SourcePath: devicePath + "/" + key, RawName: "SMART self-test " + strconv.Itoa(test.Num),
			Category: "health_status", Unit: "1", Scale: 1, IntervalSeconds: interval,
			Quality: "good", Metadata: metadata,
		})
		batch.Samples = append(batch.Samples, domain.IngestSample{
			SensorStableID: stableID, ObservedAt: at, TextValue: &text,
			Source: "smart", Quality: "good",
		})
	}
}

func appendSCSICounters(
	emit func(string, string, string, string, *float64, bool, map[string]any),
	operation string,
	counters scsiErrorCounters,
) {
	for _, metric := range []struct {
		key, label, category, unit string
		value                      *float64
	}{
		{"ecc_fast", "errors corrected by fast ECC", "error_count", "1", counters.ErrorsCorrectedByECCFast},
		{"ecc_delayed", "errors corrected by delayed ECC", "error_count", "1", counters.ErrorsCorrectedByECCDelayed},
		{"rereads_rewrites", "errors corrected by rereads/rewrites", "error_count", "1", counters.ErrorsCorrectedByRereadsRewrites},
		{"total_corrected", "total errors corrected", "error_count", "1", counters.TotalErrorsCorrected},
		{"correction_invocations", "correction algorithm invocations", "error_count", "1", counters.CorrectionAlgorithmInvocations},
		{"gigabytes_processed", "gigabytes processed", "throughput", "GBy", flexibleFloatPointer(counters.GigabytesProcessed)},
		{"total_uncorrected", "total uncorrected errors", "error_count", "1", counters.TotalUncorrectedErrors},
	} {
		emit(
			"scsi/"+operation+"/"+metric.key,
			"SCSI "+operation+" "+metric.label,
			metric.category, metric.unit, metric.value, true,
			map[string]any{"protocol": "scsi", "operation": operation},
		)
	}
}

func ataAttributeValue(attribute ataAttribute, category string) *float64 {
	if category != "temperature" {
		return attribute.Raw.Value
	}
	if fields := strings.Fields(attribute.Raw.String); len(fields) > 0 {
		if parsed, err := strconv.ParseFloat(strings.Trim(fields[0], "()"), 64); err == nil &&
			parsed >= -50 && parsed <= 250 {
			return &parsed
		}
	}
	if attribute.Value != nil {
		value := float64(*attribute.Value)
		if value >= -50 && value <= 250 {
			return &value
		}
	}
	return nil
}

func flexibleFloatPointer(value *flexibleFloat) *float64 {
	if value == nil {
		return nil
	}
	result := float64(*value)
	return &result
}

func ataAttributeSemantics(id int, name string) (category, unit string, monotonic bool) {
	lower := strings.ToLower(name)
	switch {
	case id == 190 || id == 194 || strings.Contains(lower, "temperature"):
		return "temperature", "Cel", false
	case id == 9 || strings.Contains(lower, "power_on"):
		return "time", "h", true
	case strings.Contains(lower, "percent") || strings.Contains(lower, "wear") ||
		strings.Contains(lower, "life_left") || strings.Contains(lower, "remaining"):
		return "utilization", "%", false
	case strings.Contains(lower, "error") || strings.Contains(lower, "sector") ||
		strings.Contains(lower, "crc") || strings.Contains(lower, "uncorrect") ||
		strings.Contains(lower, "fail"):
		return "error_count", "1", true
	case strings.Contains(lower, "cycle") || strings.Contains(lower, "count") ||
		strings.Contains(lower, "load") || strings.Contains(lower, "start_stop"):
		return "health_status", "1", true
	default:
		return "health_status", "1", false
	}
}

func formatWWN(naa, oui int, id uint64) string {
	if id == 0 {
		return ""
	}
	if naa != 0 || oui != 0 {
		return fmt.Sprintf("0x%x%06x%09x", naa, oui, id)
	}
	return "0x" + strconv.FormatUint(id, 16)
}

func jsonScalarString(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	return ""
}

func firstNumber(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func putOptional[T any](target map[string]any, key string, value *T) {
	if value != nil {
		target[key] = *value
	}
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
