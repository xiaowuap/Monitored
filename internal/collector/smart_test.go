package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type smartTestRunner struct {
	commands [][]string
	scan     []byte
	device   []byte
}

func (r *smartTestRunner) LookPath(name string) (string, error) {
	if name != "smartctl" {
		return "", errors.New("unexpected tool")
	}
	return "/usr/sbin/smartctl", nil
}

func (r *smartTestRunner) Run(_ context.Context, name string, arguments ...string) (CommandResult, error) {
	r.commands = append(r.commands, append([]string{name}, arguments...))
	if len(arguments) > 0 && arguments[0] == "--scan" {
		return CommandResult{Stdout: r.scan}, nil
	}
	return CommandResult{Stdout: r.device}, nil
}

func TestSMARTCollectUsesScanDeviceTypeAndMappedDeviceRoot(t *testing.T) {
	runner := &smartTestRunner{
		scan: []byte(`{"devices":[
			{"name":"/dev/sda","type":"sat","protocol":"ATA"},
			{"name":"/tmp/not-a-device","type":"sat"}
		]}`),
		device: []byte(`{
			"device":{"name":"/dev/sda","type":"sat","protocol":"ATA"},
			"model_name":"Exos","serial_number":"SERIAL-1",
			"smart_status":{"passed":true},
			"temperature":{"current":39},
			"smartctl":{"exit_status":0}
		}`),
	}
	value := SMART{
		SysRoot: t.TempDir(), DeviceRoot: "/host/dev", Host: "scale-1", Runner: runner,
	}
	batch, err := value.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 {
		t.Fatalf("assets = %d, want 1", len(batch.Assets))
	}
	if batch.Assets[0].StableID != "disk:"+stableHash("SERIAL-1") {
		t.Fatalf("disk identity did not prefer cross-source serial: %q", batch.Assets[0].StableID)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	query := strings.Join(runner.commands[1], " ")
	if !strings.Contains(query, "-d sat /host/dev/sda") {
		t.Fatalf("scan device type or mapped root missing from %q", query)
	}
	if batch.AgentID != "scale-1" {
		t.Fatalf("agent id = %q", batch.AgentID)
	}
}

func TestSMARTStandbyDeviceKeepsSysfsSerialIdentity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root+"/sys/class/block/sda/device/serial", "SLEEPING-SERIAL\n")
	runner := &smartTestRunner{
		scan: []byte(`{"devices":[{"name":"/dev/sda","type":"sat","protocol":"ATA"}]}`),
		device: []byte(`{
			"device":{"name":"/dev/sda","type":"sat","protocol":"ATA"},
			"power_mode":"STANDBY","smartctl":{"exit_status":2}
		}`),
	}
	batch, err := (SMART{SysRoot: root, Host: "scale-1", Runner: runner}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 || batch.Assets[0].StableID != "disk:"+stableHash("SLEEPING-SERIAL") {
		t.Fatalf("standby identity did not use sysfs serial: %#v", batch.Assets)
	}
	for _, sensor := range batch.Sensors {
		if sensor.SourcePath == "/dev/sda/health" || sensor.SourcePath == "/dev/sda/temperature" {
			t.Fatalf("standby-only output fabricated telemetry: %#v", sensor)
		}
	}
}

