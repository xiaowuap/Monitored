package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	monitoredv1 "github.com/monitored-io/monitored/gen/monitored/v1"
	"github.com/monitored-io/monitored/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type GRPCConfig struct {
	Target                string
	AgentID               string
	StableHostID          string
	Hostname              string
	Version               string
	CertFile              string
	KeyFile               string
	CAFile                string
	ServerName            string
	Insecure              bool
	Credentials           *CredentialStore
	RenewBefore           time.Duration
	RotationCheckInterval time.Duration
	Collectors            []string
	Tools                 []string
	Metadata              map[string]string
	TaskHandler           func(context.Context, *monitoredv1.MasterEnvelope) *monitoredv1.TaskResult
}

type GRPCClient struct {
	config   GRPCConfig
	conn     *grpc.ClientConn
	stream   grpc.BidiStreamingClient[monitoredv1.AgentEnvelope, monitoredv1.MasterEnvelope]
	sendMu   sync.Mutex
	stateMu  sync.Mutex
	sequence atomic.Uint64
	cancel   context.CancelFunc
}

func NewGRPCClient(config GRPCConfig) *GRPCClient {
	return &GRPCClient{config: config}
}

func (c *GRPCClient) Connect(ctx context.Context) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.stream != nil {
		return nil
	}
	if c.config.Credentials != nil {
		if err := c.config.Credentials.Ensure(ctx); err != nil {
			return err
		}
	}
	transport, err := c.transportCredentials()
	if err != nil {
		return err
	}
	connection, err := grpc.NewClient(
		c.config.Target,
		grpc.WithTransportCredentials(transport),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20), grpc.MaxCallSendMsgSize(16<<20)),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	streamContext, cancel := context.WithCancel(ctx)
	stream, err := monitoredv1.NewAgentControlClient(connection).Connect(streamContext)
	if err != nil {
		cancel()
		_ = connection.Close()
		return fmt.Errorf("open AgentControl stream: %w", err)
	}
	c.conn, c.stream, c.cancel = connection, stream, cancel
	if err := c.sendLocked(registrationEnvelope(c.config, c.messageID())); err != nil {
		c.resetLocked()
		return err
	}
	if err := c.sendLocked(capabilityEnvelope(c.config, c.messageID())); err != nil {
		c.resetLocked()
		return err
	}
	go c.receive(streamContext, stream)
	if c.config.Credentials != nil {
		go c.rotationLoop(streamContext, stream)
	}
	return nil
}

func (c *GRPCClient) Close() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		err := c.conn.Close()
		c.stream, c.conn, c.cancel = nil, nil, nil
		return err
	}
	return nil
}

func (c *GRPCClient) Send(ctx context.Context, batch domain.IngestBatch) error {
	if err := c.Connect(ctx); err != nil {
		return err
	}
	if batch.AgentID == "" {
		batch.AgentID = c.config.AgentID
	}
	if batch.AgentID != c.config.AgentID {
		return errors.New("batch agent ID does not match the mTLS identity")
	}
	if len(batch.Assets) > 0 || len(batch.Sensors) > 0 {
		if err := c.send(snapshotEnvelope(batch, c.messageID())); err != nil {
			return err
		}
	}
	if len(batch.Samples) > 0 {
		if err := c.send(samplesEnvelope(batch, c.messageID())); err != nil {
			return err
		}
	}
	return nil
}

func (c *GRPCClient) Heartbeat(walBytes int64, oldest time.Time) error {
	envelope := &monitoredv1.AgentEnvelope{
		AgentId: c.config.AgentID, MessageId: c.messageID(), SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_Heartbeat{Heartbeat: &monitoredv1.Heartbeat{WalBytes: walBytes}},
	}
	if !oldest.IsZero() {
		envelope.GetHeartbeat().OldestWalUnixMs = oldest.UnixMilli()
	}
	return c.send(envelope)
}

