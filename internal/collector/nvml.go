package collector

import (
	"context"
	"errors"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

var errNVMLUnavailable = errors.New("NVML library is unavailable")

// NVML prefers direct, dynamically loaded NVML calls. A nvidia-smi collector
// remains an explicit read-only compatibility fallback for static Agent builds
// or hosts without libnvidia-ml.
type NVML struct {
	Host     string
	Fallback NVIDIA
}

func (c NVML) Name() string            { return "nvidia" }
func (c NVML) Interval() time.Duration { return 10 * time.Second }
func (c NVML) Capability(ctx context.Context) Capability {
	if nvmlAvailable() {
		return Capability{
			Name: c.Name(), State: "available", Detail: "Direct dynamically loaded NVML",
			Enhancement: dcgmEnhancement(c.Fallback.runner()),
		}
	}
	fallback := c.Fallback
	fallback.Host = c.Host
	value := fallback.Capability(ctx)
	if value.State == "available" {
		value.State = "degraded"
		value.Detail = "NVML unavailable; using nvidia-smi compatibility fallback"
	}
	return value
}

func (c NVML) Collect(ctx context.Context) (domain.IngestBatch, error) {
	batch, err := collectNVML(c.Host)
	if err == nil {
		return batch, nil
	}
	fallback := c.Fallback
	fallback.Host = c.Host
	value, fallbackErr := fallback.Collect(ctx)
	if fallbackErr != nil {
		return value, errors.Join(err, fallbackErr)
	}
	return value, nil
}

func dcgmEnhancement(runner Runner) string {
	if _, err := runner.LookPath("dcgmi"); err == nil {
		return "DCGM detected for health, profiling, NVLink and NvSwitch augmentation"
	}
	return "Install DCGM for health, profiling, NVLink and NvSwitch augmentation"
}
