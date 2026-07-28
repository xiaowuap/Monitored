// Package connection runs read-only, Master-side polling for connections that
// are not assigned to an Agent. Protocol adapters remain independent from the
// scheduler so they can also be reused by Agent tasks.
package connection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monitored-io/monitored/internal/adapter/ipmi"
	"github.com/monitored-io/monitored/internal/adapter/nut"
	"github.com/monitored-io/monitored/internal/adapter/redfish"
	serviceadapter "github.com/monitored-io/monitored/internal/adapter/service"
	"github.com/monitored-io/monitored/internal/adapter/snmp"
	"github.com/monitored-io/monitored/internal/adapter/sshclient"
	"github.com/monitored-io/monitored/internal/adapter/truenas"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/security"
)

type Job struct {
	ID              string
	Name            string
	Kind            string
	Endpoint        string
	TLSMode         string
	Config          map[string]any
	EncryptedSecret []byte
}

type Repository interface {
	ListConnectionJobs(context.Context, int) ([]Job, error)
	Ingest(context.Context, domain.IngestBatch) error
	FinishConnectionPoll(context.Context, string, map[string]any, error) error
}

type Collector interface {
	Collect(context.Context) (domain.IngestBatch, error)
}

type Factory func(Job, map[string]any, string) (Collector, error)

type Poller struct {
	Repository  Repository
	Sealer      *security.Sealer
	ProfilesDir string
	Concurrency int
	Timeout     time.Duration
	Factory     Factory
}

func (p Poller) Run(ctx context.Context) error {
	if p.Repository == nil {
		return errors.New("connection poller repository is required")
	}
	jobs, err := p.Repository.ListConnectionJobs(ctx, 250)
	if err != nil {
		return fmt.Errorf("list connection jobs: %w", err)
	}
	concurrency := p.Concurrency
	if concurrency <= 0 || concurrency > 32 {
		concurrency = 8
	}
	timeout := p.Timeout
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 45 * time.Second
	}
	factory := p.Factory
	if factory == nil {
		factory = NewCollector
	}
	profilesDir := p.ProfilesDir
	if profilesDir == "" {
		profilesDir = "./profiles/snmp"
	}

	semaphore := make(chan struct{}, concurrency)
	var wait sync.WaitGroup
	var errorsMu sync.Mutex
	var runErrors []error
	for _, job := range jobs {
		job := job
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			pollContext, cancel := context.WithTimeout(ctx, timeout)
			capability, pollErr := p.poll(pollContext, factory, profilesDir, job)
			cancel()
			finishContext, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if finishErr := p.Repository.FinishConnectionPoll(finishContext, job.ID, capability, pollErr); finishErr != nil {
				pollErr = errors.Join(pollErr, finishErr)
			}
			finishCancel()
			if pollErr != nil {
				errorsMu.Lock()
				runErrors = append(runErrors, fmt.Errorf("%s: %w", job.Name, pollErr))
				errorsMu.Unlock()
			}
		}()
	}
	wait.Wait()
	return errors.Join(runErrors...)
}

func (p Poller) poll(ctx context.Context, factory Factory, profilesDir string, job Job) (map[string]any, error) {
	secret := make(map[string]any)
	if len(job.EncryptedSecret) > 0 {
		if p.Sealer == nil {
			return nil, errors.New("connection credential cannot be opened without a valid Master key")
		}
		plaintext, err := p.Sealer.Open(job.EncryptedSecret, "connection:"+job.Kind+":"+job.Endpoint)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plaintext, &secret); err != nil {
			return nil, fmt.Errorf("decode connection credential: %w", err)
		}
	}
	collector, err := factory(job, secret, profilesDir)
	if err != nil {
		return nil, err
	}
	batch, err := collector.Collect(ctx)
	if err != nil {
		return nil, err
	}
	batch.AgentID = "connection:" + job.ID
	if err := p.Repository.Ingest(ctx, batch); err != nil {
		return nil, err
	}
	capability := map[string]any{
		"collector": job.Kind,
		"assets":    len(batch.Assets),
		"sensors":   len(batch.Sensors),
		"samples":   len(batch.Samples),
		"quality":   "healthy",
	}
	var degraded []string
	for _, sample := range batch.Samples {
		if sample.Quality == "error" || sample.Quality == "unavailable" || sample.Quality == "degraded" {
			degraded = append(degraded, sample.SensorStableID)
		}
	}
	if len(degraded) > 0 {
		capability["quality"] = "degraded"
		capability["degradedSamples"] = len(degraded)
		return capability, fmt.Errorf("collector reported %d degraded samples", len(degraded))
	}
	return capability, nil
}