func (c *GRPCClient) ReportGap(from, to time.Time, reason string) error {
	return c.send(&monitoredv1.AgentEnvelope{
		AgentId: c.config.AgentID, MessageId: c.messageID(), SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_Gap{Gap: &monitoredv1.GapReport{
			FromUnixMs: from.UnixMilli(), ToUnixMs: to.UnixMilli(), Reason: reason,
		}},
	})
}

func (c *GRPCClient) send(envelope *monitoredv1.AgentEnvelope) error {
	c.stateMu.Lock()
	stream := c.stream
	c.stateMu.Unlock()
	if stream == nil {
		return errors.New("gRPC stream is not connected")
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := stream.Send(envelope); err != nil {
		c.stateMu.Lock()
		if c.stream == stream {
			c.resetLocked()
		}
		c.stateMu.Unlock()
		return fmt.Errorf("send gRPC envelope: %w", err)
	}
	return nil
}

func (c *GRPCClient) sendLocked(envelope *monitoredv1.AgentEnvelope) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.stream.Send(envelope); err != nil {
		return fmt.Errorf("send gRPC handshake: %w", err)
	}
	return nil
}

func (c *GRPCClient) receive(ctx context.Context, stream grpc.BidiStreamingClient[monitoredv1.AgentEnvelope, monitoredv1.MasterEnvelope]) {
	for {
		envelope, err := stream.Recv()
		if err != nil {
			c.stateMu.Lock()
			if c.stream == stream {
				c.resetLocked()
			}
			c.stateMu.Unlock()
			return
		}
		if bundle := envelope.GetCertificateBundle(); bundle != nil && c.config.Credentials != nil {
			if err := c.config.Credentials.InstallRotation(
				bundle.RequestId,
				bundle.CertificatePem,
				bundle.CaPem,
				time.Now(),
			); err == nil {
				c.stateMu.Lock()
				if c.stream == stream {
					c.resetLocked()
				}
				c.stateMu.Unlock()
				return
			}
			continue
		}
		if envelope.GetAcknowledgement() != nil || c.config.TaskHandler == nil {
			continue
		}
		result := c.config.TaskHandler(ctx, envelope)
		if result == nil {
			continue
		}
		_ = c.send(&monitoredv1.AgentEnvelope{
			AgentId: c.config.AgentID, MessageId: c.messageID(), SentAtUnixMs: time.Now().UnixMilli(),
			Payload: &monitoredv1.AgentEnvelope_TaskResult{TaskResult: result},
		})
	}
}

func (c *GRPCClient) rotationLoop(
	ctx context.Context,
	stream grpc.BidiStreamingClient[monitoredv1.AgentEnvelope, monitoredv1.MasterEnvelope],
) {
	interval := c.config.RotationCheckInterval
	if interval <= 0 {
		interval = time.Hour
	}
	renewBefore := c.config.RenewBefore
	if renewBefore <= 0 {
		renewBefore = 7 * 24 * time.Hour
	}
	check := func() {
		requestID, csrPEM, needed, err := c.config.Credentials.RotationRequest(
			c.config.AgentID,
			renewBefore,
			time.Now(),
		)
		if err != nil || !needed {
			return
		}
		err = c.send(&monitoredv1.AgentEnvelope{
			AgentId:      c.config.AgentID,
			MessageId:    c.messageID(),
			SentAtUnixMs: time.Now().UnixMilli(),
			Payload: &monitoredv1.AgentEnvelope_CertificateSigningRequest{
				CertificateSigningRequest: &monitoredv1.CertificateSigningRequest{
					RequestId: requestID,
					CsrPem:    csrPEM,
				},
			},
		})
		if err != nil {
			c.config.Credentials.CancelRotation(requestID)
		}
	}
	check()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer c.config.Credentials.CancelRotation("")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.stateMu.Lock()
			current := c.stream == stream
			c.stateMu.Unlock()
			if !current {
				return
			}
			check()
		}
	}
}

func (c *GRPCClient) resetLocked() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.stream, c.conn, c.cancel = nil, nil, nil
}

