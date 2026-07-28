package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAndTCPAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	batch, err := (HTTP{URL: server.URL, ExpectedStatus: http.StatusNoContent}).Collect(context.Background())
	if err != nil || len(batch.Samples) != 2 || batch.Samples[1].TextValue == nil || *batch.Samples[1].TextValue != "up" {
		t.Fatalf("unexpected HTTP result %#v, %v", batch, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	batch, err = (TCP{Address: listener.Addr().String()}).Collect(context.Background())
	if err != nil || batch.Samples[1].Value == nil || *batch.Samples[1].Value != 1 {
		t.Fatalf("unexpected TCP result %#v, %v", batch, err)
	}
}
