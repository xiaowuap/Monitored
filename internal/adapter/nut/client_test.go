package nut

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestCollectNUTVariables(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, _ := listener.Accept()
		defer connection.Close()
		reader := bufio.NewReader(connection)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) == "LIST UPS" {
			_, _ = fmt.Fprint(connection, "BEGIN LIST UPS\nUPS rack-a \"Rack A UPS\"\nEND LIST UPS\n")
		}
		line, _ = reader.ReadString('\n')
		if strings.HasPrefix(line, "LIST VAR") {
			_, _ = fmt.Fprint(connection, "BEGIN LIST VAR\nVAR rack-a ups.mfr \"APC\"\nVAR rack-a ups.model \"SRT\"\nVAR rack-a ups.status \"OL LB\"\nVAR rack-a battery.charge \"42\"\nVAR rack-a battery.runtime \"1440\"\nEND LIST VAR\n")
		}
	}()

	batch, err := (Client{Address: listener.Addr().String()}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Assets) != 1 || len(batch.Sensors) != 3 {
		t.Fatalf("unexpected batch assets=%d sensors=%d", len(batch.Assets), len(batch.Sensors))
	}
	if batch.Assets[0].Vendor != "APC" {
		t.Fatalf("unexpected asset %#v", batch.Assets[0])
	}
}
