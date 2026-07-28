// Package snmp provides a constrained declarative profile mapper and SNMP
// v2c/v3 authPriv transport. Profiles can scale/add/enum values but cannot
// execute expressions or arbitrary code.
package snmp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/monitored-io/monitored/internal/domain"
	"gopkg.in/yaml.v3"
)

var oidPattern = regexp.MustCompile(`^\.?[0-9]+(\.[0-9]+)+$`)

type Profile struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Vendor      string `yaml:"vendor" json:"vendor"`
}

type Spec struct {
	Matches  Matches  `yaml:"matches" json:"matches"`
	Entities []Entity `yaml:"entities" json:"entities"`
	Sensors  []Sensor `yaml:"sensors" json:"sensors"`
}

type Matches struct {
	SysObjectIDPrefix []string `yaml:"sysObjectIDPrefix" json:"sysObjectIDPrefix"`
	SysDescrRegex     string   `yaml:"sysDescrRegex" json:"sysDescrRegex"`
}

type Entity struct {
	Key            string     `yaml:"key" json:"key"`
	AssetKind      string     `yaml:"assetKind" json:"assetKind"`
	StableIdentity []Identity `yaml:"stableIdentity" json:"stableIdentity"`
}

type Identity struct {
	OID       string `yaml:"oid" json:"oid"`
	IndexFrom string `yaml:"indexFrom" json:"indexFrom"`
}

type Sensor struct {
	ID         string             `yaml:"id" json:"id"`
	Entity     string             `yaml:"entity" json:"entity"`
	OID        string             `yaml:"oid" json:"oid"`
	Name       string             `yaml:"name" json:"name"`
	Category   string             `yaml:"category" json:"category"`
	Unit       string             `yaml:"unit" json:"unit"`
	Scale      float64            `yaml:"scale" json:"scale"`
	Monotonic  bool               `yaml:"monotonic" json:"monotonic"`
	Enum       map[string]string  `yaml:"enum" json:"enum"`
	Thresholds map[string]float64 `yaml:"thresholds" json:"thresholds"`
	Transform  Transform          `yaml:"transform" json:"transform"`
}

type Transform struct {
	Multiply *float64 `yaml:"multiply" json:"multiply"`
	Add      *float64 `yaml:"add" json:"add"`
}

type Value struct {
	OID   string
	Value any
}

func LoadProfile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("decode SNMP profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (p Profile) Validate() error {
	if p.APIVersion != "monitored.io/v1alpha1" || p.Kind != "SNMPProfile" {
		return errors.New("unsupported SNMP profile API version or kind")
	}
	if p.Metadata.Name == "" || len(p.Spec.Entities) == 0 || len(p.Spec.Sensors) == 0 {
		return errors.New("profile name, entities, and sensors are required")
	}
	entities := make(map[string]struct{}, len(p.Spec.Entities))
	for _, entity := range p.Spec.Entities {
		if entity.Key == "" || entity.AssetKind == "" || len(entity.StableIdentity) == 0 {
			return errors.New("each entity requires key, asset kind, and stable identity")
		}
		entities[entity.Key] = struct{}{}
		for _, identity := range entity.StableIdentity {
			if !oidPattern.MatchString(identity.OID) {
				return fmt.Errorf("invalid identity OID %q", identity.OID)
			}
		}
	}
	for _, sensor := range p.Spec.Sensors {
		if sensor.ID == "" || !oidPattern.MatchString(sensor.OID) {
			return fmt.Errorf("invalid sensor %q", sensor.ID)
		}
		if _, ok := entities[sensor.Entity]; !ok {
			return fmt.Errorf("sensor %q references unknown entity %q", sensor.ID, sensor.Entity)
		}
	}
	if p.Spec.Matches.SysDescrRegex != "" {
		if _, err := regexp.Compile(p.Spec.Matches.SysDescrRegex); err != nil {
			return fmt.Errorf("invalid sysDescr regex: %w", err)
		}
	}
	return nil
}

