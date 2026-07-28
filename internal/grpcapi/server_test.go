package grpcapi

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	monitoredv1 "github.com/monitored-io/monitored/gen/monitored/v1"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type fakeCertificateIssuer struct {
	issuedAgentID string
	issuedCSR     []byte
}

func (f *fakeCertificateIssuer) IssueAgentCertificate(agentID string, csr []byte, _ time.Time) (security.AgentCertificate, error) {
	if len(csr) == 0 {
		return security.AgentCertificate{}, errors.New("empty CSR")
	}
	f.issuedAgentID = agentID
	f.issuedCSR = append([]byte(nil), csr...)
	return security.AgentCertificate{
		CertificatePEM: []byte("certificate"),
		CAPEM:          []byte("ca"),
		NotAfter:       time.Unix(2_000_000_000, 0),
	}, nil
}

type fakeRepository struct {
	mu         sync.Mutex
	registered domain.Agent
	batches    []domain.IngestBatch
	heartbeat  int64
	renewalErr error
}

func (f *fakeRepository) RegisterAgent(_ context.Context, value domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = value
	return nil
}
func (f *fakeRepository) UpdateAgentCapabilities(context.Context, string, []string, []string, map[string]any) error {
	return nil
}
func (f *fakeRepository) AgentHeartbeat(_ context.Context, _ string, value int64, _ *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeat = value
	return nil
}
func (f *fakeRepository) RecordAgentGap(context.Context, string, time.Time, time.Time, string) error {
	return nil
}
func (f *fakeRepository) Ingest(_ context.Context, batch domain.IngestBatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, batch)
	return nil
}
func (f *fakeRepository) ApplyDiscoverySnapshot(_ context.Context, _, _ string, batch domain.IngestBatch) error {
	return f.Ingest(context.Background(), batch)
}
func (f *fakeRepository) NextAgentTasks(context.Context, string, int) ([]domain.AgentTask, error) {
	return nil, nil
}
func (f *fakeRepository) CompleteAgentTask(context.Context, string, string, string, map[string]any) error {
	return nil
}
func (f *fakeRepository) AuthorizeAgentCertificateRenewal(context.Context, string) error {
	return f.renewalErr
}

