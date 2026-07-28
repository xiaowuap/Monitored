// Package extension defines the stable seams used by optional Monitored
// integrations. Implementations can live in-tree or in a separately deployed
// adapter without coupling the core domain to a vendor SDK.
package extension

import (
	"context"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Collector interface {
	Name() string
	Collect(context.Context) (domain.IngestBatch, error)
}

type DiscoveryRequest struct {
	AgentID string
	Kind    string
	Targets []string
	Options map[string]any
}

type Discovery interface {
	Name() string
	Discover(context.Context, DiscoveryRequest) (domain.IngestBatch, error)
}

type Normalizer interface {
	Name() string
	Normalize(domain.IngestSensor) (domain.IngestSensor, error)
}

type Notification struct {
	Title      string
	Body       string
	Severity   string
	DedupKey   string
	OccurredAt time.Time
	Labels     map[string]string
}

type Notifier interface {
	Name() string
	Send(context.Context, Notification) error
}

type DiagnosisRequest struct {
	Incident domain.Incident
	Assets   []domain.Asset
	Alerts   []domain.Alert
	Metrics  map[string][]domain.MetricPoint
	Context  map[string]any
}

type Diagnosis struct {
	Summary    string
	Evidence   []string
	Confidence float64
	Model      string
}

type LabelSuggestionRequest struct {
	Sensor   domain.Sensor
	Asset    domain.Asset
	Adjacent []domain.Sensor
	Context  map[string]any
}

type LabelSuggestion struct {
	DisplayName string
	AssetKind   string
	Category    string
	Unit        string
	Confidence  float64
	Evidence    []string
}

type LLMProvider interface {
	Name() string
	Diagnose(context.Context, DiagnosisRequest) (Diagnosis, error)
}

type ActionExecution struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	StartedAt  time.Time
	FinishedAt time.Time
}

type ActionExecutor interface {
	Name() string
	Validate(domain.ActionRequest) error
	Execute(context.Context, domain.ActionRequest) (ActionExecution, error)
}
