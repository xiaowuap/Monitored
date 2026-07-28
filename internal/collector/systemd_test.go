package collector

import (
	"context"
	"testing"
)

type systemdFixtureRunner struct {
	stdout string
}

func (r systemdFixtureRunner) LookPath(string) (string, error) {
	return "/usr/bin/systemctl", nil
}

func (r systemdFixtureRunner) Run(context.Context, string, ...string) (CommandResult, error) {
	return CommandResult{Stdout: []byte(r.stdout)}, nil
}

func TestSystemdRecordedFixture(t *testing.T) {
	collector := Systemd{Host: "edge-01", Units: []string{"monitored.service"}, Runner: systemdFixtureRunner{
		stdout: "Id=monitored.service\nDescription=Monitored Agent\nLoadState=loaded\nActiveState=active\nSubState=running\nNRestarts=2\n",
	}}
	batch, err := collector.Collect(context.Background())
	if err != nil || len(batch.Assets) != 1 || len(batch.Sensors) != 2 || len(batch.Samples) != 2 {
		t.Fatalf("unexpected systemd batch %#v, %v", batch, err)
	}
}
