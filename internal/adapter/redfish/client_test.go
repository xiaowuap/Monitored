package redfish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectFollowsChassisLinks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", jsonHandler(`{"UUID":"service-1","Name":"BMC","Chassis":{"@odata.id":"/redfish/v1/Chassis"}}`))
	mux.HandleFunc("/redfish/v1/Chassis", jsonHandler(`{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`))
	mux.HandleFunc("/redfish/v1/Chassis/1", jsonHandler(`{"Id":"1","Name":"Main chassis","SerialNumber":"ABC","Thermal":{"@odata.id":"/redfish/v1/Chassis/1/Thermal"},"Power":{"@odata.id":"/redfish/v1/Chassis/1/Power"}}`))
	mux.HandleFunc("/redfish/v1/Chassis/1/Thermal", jsonHandler(`{"Temperatures":[{"MemberId":"CPU1","Name":"CPU 1 Temp","ReadingCelsius":63,"Status":{"Health":"OK"}}],"Fans":[{"Name":"FAN1","Reading":7200}]}`))
	mux.HandleFunc("/redfish/v1/Chassis/1/Power", jsonHandler(`{"PowerControl":[{"Name":"System Power","PowerConsumedWatts":412.5}]}`))
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	client := New(server.URL, "user", "pass", "", true)
	batch, err := client.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 2 || len(batch.Sensors) != 3 || len(batch.Samples) != 3 {
		t.Fatalf("unexpected result assets=%d sensors=%d samples=%d", len(batch.Assets), len(batch.Sensors), len(batch.Samples))
	}
	if *batch.Samples[0].Value != 63 {
		t.Fatalf("unexpected temperature %#v", batch.Samples[0])
	}
}

func jsonHandler(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}
}
