// Package discovery implements explicit, bounded TCP capability scanning. It
// does not guess credentials, send protocol mutations, or scan undeclared
// ports.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

type Protocol string

const (
	Redfish Protocol = "redfish"
	IPMI    Protocol = "ipmi"
	SNMP    Protocol = "snmp"
	NUT     Protocol = "nut"
)

type Request struct {
	CIDR          string
	Protocols     []Protocol
	Ports         map[Protocol][]uint16
	ConfirmLarge  bool
	RatePerSecond int
	Concurrency   int
}

type Observation struct {
	Address  string   `json:"address"`
	Protocol Protocol `json:"protocol"`
	Port     uint16   `json:"port"`
	Open     bool     `json:"open"`
	Error    string   `json:"error,omitempty"`
}

func Scan(ctx context.Context, request Request) ([]Observation, error) {
	addresses, err := expand(request.CIDR)
	if err != nil {
		return nil, err
	}
	if len(addresses) > 256 && !request.ConfirmLarge {
		return nil, fmt.Errorf("CIDR expands to %d addresses; explicit confirmation is required above 256", len(addresses))
	}
	if len(request.Protocols) == 0 {
		return nil, errors.New("at least one protocol must be selected")
	}
	rate := request.RatePerSecond
	if rate <= 0 {
		rate = 32
	}
	concurrency := request.Concurrency
	if concurrency <= 0 || concurrency > 64 {
		concurrency = 16
	}
	type target struct {
		address  string
		protocol Protocol
		port     uint16
	}
	var targets []target
	for _, address := range addresses {
		for _, protocol := range request.Protocols {
			if protocol != Redfish && protocol != IPMI && protocol != SNMP && protocol != NUT {
				return nil, fmt.Errorf("unsupported protocol %q", protocol)
			}
			ports := request.Ports[protocol]
			if len(ports) == 0 {
				ports = defaultPorts(protocol)
			}
			if len(ports) == 0 {
				return nil, fmt.Errorf("unsupported protocol %q", protocol)
			}
			for _, port := range ports {
				if port == 0 {
					return nil, errors.New("scan port must be between 1 and 65535")
				}
				targets = append(targets, target{address, protocol, port})
			}
		}
	}
	jobs := make(chan target)
	results := make(chan Observation, len(targets))
	interval := time.Second / time.Duration(rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var workers sync.WaitGroup
	for index := 0; index < concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			dialer := net.Dialer{Timeout: 800 * time.Millisecond}
			for item := range jobs {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				address := net.JoinHostPort(item.address, fmt.Sprint(item.port))
				connection, err := dialer.DialContext(ctx, "tcp", address)
				observation := Observation{Address: item.address, Protocol: item.protocol, Port: item.port, Open: err == nil}
				if err != nil {
					observation.Error = err.Error()
				} else {
					_ = connection.Close()
				}
				results <- observation
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, item := range targets {
			select {
			case <-ctx.Done():
				return
			case jobs <- item:
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	var observations []Observation
	for result := range results {
		observations = append(observations, result)
	}
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].Address == observations[j].Address {
			if observations[i].Protocol == observations[j].Protocol {
				return observations[i].Port < observations[j].Port
			}
			return observations[i].Protocol < observations[j].Protocol
		}
		return observations[i].Address < observations[j].Address
	})
	if err := ctx.Err(); err != nil {
		return observations, err
	}
	return observations, nil
}

func expand(value string) ([]string, error) {
	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}
	ip = ip.Mask(network.Mask)
	var result []string
	for current := append(net.IP(nil), ip...); network.Contains(current); increment(current) {
		result = append(result, current.String())
		if len(result) > 65536 {
			return nil, errors.New("CIDR scan is limited to 65,536 addresses")
		}
	}
	return result, nil
}

func increment(ip net.IP) {
	for index := len(ip) - 1; index >= 0; index-- {
		ip[index]++
		if ip[index] != 0 {
			break
		}
	}
}

func defaultPorts(protocol Protocol) []uint16 {
	switch protocol {
	case Redfish:
		return []uint16{443}
	case IPMI:
		// RMCP+ is UDP. TCP 623 is only a coarse reachability hint and must
		// be explicitly selected if wanted, so there is no implicit port.
		return nil
	case SNMP:
		// SNMP is UDP and intentionally requires an explicit port/probe.
		return nil
	case NUT:
		return []uint16{3493}
	default:
		return nil
	}
}