func Map(profile Profile, endpoint string, values []Value) (domain.IngestBatch, error) {
	if err := profile.Validate(); err != nil {
		return domain.IngestBatch{}, err
	}
	byOID := make(map[string]any, len(values))
	for _, value := range values {
		byOID[normalizeOID(value.OID)] = value.Value
	}
	var batch domain.IngestBatch
	assetIDs := make(map[string]string)
	for _, entity := range profile.Spec.Entities {
		identityParts := []string{endpoint, entity.Key}
		for _, identity := range entity.StableIdentity {
			identityParts = append(identityParts, stringify(byOID[normalizeOID(identity.OID)]))
		}
		stableID := "snmp:" + fingerprint(strings.Join(identityParts, ":"))
		assetIDs[entity.Key] = stableID
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: stableID, Kind: entity.AssetKind,
			Name:     first(profile.Metadata.DisplayName, profile.Metadata.Name),
			Vendor:   profile.Metadata.Vendor,
			Metadata: map[string]any{"profile": profile.Metadata.Name, "endpoint": endpoint},
		})
	}
	now := time.Now().UTC()
	observedOIDs := make([]string, 0, len(byOID))
	for observedOID := range byOID {
		observedOIDs = append(observedOIDs, observedOID)
	}
	sort.Strings(observedOIDs)
	for _, sensor := range profile.Spec.Sensors {
		baseOID := normalizeOID(sensor.OID)
		for _, observedOID := range observedOIDs {
			raw := byOID[observedOID]
			if observedOID != baseOID && !strings.HasPrefix(observedOID, baseOID+".") {
				continue
			}
			index := strings.TrimPrefix(strings.TrimPrefix(observedOID, baseOID), ".")
			if index == "" {
				index = "0"
			}
			sensorID := "snmp:" + fingerprint(assetIDs[sensor.Entity]+":"+sensor.ID+":"+observedOID)
			scale := sensor.Scale
			if scale == 0 {
				scale = 1
			}
			rawName := sensor.Name
			if observedOID != baseOID {
				rawName += " [" + index + "]"
			}
			definition := domain.IngestSensor{
				StableID: sensorID, AssetStableID: assetIDs[sensor.Entity], Source: "snmp",
				SourcePath: observedOID, RawName: rawName, Category: sensor.Category,
				Unit: sensor.Unit, Scale: scale, Monotonic: sensor.Monotonic, IntervalSeconds: 30,
				Quality: "good", Metadata: map[string]any{
					"profile": profile.Metadata.Name, "sensorId": sensor.ID,
					"index": index, "thresholds": sensor.Thresholds,
				},
			}
			sample := domain.IngestSample{SensorStableID: sensorID, ObservedAt: now, Source: "snmp", Quality: "good"}
			if numeric, ok := numeric(raw); ok {
				numeric *= scale
				if sensor.Transform.Multiply != nil {
					numeric *= *sensor.Transform.Multiply
				}
				if sensor.Transform.Add != nil {
					numeric += *sensor.Transform.Add
				}
				sample.Value = &numeric
			} else {
				text := stringify(raw)
				if mapped, exists := sensor.Enum[text]; exists {
					text = mapped
				}
				sample.TextValue = &text
			}
			batch.Sensors = append(batch.Sensors, definition)
			batch.Samples = append(batch.Samples, sample)
		}
	}
	return batch, nil
}

type Config struct {
	Target       string
	Port         uint16
	Version      string
	Community    string
	Username     string
	AuthProtocol string
	AuthPassword string
	PrivProtocol string
	PrivPassword string
	ContextName  string
	Timeout      time.Duration
	Retries      int
}

