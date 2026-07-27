package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/monitored-io/monitored/internal/domain"
)

func (r *Repository) Ingest(ctx context.Context, batch domain.IngestBatch) error {
	if batch.AgentID == "" {
		return fmt.Errorf("agentId is required")
	}
	if len(batch.Assets) > 500 || len(batch.Sensors) > 5000 || len(batch.Samples) > 20000 {
		return fmt.Errorf("ingest batch exceeds cardinality limits")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ingest: %w", err)
	}
	defer tx.Rollback(ctx)

	assetIDs := make(map[string]string, len(batch.Assets))
	for _, asset := range batch.Assets {
		metadata, marshalErr := json.Marshal(asset.Metadata)
		if marshalErr != nil {
			return fmt.Errorf("encode asset metadata: %w", marshalErr)
		}
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO assets (
				stable_id, kind, name, vendor, model, health, connection,
				source, last_seen_at, metadata
			)
			VALUES ($1, $2, $3, $4, $5, 'unknown', 'online', 'agent', now(), $6::jsonb)
			ON CONFLICT (stable_id) DO UPDATE SET
				kind = EXCLUDED.kind,
				name = EXCLUDED.name,
				vendor = EXCLUDED.vendor,
				model = EXCLUDED.model,
				connection = 'online',
				last_seen_at = now(),
				metadata = assets.metadata || EXCLUDED.metadata,
				updated_at = now()
			RETURNING id::text`,
			asset.StableID, asset.Kind, asset.Name, asset.Vendor, asset.Model, metadata,
		).Scan(&id)
		if err != nil {
			return fmt.Errorf("upsert asset %s: %w", asset.StableID, err)
		}
		assetIDs[asset.StableID] = id
	}
	for _, asset := range batch.Assets {
		if asset.ParentStableID == "" {
			continue
		}
		childID := assetIDs[asset.StableID]
		parentID, ok := assetIDs[asset.ParentStableID]
		if !ok {
			err = tx.QueryRow(ctx, `SELECT id::text FROM assets WHERE stable_id = $1`, asset.ParentStableID).Scan(&parentID)
			if err != nil {
				return fmt.Errorf("resolve parent asset %s: %w", asset.ParentStableID, err)
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE assets SET parent_id = $2::uuid WHERE id = $1::uuid`, childID, parentID); err != nil {
			return fmt.Errorf("link parent asset: %w", err)
		}
	}

	sensorIDs := make(map[string]string, len(batch.Sensors))
	for _, sensor := range batch.Sensors {
		assetID, ok := assetIDs[sensor.AssetStableID]
		if !ok {
			err = tx.QueryRow(ctx, `SELECT id::text FROM assets WHERE stable_id = $1`, sensor.AssetStableID).Scan(&assetID)
			if err != nil {
				return fmt.Errorf("resolve sensor asset %s: %w", sensor.AssetStableID, err)
			}
		}
		metadata, marshalErr := json.Marshal(sensor.Metadata)
		if marshalErr != nil {
			return fmt.Errorf("encode sensor metadata: %w", marshalErr)
		}
		scale := sensor.Scale
		if scale == 0 {
			scale = 1
		}
		interval := sensor.IntervalSeconds
		if interval <= 0 {
			interval = 30
		}
		quality := sensor.Quality
		if quality == "" {
			quality = "good"
		}
		category := sensor.Category
		if category == "" {
			category = "unknown"
		}
		unit := sensor.Unit
		if unit == "" {
			unit = "1"
		}
		var sensorID string
		err = tx.QueryRow(ctx, `
			INSERT INTO sensor_definitions (
				stable_id, asset_id, source, source_path, raw_name, category,
				unit, scale, monotonic, interval_seconds, discovery_status,
				missing_discoveries, quality, metadata, last_seen_at
			)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, 'present', 0, $11, $12::jsonb, now())
			ON CONFLICT (stable_id) DO UPDATE SET
				asset_id = EXCLUDED.asset_id,
				source = EXCLUDED.source,
				source_path = EXCLUDED.source_path,
				raw_name = EXCLUDED.raw_name,
				category = CASE
					WHEN sensor_definitions.display_alias IS NULL THEN EXCLUDED.category
					ELSE sensor_definitions.category
				END,
				unit = EXCLUDED.unit,
				scale = EXCLUDED.scale,
				monotonic = EXCLUDED.monotonic,
				interval_seconds = EXCLUDED.interval_seconds,
				discovery_status = 'present',
				missing_discoveries = 0,
				quality = EXCLUDED.quality,
				metadata = sensor_definitions.metadata || EXCLUDED.metadata,
				last_seen_at = now(),
				updated_at = now()
			RETURNING id::text`,
			sensor.StableID, assetID, sensor.Source, sensor.SourcePath, sensor.RawName,
			category, unit, scale, sensor.Monotonic, interval, quality, metadata,
		).Scan(&sensorID)
		if err != nil {
			return fmt.Errorf("upsert sensor %s: %w", sensor.StableID, err)
		}
		sensorIDs[sensor.StableID] = sensorID
		if _, err = tx.Exec(ctx, `
			INSERT INTO metric_series (sensor_id, normalized_key, labels_hash)
			VALUES ($1::uuid, $2, '')
			ON CONFLICT (sensor_id, normalized_key, labels_hash) DO NOTHING`,
			sensorID, category,
		); err != nil {
			return fmt.Errorf("ensure metric series: %w", err)
		}
	}

	for _, sample := range batch.Samples {
		sensorID, ok := sensorIDs[sample.SensorStableID]
		if !ok {
			err = tx.QueryRow(ctx, `SELECT id::text FROM sensor_definitions WHERE stable_id = $1`, sample.SensorStableID).Scan(&sensorID)
			if err != nil {
				return fmt.Errorf("resolve sample sensor %s: %w", sample.SensorStableID, err)
			}
		}
		if sample.ObservedAt.IsZero() {
			sample.ObservedAt = time.Now().UTC()
		}
		source := sample.Source
		if source == "" {
			source = "agent"
		}
		quality := sample.Quality
		if quality == "" {
			quality = "good"
		}
		tag, insertErr := tx.Exec(ctx, `
			INSERT INTO metric_samples (
				series_id, observed_at, value, text_value, source, quality, collection_error
			)
			SELECT id, $2, $3, $4, $5, $6, $7
			FROM metric_series
			WHERE sensor_id = $1::uuid
			ORDER BY created_at
			LIMIT 1`,
			sensorID, sample.ObservedAt, sample.Value, sample.TextValue,
			source, quality, sample.Error,
		)
		if insertErr != nil {
			return fmt.Errorf("insert sample %s: %w", sample.SensorStableID, insertErr)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("metric series missing for sensor %s", sample.SensorStableID)
		}
	}
	if err := appendAuditTx(ctx, tx, batch.AgentID, "telemetry.ingested", "agent", batch.AgentID, "", map[string]any{
		"assets":  len(batch.Assets),
		"sensors": len(batch.Sensors),
		"samples": len(batch.Samples),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) RunMaintenance(ctx context.Context) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin metric maintenance: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `
		INSERT INTO metric_1m (
			series_id, bucket, min_value, avg_value, max_value,
			sample_count, first_value, last_value
		)
		SELECT
			series_id,
			date_bin('1 minute', observed_at, timestamptz '2001-01-01') bucket,
			min(value),
			avg(value),
			max(value),
			count(*),
			(array_agg(value ORDER BY observed_at ASC))[1],
			(array_agg(value ORDER BY observed_at DESC))[1]
		FROM metric_samples
		WHERE observed_at >= now() - interval '2 hours'
		  AND observed_at < date_trunc('minute', now())
		  AND value IS NOT NULL
		GROUP BY series_id, bucket
		ON CONFLICT (series_id, bucket) DO UPDATE SET
			min_value = EXCLUDED.min_value,
			avg_value = EXCLUDED.avg_value,
			max_value = EXCLUDED.max_value,
			sample_count = EXCLUDED.sample_count,
			first_value = EXCLUDED.first_value,
			last_value = EXCLUDED.last_value`); err != nil {
		return fmt.Errorf("downsample metrics to 1m: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO metric_1h (
			series_id, bucket, min_value, avg_value, max_value,
			sample_count, first_value, last_value
		)
		SELECT
			series_id,
			date_bin('1 hour', bucket, timestamptz '2001-01-01') hour_bucket,
			min(min_value),
			sum(avg_value * sample_count) / sum(sample_count),
			max(max_value),
			sum(sample_count),
			(array_agg(first_value ORDER BY bucket ASC))[1],
			(array_agg(last_value ORDER BY bucket DESC))[1]
		FROM metric_1m
		WHERE bucket >= now() - interval '2 days'
		  AND bucket < date_trunc('hour', now())
		GROUP BY series_id, hour_bucket
		ON CONFLICT (series_id, bucket) DO UPDATE SET
			min_value = EXCLUDED.min_value,
			avg_value = EXCLUDED.avg_value,
			max_value = EXCLUDED.max_value,
			sample_count = EXCLUDED.sample_count,
			first_value = EXCLUDED.first_value,
			last_value = EXCLUDED.last_value`); err != nil {
		return fmt.Errorf("downsample metrics to 1h: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM metric_samples WHERE recorded_at < now() - interval '7 days'`); err != nil {
		return fmt.Errorf("expire raw metrics: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM metric_1m WHERE bucket < now() - interval '90 days'`); err != nil {
		return fmt.Errorf("expire 1m metrics: %w", err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM metric_1h WHERE bucket < now() - interval '2 years'`); err != nil {
		return fmt.Errorf("expire 1h metrics: %w", err)
	}
	return tx.Commit(ctx)
}
