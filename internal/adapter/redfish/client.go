// Package redfish implements the read-only Redfish inventory and telemetry
// adapter. It follows resource links exposed by the service root instead of
// assuming vendor-specific chassis paths.
package redfish

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Client struct {
	Endpoint   string
	Username   string
	Password   string
	Token      string
	HTTPClient *http.Client
}

func New(endpoint, username, password, token string, insecure bool) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // #nosec G402 -- explicit per-connection setting
	return &Client{
		Endpoint: endpoint, Username: username, Password: password, Token: token,
		HTTPClient: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

func (c *Client) Collect(ctx context.Context) (domain.IngestBatch, error) {
	root, err := c.get(ctx, "/redfish/v1/")
	if err != nil {
		return domain.IngestBatch{}, err
	}
	rootID := text(root, "UUID")
	if rootID == "" {
		rootID = fingerprint(c.Endpoint)
	}
	hostStable := "redfish-service:" + rootID
	batch := domain.IngestBatch{Assets: []domain.IngestAsset{{
		StableID: hostStable, Kind: "bmc", Name: first(text(root, "Name"), "Redfish service"),
		Vendor: text(root, "Vendor"), Metadata: map[string]any{"endpoint": c.Endpoint, "redfishVersion": text(root, "RedfishVersion")},
	}}}
	chassisLink := link(root, "Chassis")
	if chassisLink == "" {
		return batch, nil
	}
	collection, err := c.get(ctx, chassisLink)
	if err != nil {
		return batch, fmt.Errorf("read chassis collection: %w", err)
	}
	for _, memberLink := range links(collection, "Members") {
		chassis, err := c.get(ctx, memberLink)
		if err != nil {
			return batch, fmt.Errorf("read chassis %s: %w", memberLink, err)
		}
		chassisID := first(text(chassis, "UUID"), text(chassis, "SerialNumber"), text(chassis, "Id"), fingerprint(memberLink))
		stableID := "redfish-chassis:" + chassisID
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: stableID, ParentStableID: hostStable, Kind: "chassis",
			Name: first(text(chassis, "Name"), chassisID), Vendor: text(chassis, "Manufacturer"),
			Model: text(chassis, "Model"), Metadata: map[string]any{
				"serial": text(chassis, "SerialNumber"), "health": nestedText(chassis, "Status", "Health"),
			},
		})
		for _, resource := range []string{"Thermal", "Power", "EnvironmentMetrics"} {
			resourceLink := link(chassis, resource)
			if resourceLink == "" {
				continue
			}
			payload, getErr := c.get(ctx, resourceLink)
			if getErr != nil {
				continue
			}
			parseTelemetry(&batch, stableID, resourceLink, payload)
		}
	}
	return batch, nil
}

func parseTelemetry(batch *domain.IngestBatch, assetID, sourcePath string, payload map[string]any) {
	now := time.Now().UTC()
	type mapping struct {
		arrays   []string
		valueKey []string
		category string
		unit     string
	}
	mappings := []mapping{
		{[]string{"Temperatures"}, []string{"ReadingCelsius", "Reading"}, "temperature", "Cel"},
		{[]string{"Fans"}, []string{"Reading", "SpeedRPM"}, "fan", "rpm"},
		{[]string{"Voltages"}, []string{"ReadingVolts", "Reading"}, "voltage", "V"},
		{[]string{"PowerSupplies"}, []string{"PowerOutputWatts", "PowerInputWatts", "LastPowerOutputWatts"}, "power", "W"},
		{[]string{"PowerControl"}, []string{"PowerConsumedWatts", "PowerAllocatedWatts"}, "power", "W"},
	}
	for _, item := range mappings {
		for _, arrayName := range item.arrays {
			array, _ := payload[arrayName].([]any)
			for index, raw := range array {
				entry, _ := raw.(map[string]any)
				name := first(text(entry, "Name"), text(entry, "MemberId"), fmt.Sprintf("%s-%d", arrayName, index))
				for _, valueKey := range item.valueKey {
					value, ok := number(entry[valueKey])
					if !ok {
						continue
					}
					sensorID := "redfish:" + fingerprint(assetID+":"+sourcePath+":"+name+":"+valueKey)
					batch.Sensors = append(batch.Sensors, domain.IngestSensor{
						StableID: sensorID, AssetStableID: assetID, Source: "redfish",
						SourcePath: sourcePath + "#/" + arrayName + "/" + strconv.Itoa(index) + "/" + valueKey,
						RawName:    name + " " + valueKey, Category: item.category, Unit: item.unit,
						Scale: 1, IntervalSeconds: 30, Quality: quality(entry),
						Metadata: map[string]any{"memberId": text(entry, "MemberId"), "health": nestedText(entry, "Status", "Health")},
					})
					batch.Samples = append(batch.Samples, domain.IngestSample{
						SensorStableID: sensorID, ObservedAt: now, Value: &value,
						Source: "redfish", Quality: quality(entry),
					})
					break
				}
			}
		}
	}
}

func (c *Client) get(ctx context.Context, path string) (map[string]any, error) {
	target, err := resolve(c.Endpoint, path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if c.Token != "" {
		request.Header.Set("X-Auth-Token", c.Token)
	} else if c.Username != "" {
		request.SetBasicAuth(c.Username, c.Password)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("GET %s returned %s: %s", target, response.Status, strings.TrimSpace(string(body)))
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", target, err)
	}
	return payload, nil
}

func resolve(endpoint, path string) (string, error) {
	base, err := url.Parse(strings.TrimRight(endpoint, "/") + "/")
	if err != nil {
		return "", err
	}
	reference, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(reference).String(), nil
}

func links(value map[string]any, key string) []string {
	raw, _ := value[key].([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		if value := text(entry, "@odata.id"); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func link(value map[string]any, key string) string {
	entry, _ := value[key].(map[string]any)
	return text(entry, "@odata.id")
}

func text(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	result, _ := value[key].(string)
	return strings.TrimSpace(result)
}

func nestedText(value map[string]any, parent, key string) string {
	nested, _ := value[parent].(map[string]any)
	return text(nested, key)
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	default:
		return 0, false
	}
}

func quality(value map[string]any) string {
	health := strings.ToLower(nestedText(value, "Status", "Health"))
	if health == "critical" {
		return "error"
	}
	if health == "warning" {
		return "degraded"
	}
	return "good"
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
