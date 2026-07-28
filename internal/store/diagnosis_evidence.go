package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/monitored-io/monitored/internal/domain"
	"github.com/monitored-io/monitored/internal/extension"
)

const (
	diagnosisSensorLimit  = 40
	diagnosisMetricWindow = 24 * time.Hour
)

// BuildDiagnosisRequest assembles a bounded, read-only evidence package from
// persisted telemetry. Caller-supplied context is preserved as untrusted
// supplemental evidence; it never changes the selected database resources.
func (r *Repository) BuildDiagnosisRequest(ctx context.Context, item domain.Diagnosis) (extension.DiagnosisRequest, error) {
	request := extension.DiagnosisRequest{
		Metrics: make(map[string][]domain.MetricPoint),
		Context: cloneJSONMap(item.Context),
	}
	if request.Context == nil {
		request.Context = make(map[string]any)
	}
	request.Context["evidenceWindowHours"] = int(diagnosisMetricWindow.Hours())
	request.Context["assembledAt"] = time.Now().UTC()

	assetIDs := make(map[string]struct{})
	if item.IncidentID != nil {
		incident, alerts, err := r.diagnosisIncident(ctx, *item.IncidentID)
		if err != nil {
			return extension.DiagnosisRequest{}, err
		}
		request.Incident = incident
		request.Alerts = alerts
		for _, alert := range alerts {
			assetIDs[alert.AssetID] = struct{}{}
		}
	}
	if item.AssetID != nil {
		assetIDs[*item.AssetID] = struct{}{}
		if len(request.Alerts) == 0 {
			alerts, err := r.diagnosisAssetAlerts(ctx, *item.AssetID)
			if err != nil {
				return extension.DiagnosisRequest{}, err
			}
			request.Alerts = alerts
		}
	}
	if len(assetIDs) == 0 {
		return request, nil
	}

	assets, err := r.diagnosisAssets(ctx, assetIDs)
	if err != nil {
		return extension.DiagnosisRequest{}, err
	}
	request.Assets = assets

	sensors, err := r.diagnosisSensors(ctx, assetIDs, diagnosisSensorLimit)
	if err != nil {
		return extension.DiagnosisRequest{}, err
	}
	request.Context["sensors"] = sensors
	since := time.Now().UTC().Add(-diagnosisMetricWindow)
	for _, sensor := range sensors {
		points, metricErr := r.Metrics(ctx, sensor.ID, since)
		if metricErr != nil {
			return extension.DiagnosisRequest{}, metricErr
		}
		if len(points) > 0 {
			request.Metrics[sensor.ID] = points
		}
	}
	return request, nil
}