func Collect(ctx context.Context, config Config, profile Profile) (domain.IngestBatch, error) {
	client, err := newClient(config)
	if err != nil {
		return domain.IngestBatch{}, err
	}
	if err := client.Connect(); err != nil {
		return domain.IngestBatch{}, fmt.Errorf("connect SNMP: %w", err)
	}
	defer client.Conn.Close()
	oids := uniqueOIDs(profile)
	values := make([]Value, 0, len(oids))
	for _, oid := range oids {
		if err := ctx.Err(); err != nil {
			return domain.IngestBatch{}, err
		}
		if strings.HasSuffix(normalizeOID(oid), ".0") {
			packet, err := client.Get([]string{"." + normalizeOID(oid)})
			if err != nil {
				return domain.IngestBatch{}, fmt.Errorf("GET %s: %w", oid, err)
			}
			if len(packet.Variables) == 0 || packet.Variables[0].Type == gosnmp.NoSuchObject || packet.Variables[0].Type == gosnmp.NoSuchInstance {
				continue
			}
			values = append(values, Value{OID: packet.Variables[0].Name, Value: variableValue(packet.Variables[0])})
			continue
		}
		variables, err := client.BulkWalkAll("." + normalizeOID(oid))
		if err != nil {
			return domain.IngestBatch{}, fmt.Errorf("WALK %s: %w", oid, err)
		}
		for _, variable := range variables {
			values = append(values, Value{OID: variable.Name, Value: variableValue(variable)})
		}
	}
	return Map(profile, config.Target, values)
}

func newClient(config Config) (*gosnmp.GoSNMP, error) {
	client := &gosnmp.GoSNMP{
		Target: config.Target, Port: config.Port, Community: config.Community,
		Timeout: config.Timeout, Retries: config.Retries, ContextName: config.ContextName,
		MaxOids: gosnmp.MaxOids,
	}
	if client.Port == 0 {
		client.Port = 161
	}
	if client.Timeout <= 0 {
		client.Timeout = 5 * time.Second
	}
	switch strings.ToLower(config.Version) {
	case "", "2c", "v2c":
		client.Version = gosnmp.Version2c
	case "3", "v3":
		if config.Username == "" || config.AuthPassword == "" || config.PrivPassword == "" {
			return nil, errors.New("SNMPv3 authPriv requires username, auth password, and privacy password")
		}
		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = gosnmp.AuthPriv
		client.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName: config.Username, AuthenticationProtocol: authProtocol(config.AuthProtocol),
			AuthenticationPassphrase: config.AuthPassword, PrivacyProtocol: privProtocol(config.PrivProtocol),
			PrivacyPassphrase: config.PrivPassword,
		}
	default:
		return nil, fmt.Errorf("unsupported SNMP version %q", config.Version)
	}
	return client, nil
}

func authProtocol(value string) gosnmp.SnmpV3AuthProtocol {
	switch strings.ToUpper(value) {
	case "SHA256":
		return gosnmp.SHA256
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.SHA
	}
}

func privProtocol(value string) gosnmp.SnmpV3PrivProtocol {
	switch strings.ToUpper(value) {
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.AES
	}
}

func uniqueOIDs(profile Profile) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, entity := range profile.Spec.Entities {
		for _, identity := range entity.StableIdentity {
			oid := normalizeOID(identity.OID)
			if _, ok := seen[oid]; !ok {
				seen[oid] = struct{}{}
				result = append(result, oid)
			}
		}
	}
	for _, sensor := range profile.Spec.Sensors {
		oid := normalizeOID(sensor.OID)
		if _, ok := seen[oid]; !ok {
			seen[oid] = struct{}{}
			result = append(result, oid)
		}
	}
	return result
}

func variableValue(value gosnmp.SnmpPDU) any {
	switch value.Type {
	case gosnmp.OctetString:
		return string(value.Value.([]byte))
	default:
		return gosnmp.ToBigInt(value.Value).String()
	}
}

func numeric(value any) (float64, bool) {
	result, err := strconv.ParseFloat(stringify(value), 64)
	return result, err == nil
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func normalizeOID(value string) string { return strings.TrimPrefix(value, ".") }

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
