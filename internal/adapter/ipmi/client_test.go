package ipmi

import "testing"

func TestRecordedSDRAndSELMapping(t *testing.T) {
	batch := mapReadings("bmc.test", `
CPU1 Temp        | 54 degrees C      | ok
System Fan 1     | 4200 RPM          | ok
12V              | 12.08 Volts       | ok
PSU Input Power  | 311 Watts         | ok
Presence         | 0x01              | ok
`, "0001 | 07/28/2026 | 12:00:00 | Temperature #0x01 | Upper Critical going high\n", nil)
	if len(batch.Assets) != 1 || len(batch.Sensors) != 5 || len(batch.Samples) != 5 {
		t.Fatalf("unexpected IPMI mapping: %#v", batch)
	}
	if batch.Sensors[0].Category != "temperature" || batch.Sensors[1].Unit != "rpm" {
		t.Fatalf("unexpected sensor semantics: %#v", batch.Sensors)
	}
}
