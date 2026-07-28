package action

import (
	"context"
	"strings"
	"testing"

	"github.com/monitored-io/monitored/internal/domain"
)

func TestSplitCommandRejectsShellSyntax(t *testing.T) {
	for _, command := range []string{"smartctl -a /dev/sda; reboot", "x | sh", "x $(id)", "x > /tmp/o"} {
		if _, err := SplitCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
	argv, err := SplitCommand(`smartctl -a "/dev/disk by-id/test"`)
	if err != nil || len(argv) != 3 || argv[2] != "/dev/disk by-id/test" {
		t.Fatalf("unexpected argv %#v, %v", argv, err)
	}
}

func TestImmutableHashDetectsMutationAndRootDefaultsOff(t *testing.T) {
	executor := &Executor{
		AllowedExecutables: map[string]string{"echo": "/bin/echo"},
		AllowedDirectories: []string{"/tmp"},
	}
	request := domain.ActionRequest{
		AssetID: "asset-1", Command: `echo "hello"`, WorkingDirectory: "/tmp",
		Identity: "monitored-action", TimeoutSeconds: 5, Risk: "low",
	}
	hash, err := executor.Hash(request)
	if err != nil {
		t.Fatal(err)
	}
	request.ImmutableHash = hash
	request.Command = `echo "changed"`
	if _, err := executor.Execute(context.Background(), request); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("expected immutable hash mismatch, got %v", err)
	}
	request.Command = `echo "hello"`
	request.Identity = "root"
	if err := executor.Validate(request); err == nil {
		t.Fatal("root action must be disabled by default")
	}
	executor.AllowRoot = true
	request.Risk = "root"
	if err := executor.Validate(request); err != nil {
		t.Fatalf("explicitly enabled root action should validate: %v", err)
	}
	request.Risk = "high"
	if err := executor.Validate(request); err == nil {
		t.Fatal("root identity and risk mismatch must fail")
	}
}
