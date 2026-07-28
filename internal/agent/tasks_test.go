package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	monitoredv1 "github.com/monitored-io/monitored/gen/monitored/v1"
	"github.com/monitored-io/monitored/internal/collector"
	"github.com/monitored-io/monitored/internal/domain"
)

type taskFixtureCollector struct{}

func (taskFixtureCollector) Name() string            { return "fixture" }
func (taskFixtureCollector) Interval() time.Duration { return time.Minute }
func (taskFixtureCollector) Capability(context.Context) collector.Capability {
	return collector.Capability{Name: "fixture", State: "available"}
}
func (taskFixtureCollector) Collect(context.Context) (domain.IngestBatch, error) {
	value := 42.0
	return domain.IngestBatch{
		Assets: []domain.IngestAsset{{StableID: "host:fixture", Kind: "host", Name: "fixture"}},
		Sensors: []domain.IngestSensor{{
			StableID: "sensor:unknown", AssetStableID: "host:fixture",
			Source: "fixture", SourcePath: "fixture/temp1", RawName: "temp1",
			Category: "unknown", Unit: "Cel", Scale: 1, Quality: "good",
		}},
		Samples: []domain.IngestSample{{
			SensorStableID: "sensor:unknown", Value: &value,
			Source: "fixture", Quality: "good",
		}},
	}, nil
}

func TestDiscoveryTaskPublishesSnapshotBeforeSuccess(t *testing.T) {
	var published domain.IngestBatch
	handler := TaskHandler{
		Collectors: collector.NewRegistry(taskFixtureCollector{}),
		Publish: func(_ context.Context, batch domain.IngestBatch) error {
			published = batch
			return nil
		},
	}
	result := handler.Handle(context.Background(), &monitoredv1.MasterEnvelope{
		Payload: &monitoredv1.MasterEnvelope_Discovery{Discovery: &monitoredv1.DiscoveryTask{
			TaskId: "task-1", Collectors: []string{"fixture"},
		}},
	})
	if result == nil || result.Status != "succeeded" || len(published.Sensors) != 1 {
		t.Fatalf("unexpected discovery result=%#v published=%#v", result, published)
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(result.StructuredJson), &structured); err != nil {
		t.Fatal(err)
	}
	if structured["unknown"] != float64(1) {
		t.Fatalf("unknown sensor count missing: %#v", structured)
	}
}

func TestDiscoveryScanRejectsInvalidPortWithoutDialing(t *testing.T) {
	handler := TaskHandler{}
	result := handler.Handle(context.Background(), &monitoredv1.MasterEnvelope{
		Payload: &monitoredv1.MasterEnvelope_Discovery{Discovery: &monitoredv1.DiscoveryTask{
			TaskId: "task-scan", Cidr: "127.0.0.1/32",
			ScanProtocols: []string{"snmp"},
			ScanPorts: []*monitoredv1.ScanPorts{{
				Protocol: "snmp", Ports: []uint32{70000},
			}},
		}},
	})
	if result == nil || result.Status != "failed" || result.Error == "" {
		t.Fatalf("invalid scan must fail safely: %#v", result)
	}
}
