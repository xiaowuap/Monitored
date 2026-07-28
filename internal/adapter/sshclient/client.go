// Package sshclient provides a strict-host-key, read-only SSH fallback for
// FreeBSD and legacy appliances. The command set is fixed in code and is never
// taken from a connection profile or LLM output.
package sshclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
	"golang.org/x/crypto/ssh"
)

type Client struct {
	Address            string
	Username           string
	Password           string
	PrivateKey         string
	PrivateKeyPassword string
	HostKeySHA256      string
	Timeout            time.Duration
}

func (c Client) Collect(ctx context.Context) (domain.IngestBatch, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return domain.IngestBatch{}, err
	}
	defer client.Close()
	hostname, err := run(client, "uname -n")
	if err != nil {
		return domain.IngestBatch{}, fmt.Errorf("read remote hostname: %w", err)
	}
	osVersion, _ := run(client, "uname -sr")
	hostID := "ssh:" + fingerprint(c.Address+":"+strings.TrimSpace(hostname))
	batch := domain.IngestBatch{Assets: []domain.IngestAsset{{
		StableID: hostID, Kind: "host", Name: strings.TrimSpace(hostname),
		Metadata: map[string]any{
			"endpoint": c.Address, "os": strings.TrimSpace(osVersion),
			"transport": "ssh-readonly", "hostKey": c.HostKeySHA256,
		},
	}}}
	if raw, commandErr := run(client, "sysctl -a"); commandErr == nil {
		appendSysctl(&batch, hostID, raw)
	}
	if raw, commandErr := run(client, "zpool list -Hp -o name,size,alloc,free,health"); commandErr == nil {
		appendPools(&batch, hostID, raw)
	}
	return batch, nil
}

func (c Client) connect(ctx context.Context) (*ssh.Client, error) {
	if strings.TrimSpace(c.Username) == "" {
		return nil, errors.New("SSH username is required")
	}
	if strings.TrimSpace(c.HostKeySHA256) == "" {
		return nil, errors.New("SSH hostKeySHA256 is required; trust-on-first-use is not supported")
	}
	var auth []ssh.AuthMethod
	if c.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if c.PrivateKeyPassword != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.PrivateKeyPassword))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("parse SSH private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("SSH password or private key is required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	config := &ssh.ClientConfig{
		User: c.Username, Auth: auth, Timeout: timeout,
		HostKeyCallback: verifyHostKey(c.HostKeySHA256),
	}
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return nil, fmt.Errorf("connect SSH: %w", err)
	}
	transport, channels, requests, err := ssh.NewClientConn(connection, c.Address, config)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("negotiate SSH: %w", err)
	}
	return ssh.NewClient(transport, channels, requests), nil
}

func verifyHostKey(expected string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if actual != strings.TrimSpace(expected) {
			return fmt.Errorf("SSH host key mismatch: received %s", actual)
		}
		return nil
	}
}

func run(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 512<<10, 64<<10
	session.Stdout, session.Stderr = &stdout, &stderr
	if err := session.Run(command); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func appendSysctl(batch *domain.IngestBatch, hostID, raw string) {
	now := time.Now().UTC()
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		category, unit, numericValue, supported := sysctlMetric(key, value)
		if !supported {
			continue
		}
		sensorID := "ssh:" + fingerprint(hostID+":"+key)
		batch.Sensors = append(batch.Sensors, domain.IngestSensor{
			StableID: sensorID, AssetStableID: hostID, Source: "ssh",
			SourcePath: key, RawName: key, Category: category, Unit: unit,
			Scale: 1, IntervalSeconds: 30, Quality: "good",
		})
		batch.Samples = append(batch.Samples, domain.IngestSample{
			SensorStableID: sensorID, ObservedAt: now, Value: &numericValue,
			Source: "ssh", Quality: "good",
		})
	}
}

func sysctlMetric(key, value string) (string, string, float64, bool) {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "temperature") || strings.HasSuffix(lower, ".temp"):
		parsed, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "C"), 64)
		return "temperature", "Cel", parsed, err == nil
	case strings.Contains(lower, "fan") && strings.HasSuffix(lower, ".rpm"):
		parsed, err := strconv.ParseFloat(value, 64)
		return "fan", "rpm", parsed, err == nil
	default:
		return "", "", 0, false
	}
}

func appendPools(batch *domain.IngestBatch, hostID, raw string) {
	now := time.Now().UTC()
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		assetID := "zfs:" + fingerprint(hostID+":"+fields[0])
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: hostID, Kind: "pool", Name: fields[0],
			Vendor: "OpenZFS", Metadata: map[string]any{"health": fields[4], "source": "ssh"},
		})
		for index, metric := range []struct {
			name string
			unit string
		}{{"size", "By"}, {"allocated", "By"}, {"free", "By"}} {
			value, err := strconv.ParseFloat(fields[index+1], 64)
			if err != nil {
				continue
			}
			sensorID := "ssh:" + fingerprint(assetID+":"+metric.name)
			batch.Sensors = append(batch.Sensors, domain.IngestSensor{
				StableID: sensorID, AssetStableID: assetID, Source: "ssh",
				SourcePath: "zpool/" + fields[0] + "/" + metric.name,
				RawName:    metric.name, Category: "capacity", Unit: metric.unit,
				Scale: 1, IntervalSeconds: 30, Quality: "good",
			})
			batch.Samples = append(batch.Samples, domain.IngestSample{
				SensorStableID: sensorID, ObservedAt: now, Value: &value,
				Source: "ssh", Quality: "good",
			})
		}
	}
}

func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	count := len(value)
	if b.Len() < b.limit {
		_, _ = b.Buffer.Write(value[:min(len(value), b.limit-b.Len())])
	}
	return count, nil
}
