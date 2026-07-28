package grpcapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	monitoredv1 "github.com/monitored-io/monitored/gen/monitored/v1"
	"github.com/monitored-io/monitored/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type Repository interface {
	RegisterAgent(context.Context, domain.Agent) error
	UpdateAgentCapabilities(context.Context, string, []string, []string, map[string]any) error
	AgentHeartbeat(context.Context, string, int64, *time.Time) error
	RecordAgentGap(context.Context, string, time.Time, time.Time, string) error
	Ingest(context.Context, domain.IngestBatch) error
	ApplyDiscoverySnapshot(context.Context, string, string, domain.IngestBatch) error
	NextAgentTasks(context.Context, string, int) ([]domain.AgentTask, error)
	CompleteAgentTask(context.Context, string, string, string, map[string]any) error
}

type AgentControl struct {
	monitoredv1.UnimplementedAgentControlServer
	repository    Repository
	allowInsecure bool
}

func New(repository Repository, allowInsecure bool) *AgentControl {
	return &AgentControl{repository: repository, allowInsecure: allowInsecure}
}

func NewServer(service *AgentControl, certFile, keyFile, clientCAFile string, insecureMode bool) (*grpc.Server, error) {
	options := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.MaxRecvMsgSize(16 << 20),
		grpc.MaxSendMsgSize(16 << 20),
	}
	if !insecureMode {
		transportCredentials, err := serverCredentials(certFile, keyFile, clientCAFile)
		if err != nil {
			return nil, err
		}
		options = append(options, grpc.Creds(transportCredentials))
	}
	server := grpc.NewServer(options...)
	monitoredv1.RegisterAgentControlServer(server, service)
	return server, nil
}

func serverCredentials(certFile, keyFile, clientCAFile string) (credentials.TransportCredentials, error) {
	if certFile == "" || keyFile == "" || clientCAFile == "" {
		return nil, errors.New("gRPC mTLS requires server certificate, key, and client CA files")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load gRPC server key pair: %w", err)
	}
	caPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read gRPC client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("gRPC client CA contains no certificates")
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert,
	}), nil
}

func (s *AgentControl) Connect(stream grpc.BidiStreamingServer[monitoredv1.AgentEnvelope, monitoredv1.MasterEnvelope]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	registration := first.GetRegistration()
	if registration == nil || strings.TrimSpace(first.AgentId) == "" {
		return status.Error(codes.InvalidArgument, "the first envelope must contain registration and agent_id")
	}
	certificateID, err := s.authorize(stream.Context(), first.AgentId)
	if err != nil {
		return err
	}
	agent := domain.Agent{
		ID: first.AgentId, StableHostID: registration.StableHostId, Hostname: registration.Hostname,
		OS: registration.Os, Architecture: registration.Architecture, Version: registration.Version,
		CertificateID: certificateID, Metadata: map[string]any{"transport": "grpc"},
	}
	if err := s.repository.RegisterAgent(stream.Context(), agent); err != nil {
		return status.Error(codes.Internal, "agent registration failed")
	}
	var sendMu sync.Mutex
	send := func(envelope *monitoredv1.MasterEnvelope) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(envelope)
	}
	if err := acknowledge(send, first.MessageId); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go s.dispatchTasks(stream.Context(), first.AgentId, send, done)
	for {
		envelope, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if envelope.AgentId != first.AgentId {
			return status.Error(codes.PermissionDenied, "agent_id changed within a stream")
		}
		if err := s.handle(stream.Context(), first.AgentId, envelope); err != nil {
			return err
		}
		if err := acknowledge(send, envelope.MessageId); err != nil {
			return err
		}
	}
}

