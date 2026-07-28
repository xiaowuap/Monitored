package sshclient

import (
	"context"
	"testing"

	"github.com/monitored-io/monitored/internal/domain"
)

func TestParsersKeepReadOnlySSHOutputStructured(t *testing.T) {
	var batch domain.IngestBatch
	appendSysctl(&batch, "host:1", "dev.cpu.0.temperature: 54.0C\nhw.acpi.thermal.tz0.temperature: 310.2K\nfan.0.rpm: 4200\n")
	if len(batch.Sensors) != 2 || len(batch.Samples) != 2 {
		t.Fatalf("unexpected sysctl mapping: %#v", batch)
	}
	appendPools(&batch, "host:1", "tank\t1000\t400\t600\tONLINE\n")
	if len(batch.Assets) != 1 || len(batch.Sensors) != 5 {
		t.Fatalf("unexpected pool mapping: %#v", batch)
	}
}

func TestSSHRequiresPinnedHostKey(t *testing.T) {
	_, err := (Client{Address: "127.0.0.1:22", Username: "root", Password: "x"}).connect(context.Background())
	if err == nil {
		t.Fatal("expected missing host key rejection")
	}
}
