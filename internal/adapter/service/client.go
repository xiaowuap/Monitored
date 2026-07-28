// Package service implements the intentionally small post-device-GA HTTP and
// TCP availability baseline. It is not a proxy, log collector, APM, or tracer.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type HTTP struct {
	URL            string
	Method         string
	ExpectedStatus int
	Username       string
	Password       string
	BearerToken    string
	Client         *http.Client
}

func (c HTTP) Collect(ctx context.Context) (domain.IngestBatch, error) {
	method := strings.ToUpper(strings.TrimSpace(c.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return domain.IngestBatch{}, errors.New("HTTP monitor method must be GET or HEAD")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.URL, nil)
	if err != nil {
		return domain.IngestBatch{}, err
	}
	if c.BearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.BearerToken)
	} else if c.Username != "" {
		request.SetBasicAuth(c.Username, c.Password)
	}
	started := time.Now()
	response, err := client.Do(request)
	latency := time.Since(started).Seconds() * 1000
	if err != nil {
		return availabilityBatch("http", c.URL, latency, false, err.Error()), nil
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	healthy := response.StatusCode >= 200 && response.StatusCode < 400
	if c.ExpectedStatus > 0 {
		healthy = response.StatusCode == c.ExpectedStatus
	}
	return availabilityBatch("http", c.URL, latency, healthy, response.Status), nil
}

type TCP struct {
	Address string
	Timeout time.Duration
}

func (c TCP) Collect(ctx context.Context) (domain.IngestBatch, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	started := time.Now()
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", c.Address)
	latency := time.Since(started).Seconds() * 1000
	if err != nil {
		return availabilityBatch("tcp", c.Address, latency, false, err.Error()), nil
	}
	_ = connection.Close()
	return availabilityBatch("tcp", c.Address, latency, true, "connected"), nil
}

func availabilityBatch(kind, endpoint string, latency float64, healthy bool, detail string) domain.IngestBatch {
	assetID := "service:" + fingerprint(kind+":"+endpoint)
	state, quality, reachable := "down", "error", 0.0
	if healthy {
		state, quality, reachable = "up", "good", 1
	}
	now := time.Now().UTC()
	latencyID := "service:" + fingerprint(assetID+":latency")
	stateID := "service:" + fingerprint(assetID+":state")
	return domain.IngestBatch{
		Assets: []domain.IngestAsset{{
			StableID: assetID, Kind: "service", Name: strings.ToUpper(kind) + " · " + endpoint,
			Metadata: map[string]any{"protocol": kind, "endpoint": endpoint, "detail": detail},
		}},
		Sensors: []domain.IngestSensor{
			{
				StableID: latencyID, AssetStableID: assetID, Source: kind,
				SourcePath: endpoint + "/latency", RawName: "latency",
				Category: "time", Unit: "ms", Scale: 1, IntervalSeconds: 30, Quality: quality,
			},
			{
				StableID: stateID, AssetStableID: assetID, Source: kind,
				SourcePath: endpoint + "/state", RawName: "state",
				Category: "health", Unit: "1", Scale: 1, IntervalSeconds: 30, Quality: quality,
			},
		},
		Samples: []domain.IngestSample{
			{SensorStableID: latencyID, ObservedAt: now, Value: &latency, Source: kind, Quality: quality},
			{SensorStableID: stateID, ObservedAt: now, Value: &reachable, TextValue: &state, Source: kind, Quality: quality},
		},
	}
}

func ParseExpectedStatus(value any) (int, error) {
	switch typed := value.(type) {
	case nil:
		return 0, nil
	case float64:
		if typed < 100 || typed > 599 || typed != float64(int(typed)) {
			return 0, fmt.Errorf("expected HTTP status %v is invalid", typed)
		}
		return int(typed), nil
	case int:
		if typed < 100 || typed > 599 {
			return 0, fmt.Errorf("expected HTTP status %d is invalid", typed)
		}
		return typed, nil
	default:
		return 0, errors.New("expectedStatus must be an integer")
	}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}
