package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/extension"
)

func (r *Repository) CreateSuggestionJob(ctx context.Context, actor string, item domain.LLMSuggestionJob) (domain.LLMSuggestionJob, error) {
	contextJSON, _ := json.Marshal(item.Context)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO llm_suggestion_jobs (
			sensor_id, provider, model, requested_by, context
		)
		SELECT sensor.id, $2, $3, $4, $5::jsonb
		FROM sensor_definitions sensor
		WHERE sensor.id = $1::uuid
		  AND sensor.display_alias IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM llm_suggestion_jobs job
			WHERE job.sensor_id = sensor.id
			  AND job.status IN ('queued', 'running')
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM llm_suggestions suggestion
			WHERE suggestion.sensor_id = sensor.id
			  AND suggestion.status IN ('Pending', 'Accepted', 'Rejected')
		  )
		RETURNING id::text, status, requested_at`,
		item.SensorID, item.Provider, item.Model, actor, contextJSON,
	).Scan(&item.ID, &item.Status, &item.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if checkErr := r.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sensor_definitions WHERE id = $1::uuid
			)`, item.SensorID).Scan(&exists); checkErr != nil {
			return domain.LLMSuggestionJob{}, checkErr
		}
		if !exists {
			return domain.LLMSuggestionJob{}, ErrNotFound
		}
		return domain.LLMSuggestionJob{}, ErrConflict
	}
	if err != nil {
		return domain.LLMSuggestionJob{}, fmt.Errorf("create LLM suggestion job: %w", err)
	}
	item.RequestedBy = actor
	_ = r.AppendAudit(ctx, actor, "llm_suggestion.queued", "sensor", item.SensorID, "", map[string]any{
		"jobId": item.ID, "provider": item.Provider, "model": item.Model,
	})
	return item, nil
}