func NewCollector(job Job, secret map[string]any, profilesDir string) (Collector, error) {
	switch job.Kind {
	case "truenas":
		tlsConfig, err := tlsConfiguration(job, secret)
		if err != nil {
			return nil, err
		}
		dialer := *websocket.DefaultDialer
		dialer.TLSClientConfig = tlsConfig
		return &truenas.Client{
			Endpoint: job.Endpoint,
			Token:    firstString(secret, "token", "apiKey"),
			Dialer:   &dialer,
		}, nil
	case "redfish":
		tlsConfig, err := tlsConfiguration(job, secret)
		if err != nil {
			return nil, err
		}
		client := redfish.New(
			job.Endpoint,
			stringValue(secret["username"]),
			stringValue(secret["password"]),
			stringValue(secret["token"]),
			job.TLSMode == "insecure",
		)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsConfig
		client.HTTPClient.Transport = transport
		return client, nil
	case "nut":
		address, err := hostPort(job.Endpoint, 3493)
		if err != nil {
			return nil, err
		}
		return nut.Client{
			Address: address, Username: stringValue(secret["username"]),
			Password: stringValue(secret["password"]), Timeout: 10 * time.Second,
		}, nil
	case "snmp":
		profileName := firstString(job.Config, "profile")
		if profileName == "" {
			profileName = "standard-mibs.yaml"
		}
		if filepath.Base(profileName) != profileName ||
			(!strings.HasSuffix(profileName, ".yaml") && !strings.HasSuffix(profileName, ".yml")) {
			return nil, errors.New("SNMP profile must be a YAML file name without a path")
		}
		profile, err := snmp.LoadProfile(filepath.Join(profilesDir, profileName))
		if err != nil {
			return nil, err
		}
		target, port, err := splitTarget(job.Endpoint, 161)
		if err != nil {
			return nil, err
		}
		return snmpCollector{config: snmp.Config{
			Target: target, Port: port, Version: firstString(job.Config, "version"),
			Community: firstString(secret, "community"), Username: firstString(secret, "username"),
			AuthPassword: firstString(secret, "authPassword"), PrivPassword: firstString(secret, "privPassword"),
			AuthProtocol: firstString(job.Config, "authProtocol"), PrivProtocol: firstString(job.Config, "privProtocol"),
			ContextName: firstString(job.Config, "contextName"), Timeout: 5 * time.Second, Retries: 1,
		}, profile: profile}, nil
	case "ssh":
		address, err := hostPort(job.Endpoint, 22)
		if err != nil {
			return nil, err
		}
		return sshclient.Client{
			Address: address, Username: firstString(secret, "username"),
			Password: firstString(secret, "password"), PrivateKey: firstString(secret, "privateKey"),
			PrivateKeyPassword: firstString(secret, "privateKeyPassword"),
			HostKeySHA256:      firstString(job.Config, "hostKeySHA256"),
			Timeout:            15 * time.Second,
		}, nil
	case "ipmi":
		host, port, err := splitTarget(job.Endpoint, 623)
		if err != nil {
			return nil, err
		}
		return ipmi.Client{
			Host: host, Port: port, Username: firstString(secret, "username"),
			Password: firstString(secret, "password"),
		}, nil
	case "http":
		tlsConfig, err := tlsConfiguration(job, secret)
		if err != nil {
			return nil, err
		}
		expectedStatus, err := serviceadapter.ParseExpectedStatus(job.Config["expectedStatus"])
		if err != nil {
			return nil, err
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsConfig
		return serviceadapter.HTTP{
			URL: job.Endpoint, Method: firstString(job.Config, "method"),
			ExpectedStatus: expectedStatus, Username: firstString(secret, "username"),
			Password: firstString(secret, "password"), BearerToken: firstString(secret, "token"),
			Client: &http.Client{Transport: transport, Timeout: 15 * time.Second},
		}, nil
	case "tcp":
		address, err := hostPort(job.Endpoint, 80)
		if err != nil {
			return nil, err
		}
		return serviceadapter.TCP{Address: address, Timeout: 10 * time.Second}, nil
	default:
		return nil, fmt.Errorf("unsupported connection kind %q", job.Kind)
	}
}

func tlsConfiguration(job Job, secret map[string]any) (*tls.Config, error) {
	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: job.TLSMode == "insecure", // #nosec G402 -- explicit per-connection opt-in
	}
	if job.TLSMode != "custom_ca" {
		return config, nil
	}
	caPEM := firstString(secret, "caPEM", "ca")
	if caPEM == "" {
		return nil, errors.New("custom_ca mode requires caPEM in the encrypted connection secret")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("custom connection CA contains no certificates")
	}
	config.RootCAs = roots
	return config, nil
}

type snmpCollector struct {
	config  snmp.Config
	profile snmp.Profile
}

func (c snmpCollector) Collect(ctx context.Context) (domain.IngestBatch, error) {
	return snmp.Collect(ctx, c.config, c.profile)
}

func hostPort(endpoint string, defaultPort int) (string, error) {
	host, port, err := splitTarget(endpoint, defaultPort)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func splitTarget(endpoint string, defaultPort int) (string, uint16, error) {
	value := strings.TrimSpace(endpoint)
	if value == "" {
		return "", 0, errors.New("connection endpoint is required")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", 0, err
		}
		value = parsed.Host
	}
	host, portText, err := net.SplitHostPort(value)
	if err == nil {
		port, parseErr := strconv.ParseUint(portText, 10, 16)
		return host, uint16(port), parseErr
	}
	host = strings.Trim(value, "[]")
	if host == "" || strings.Contains(host, "/") {
		return "", 0, fmt.Errorf("invalid endpoint %q", endpoint)
	}
	return host, uint16(defaultPort), nil
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
