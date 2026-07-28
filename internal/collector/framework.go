package collector

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Capability struct {
	Name        string
	State       string
	Detail      string
	Enhancement string
}

type Scheduled interface {
	Name() string
	Interval() time.Duration
	Capability(context.Context) Capability
	Collect(context.Context) (domain.IngestBatch, error)
}

type Registry struct {
	mu         sync.RWMutex
	collectors map[string]Scheduled
}

func NewRegistry(collectors ...Scheduled) *Registry {
	registry := &Registry{collectors: make(map[string]Scheduled)}
	for _, item := range collectors {
		registry.collectors[item.Name()] = item
	}
	return registry
}

func (r *Registry) Register(item Scheduled) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item == nil || strings.TrimSpace(item.Name()) == "" {
		return errors.New("collector name is required")
	}
	if _, exists := r.collectors[item.Name()]; exists {
		return fmt.Errorf("collector %q is already registered", item.Name())
	}
	r.collectors[item.Name()] = item
	return nil
}

func (r *Registry) All() []Scheduled {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Scheduled, 0, len(r.collectors))
	for _, item := range r.collectors {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
	return items
}

func (r *Registry) Names() []string {
	items := r.All()
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	return names
}

func (r *Registry) Collect(ctx context.Context, selected []string) (domain.IngestBatch, []error) {
	allow := make(map[string]struct{}, len(selected))
	for _, name := range selected {
		allow[name] = struct{}{}
	}
	var merged domain.IngestBatch
	var errs []error
	for _, item := range r.All() {
		if len(allow) > 0 {
			if _, ok := allow[item.Name()]; !ok {
				continue
			}
		}
		batch, err := item.Collect(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", item.Name(), err))
			continue
		}
		merged = Merge(merged, batch)
	}
	return merged, errs
}

func Merge(left, right domain.IngestBatch) domain.IngestBatch {
	if left.AgentID == "" {
		left.AgentID = right.AgentID
	}
	left.Assets = appendUniqueAssets(left.Assets, right.Assets)
	left.Sensors = appendUniqueSensors(left.Sensors, right.Sensors)
	left.Samples = append(left.Samples, right.Samples...)
	return left
}

func appendUniqueAssets(left, right []domain.IngestAsset) []domain.IngestAsset {
	index := make(map[string]int, len(left)+len(right))
	for offset, item := range left {
		index[item.StableID] = offset
	}
	for _, item := range right {
		if offset, ok := index[item.StableID]; ok {
			left[offset].Metadata = mergeMap(left[offset].Metadata, item.Metadata)
			if left[offset].Name == "" {
				left[offset].Name = item.Name
			}
			continue
		}
		index[item.StableID] = len(left)
		left = append(left, item)
	}
	return left
}

func appendUniqueSensors(left, right []domain.IngestSensor) []domain.IngestSensor {
	index := make(map[string]int, len(left)+len(right))
	for offset, item := range left {
		index[item.StableID] = offset
	}
	for _, item := range right {
		if offset, ok := index[item.StableID]; ok {
			left[offset].Metadata = mergeMap(left[offset].Metadata, item.Metadata)
			continue
		}
		index[item.StableID] = len(left)
		left = append(left, item)
	}
	return left
}

func mergeMap(left, right map[string]any) map[string]any {
	if left == nil {
		left = make(map[string]any)
	}
	for key, value := range right {
		left[key] = value
	}
	return left
}

type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) (CommandResult, error)
}

type ExecRunner struct {
	MaxOutput int
}

func (r ExecRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (r ExecRunner) Run(ctx context.Context, name string, arguments ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr cappedBuffer
	limit := r.MaxOutput
	if limit <= 0 {
		limit = 4 << 20
	}
	stdout.limit = limit
	stderr.limit = limit
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.bytes, Stderr: stderr.bytes}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, fmt.Errorf("%s exited with status %d", name, result.ExitCode)
	}
	return result, err
}

type cappedBuffer struct {
	bytes []byte
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(b.bytes) < b.limit {
		remaining := b.limit - len(b.bytes)
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.bytes = append(b.bytes, value...)
	}
	return original, nil
}

type HWMonScheduled struct {
	HWMon
}

func (c HWMonScheduled) Name() string            { return "linux-hwmon" }
func (c HWMonScheduled) Interval() time.Duration { return 10 * time.Second }
func (c HWMonScheduled) Capability(context.Context) Capability {
	return Capability{Name: c.Name(), State: "available", Detail: "Linux sysfs hwmon raw channels"}
}
func (c HWMonScheduled) Collect(context.Context) (domain.IngestBatch, error) {
	return c.HWMon.Collect()
}