func (r *Repository) ListSuggestionJobs(ctx context.Context, limit int) ([]domain.LLMSuggestionJob, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, sensor_id::text, provider, model, status, token_usage,
			requested_by, requested_at, completed_at, context, error
		FROM llm_suggestion_jobs
		ORDER BY requested_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list LLM suggestion jobs: %w", err)
	}
	defer rows.Close()
	items := make([]domain.LLMSuggestionJob, 0)
	for rows.Next() {
		var item domain.LLMSuggestionJob
		var contextJSON []byte
		if err := rows.Scan(
			&item.ID, &item.SensorID, &item.Provider, &item.Model, &item.Status,
			&item.TokenUsage, &item.RequestedBy, &item.RequestedAt,
			&item.CompletedAt, &contextJSON, &item.Error,
		); err != nil {
			return nil, err
		}
		item.Context = domain.JSONMap(contextJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ClaimSuggestionJobs(ctx context.Context, limit int) ([]domain.LLMSuggestionJob, error) {
	limit = bounded(limit, 10)
	rows, err := r.pool.Query(ctx, `
		WITH selected AS (
			SELECT id FROM llm_suggestion_jobs
			WHERE status = 'queued'
			ORDER BY requested_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE llm_suggestion_jobs job
		SET status = 'running'
		FROM selected
		WHERE job.id = selected.id
		RETURNING job.id::text, job.sensor_id::text, job.provider, job.model,
			job.requested_by, job.requested_at, job.context`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim LLM suggestion jobs: %w", err)
	}
	defer rows.Close()
	items := make([]domain.LLMSuggestionJob, 0)
	for rows.Next() {
		var item domain.LLMSuggestionJob
		var contextJSON []byte
		if err := rows.Scan(
			&item.ID, &item.SensorID, &item.Provider, &item.Model,
			&item.RequestedBy, &item.RequestedAt, &contextJSON,
		); err != nil {
			return nil, err
		}
		item.Status = "running"
		item.Context = domain.JSONMap(contextJSON)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) BuildLabelSuggestionRequest(ctx context.Context, item domain.LLMSuggestionJob) (extension.LabelSuggestionRequest, error) {
	var request extension.LabelSuggestionRequest
	var sensorMetadata, assetMetadata []byte
	err := r.pool.QueryRow(ctx, `
		SELECT sensor.id::text, sensor.stable_id, sensor.asset_id::text,
			asset.name, sensor.source, sensor.source_path, sensor.raw_name,
			sensor.display_alias, sensor.category, sensor.unit, sensor.scale,
			sensor.monotonic, sensor.hardware_warning, sensor.hardware_critical,
			sensor.user_warning, sensor.user_critical, sensor.interval_seconds,
			sensor.discovery_status, sensor.missing_discoveries, sensor.quality,
			sensor.last_seen_at, latest.value, latest.text_value,
			latest.observed_at, sensor.metadata,
			asset.id::text, asset.stable_id, asset.parent_id::text, asset.kind,
			asset.name, asset.vendor, asset.model, asset.health, asset.connection,
			asset.last_seen_at, asset.metadata
		FROM sensor_definitions sensor
		JOIN assets asset ON asset.id = sensor.asset_id
		LEFT JOIN LATERAL (
			SELECT sample.value, sample.text_value, sample.observed_at
			FROM metric_series series
			JOIN metric_samples sample ON sample.series_id = series.id
			WHERE series.sensor_id = sensor.id
			ORDER BY sample.observed_at DESC LIMIT 1
		) latest ON true
		WHERE sensor.id = $1::uuid`, item.SensorID).Scan(
		&request.Sensor.ID, &request.Sensor.StableID, &request.Sensor.AssetID,
		&request.Sensor.AssetName, &request.Sensor.Source, &request.Sensor.SourcePath,
		&request.Sensor.RawName, &request.Sensor.DisplayAlias,
		&request.Sensor.Category, &request.Sensor.Unit, &request.Sensor.Scale,
		&request.Sensor.Monotonic, &request.Sensor.HardwareWarning,
		&request.Sensor.HardwareCritical, &request.Sensor.UserWarning,
		&request.Sensor.UserCritical, &request.Sensor.IntervalSeconds,
		&request.Sensor.DiscoveryStatus, &request.Sensor.MissingDiscoveries,
		&request.Sensor.Quality, &request.Sensor.LastSeenAt,
		&request.Sensor.CurrentValue, &request.Sensor.CurrentText,
		&request.Sensor.ObservedAt, &sensorMetadata,
		&request.Asset.ID, &request.Asset.StableID, &request.Asset.ParentID,
		&request.Asset.Kind, &request.Asset.Name, &request.Asset.Vendor,
		&request.Asset.Model, &request.Asset.Health, &request.Asset.Connection,
		&request.Asset.LastSeenAt, &assetMetadata,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return extension.LabelSuggestionRequest{}, ErrNotFound
	}
	if err != nil {
		return extension.LabelSuggestionRequest{}, fmt.Errorf("query LLM suggestion evidence: %w", err)
	}
	request.Sensor.Metadata = domain.JSONMap(sensorMetadata)
	request.Asset.Metadata = domain.JSONMap(assetMetadata)
	request.Context = cloneJSONMap(item.Context)

	rows, err := r.pool.Query(ctx, `
		SELECT sensor.id::text, sensor.stable_id, sensor.asset_id::text,
			asset.name, sensor.source, sensor.source_path, sensor.raw_name,
			sensor.display_alias, sensor.category, sensor.unit, sensor.scale,
			sensor.monotonic, sensor.hardware_warning, sensor.hardware_critical,
			sensor.user_warning, sensor.user_critical, sensor.interval_seconds,
			sensor.discovery_status, sensor.missing_discoveries, sensor.quality,
			sensor.last_seen_at, latest.value, latest.text_value,
			latest.observed_at, sensor.metadata
		FROM sensor_definitions sensor
		JOIN assets asset ON asset.id = sensor.asset_id
		LEFT JOIN LATERAL (
			SELECT sample.value, sample.text_value, sample.observed_at
			FROM metric_series series
			JOIN metric_samples sample ON sample.series_id = series.id
			WHERE series.sensor_id = sensor.id
			ORDER BY sample.observed_at DESC LIMIT 1
		) latest ON true
		WHERE sensor.asset_id = $1::uuid AND sensor.id <> $2::uuid
		ORDER BY sensor.source_path, sensor.raw_name
		LIMIT 20`, request.Sensor.AssetID, item.SensorID)
	if err != nil {
		return extension.LabelSuggestionRequest{}, fmt.Errorf("query adjacent sensors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sensor domain.Sensor
		var metadata []byte
		if err := rows.Scan(
			&sensor.ID, &sensor.StableID, &sensor.AssetID, &sensor.AssetName,
			&sensor.Source, &sensor.SourcePath, &sensor.RawName,
			&sensor.DisplayAlias, &sensor.Category, &sensor.Unit, &sensor.Scale,
			&sensor.Monotonic, &sensor.HardwareWarning, &sensor.HardwareCritical,
			&sensor.UserWarning, &sensor.UserCritical, &sensor.IntervalSeconds,
			&sensor.DiscoveryStatus, &sensor.MissingDiscoveries, &sensor.Quality,
			&sensor.LastSeenAt, &sensor.CurrentValue, &sensor.CurrentText,
			&sensor.ObservedAt, &metadata,
		); err != nil {
			return extension.LabelSuggestionRequest{}, err
		}
		sensor.Metadata = domain.JSONMap(metadata)
		request.Adjacent = append(request.Adjacent, sensor)
	}
	return request, rows.Err()
}

func (r *Repository) FinishSuggestionJob(
	ctx context.Context,
	item domain.LLMSuggestionJob,
	result *extension.LabelSuggestion,
	status string,
	jobErr error,
) error {
	if status != "succeeded" && status != "failed" && status != "budget_exceeded" {
		return fmt.Errorf("unsupported LLM suggestion job status %q", status)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	errorText := ""
	if jobErr != nil {
		errorText = jobErr.Error()
	}
	tag, err := tx.Exec(ctx, `
		UPDATE llm_suggestion_jobs
		SET status = $2, token_usage = $3, error = left($4, 4096),
			completed_at = now()
		WHERE id = $1::uuid AND status = 'running'`,
		item.ID, status, item.TokenUsage, errorText)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	if result != nil && status == "succeeded" {
		evidence, _ := json.Marshal(result.Evidence)
		contextJSON, _ := json.Marshal(item.Context)
		if _, err := tx.Exec(ctx, `
			INSERT INTO llm_suggestions (
				sensor_id, suggested_name, suggested_asset_kind,
				suggested_category, suggested_unit, confidence, evidence,
				context, provider, model
			)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10)`,
			item.SensorID, result.DisplayName, result.AssetKind, result.Category,
			result.Unit, result.Confidence, evidence, contextJSON,
			item.Provider, item.Model,
		); err != nil {
			return fmt.Errorf("persist LLM suggestion: %w", err)
		}
	}
	if err := appendAuditTx(ctx, tx, "worker", "llm_suggestion."+status, "sensor", item.SensorID, "", map[string]any{
		"jobId": item.ID, "provider": item.Provider, "model": item.Model,
		"tokenUsage": item.TokenUsage,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
