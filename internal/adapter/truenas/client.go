// Package truenas implements the versioned JSON-RPC-over-WebSocket adapter
// used by current TrueNAS SCALE releases. It keeps CORE/legacy fallback
// explicit through its returned capability metadata.
package truenas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monitored-io/monitored/internal/domain"
)

type Client struct {
	Endpoint     string
	Token        string
	AgentID      string
	HostIdentity string
	Dialer       *websocket.Dialer
	nextID       atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (c *Client) Collect(ctx context.Context) (domain.IngestBatch, error) {
	connection, _, err := c.dial(ctx)
	if err != nil {
		return domain.IngestBatch{}, err
	}
	defer connection.Close()
	system, err := call[map[string]any](ctx, c, connection, "system.info", []any{})
	if err != nil {
		return domain.IngestBatch{}, err
	}
	pools, poolErr := call[[]map[string]any](ctx, c, connection, "pool.query", []any{})
	disks, diskErr := call[[]map[string]any](ctx, c, connection, "disk.query", []any{})
	if poolErr != nil && diskErr != nil {
		return domain.IngestBatch{}, errors.Join(poolErr, diskErr)
	}
	var diskNames []string
	for _, disk := range disks {
		name := first(text(disk, "name"), strings.TrimPrefix(text(disk, "devname"), "/dev/"))
		if name != "" {
			diskNames = append(diskNames, name)
		}
	}
	// disk.query no longer guarantees an inline temperature on current SCALE
	// releases. disk.temperatures is a reporting-read endpoint with a five-minute
	// server-side cache, and includes the drive critical threshold when requested.
	rawTemperatures, _ := call[map[string]json.RawMessage](
		ctx, c, connection, "disk.temperatures", []any{diskNames, true},
	)
	batch := mapInventory(c.Endpoint, system, pools, disks, parseDiskTemperatures(rawTemperatures))
	batch.AgentID = c.AgentID
	if strings.TrimSpace(c.HostIdentity) != "" && len(batch.Assets) > 0 {
		oldHostID := batch.Assets[0].StableID
		hostID := "host:" + fingerprint(strings.TrimSpace(c.HostIdentity))
		batch.Assets[0].StableID = hostID
		for index := range batch.Assets {
			if batch.Assets[index].ParentStableID == oldHostID {
				batch.Assets[index].ParentStableID = hostID
			}
		}
	}
	return batch, nil
}

func call[T any](ctx context.Context, client *Client, connection *websocket.Conn, method string, params any) (T, error) {
	var zero T
	id := client.nextID.Add(1)
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetWriteDeadline(deadline)
	if err := connection.WriteJSON(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return zero, fmt.Errorf("%s write: %w", method, err)
	}
	_ = connection.SetReadDeadline(deadline)
	for {
		var response rpcResponse
		if err := connection.ReadJSON(&response); err != nil {
			return zero, fmt.Errorf("%s read: %w", method, err)
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return zero, fmt.Errorf("%s: JSON-RPC %d: %s", method, response.Error.Code, response.Error.Message)
		}
		var result T
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return zero, fmt.Errorf("%s result: %w", method, err)
		}
		return result, nil
	}
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	target, err := websocketURL(c.Endpoint)
	if err != nil {
		return nil, nil, err
	}
	dialer := c.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	headers := http.Header{}
	if c.Token != "" {
		headers.Set("Authorization", "Bearer "+c.Token)
	}
	connection, response, err := dialer.DialContext(ctx, target, headers)
	if err != nil {
		return nil, response, fmt.Errorf("connect TrueNAS WebSocket: %w", err)
	}
	return connection, response, nil
}

func websocketURL(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", errors.New("TrueNAS endpoint must use http, https, ws, or wss")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/api/current"
	}
	return parsed.String(), nil
}

type diskTemperature struct {
	Current  *float64
	Critical *float64
}

func parseDiskTemperatures(values map[string]json.RawMessage) map[string]diskTemperature {
	result := make(map[string]diskTemperature, len(values))
	for name, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var tuple []*float64
		if json.Unmarshal(raw, &tuple) == nil && len(tuple) > 0 {
			value := diskTemperature{Current: tuple[0]}
			if len(tuple) > 1 {
				value.Critical = tuple[1]
			}
			result[name] = value
			continue
		}
		var current float64
		if json.Unmarshal(raw, &current) == nil {
			result[name] = diskTemperature{Current: &current}
			continue
		}
		var object struct {
			Current     *float64 `json:"current"`
			Temperature *float64 `json:"temperature"`
			Critical    *float64 `json:"critical"`
		}
		if json.Unmarshal(raw, &object) == nil {
			if object.Current == nil {
				object.Current = object.Temperature
			}
			if object.Current != nil {
				result[name] = diskTemperature{Current: object.Current, Critical: object.Critical}
			}
		}
	}
	return result
}