func (r *Repository) diagnosisIncident(ctx context.Context, id string) (domain.Incident, []domain.Alert, error) {
	var incident domain.Incident
	err := r.pool.QueryRow(ctx, `
		SELECT i.id::text, i.title, i.severity, i.state,
			count(DISTINCT alert.asset_id), count(link.alert_id), i.diagnosis,
			i.opened_at, i.updated_at, i.resolved_at
		FROM incidents i
		LEFT JOIN incident_alerts link ON link.incident_id = i.id
		LEFT JOIN alerts alert ON alert.id = link.alert_id
		WHERE i.id = $1::uuid
		GROUP BY i.id`, id).Scan(
		&incident.ID, &incident.Title, &incident.Severity, &incident.State,
		&incident.AssetCount, &incident.AlertCount, &incident.Diagnosis,
		&incident.OpenedAt, &incident.UpdatedAt, &incident.ResolvedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, nil, ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, nil, fmt.Errorf("query diagnosis incident: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT alert.id::text, alert.fingerprint, alert.rule_name,
			alert.asset_id::text, asset.name, alert.sensor_id::text,
			alert.severity, alert.state, alert.summary, alert.value, alert.unit,
			alert.started_at, alert.updated_at, alert.resolved_at
		FROM incident_alerts link
		JOIN alerts alert ON alert.id = link.alert_id
		JOIN assets asset ON asset.id = alert.asset_id
		WHERE link.incident_id = $1::uuid
		ORDER BY alert.updated_at DESC
		LIMIT 100`, id)
	if err != nil {
		return domain.Incident{}, nil, fmt.Errorf("query diagnosis alerts: %w", err)
	}
	defer rows.Close()
	alerts, err := scanDiagnosisAlerts(rows)
	return incident, alerts, err
}

func (r *Repository) diagnosisAssetAlerts(ctx context.Context, assetID string) ([]domain.Alert, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT alert.id::text, alert.fingerprint, alert.rule_name,
			alert.asset_id::text, asset.name, alert.sensor_id::text,
			alert.severity, alert.state, alert.summary, alert.value, alert.unit,
			alert.started_at, alert.updated_at, alert.resolved_at
		FROM alerts alert
		JOIN assets asset ON asset.id = alert.asset_id
		WHERE alert.asset_id = $1::uuid
		ORDER BY alert.updated_at DESC
		LIMIT 100`, assetID)
	if err != nil {
		return nil, fmt.Errorf("query diagnosis asset alerts: %w", err)
	}
	defer rows.Close()
	return scanDiagnosisAlerts(rows)
}

type diagnosisAlertRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanDiagnosisAlerts(rows diagnosisAlertRows) ([]domain.Alert, error) {
	alerts := make([]domain.Alert, 0)
	for rows.Next() {
		var alert domain.Alert
		if err := rows.Scan(
			&alert.ID, &alert.Fingerprint, &alert.RuleName, &alert.AssetID,
			&alert.AssetName, &alert.SensorID, &alert.Severity, &alert.State,
			&alert.Summary, &alert.Value, &alert.Unit, &alert.StartedAt,
			&alert.UpdatedAt, &alert.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan diagnosis alert: %w", err)
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (r *Repository) diagnosisAssets(ctx context.Context, requested map[string]struct{}) ([]domain.Asset, error) {
	ids := make([]string, 0, len(requested))
	for id := range requested {
		ids = append(ids, id)
	}
	rows, err := r.pool.Query(ctx, `
		WITH requested AS (
			SELECT unnest($1::uuid[]) AS id
		)
		SELECT DISTINCT asset.id::text, asset.stable_id, asset.parent_id::text,
			asset.kind, asset.name, asset.vendor, asset.model, asset.health,
			asset.connection, asset.last_seen_at, asset.metadata
		FROM assets asset
		WHERE asset.id IN (SELECT id FROM requested)
		   OR asset.parent_id IN (SELECT id FROM requested)
		   OR asset.id IN (
				SELECT parent_id FROM assets
				WHERE id IN (SELECT id FROM requested) AND parent_id IS NOT NULL
		   )
		ORDER BY asset.name
		LIMIT 100`, ids)
	if err != nil {
		return nil, fmt.Errorf("query diagnosis asset topology: %w", err)
	}
	defer rows.Close()
	assets := make([]domain.Asset, 0)
	for rows.Next() {
		var asset domain.Asset
		var metadata []byte
		if err := rows.Scan(
			&asset.ID, &asset.StableID, &asset.ParentID, &asset.Kind, &asset.Name,
			&asset.Vendor, &asset.Model, &asset.Health, &asset.Connection,
			&asset.LastSeenAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan diagnosis asset: %w", err)
		}
		asset.Metadata = domain.JSONMap(metadata)
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, ErrNotFound
	}
	return assets, nil
}

func (r *Repository) diagnosisSensors(ctx context.Context, assetIDs map[string]struct{}, limit int) ([]domain.Sensor, error) {
	ids := make([]string, 0, len(assetIDs))
	for id := range assetIDs {
		ids = append(ids, id)
	}
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
			ORDER BY sample.observed_at DESC
			LIMIT 1
		) latest ON true
		WHERE sensor.asset_id = ANY($1::uuid[])
		ORDER BY
			CASE sensor.quality WHEN 'error' THEN 0 WHEN 'unavailable' THEN 1
				WHEN 'degraded' THEN 2 ELSE 3 END,
			sensor.last_seen_at DESC
		LIMIT $2`, ids, limit)
	if err != nil {
		return nil, fmt.Errorf("query diagnosis sensors: %w", err)
	}
	defer rows.Close()
	sensors := make([]domain.Sensor, 0)
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
			return nil, fmt.Errorf("scan diagnosis sensor: %w", err)
		}
		sensor.Metadata = domain.JSONMap(metadata)
		sensors = append(sensors, sensor)
	}
	return sensors, rows.Err()
}

func cloneJSONMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return map[string]any{}
	}
	return result
}
