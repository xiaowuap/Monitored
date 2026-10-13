package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/monitored-io/monitored/internal/domain"
)

// The URL must point at a test PostgreSQL instance with CREATEDB permission.
// Each run owns a separate database; the configured database is never migrated.
func TestDiskWritesPostgres(t *testing.T) {
	url := os.Getenv("MONITORED_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MONITORED_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("monitored_disk_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	}()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	// ConnString reflects the parsed config's original string, so override the
	// database explicitly using a keyword connection string for the repository.
	testURL := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable", cfg.Host, cfg.Port, cfg.User, name)
	if cfg.Password != "" {
		t.Fatal("use an isolated trust-authenticated test instance")
	}
	r, err := Open(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	observed := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Minute)
	value := 10.0
	batch := domain.IngestBatch{
		AgentID: "disk-test", Assets: []domain.IngestAsset{{StableID: "disk-host", Kind: "host", Name: "host"}},
		Sensors: []domain.IngestSensor{{StableID: "disk-temp", AssetStableID: "disk-host", Source: "fixture", SourcePath: "temp", RawName: "temp", Category: "temperature", Unit: "Cel"}},
		Samples: []domain.IngestSample{{SensorStableID: "disk-temp", ObservedAt: observed, Value: &value}},
	}
	if err := r.Ingest(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := r.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	versions := func(table string) string {
		t.Helper()
		var version string
		if err := r.pool.QueryRow(ctx, "SELECT xmin::text || ':' || ctid::text FROM "+table).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}
	minute, hour := versions("metric_1m"), versions("metric_1h")
	for range 3 {
		if err := r.RunMaintenance(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if minute != versions("metric_1m") || hour != versions("metric_1h") {
		t.Fatal("unchanged aggregates were rewritten")
	}
	value = 30
	batch.Samples[0].ObservedAt = observed.Add(time.Second)
	if err := r.Ingest(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := r.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"metric_1m", "metric_1h"} {
		var count int
		var min, avg, max, first, last float64
		if err := r.pool.QueryRow(ctx, "SELECT sample_count,min_value,avg_value,max_value,first_value,last_value FROM "+table).Scan(&count, &min, &avg, &max, &first, &last); err != nil {
			t.Fatal(err)
		}
		if count != 2 || min != 10 || avg != 20 || max != 30 || first != 10 || last != 30 {
			t.Fatalf("%s late sample lost: %d %v %v %v %v %v", table, count, min, avg, max, first, last)
		}
	}
	minute, hour = versions("metric_1m"), versions("metric_1h")
	if err := r.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if minute != versions("metric_1m") || hour != versions("metric_1h") {
		t.Fatal("updated aggregates are not stable")
	}
	empty := domain.IngestBatch{AgentID: "disk-test"}
	for range 6 {
		if err := r.ApplyDiscoverySnapshot(ctx, "", "disk-test", empty); err != nil {
			t.Fatal(err)
		}
	}
	removed := versions("sensor_definitions")
	for range 3 {
		if err := r.ApplyDiscoverySnapshot(ctx, "", "disk-test", empty); err != nil {
			t.Fatal(err)
		}
	}
	if removed != versions("sensor_definitions") {
		t.Fatal("removed sensor was rewritten")
	}
	var missing int
	var status string
	if err := r.pool.QueryRow(ctx, "SELECT missing_discoveries,discovery_status FROM sensor_definitions").Scan(&missing, &status); err != nil {
		t.Fatal(err)
	}
	if missing != 6 || status != "removed" {
		t.Fatalf("terminal state: %d %s", missing, status)
	}
	batch.Samples = nil
	if err := r.ApplyDiscoverySnapshot(ctx, "", "disk-test", batch); err != nil {
		t.Fatal(err)
	}
	if err := r.pool.QueryRow(ctx, "SELECT missing_discoveries,discovery_status FROM sensor_definitions").Scan(&missing, &status); err != nil {
		t.Fatal(err)
	}
	if missing != 0 || status != "present" {
		t.Fatalf("rediscovery did not restore sensor: %d %s", missing, status)
	}
}
