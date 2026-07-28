// Package action validates and executes an already approved immutable action.
// It never invokes a shell and rejects shell control syntax.
package action

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/extension"
)

type Canonical struct {
	AssetID          string            `json:"assetId"`
	Argv             []string          `json:"argv"`
	WorkingDirectory string            `json:"workingDirectory"`
	Environment      map[string]string `json:"environment"`
	Identity         string            `json:"identity"`
	TimeoutSeconds   int               `json:"timeoutSeconds"`
}

type Executor struct {
	AllowedExecutables map[string]string
	AllowedDirectories []string
	AllowRoot          bool
	MaxOutput          int
}

func Default() *Executor {
	return &Executor{
		AllowedExecutables: map[string]string{
			"camcontrol": "/sbin/camcontrol",
			"ipmitool":   "/usr/bin/ipmitool",
			"mlxlink":    "/usr/bin/mlxlink",
			"modprobe":   "/sbin/modprobe",
			"nvme":       "/usr/sbin/nvme",
			"smartctl":   "/usr/sbin/smartctl",
			"systemctl":  "/usr/bin/systemctl",
			"zpool":      "/usr/sbin/zpool",
		},
		AllowedDirectories: []string{"/", "/tmp", "/var/tmp"},
		MaxOutput:          1 << 20,
	}
}

func (e *Executor) Name() string { return "restricted-local" }

func (e *Executor) Validate(request domain.ActionRequest) error {
	_, err := e.Canonicalize(request)
	return err
}

func (e *Executor) Canonicalize(request domain.ActionRequest) (Canonical, error) {
	argv, err := SplitCommand(request.Command)
	if err != nil {
		return Canonical{}, err
	}
	if len(argv) == 0 {
		return Canonical{}, errors.New("command is empty")
	}
	configured, allowed := e.AllowedExecutables[filepath.Base(argv[0])]
	if !allowed {
		return Canonical{}, fmt.Errorf("executable %q is not allowlisted", filepath.Base(argv[0]))
	}
	resolved := configured
	if resolved == "" {
		resolved, err = exec.LookPath(argv[0])
		if err != nil {
			return Canonical{}, fmt.Errorf("resolve executable: %w", err)
		}
	}
	argv[0] = filepath.Clean(resolved)

	workingDirectory := filepath.Clean(request.WorkingDirectory)
	if !filepath.IsAbs(workingDirectory) || !withinAllowed(workingDirectory, e.AllowedDirectories) {
		return Canonical{}, errors.New("working directory is outside the allowlist")
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > 3600 {
		return Canonical{}, errors.New("timeout must be between 1 and 3600 seconds")
	}
	if (request.Identity == "root" || request.Risk == "root") && !e.AllowRoot {
		return Canonical{}, errors.New("root actions are disabled")
	}
	identity := strings.TrimSpace(request.Identity)
	if identity == "" {
		identity = "monitored-action"
	}
	if (identity == "root") != (request.Risk == "root") {
		return Canonical{}, errors.New("root identity and root risk must be selected together")
	}
	environment := make(map[string]string, len(request.Environment))
	for key, value := range request.Environment {
		if key != "LANG" && key != "LC_ALL" && key != "TZ" {
			return Canonical{}, fmt.Errorf("environment variable %q is not allowlisted", key)
		}
		if strings.ContainsRune(value, 0) {
			return Canonical{}, fmt.Errorf("environment variable %q contains NUL", key)
		}
		environment[key] = value
	}
	return Canonical{
		AssetID:          request.AssetID,
		Argv:             argv,
		WorkingDirectory: workingDirectory,
		Environment:      environment,
		Identity:         identity,
		TimeoutSeconds:   request.TimeoutSeconds,
	}, nil
}

func (e *Executor) Hash(request domain.ActionRequest) (string, error) {
	canonical, err := e.Canonicalize(request)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (e *Executor) Execute(ctx context.Context, request domain.ActionRequest) (extension.ActionExecution, error) {
	started := time.Now().UTC()
	canonical, err := e.Canonicalize(request)
	if err != nil {
		return extension.ActionExecution{}, err
	}
	actualHash, err := e.Hash(request)
	if err != nil {
		return extension.ActionExecution{}, err
	}
	if request.ImmutableHash == "" || !strings.EqualFold(request.ImmutableHash, actualHash) {
		return extension.ActionExecution{}, errors.New("immutable action hash mismatch")
	}
	runContext, cancel := context.WithTimeout(ctx, time.Duration(canonical.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(runContext, canonical.Argv[0], canonical.Argv[1:]...)
	command.Dir = canonical.WorkingDirectory
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	keys := make([]string, 0, len(canonical.Environment))
	for key := range canonical.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+canonical.Environment[key])
	}
	credential, err := executionCredential(canonical.Identity)
	if err != nil {
		return extension.ActionExecution{}, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: credential}
	var stdout, stderr cappedBuffer
	limit := e.MaxOutput
	if limit <= 0 {
		limit = 1 << 20
	}
	stdout.limit, stderr.limit = limit, limit
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	finished := time.Now().UTC()
	result := extension.ActionExecution{
		Stdout: string(stdout.value), Stderr: string(stderr.value),
		StartedAt: started, FinishedAt: finished,
	}
	if exitError := (*exec.ExitError)(nil); errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	}
	if errors.Is(runContext.Err(), context.DeadlineExceeded) {
		return result, errors.New("action timed out")
	}
	if err != nil {
		return result, fmt.Errorf("action failed: %w", err)
	}
	return result, nil
}

func executionCredential(identity string) (*syscall.Credential, error) {
	effectiveUID := os.Geteuid()
	if identity == "root" {
		if effectiveUID != 0 {
			return nil, errors.New("root action requires a root-capable Agent process")
		}
		return nil, nil
	}
	if identity == "monitored-action" && effectiveUID != 0 {
		// A non-root Agent already provides the low-privilege boundary. The
		// logical identity remains part of the immutable approval hash.
		return nil, nil
	}
	account, err := user.Lookup(identity)
	if err != nil {
		return nil, fmt.Errorf("resolve execution identity %q: %w", identity, err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse execution UID: %w", err)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("parse execution GID: %w", err)
	}
	if effectiveUID != 0 {
		if uint64(effectiveUID) != uid {
			return nil, errors.New("non-root Agent cannot switch execution identity")
		}
		return nil, nil
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}

func SplitCommand(value string) ([]string, error) {
	var result []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for _, char := range value {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case ' ', '\t', '\r', '\n':
			flush()
		case ';', '|', '&', '>', '<', '`', '$', '(', ')':
			return nil, fmt.Errorf("shell control character %q is forbidden", char)
		default:
			current.WriteRune(char)
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated escape or quote")
	}
	flush()
	return result, nil
}

func NewOneTimeToken() (plain, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(sum[:]), nil
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func withinAllowed(path string, roots []string) bool {
	sort.Strings(roots)
	for _, root := range roots {
		root = filepath.Clean(root)
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

type cappedBuffer struct {
	value []byte
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	count := len(value)
	if len(b.value) < b.limit {
		remaining := b.limit - len(b.value)
		b.value = append(b.value, value[:min(len(value), remaining)]...)
	}
	return count, nil
}
