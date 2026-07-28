package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monitored-io/monitored/internal/config"
)

func TestDecodeJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	var payload struct {
		Name string `json:"name"`
	}
	unknown := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ok","extra":true}`))
	if err := decodeJSON(unknown, &payload); err == nil {
		t.Fatal("decodeJSON accepted an unknown field")
	}

	trailing := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ok"} {"name":"second"}`))
	if err := decodeJSON(trailing, &payload); err == nil {
		t.Fatal("decodeJSON accepted multiple JSON values")
	}

	valid := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ok"}`))
	if err := decodeJSON(valid, &payload); err != nil || payload.Name != "ok" {
		t.Fatalf("decodeJSON valid payload: name=%q err=%v", payload.Name, err)
	}
}

func TestOriginAllowedLimitsLocalhostExceptionToLocalDevelopment(t *testing.T) {
	local := &API{config: config.Server{PublicURL: "http://localhost:3000"}}
	if !local.originAllowed("http://127.0.0.1:3001") {
		t.Fatal("local development origin should be allowed")
	}

	production := &API{config: config.Server{PublicURL: "https://monitored.example"}}
	if production.originAllowed("http://localhost:3001") {
		t.Fatal("production configuration accepted a localhost origin")
	}
	if !production.originAllowed("https://monitored.example") {
		t.Fatal("configured production origin should be allowed")
	}
}

func TestAgentEnrollmentRequiresHTTPSOutsideLoopback(t *testing.T) {
	for _, test := range []struct {
		publicURL string
		allowed   bool
	}{
		{publicURL: "https://monitored.example", allowed: true},
		{publicURL: "http://localhost:3000", allowed: true},
		{publicURL: "http://127.0.0.1:3000", allowed: true},
		{publicURL: "http://monitored.example", allowed: false},
	} {
		api := &API{config: config.Server{PublicURL: test.publicURL}}
		if got := api.secureEnrollmentEndpoint(); got != test.allowed {
			t.Fatalf("secureEnrollmentEndpoint(%q) = %v", test.publicURL, got)
		}
	}
}
