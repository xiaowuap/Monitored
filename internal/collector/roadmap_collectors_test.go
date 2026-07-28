package collector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type inventoryFixtureRunner struct {
	output []byte
}

func (r inventoryFixtureRunner) LookPath(string) (string, error) {
	if r.output == nil {
		return "", errors.New("not installed")
	}
	return "/usr/sbin/devlink", nil
}

func (r inventoryFixtureRunner) Run(context.Context, string, ...string) (CommandResult, error) {
	return CommandResult{Stdout: r.output}, nil
}

func TestSMARTParsesNVMeAndKeepsUnsupportedOut(t *testing.T) {
	payload := []byte(`{
		"serial_number":"NVME-1",
		"model_name":"FastDrive",
		"temperature":{"current":41},
		"smart_status":{"passed":true},
		"nvme_smart_health_information_log":{
			"temperature":42,
			"percentage_used":7,
			"media_errors":3,
			"power_on_hours":1234
		}
	}`)
	batch, err := parseSMARTJSON(payload, "nvme0n1", "host:1", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 || len(batch.Samples) < 4 {
		t.Fatalf("unexpected SMART batch assets=%d samples=%d", len(batch.Assets), len(batch.Samples))
	}
	for _, sample := range batch.Samples {
		if sample.Value == nil {
			t.Fatalf("SMART emitted an unavailable empty value: %#v", sample)
		}
	}
}

func TestZFSListAndFreeBSDNormalization(t *testing.T) {
	zfs, err := parseZPoolList([]byte("tank\t1000\t400\t600\tONLINE\t12\n"), "nas-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(zfs.Assets) != 1 || len(zfs.Samples) != 6 {
		t.Fatalf("unexpected ZFS output %#v", zfs)
	}
	freebsd := parseFreeBSDSysctl([]byte("hw.model: AMD EPYC\n dev.cpu.0.temperature: 47.5C\n"), "core-1")
	if len(freebsd.Assets) != 1 || freebsd.Assets[0].Model != "AMD EPYC" || len(freebsd.Samples) != 1 {
		t.Fatalf("unexpected FreeBSD output %#v", freebsd)
	}
}

func TestZFSVdevDatasetSnapshotAndARC(t *testing.T) {
	status := parseZPoolStatus([]byte(`
  pool: tank
 state: DEGRADED
  scan: scrub repaired 128K in 00:01:04 with 2 errors
config:

        NAME            STATE     READ WRITE CKSUM
        tank            DEGRADED     0     0     0
          mirror-0      DEGRADED     0     0     0
            /dev/sda    ONLINE       1     0     2
            /dev/sdb    ONLINE       0     1     0

errors: Permanent errors have been detected
`), "nas-1")
	if len(status.Assets) != 3 || len(status.Samples) != 9 {
		t.Fatalf("unexpected vdev status output: assets=%d samples=%d", len(status.Assets), len(status.Samples))
	}
	if status.Assets[1].Metadata["scan"] == "" {
		t.Fatal("scrub evidence was not retained")
	}

	datasets := parseZFSList([]byte("tank/data\tfilesystem\t100\t900\t90\t1000\t/mnt/data\n"), "nas-1")
	if len(datasets.Assets) != 1 || len(datasets.Samples) != 4 {
		t.Fatalf("unexpected dataset output: %#v", datasets)
	}
	snapshots := parseZFSSnapshots([]byte("tank/data@snap-1\t42\t1720000000\n"), "nas-1")
	if len(snapshots.Assets) != 1 || snapshots.Assets[0].Kind != "snapshot" {
		t.Fatalf("unexpected snapshot output: %#v", snapshots)
	}

	root := t.TempDir()
	writeRoadmapFixture(t, root, "proc/spl/kstat/zfs/arcstats", "name type data\nsize 4 2048\nhits 4 12\nmisses 4 3\n")
	arc := (ZFS{Host: "nas-1", SysRoot: root}).collectARC()
	if len(arc.Samples) != 3 {
		t.Fatalf("unexpected ARC samples: %#v", arc.Samples)
	}
}

func TestNVIDIAUnavailableFieldsAreNotWrittenAsZero(t *testing.T) {
	fields := []string{"uuid", "name", "temperature.gpu", "temperature.memory", "power.draw"}
	batch, err := parseNvidiaSMI([]byte("GPU-1, A40, 68, N/A, [Not Supported]\n"), fields, "gpu-host")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 || len(batch.Samples) != 1 {
		t.Fatalf("unsupported NVIDIA fields became samples: %#v", batch.Samples)
	}
	if *batch.Samples[0].Value != 68 {
		t.Fatalf("unexpected NVIDIA temperature %#v", batch.Samples[0])
	}
}

func TestMellanoxAndIPMIFixtures(t *testing.T) {
	mellanox := parseMGetTemp([]byte("Device 0 temperature: 54.25 C\n"), "edge")
	if len(mellanox.Assets) != 1 || len(mellanox.Samples) != 1 {
		t.Fatalf("unexpected MFT output %#v", mellanox)
	}
	ipmi := parseIPMISensors([]byte("CPU Temp,43,degrees C,ok\nFAN1,7200,RPM,ok\n"), "edge")
	if len(ipmi.Samples) != 2 {
		t.Fatalf("unexpected IPMI output %#v", ipmi)
	}
}

func TestLinuxInventoryHotplugSources(t *testing.T) {
	root := t.TempDir()
	writeRoadmapFixture(t, root, "etc/machine-id", "machine-1\n")
	writeRoadmapFixture(t, root, "sys/class/thermal/thermal_zone0/type", "x86_pkg_temp\n")
	writeRoadmapFixture(t, root, "sys/class/thermal/thermal_zone0/temp", "53000\n")
	writeRoadmapFixture(t, root, "sys/class/net/eth0/address", "02:00:00:00:00:01\n")
	writeRoadmapFixture(t, root, "sys/class/net/eth0/operstate", "up\n")
	writeRoadmapFixture(t, root, "sys/class/net/eth0/speed", "10000\n")
	writeRoadmapFixture(t, root, "sys/bus/usb/devices/1-2/idVendor", "0781\n")
	writeRoadmapFixture(t, root, "sys/bus/usb/devices/1-2/idProduct", "5581\n")
	writeRoadmapFixture(t, root, "sys/bus/usb/devices/1-2/serial", "USB-1\n")
	writeRoadmapFixture(t, root, "sys/devices/system/edac/mc/mc0/ce_count", "7\n")
	writeRoadmapFixture(t, root, "sys/devices/system/edac/mc/mc0/ue_count", "0\n")
	writeRoadmapFixture(t, root, "sys/class/drm/card0/device/vendor", "0x1002\n")
	writeRoadmapFixture(t, root, "sys/class/drm/card0/device/device", "0x744c\n")
	writeRoadmapFixture(t, root, "sys/class/drm/card0/device/serial", "DRM-GPU-1\n")
	writeRoadmapFixture(t, root, "sys/bus/pci/devices/0000:01:00.0/vendor", "0x15b3\n")
	writeRoadmapFixture(t, root, "sys/bus/pci/devices/0000:01:00.0/device", "0x1019\n")
	writeRoadmapFixture(t, root, "sys/bus/pci/devices/0000:01:00.0/class", "0x020000\n")
	writeRoadmapFixture(t, root, "sys/bus/pci/devices/0000:01:00.0/serial", "NIC-SERIAL\n")
	batch, err := (LinuxInventory{
		SysRoot: root,
		Host:    "edge",
		Runner: inventoryFixtureRunner{output: []byte(`{
			"info":{"pci/0000:01:00.0":{
				"driver":"mlx5_core","serial_number":"NIC-SERIAL",
				"versions":{"running":{"fw":"28.43.1014"}}
			}}
		}`)},
	}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) < 6 || len(batch.Samples) < 3 {
		t.Fatalf("inventory missed fixture assets/samples: assets=%d samples=%d", len(batch.Assets), len(batch.Samples))
	}
	var usb, edac, drm, devlink bool
	for _, asset := range batch.Assets {
		usb = usb || asset.Kind == "usb_device"
		edac = edac || asset.Kind == "memory_controller"
		drm = drm || asset.Metadata["drmCard"] == "card0"
		devlink = devlink || asset.Metadata["driver"] == "mlx5_core"
	}
	if !usb || !edac || !drm || !devlink {
		t.Fatalf("missing USB/EDAC/DRM/devlink assets: usb=%v edac=%v drm=%v devlink=%v", usb, edac, drm, devlink)
	}
}

func TestPCIStableIdentitySurvivesAddressMoveWithSerial(t *testing.T) {
	collect := func(address string) string {
		root := t.TempDir()
		writeRoadmapFixture(t, root, "etc/machine-id", "machine-1\n")
		base := filepath.Join("sys/bus/pci/devices", address)
		writeRoadmapFixture(t, root, filepath.Join(base, "vendor"), "0x10de\n")
		writeRoadmapFixture(t, root, filepath.Join(base, "device"), "0x2235\n")
		writeRoadmapFixture(t, root, filepath.Join(base, "class"), "0x030000\n")
		writeRoadmapFixture(t, root, filepath.Join(base, "serial"), "GPU-STABLE-1\n")
		batch, err := (LinuxInventory{
			SysRoot: root, Host: "edge", Runner: inventoryFixtureRunner{},
		}).Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, asset := range batch.Assets {
			if asset.Kind == "gpu" {
				return asset.StableID
			}
		}
		t.Fatal("GPU asset missing")
		return ""
	}
	if before, after := collect("0000:01:00.0"), collect("0000:41:00.0"); before != after {
		t.Fatalf("PCI address move changed stable identity: %s != %s", before, after)
	}
}

func writeRoadmapFixture(t *testing.T, root, relative, value string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
