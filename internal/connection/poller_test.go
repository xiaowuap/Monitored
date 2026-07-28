package connection

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/security"
)

type fakeRepository struct {
	jobs     []Job
	mu       sync.Mutex
	batches  []domain.IngestBatch
	finished map[string]error
}

func (f *fakeRepository) ListConnectionJobs(context.Context, int) ([]Job, error) {
	return f.jobs, nil
}

func (f *fakeRepository) Ingest(_ context.Context, batch domain.IngestBatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, batch)
	return nil
}

func (f *fakeRepository) FinishConnectionPoll(_ context.Context, id string, _ map[string]any, err error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished[id] = err
	return nil
}

type fixtureCollector struct {
	batch domain.IngestBatch
	err   error
}

func (f fixtureCollector) Collect(context.Context) (domain.IngestBatch, error) {
	return f.batch, f.err
}

func TestPollerDecryptsCredentialsAndMarksResults(t *testing.T) {
	sealer, err := security.NewSealer("connection-poller-test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := sealer.Seal([]byte(`{"token":"secret"}`), "connection:truenas:https://nas.test")
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{
		jobs: []Job{
			{ID: "ok", Name: "NAS", Kind: "truenas", Endpoint: "https://nas.test", EncryptedSecret: encrypted},
			{ID: "bad", Name: "BMC", Kind: "redfish", Endpoint: "https://bmc.test"},
		},
		finished: make(map[string]error),
	}
	poller := Poller{
		Repository: repository, Sealer: sealer, Concurrency: 2,
		Factory: func(job Job, secret map[string]any, _ string) (Collector, error) {
			if job.ID == "bad" {
				return fixtureCollector{err: errors.New("offline")}, nil
			}
			if secret["token"] != "secret" {
				t.Fatalf("decrypted token = %v", secret["token"])
			}
			return fixtureCollector{batch: domain.IngestBatch{
				Assets: []domain.IngestAsset{{StableID: "host:1", Kind: "host", Name: "host"}},
			}}, nil
		},
	}
	if err := poller.Run(context.Background()); err == nil {
		t.Fatal("expected aggregate error for failed connection")
	}
	if len(repository.batches) != 1 || repository.batches[0].AgentID != "connection:ok" {
		t.Fatalf("unexpected batches: %#v", repository.batches)
	}
	if repository.finished["ok"] != nil || repository.finished["bad"] == nil {
		t.Fatalf("unexpected results: %#v", repository.finished)
	}
}

func TestEndpointParsingAndProfileTraversal(t *testing.T) {
	host, port, err := splitTarget("snmp://[2001:db8::1]:1161", 161)
	if err != nil || host != "2001:db8::1" || port != 1161 {
		t.Fatalf("parsed %q:%d, err=%v", host, port, err)
	}
	_, err = NewCollector(Job{
		Kind: "snmp", Endpoint: "switch.test", Config: map[string]any{"profile": "../secret.yaml"},
	}, nil, "./profiles/snmp")
	if err == nil {
		t.Fatal("expected profile traversal rejection")
	}
}

func TestPollerPersistsDegradedTelemetryAndMarksConnection(t *testing.T) {
	value := 0.0
	repository := &fakeRepository{
		jobs:     []Job{{ID: "service", Name: "TCP", Kind: "tcp", Endpoint: "service.test:443"}},
		finished: make(map[string]error),
	}
	poller := Poller{
		Repository: repository,
		Factory: func(Job, map[string]any, string) (Collector, error) {
			return fixtureCollector{batch: domain.IngestBatch{
				Assets: []domain.IngestAsset{{StableID: "service:1", Kind: "service", Name: "service"}},
				Sensors: []domain.IngestSensor{{
					StableID: "service:state", AssetStableID: "service:1",
					Source: "tcp", SourcePath: "state", RawName: "state",
					Category: "health", Unit: "1", Quality: "error",
				}},
				Samples: []domain.IngestSample{{
					SensorStableID: "service:state", Value: &value,
					Source: "tcp", Quality: "error",
				}},
			}}, nil
		},
	}
	if err := poller.Run(context.Background()); err == nil {
		t.Fatal("degraded telemetry must mark the connection degraded")
	}
	if len(repository.batches) != 1 || repository.finished["service"] == nil {
		t.Fatalf("degraded data was not persisted and reported: %#v", repository)
	}
}
