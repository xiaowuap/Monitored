package agentupdate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBinaryUpdateVerifiesAndAtomicallyReplacesExecutable(t *testing.T) {
	directory := t.TempDir()
	installPath := filepath.Join(directory, "monitored-agent")
	if err := os.WriteFile(installPath, []byte("#!/bin/sh\necho monitored-agent old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("#!/bin/sh\necho monitored-agent 1.2.3\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	sum := sha256.Sum256(body)
	var restarted bool
	updater := &Updater{
		Mode: "binary", InstallPath: installPath,
		ScheduleRestart: func(strategy, command string) {
			restarted = strategy == "binary" && command == ""
		},
	}
	result, err := updater.Apply(context.Background(), Update{
		Version: "1.2.3", Strategy: "binary", TargetOS: runtime.GOOS,
		Architecture: runtime.GOARCH, DownloadURL: server.URL,
		SHA256: fmt.Sprintf("%x", sum), SizeBytes: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(installPath + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(body) || !strings.Contains(string(backup), "old") {
		t.Fatalf("current=%q backup=%q", current, backup)
	}
	if !restarted || !result.RestartScheduled || result.ActivationPending {
		t.Fatalf("unexpected update result: %+v restarted=%v", result, restarted)
	}
}

type recordingRunner struct {
	name      string
	arguments []string
}

func (r *recordingRunner) Run(_ context.Context, name string, arguments ...string) ([]byte, error) {
	r.name = name
	r.arguments = append([]string(nil), arguments...)
	return nil, nil
}

func TestDockerUpdateUsesImmutableDigestAndLocalRestartCommand(t *testing.T) {
	runner := &recordingRunner{}
	var restartCommand string
	updater := &Updater{
		Mode: "docker", Runner: runner,
		DockerRestartCommand: "local-recreate-agent",
		ScheduleRestart: func(strategy, command string) {
			if strategy == "docker" {
				restartCommand = command
			}
		},
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	result, err := updater.Apply(context.Background(), Update{
		Version: "1.2.3", Strategy: "docker", TargetOS: runtime.GOOS,
		Architecture: runtime.GOARCH, Image: "registry.example/agent:1.2.3",
		ImageDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "docker" || len(runner.arguments) != 2 ||
		runner.arguments[0] != "pull" || !strings.HasSuffix(runner.arguments[1], "@"+digest) {
		t.Fatalf("unexpected Docker pull: %s %v", runner.name, runner.arguments)
	}
	if restartCommand != "local-recreate-agent" || !result.RestartScheduled {
		t.Fatalf("unexpected restart result: %+v command=%q", result, restartCommand)
	}
}