func (s *AgentControl) handle(ctx context.Context, agentID string, envelope *monitoredv1.AgentEnvelope) error {
	switch payload := envelope.Payload.(type) {
	case *monitoredv1.AgentEnvelope_Capabilities:
		metadata := make(map[string]any, len(payload.Capabilities.Metadata))
		for key, value := range payload.Capabilities.Metadata {
			metadata[key] = value
		}
		return repositoryError(s.repository.UpdateAgentCapabilities(ctx, agentID, payload.Capabilities.Collectors, payload.Capabilities.Tools, metadata))
	case *monitoredv1.AgentEnvelope_AssetSnapshot:
		batch := snapshotBatch(agentID, payload.AssetSnapshot)
		return repositoryError(s.repository.ApplyDiscoverySnapshot(ctx, "", agentID, batch))
	case *monitoredv1.AgentEnvelope_Samples:
		return repositoryError(s.repository.Ingest(ctx, sampleBatch(agentID, payload.Samples)))
	case *monitoredv1.AgentEnvelope_Heartbeat:
		var oldest *time.Time
		if payload.Heartbeat.OldestWalUnixMs > 0 {
			value := time.UnixMilli(payload.Heartbeat.OldestWalUnixMs).UTC()
			oldest = &value
		}
		return repositoryError(s.repository.AgentHeartbeat(ctx, agentID, payload.Heartbeat.WalBytes, oldest))
	case *monitoredv1.AgentEnvelope_Gap:
		return repositoryError(s.repository.RecordAgentGap(
			ctx, agentID, time.UnixMilli(payload.Gap.FromUnixMs).UTC(),
			time.UnixMilli(payload.Gap.ToUnixMs).UTC(), payload.Gap.Reason,
		))
	case *monitoredv1.AgentEnvelope_TaskResult:
		result := map[string]any{
			"structured": payload.TaskResult.StructuredJson,
			"stdout":     payload.TaskResult.StdoutSummary,
			"stderr":     payload.TaskResult.StderrSummary,
			"error":      payload.TaskResult.Error,
		}
		taskStatus := payload.TaskResult.Status
		if taskStatus != "succeeded" {
			taskStatus = "failed"
		}
		return repositoryError(s.repository.CompleteAgentTask(ctx, agentID, payload.TaskResult.TaskId, taskStatus, result))
	case *monitoredv1.AgentEnvelope_Registration:
		return status.Error(codes.InvalidArgument, "registration may only be sent once")
	default:
		return status.Error(codes.InvalidArgument, "unknown agent envelope payload")
	}
}

func (s *AgentControl) dispatchTasks(ctx context.Context, agentID string, send func(*monitoredv1.MasterEnvelope) error, done <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			tasks, err := s.repository.NextAgentTasks(ctx, agentID, 10)
			if err != nil {
				continue
			}
			for _, task := range tasks {
				envelope := taskEnvelope(task)
				if envelope != nil {
					if err := send(envelope); err != nil {
						return
					}
				}
			}
		}
	}
}

func taskEnvelope(task domain.AgentTask) *monitoredv1.MasterEnvelope {
	envelope := &monitoredv1.MasterEnvelope{
		MessageId: task.ID, SentAtUnixMs: time.Now().UnixMilli(),
	}
	switch task.Kind {
	case "discovery":
		discovery := &monitoredv1.DiscoveryTask{
			TaskId: task.ID, Collectors: stringsValue(task.Payload["collectors"]),
			Cidr:          stringValue(task.Payload["cidr"]),
			ScanProtocols: stringsValue(task.Payload["protocols"]),
			ConfirmLarge:  boolValue(task.Payload["confirmLarge"]),
			RatePerSecond: int32(numberValue(task.Payload["ratePerSecond"], 32)),
			Concurrency:   int32(numberValue(task.Payload["concurrency"], 16)),
		}
		if ports, ok := task.Payload["ports"].(map[string]any); ok {
			for protocol, raw := range ports {
				values := numberSlice(raw)
				mapping := &monitoredv1.ScanPorts{Protocol: protocol}
				for _, value := range values {
					mapping.Ports = append(mapping.Ports, uint32(value))
				}
				discovery.ScanPorts = append(discovery.ScanPorts, mapping)
			}
		}
		envelope.Payload = &monitoredv1.MasterEnvelope_Discovery{Discovery: discovery}
	case "diagnostic":
		envelope.Payload = &monitoredv1.MasterEnvelope_Diagnostic{Diagnostic: &monitoredv1.DiagnosticTask{
			TaskId: task.ID, Commands: stringsValue(task.Payload["commands"]),
			TimeoutSeconds: int32(numberValue(task.Payload["timeoutSeconds"], 60)),
		}}
	case "action":
		environment := make(map[string]string)
		if values, ok := task.Payload["environment"].(map[string]any); ok {
			for key, value := range values {
				environment[key] = fmt.Sprint(value)
			}
		}
		envelope.Payload = &monitoredv1.MasterEnvelope_Action{Action: &monitoredv1.ApprovedAction{
			ActionId: task.ID, ImmutableHash: task.ImmutableHash,
			OneTimeToken:     stringValue(task.Payload["oneTimeToken"]),
			Command:          stringValue(task.Payload["command"]),
			WorkingDirectory: stringValue(task.Payload["workingDirectory"]),
			Environment:      environment,
			TimeoutSeconds:   int32(numberValue(task.Payload["timeoutSeconds"], 60)),
			Identity:         stringValue(task.Payload["identity"]), Risk: stringValue(task.Payload["risk"]),
			AssetId: stringValue(task.Payload["assetId"]),
		}}
	default:
		return nil
	}
	return envelope
}