func TestSMARTParsesCompleteNVMeHealthIncludingZeroCounters(t *testing.T) {
	body := []byte(`{
		"device":{"name":"/dev/nvme0","type":"nvme","protocol":"NVMe"},
		"model_name":"FastDrive","serial_number":"NVME-1","firmware_version":"1.2.3",
		"user_capacity":{"bytes":2000000000000},
		"smart_support":{"available":true,"enabled":true},
		"smart_status":{"passed":true},
		"temperature":{"current":41,"op_limit_max":84},
		"power_on_time":{"hours":1234},
		"power_cycle_count":21,
		"nvme_smart_health_information_log":{
			"critical_warning":0,
			"temperature":42,
			"temperature_sensors":[43,44,0],
			"available_spare":100,
			"available_spare_threshold":10,
			"percentage_used":7,
			"data_units_read":11,
			"data_units_written":12,
			"host_reads":13,
			"host_writes":14,
			"controller_busy_time":15,
			"unsafe_shutdowns":0,
			"media_errors":0,
			"num_err_log_entries":2,
			"warning_temp_time":3,
			"critical_comp_time":0,
			"thermal_mgmt_t1_trans_count":4,
			"thermal_mgmt_t1_total_time":5
		},
		"nvme_self_test_log":{"table":[
			{"num":1,"description":"Short","status":"Completed without error","lifetime_hours":1200}
		]},
		"smartctl":{"exit_status":0}
	}`)
	batch, err := parseSMARTJSON(body, "nvme0", "host:1", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"/dev/nvme0/temperature":           false,
		"/dev/nvme0/temperature/sensor_1":  false,
		"/dev/nvme0/nvme/media_errors":     false,
		"/dev/nvme0/nvme/bytes_read":       false,
		"/dev/nvme0/nvme/percentage_used":  false,
		"/dev/nvme0/nvme/critical_warning": false,
		"/dev/nvme0/self_test/nvme/1":      false,
	}
	for _, sensor := range batch.Sensors {
		if _, ok := required[sensor.SourcePath]; ok {
			required[sensor.SourcePath] = true
		}
	}
	for path, found := range required {
		if !found {
			t.Errorf("missing sensor %s", path)
		}
	}
	for _, sample := range batch.Samples {
		if sample.Value == nil && sample.TextValue == nil {
			t.Fatalf("empty sample emitted: %#v", sample)
		}
	}
}

func TestSMARTParsesATAAndSCSIWithoutFabricatingHealth(t *testing.T) {
	body := []byte(`{
		"device":{"name":"/dev/sdb","type":"scsi","protocol":"SCSI"},
		"scsi_vendor":"SEAGATE","scsi_product":"EXOS","logical_unit_id":123456,
		"temperature":{"current":36,"drive_trip":60},
		"scsi_grown_defect_list":0,
		"scsi_error_counter_log":{
			"read":{"total_uncorrected_errors":0,"gigabytes_processed":"123.5"},
			"write":{"total_uncorrected_errors":2}
		},
		"ata_smart_attributes":{"table":[
			{"id":5,"name":"Reallocated_Sector_Ct","value":100,"worst":100,"thresh":10,
			 "raw":{"value":0,"string":"0"}},
			{"id":194,"name":"Temperature_Celsius","value":36,
			 "raw":{"value":98784247834,"string":"36 (0 23 0 0 0)"}}
		]},
		"ata_smart_error_log":{"summary":{"count":0}}
	}`)
	batch, err := parseSMARTJSON(body, "sdb", "host:1", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := batch.Assets[0].Metadata["logicalUnitId"]; got != "123456" {
		t.Fatalf("logical unit id = %#v", got)
	}
	foundHealth := false
	foundZeroDefects := false
	foundCorrectTemperature := false
	for index, sensor := range batch.Sensors {
		if sensor.SourcePath == "/dev/sdb/health" {
			foundHealth = true
		}
		if sensor.SourcePath == "/dev/sdb/scsi/grown_defect_list" &&
			batch.Samples[index].Value != nil && *batch.Samples[index].Value == 0 {
			foundZeroDefects = true
		}
		if sensor.SourcePath == "/dev/sdb/ata/attribute/194" &&
			batch.Samples[index].Value != nil && *batch.Samples[index].Value == 36 {
			foundCorrectTemperature = true
		}
	}
	if foundHealth {
		t.Fatal("SMART health was emitted even though smart_status was absent")
	}
	if !foundZeroDefects {
		t.Fatal("present zero-valued SCSI defect counter was not emitted")
	}
	if !foundCorrectTemperature {
		t.Fatal("packed ATA temperature raw value was not normalized from raw string")
	}
}
