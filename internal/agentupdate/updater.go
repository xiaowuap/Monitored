package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Update struct {
	TaskID       string
	PackageID    string
	Version      string
	Strategy     string
	TargetOS     string
	Architecture string
	DownloadURL  string
	SHA256       string
	SizeBytes    int64
	Image        string
	ImageDigest  string
}

type Result struct {
	Version           string `json:"version"`
	Strategy          string `json:"strategy"`
	InstalledPath     string `json:"installedPath,omitempty"`
	BackupPath        string `json:"backupPath,omitempty"`
	Image             string `json:"image,omitempty"`
	RestartScheduled  bool   `json:"restartScheduled"`
	ActivationPending bool   `json:"activationPending"`
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type Updater struct {
	Mode                 string
	InstallPath          string
	DockerRestartCommand string
	HTTPClient           *http.Client
	Runner               CommandRunner
	ScheduleRestart      func(strategy, command string)
}

func (u *Updater) Apply(ctx context.Context, update Update) (Result, error) {
	if u == nil || u.Mode == "" || u.Mode == "disabled" {
		return Result{}, errors.New("Agent self-update is disabled by local policy")
	}
	if update.TargetOS != runtime.GOOS || update.Architecture != runtime.GOARCH {
		return Result{}, fmt.Errorf("update targets %s/%s, Agent runs %s/%s", update.TargetOS, update.Architecture, runtime.GOOS, runtime.GOARCH)
	}
	if u.Mode != "auto" && u.Mode != update.Strategy {
		return Result{}, fmt.Errorf("local update policy %q rejects %q updates", u.Mode, update.Strategy)
	}
	switch update.Strategy {
	case "binary":
		return u.applyBinary(ctx, update)
	case "docker":
		return u.applyDocker(ctx, update)
	default:
		return Result{}, fmt.Errorf("unsupported Agent update strategy %q", update.Strategy)
	}
}

func (u *Updater) applyBinary(ctx context.Context, update Update) (Result, error) {
	if update.DownloadURL == "" || len(update.SHA256) != sha256.Size*2 || update.SizeBytes <= 0 {
		return Result{}, errors.New("binary update is missing its download URL, size, or sha256")
	}
	installPath := u.InstallPath
	if installPath == "" {
		var err error
		installPath, err = os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("resolve running Agent executable: %w", err)
		}
	}
	installPath, err := filepath.Abs(installPath)
	if err != nil {
		return Result{}, fmt.Errorf("resolve Agent install path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare Agent install directory: %w", err)
	}
	runner := u.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	currentContext, currentCancel := context.WithTimeout(ctx, 15*time.Second)
	currentVersion, currentErr := runner.Run(currentContext, installPath, "--version")
	currentCancel()
	if currentErr == nil && update.Version != "" && strings.Contains(string(currentVersion), update.Version) {
		return Result{
			Version: update.Version, Strategy: update.Strategy, InstalledPath: installPath,
		}, nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(installPath), ".monitored-agent-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create Agent update staging file: %w", err)
	}
	stagedPath := temporary.Name()
	defer os.Remove(stagedPath)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, update.DownloadURL, nil)
	if err != nil {
		_ = temporary.Close()
		return Result{}, fmt.Errorf("create Agent update download request: %w", err)
	}
	client := u.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	response, err := client.Do(request)
	if err != nil {
		_ = temporary.Close()
		return Result{}, fmt.Errorf("download Agent update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = temporary.Close()
		return Result{}, fmt.Errorf("download Agent update: Master returned %s", response.Status)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, update.SizeBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return Result{}, fmt.Errorf("write Agent update staging file: %w", copyErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close Agent update staging file: %w", closeErr)
	}
	if written != update.SizeBytes {
		return Result{}, fmt.Errorf("Agent update size mismatch: expected %d, received %d", update.SizeBytes, written)
	}
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualHash, update.SHA256) {
		return Result{}, errors.New("Agent update sha256 verification failed")
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return Result{}, fmt.Errorf("mark Agent update executable: %w", err)
	}
	verifyContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	output, verifyErr := runner.Run(verifyContext, stagedPath, "--version")
	cancel()
	if verifyErr != nil {
		return Result{}, fmt.Errorf("validate staged Agent executable: %w", verifyErr)
	}
	if update.Version != "" && !strings.Contains(string(output), update.Version) {
		return Result{}, fmt.Errorf("staged Agent reports an unexpected version: %s", strings.TrimSpace(string(output)))
	}

	backupPath := installPath + ".previous"
	_ = os.Remove(backupPath)
	if err := os.Rename(installPath, backupPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("back up current Agent executable: %w", err)
	}
	if err := os.Rename(stagedPath, installPath); err != nil {
		_ = os.Rename(backupPath, installPath)
		return Result{}, fmt.Errorf("activate Agent update: %w", err)
	}
	if err := syncDirectory(filepath.Dir(installPath)); err != nil {
		_ = os.Remove(installPath)
		_ = os.Rename(backupPath, installPath)
		return Result{}, err
	}
	restartScheduled := u.ScheduleRestart != nil
	if restartScheduled {
		u.ScheduleRestart("binary", "")
	}
	return Result{
		Version: update.Version, Strategy: update.Strategy, InstalledPath: installPath,
		BackupPath: backupPath, RestartScheduled: restartScheduled,
		ActivationPending: !restartScheduled,
	}, nil
}

func (u *Updater) applyDocker(ctx context.Context, update Update) (Result, error) {
	reference := update.Image
	if update.ImageDigest != "" {
		separator := strings.LastIndex(reference, "@")
		if separator >= 0 {
			if !strings.EqualFold(reference[separator+1:], update.ImageDigest) {
				return Result{}, errors.New("Docker image and task digest do not match")
			}
		} else {
			reference += "@" + update.ImageDigest
		}
	}
	if reference == "" || !strings.Contains(reference, "@sha256:") {
		return Result{}, errors.New("Docker update requires an immutable image digest")
	}
	runner := u.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if _, err := runner.Run(ctx, "docker", "pull", reference); err != nil {
		return Result{}, fmt.Errorf("pull Agent image: %w", err)
	}
	restartScheduled := u.ScheduleRestart != nil && strings.TrimSpace(u.DockerRestartCommand) != ""
	if restartScheduled {
		u.ScheduleRestart("docker", u.DockerRestartCommand)
	}
	return Result{
		Version: update.Version, Strategy: update.Strategy, Image: reference,
		RestartScheduled: restartScheduled, ActivationPending: !restartScheduled,
	}, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Agent install directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync Agent install directory: %w", err)
	}
	return nil
}