func (c *GRPCClient) transportCredentials() (credentials.TransportCredentials, error) {
	if c.config.Insecure {
		return insecure.NewCredentials(), nil
	}
	if c.config.Credentials != nil {
		tlsConfig, err := c.config.Credentials.TLSConfig(c.config.ServerName)
		if err != nil {
			return nil, err
		}
		return credentials.NewTLS(tlsConfig), nil
	}
	if c.config.CertFile == "" || c.config.KeyFile == "" || c.config.CAFile == "" {
		return nil, errors.New("Agent gRPC mTLS requires certificate, key, and CA files")
	}
	certificate, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load agent key pair: %w", err)
	}
	caPEM, err := os.ReadFile(c.config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read agent CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("agent CA contains no certificates")
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		RootCAs: roots, ServerName: c.config.ServerName,
	}), nil
}

func (c *GRPCClient) messageID() string {
	return c.config.AgentID + "-" + strconv.FormatUint(c.sequence.Add(1), 10)
}

func registrationEnvelope(config GRPCConfig, messageID string) *monitoredv1.AgentEnvelope {
	return &monitoredv1.AgentEnvelope{
		AgentId: config.AgentID, MessageId: messageID, SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_Registration{Registration: &monitoredv1.Registration{
			StableHostId: config.StableHostID, Hostname: config.Hostname, Os: runtime.GOOS,
			Architecture: runtime.GOARCH, Version: config.Version,
		}},
	}
}

func capabilityEnvelope(config GRPCConfig, messageID string) *monitoredv1.AgentEnvelope {
	return &monitoredv1.AgentEnvelope{
		AgentId: config.AgentID, MessageId: messageID, SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_Capabilities{Capabilities: &monitoredv1.CapabilityManifest{
			Collectors: config.Collectors, Tools: config.Tools, Metadata: config.Metadata,
		}},
	}
}

func snapshotEnvelope(batch domain.IngestBatch, messageID string) *monitoredv1.AgentEnvelope {
	snapshot := &monitoredv1.AssetSnapshot{}
	for _, asset := range batch.Assets {
		snapshot.Assets = append(snapshot.Assets, &monitoredv1.Asset{
			StableId: asset.StableID, ParentStableId: asset.ParentStableID, Kind: asset.Kind,
			Name: asset.Name, Vendor: asset.Vendor, Model: asset.Model, Metadata: stringMap(asset.Metadata),
		})
	}
	for _, sensor := range batch.Sensors {
		snapshot.Sensors = append(snapshot.Sensors, &monitoredv1.SensorDefinition{
			StableId: sensor.StableID, AssetStableId: sensor.AssetStableID, Source: sensor.Source,
			SourcePath: sensor.SourcePath, RawName: sensor.RawName, Category: sensor.Category,
			Unit: sensor.Unit, Scale: sensor.Scale, Monotonic: sensor.Monotonic,
			IntervalSeconds: int32(sensor.IntervalSeconds), Quality: sensor.Quality,
			Metadata: stringMap(sensor.Metadata),
		})
	}
	return &monitoredv1.AgentEnvelope{
		AgentId: batch.AgentID, MessageId: messageID, SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_AssetSnapshot{AssetSnapshot: snapshot},
	}
}

func samplesEnvelope(batch domain.IngestBatch, messageID string) *monitoredv1.AgentEnvelope {
	samples := &monitoredv1.SampleBatch{}
	for _, sample := range batch.Samples {
		samples.Samples = append(samples.Samples, &monitoredv1.Sample{
			SensorStableId: sample.SensorStableID, ObservedAtUnixMs: sample.ObservedAt.UnixMilli(),
			Value: sample.Value, TextValue: sample.TextValue, Source: sample.Source,
			Quality: sample.Quality, Error: sample.Error,
		})
	}
	return &monitoredv1.AgentEnvelope{
		AgentId: batch.AgentID, MessageId: messageID, SentAtUnixMs: time.Now().UnixMilli(),
		Payload: &monitoredv1.AgentEnvelope_Samples{Samples: samples},
	}
}

func stringMap(values map[string]any) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			result[key] = typed
		default:
			encoded, err := json.Marshal(typed)
			if err == nil {
				result[key] = string(encoded)
			}
		}
	}
	return result
}
