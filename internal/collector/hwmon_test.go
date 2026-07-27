package collector

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestHWMonCollectNormalizesAndKeepsStableIdentity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "etc/machine-id"), "machine-fixture-01\n")
	chip := filepath.Join(root, "sys/class/hwmon/hwmon0")
	writeFixture(t, filepath.Join(chip, "name"), "coretemp\n")
	writeFixture(t, filepath.Join(chip, "temp1_input"), "42000\n")
	writeFixture(t, filepath.Join(chip, "temp1_label"), "Package id 0\n")
	writeFixture(t, filepath.Join(chip, "power1_input"), "150000000\n")
	writeFixture(t, filepath.Join(chip, "noise1_input"), "not-a-number\n")

	collector := HWMon{SysRoot: root, Host: "edge-01"}
	first, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	second, err := collector.Collect()
	if err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}

	if len(first.Assets) != 1 || len(first.Sensors) != 2 || len(first.Samples) != 2 {
		t.Fatalf("unexpected batch sizes: assets=%d sensors=%d samples=%d", len(first.Assets), len(first.Sensors), len(first.Samples))
	}
	if first.Assets[0].StableID != second.Assets[0].StableID {
		t.Fatalf("host identity changed: %q != %q", first.Assets[0].StableID, second.Assets[0].StableID)
	}

	values := map[string]float64{}
	ids := map[string]string{}
	for index, sensor := range first.Sensors {
		if first.Samples[index].Value == nil {
			t.Fatalf("sample %d has no numeric value", index)
		}
		values[sensor.Category] = *first.Samples[index].Value
		ids[sensor.Category] = sensor.StableID
	}
	if math.Abs(values["temperature"]-42) > 0.0001 {
		t.Fatalf("temperature = %v, want 42", values["temperature"])
	}
	if math.Abs(values["power"]-150) > 0.0001 {
		t.Fatalf("power = %v, want 150", values["power"])
	}
	for _, sensor := range second.Sensors {
		if ids[sensor.Category] != sensor.StableID {
			t.Fatalf("%s identity changed", sensor.Category)
		}
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
