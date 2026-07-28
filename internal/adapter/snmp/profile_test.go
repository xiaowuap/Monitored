package snmp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndMapProfile(t *testing.T) {
	profile, err := LoadProfile(filepath.Join("..", "..", "..", "profiles", "snmp", "example-ups.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Map(profile, "ups.local", []Value{
		{OID: ".1.3.6.1.2.1.33.1.1.2.0", Value: "SRT-5000"},
		{OID: "1.3.6.1.2.1.33.1.2.4.0", Value: 42},
		{OID: "1.3.6.1.2.1.33.1.2.3.0", Value: 24},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 || len(batch.Samples) != 2 {
		t.Fatalf("unexpected mapped batch %#v", batch)
	}
	if *batch.Samples[1].Value != 1440 {
		t.Fatalf("transform was not applied: %#v", batch.Samples[1])
	}
}

func TestProfileRejectsUnknownFieldsAndInvalidOID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte(`
apiVersion: monitored.io/v1alpha1
kind: SNMPProfile
metadata: {name: bad-profile}
spec:
  matches: {}
  entities:
    - key: x
      assetKind: snmp
      stableIdentity: [{oid: "not-an-oid"}]
  sensors:
    - {id: x, entity: x, oid: "1.2.3", name: x, category: unknown, unit: "1"}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(path); err == nil {
		t.Fatal("invalid identity OID must be rejected")
	}
}

func TestProfileMapsTableIndexesWithoutExecutingExpressions(t *testing.T) {
	profile := Profile{
		APIVersion: "monitored.io/v1alpha1", Kind: "SNMPProfile",
		Metadata: Metadata{Name: "table-profile"},
		Spec: Spec{
			Entities: []Entity{{Key: "device", AssetKind: "snmp", StableIdentity: []Identity{{OID: "1.3.6.1.2.1.1.2.0"}}}},
			Sensors: []Sensor{{ID: "if-in", Entity: "device", OID: "1.3.6.1.2.1.2.2.1.10", Name: "ifInOctets", Category: "throughput", Unit: "By", Monotonic: true}},
		},
	}
	batch, err := Map(profile, "switch", []Value{
		{OID: "1.3.6.1.2.1.1.2.0", Value: "1.3.6.1.4.1.9"},
		{OID: "1.3.6.1.2.1.2.2.1.10.1", Value: 100},
		{OID: "1.3.6.1.2.1.2.2.1.10.2", Value: 200},
	})
	if err != nil || len(batch.Samples) != 2 || batch.Sensors[1].Metadata["index"] != "2" {
		t.Fatalf("table mapping failed %#v, %v", batch, err)
	}
}
