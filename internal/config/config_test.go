package config

import "testing"

func TestPublicGRPCTargetUsesPublicHostAndListenPort(t *testing.T) {
	if got := publicGRPCTarget("https://monitor.example/app", ":9443"); got != "monitor.example:9443" {
		t.Fatalf("publicGRPCTarget() = %q", got)
	}
	if got := publicGRPCTarget("not a URL", ":9443"); got != "" {
		t.Fatalf("publicGRPCTarget() accepted invalid URL: %q", got)
	}
}
