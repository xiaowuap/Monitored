package discovery

import (
	"context"
	"net"
	"testing"
)

func TestScanOnlySelectedEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	go func() {
		connection, _ := listener.Accept()
		if connection != nil {
			_ = connection.Close()
		}
	}()
	results, err := Scan(context.Background(), Request{
		CIDR: "127.0.0.1/32", Protocols: []Protocol{NUT},
		Ports: map[Protocol][]uint16{NUT: {port}}, RatePerSecond: 1000,
	})
	if err != nil || len(results) != 1 || !results[0].Open {
		t.Fatalf("unexpected scan %#v, %v", results, err)
	}
}

func TestLargeCIDRRequiresConfirmation(t *testing.T) {
	if _, err := Scan(context.Background(), Request{CIDR: "10.0.0.0/23", Protocols: []Protocol{Redfish}}); err == nil {
		t.Fatal("large CIDR must require confirmation")
	}
}
