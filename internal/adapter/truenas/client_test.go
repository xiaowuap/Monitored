package truenas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestCollectJSONRPCInventory(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing token header")
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for index := 0; index < 4; index++ {
			var request rpcRequest
			if err := connection.ReadJSON(&request); err != nil {
				return
			}
			var result any
			switch request.Method {
			case "system.info":
				result = map[string]any{"hostname": "nas-1", "system_serial": "SERIAL-1", "version": "25.10"}
			case "pool.query":
				result = []map[string]any{{"name": "tank", "status": "ONLINE", "size": 1000.0, "allocated": 400.0, "free": 600.0}}
			case "disk.query":
				result = []map[string]any{{"name": "sda", "serial": "DISK-1", "model": "Exos", "temperature": 41.0}}
			case "disk.temperatures":
				result = map[string]any{"sda": []any{42.0, 60.0}}
			}
			raw, _ := json.Marshal(result)
			_ = connection.WriteJSON(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: raw})
		}
	}))
	defer server.Close()

	client := &Client{
		Endpoint: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "test-token",
		AgentID: "scale-agent", HostIdentity: "machine-id-1",
	}
	batch, err := client.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 3 || len(batch.Samples) != 4 {
		t.Fatalf("unexpected batch assets=%d samples=%d", len(batch.Assets), len(batch.Samples))
	}
	if batch.AgentID != "scale-agent" || batch.Assets[0].StableID != "host:"+fingerprint("machine-id-1") ||
		batch.Assets[1].ParentStableID != batch.Assets[0].StableID {
		t.Fatalf("agent host identity was not merged: %#v", batch)
	}
	if batch.Assets[2].StableID != "disk:"+fingerprint("DISK-1") {
		t.Fatalf("disk identity did not match local SMART serial identity: %q", batch.Assets[2].StableID)
	}
	temperature := batch.Samples[len(batch.Samples)-1]
	if temperature.Value == nil || *temperature.Value != 42 {
		t.Fatalf("disk.temperatures value was not preferred: %#v", temperature)
	}
	temperatureSensor := batch.Sensors[len(batch.Sensors)-1]
	if temperatureSensor.Metadata["criticalC"] != 60.0 || temperatureSensor.IntervalSeconds != 300 {
		t.Fatalf("temperature threshold/cache metadata missing: %#v", temperatureSensor)
	}
}

func TestParseDiskTemperaturesSupportsCurrentShapes(t *testing.T) {
	values := map[string]json.RawMessage{
		"sda": []byte(`[41,60]`),
		"sdb": []byte(`42`),
		"sdc": []byte(`{"temperature":43,"critical":65}`),
		"sdd": []byte(`null`),
	}
	parsed := parseDiskTemperatures(values)
	if len(parsed) != 3 || *parsed["sda"].Current != 41 || *parsed["sda"].Critical != 60 ||
		*parsed["sdb"].Current != 42 || *parsed["sdc"].Current != 43 || *parsed["sdc"].Critical != 65 {
		t.Fatalf("unexpected temperature shapes: %#v", parsed)
	}
}

func TestMapInventoryFallsBackToInlineDiskTemperature(t *testing.T) {
	batch := mapInventory(
		"wss://nas.example/api/current",
		map[string]any{"hostname": "nas"},
		nil,
		[]map[string]any{{"name": "sda", "serial": "DISK-1", "temperature": 39.0}},
		nil,
	)
	if len(batch.Samples) != 1 || batch.Samples[0].Value == nil || *batch.Samples[0].Value != 39 {
		t.Fatalf("inline temperature fallback missing: %#v", batch)
	}
}