func mapInventory(
	endpoint string,
	system map[string]any,
	pools, disks []map[string]any,
	temperatures map[string]diskTemperature,
) domain.IngestBatch {
	hostIdentity := first(text(system, "system_serial"), text(system, "hostname"), endpoint)
	hostID := "truenas:" + fingerprint(hostIdentity)
	batch := domain.IngestBatch{Assets: []domain.IngestAsset{{
		StableID: hostID, Kind: "host", Name: first(text(system, "hostname"), "TrueNAS"),
		Vendor: "iXsystems", Model: text(system, "system_product"),
		Metadata: map[string]any{"version": text(system, "version"), "endpoint": endpoint, "api": "json-rpc-websocket"},
	}}}
	now := time.Now().UTC()
	for _, pool := range pools {
		name := first(text(pool, "name"), text(pool, "id"))
		if name == "" {
			continue
		}
		assetID := "zfs:" + fingerprint(hostID+":"+name)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: hostID, Kind: "pool", Name: name,
			Vendor: "OpenZFS", Metadata: map[string]any{"status": text(pool, "status"), "guid": stringify(pool["guid"])},
		})
		for _, metric := range []struct {
			key, category, unit string
		}{{"size", "capacity", "By"}, {"allocated", "capacity", "By"}, {"free", "capacity", "By"}, {"fragmentation", "utilization", "%"}} {
			if value, ok := numeric(pool[metric.key]); ok {
				appendSample(&batch, assetID, "pool."+metric.key, metric.key, metric.category, metric.unit, value, now)
			}
		}
	}
	for _, disk := range disks {
		name := first(text(disk, "name"), text(disk, "devname"), text(disk, "identifier"))
		if name == "" {
			continue
		}
		identity := first(
			text(disk, "serial"), text(disk, "wwn"), text(disk, "lunid"),
			text(disk, "identifier"), name,
		)
		assetID := "disk:" + fingerprint(identity)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: hostID, Kind: "disk", Name: name,
			Vendor: text(disk, "subsystem"), Model: text(disk, "model"),
			Metadata: map[string]any{
				"serial": text(disk, "serial"), "wwn": text(disk, "wwn"),
				"lunid": text(disk, "lunid"), "type": text(disk, "type"),
				"bus":     text(disk, "bus"),
				"devname": text(disk, "devname"), "pool": text(disk, "pool"),
				"sizeBytes": disk["size"], "rotationRate": disk["rotationrate"],
				"enclosure": disk["enclosure"],
			},
		})
		temperature, ok := temperatures[strings.TrimPrefix(name, "/dev/")]
		if ok && temperature.Current != nil {
			metadata := map[string]any{"apiMethod": "disk.temperatures"}
			if temperature.Critical != nil {
				metadata["criticalC"] = *temperature.Critical
			}
			appendSampleMetadata(
				&batch, assetID, "disk.temperature", "temperature", "temperature",
				"Cel", *temperature.Current, now, 300, metadata,
			)
		} else if value, inline := numeric(disk["temperature"]); inline {
			appendSample(&batch, assetID, "disk.temperature", "temperature", "temperature", "Cel", value, now)
		}
	}
	return batch
}

func appendSample(batch *domain.IngestBatch, assetID, key, name, category, unit string, value float64, at time.Time) {
	appendSampleMetadata(batch, assetID, key, name, category, unit, value, at, 30, nil)
}

func appendSampleMetadata(
	batch *domain.IngestBatch,
	assetID, key, name, category, unit string,
	value float64,
	at time.Time,
	intervalSeconds int,
	metadata map[string]any,
) {
	sensorID := "truenas:" + fingerprint(assetID+":"+key)
	batch.Sensors = append(batch.Sensors, domain.IngestSensor{
		StableID: sensorID, AssetStableID: assetID, Source: "truenas-api",
		SourcePath: key, RawName: name, Category: category, Unit: unit, Scale: 1,
		IntervalSeconds: intervalSeconds, Quality: "good", Metadata: metadata,
	})
	batch.Samples = append(batch.Samples, domain.IngestSample{
		SensorStableID: sensorID, ObservedAt: at, Value: &value, Source: "truenas-api", Quality: "good",
	})
}

func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSuffix(typed, "%"), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringify(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func text(value map[string]any, key string) string {
	raw, _ := value[key].(string)
	return strings.TrimSpace(raw)
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