func (s *AgentControl) authorize(ctx context.Context, agentID string) (string, error) {
	value, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "transport identity is unavailable")
	}
	tlsInfo, ok := value.AuthInfo.(credentials.TLSInfo)
	if !ok {
		if s.allowInsecure {
			return "insecure-development", nil
		}
		return "", status.Error(codes.Unauthenticated, "mTLS is required")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return "", status.Error(codes.Unauthenticated, "client certificate is required")
	}
	certificate := tlsInfo.State.PeerCertificates[0]
	if !certificateMatches(certificate, agentID) {
		return "", status.Error(codes.PermissionDenied, "client certificate identity does not match agent_id")
	}
	return certificate.SerialNumber.String(), nil
}

func certificateMatches(certificate *x509.Certificate, agentID string) bool {
	if certificate.Subject.CommonName == agentID {
		return true
	}
	for _, name := range certificate.DNSNames {
		if name == agentID {
			return true
		}
	}
	for _, identity := range certificate.URIs {
		if identity.String() == agentID || path.Base(identity.Path) == agentID {
			return true
		}
	}
	return false
}

func snapshotBatch(agentID string, snapshot *monitoredv1.AssetSnapshot) domain.IngestBatch {
	batch := domain.IngestBatch{AgentID: agentID}
	for _, asset := range snapshot.Assets {
		metadata := make(map[string]any, len(asset.Metadata))
		for key, value := range asset.Metadata {
			metadata[key] = value
		}
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: asset.StableId, ParentStableID: asset.ParentStableId, Kind: asset.Kind,
			Name: asset.Name, Vendor: asset.Vendor, Model: asset.Model, Metadata: metadata,
		})
	}
	for _, sensor := range snapshot.Sensors {
		metadata := make(map[string]any, len(sensor.Metadata))
		for key, value := range sensor.Metadata {
			metadata[key] = value
		}
		batch.Sensors = append(batch.Sensors, domain.IngestSensor{
			StableID: sensor.StableId, AssetStableID: sensor.AssetStableId, Source: sensor.Source,
			SourcePath: sensor.SourcePath, RawName: sensor.RawName, Category: sensor.Category,
			Unit: sensor.Unit, Scale: sensor.Scale, Monotonic: sensor.Monotonic,
			IntervalSeconds: int(sensor.IntervalSeconds), Quality: sensor.Quality, Metadata: metadata,
		})
	}
	return batch
}

func sampleBatch(agentID string, value *monitoredv1.SampleBatch) domain.IngestBatch {
	batch := domain.IngestBatch{AgentID: agentID}
	for _, sample := range value.Samples {
		item := domain.IngestSample{
			SensorStableID: sample.SensorStableId, ObservedAt: time.UnixMilli(sample.ObservedAtUnixMs).UTC(),
			Source: sample.Source, Quality: sample.Quality, Error: sample.Error,
		}
		if sample.TextValue != nil {
			item.TextValue = sample.TextValue
		}
		if sample.Value != nil {
			item.Value = sample.Value
		}
		batch.Samples = append(batch.Samples, item)
	}
	return batch
}

func acknowledge(send func(*monitoredv1.MasterEnvelope) error, messageID string) error {
	return send(&monitoredv1.MasterEnvelope{
		MessageId: "ack-" + messageID, SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.MasterEnvelope_Acknowledgement{
			Acknowledgement: &monitoredv1.Acknowledgement{MessageId: messageID},
		},
	})
}

func repositoryError(err error) error {
	if err == nil {
		return nil
	}
	return status.Error(codes.Internal, "repository operation failed")
}

func stringsValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result
	default:
		return nil
	}
}

func numberSlice(value any) []int {
	switch typed := value.(type) {
	case []int:
		return typed
	case []any:
		result := make([]int, 0, len(typed))
		for _, item := range typed {
			result = append(result, int(numberValue(item, 0)))
		}
		return result
	default:
		return nil
	}
}

func boolValue(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func numberValue(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return fallback
	}
}