func TestInsecureDevelopmentStreamRegistrationSnapshotAndHeartbeat(t *testing.T) {
	repository := &fakeRepository{}
	server, err := NewServer(New(repository, true, nil), "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient(
		"passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	stream, err := monitoredv1.NewAgentControlClient(connection).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&monitoredv1.AgentEnvelope{
		AgentId: "agent-1", MessageId: "register",
		Payload: &monitoredv1.AgentEnvelope_Registration{Registration: &monitoredv1.Registration{
			StableHostId: "machine-1", Hostname: "host-1", Os: "linux", Architecture: "amd64", Version: "test",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if ack, err := stream.Recv(); err != nil || ack.GetAcknowledgement().MessageId != "register" {
		t.Fatalf("registration ACK failed %#v, %v", ack, err)
	}
	value := 42.0
	if err := stream.Send(&monitoredv1.AgentEnvelope{
		AgentId: "agent-1", MessageId: "snapshot",
		Payload: &monitoredv1.AgentEnvelope_AssetSnapshot{AssetSnapshot: &monitoredv1.AssetSnapshot{
			Assets:  []*monitoredv1.Asset{{StableId: "host:1", Kind: "host", Name: "host-1"}},
			Sensors: []*monitoredv1.SensorDefinition{{StableId: "sensor:1", AssetStableId: "host:1", RawName: "temp1", Category: "temperature", Unit: "Cel"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	if err := stream.Send(&monitoredv1.AgentEnvelope{
		AgentId: "agent-1", MessageId: "samples",
		Payload: &monitoredv1.AgentEnvelope_Samples{Samples: &monitoredv1.SampleBatch{
			Samples: []*monitoredv1.Sample{{SensorStableId: "sensor:1", Value: &value, ObservedAtUnixMs: time.Now().UnixMilli()}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	if err := stream.Send(&monitoredv1.AgentEnvelope{
		AgentId: "agent-1", MessageId: "heartbeat",
		Payload: &monitoredv1.AgentEnvelope_Heartbeat{Heartbeat: &monitoredv1.Heartbeat{WalBytes: 99}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.registered.ID != "agent-1" || len(repository.batches) != 2 || repository.heartbeat != 99 {
		t.Fatalf("unexpected repository state %#v batches=%d heartbeat=%d", repository.registered, len(repository.batches), repository.heartbeat)
	}
}

func TestCertificateIdentityMatching(t *testing.T) {
	certificate := &x509.Certificate{}
	certificate.Subject.CommonName = "agent-a"
	certificate.DNSNames = []string{"agent-b"}
	if !certificateMatches(certificate, "agent-a") || !certificateMatches(certificate, "agent-b") || certificateMatches(certificate, "agent-c") {
		t.Fatal("certificate identity matching failed")
	}
}

func TestAuthenticatedStreamCanRotateCertificate(t *testing.T) {
	issuer := &fakeCertificateIssuer{}
	service := New(&fakeRepository{}, false, issuer)
	var sent *monitoredv1.MasterEnvelope
	err := service.handle(context.Background(), "edge-01", &monitoredv1.AgentEnvelope{
		Payload: &monitoredv1.AgentEnvelope_CertificateSigningRequest{
			CertificateSigningRequest: &monitoredv1.CertificateSigningRequest{
				RequestId: "rotation-1",
				CsrPem:    []byte("csr"),
			},
		},
	}, func(value *monitoredv1.MasterEnvelope) error {
		sent = value
		return nil
	})
	if err != nil {
		t.Fatalf("handle certificate rotation: %v", err)
	}
	if issuer.issuedAgentID != "edge-01" || string(issuer.issuedCSR) != "csr" {
		t.Fatalf("issuer received Agent %q CSR %q", issuer.issuedAgentID, issuer.issuedCSR)
	}
	bundle := sent.GetCertificateBundle()
	if bundle == nil || bundle.RequestId != "rotation-1" || string(bundle.CertificatePem) != "certificate" {
		t.Fatalf("certificate bundle = %#v", bundle)
	}
}

func TestInsecureStreamCannotRotateCertificate(t *testing.T) {
	service := New(&fakeRepository{}, true, &fakeCertificateIssuer{})
	err := service.handle(context.Background(), "edge-01", &monitoredv1.AgentEnvelope{
		Payload: &monitoredv1.AgentEnvelope_CertificateSigningRequest{
			CertificateSigningRequest: &monitoredv1.CertificateSigningRequest{
				RequestId: "rotation-1",
				CsrPem:    []byte("csr"),
			},
		},
	}, func(*monitoredv1.MasterEnvelope) error { return nil })
	if err == nil {
		t.Fatal("insecure stream was allowed to rotate a certificate")
	}
}

func TestRevokedAgentCannotRotateCertificate(t *testing.T) {
	issuer := &fakeCertificateIssuer{}
	service := New(&fakeRepository{renewalErr: store.ErrNotFound}, false, issuer)
	err := service.handle(context.Background(), "edge-01", &monitoredv1.AgentEnvelope{
		Payload: &monitoredv1.AgentEnvelope_CertificateSigningRequest{
			CertificateSigningRequest: &monitoredv1.CertificateSigningRequest{
				RequestId: "rotation-1",
				CsrPem:    []byte("csr"),
			},
		},
	}, func(*monitoredv1.MasterEnvelope) error { return nil })
	if err == nil || issuer.issuedAgentID != "" {
		t.Fatal("revoked Agent was allowed to reach the certificate issuer")
	}
}

func TestDiscoveryTaskEnvelopeCarriesBoundedScanConfiguration(t *testing.T) {
	envelope := taskEnvelope(domain.AgentTask{
		ID: "task-1", Kind: "discovery",
		Payload: map[string]any{
			"runId": "run-1", "cidr": "10.0.0.0/24",
			"protocols": []any{"redfish", "nut"},
			"ports": map[string]any{
				"redfish": []any{float64(8443)},
			},
			"confirmLarge": false, "ratePerSecond": float64(20),
			"concurrency": float64(8),
		},
	})
	task := envelope.GetDiscovery()
	if task == nil || task.Cidr != "10.0.0.0/24" ||
		len(task.ScanProtocols) != 2 || len(task.ScanPorts) != 1 ||
		task.ScanPorts[0].Ports[0] != 8443 ||
		task.RatePerSecond != 20 || task.Concurrency != 8 {
		t.Fatalf("scan task configuration was not preserved: %#v", task)
	}
}

func TestAgentUpdateTaskEnvelopePreservesVerifiedPackageMetadata(t *testing.T) {
	envelope := taskEnvelope(domain.AgentTask{
		ID: "task-update", Kind: "update",
		Payload: map[string]any{
			"packageId": "package-1", "version": "1.2.3", "strategy": "binary",
			"targetOs": "linux", "architecture": "amd64",
			"downloadUrl": "https://master.example/package", "sha256": "abc",
			"sizeBytes": float64(42),
		},
	})
	task := envelope.GetUpdate()
	if task == nil || task.TaskId != "task-update" || task.PackageId != "package-1" ||
		task.Version != "1.2.3" || task.DownloadUrl != "https://master.example/package" ||
		task.SizeBytes != 42 {
		t.Fatalf("Agent update task metadata was not preserved: %#v", task)
	}
}
