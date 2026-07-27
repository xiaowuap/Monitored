package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/monitored-io/monitored/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("resource not found")
var ErrConflict = errors.New("resource state conflict")

type Repository struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Repository, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	config.MaxConns = 12
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	repository := &Repository{pool: pool}
	if err := repository.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return repository, nil
}

func (r *Repository) Close() {
	r.pool.Close()
}

func (r *Repository) Ping(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

func (r *Repository) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), readErr)
		}
		if _, execErr := r.pool.Exec(ctx, string(body)); execErr != nil {
			return fmt.Errorf("execute migration %s: %w", entry.Name(), execErr)
		}
	}
	return nil
}

func (r *Repository) ListAssets(ctx context.Context, limit int) ([]domain.Asset, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			a.id::text, a.stable_id, a.parent_id::text, a.kind, a.name, a.vendor, a.model,
			a.health, a.connection, a.last_seen_at, a.metadata,
			(SELECT count(*) FROM sensor_definitions sd WHERE sd.asset_id = a.id),
			(SELECT count(*) FROM sensor_definitions sd WHERE sd.asset_id = a.id AND sd.display_alias IS NULL),
			(
				SELECT max(latest.value)
				FROM sensor_definitions sd
				JOIN LATERAL (
					SELECT sm.value
					FROM metric_series series
					JOIN metric_samples sm ON sm.series_id = series.id
					WHERE series.sensor_id = sd.id AND sm.value IS NOT NULL
					ORDER BY sm.observed_at DESC LIMIT 1
				) latest ON true
				WHERE sd.asset_id = a.id AND sd.category = 'temperature'
			),
			(
				SELECT sum(latest.value)
				FROM sensor_definitions sd
				JOIN LATERAL (
					SELECT sm.value
					FROM metric_series series
					JOIN metric_samples sm ON sm.series_id = series.id
					WHERE series.sensor_id = sd.id AND sm.value IS NOT NULL
					ORDER BY sm.observed_at DESC LIMIT 1
				) latest ON true
				WHERE sd.asset_id = a.id AND sd.category = 'power'
			),
			(
				SELECT count(*) FROM alerts al
				WHERE al.asset_id = a.id AND al.state IN ('Pending', 'Firing')
				AND al.rule_name ILIKE '%增长%'
			)
		FROM assets a
		ORDER BY
			CASE a.health WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 WHEN 'unknown' THEN 2 ELSE 3 END,
			a.name
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query assets: %w", err)
	}
	defer rows.Close()

	assets := make([]domain.Asset, 0)
	for rows.Next() {
		var asset domain.Asset
		var parentID *string
		var metadata []byte
		if err := rows.Scan(
			&asset.ID, &asset.StableID, &parentID, &asset.Kind, &asset.Name, &asset.Vendor,
			&asset.Model, &asset.Health, &asset.Connection, &asset.LastSeenAt, &metadata,
			&asset.Summary.SensorCount, &asset.Summary.UnknownCount, &asset.Summary.HighestTempC,
			&asset.Summary.PowerW, &asset.Summary.GrowingErrorCount,
		); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		asset.ParentID = parentID
		asset.Metadata = domain.JSONMap(metadata)
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assets: %w", err)
	}
	return assets, nil
}

func (r *Repository) ListSensors(ctx context.Context, status string, limit int) ([]domain.Sensor, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			sd.id::text, sd.stable_id, sd.asset_id::text, a.name, sd.source, sd.source_path,
			sd.raw_name, sd.display_alias, sd.category, sd.unit, sd.scale, sd.monotonic,
			sd.hardware_warning, sd.hardware_critical, sd.user_warning, sd.user_critical,
			sd.interval_seconds, sd.discovery_status, sd.missing_discoveries, sd.quality,
			sd.last_seen_at, latest.value, latest.text_value, latest.observed_at, sd.metadata
		FROM sensor_definitions sd
		JOIN assets a ON a.id = sd.asset_id
		LEFT JOIN LATERAL (
			SELECT sm.value, sm.text_value, sm.observed_at
			FROM metric_series series
			JOIN metric_samples sm ON sm.series_id = series.id
			WHERE series.sensor_id = sd.id
			ORDER BY sm.observed_at DESC
			LIMIT 1
		) latest ON true
		WHERE ($1 = '' OR sd.discovery_status = $1)
		ORDER BY
			CASE sd.discovery_status WHEN 'new' THEN 0 WHEN 'stale' THEN 1 WHEN 'present' THEN 2 ELSE 3 END,
			sd.last_seen_at DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query sensors: %w", err)
	}
	defer rows.Close()

	sensors := make([]domain.Sensor, 0)
	for rows.Next() {
		var sensor domain.Sensor
		var metadata []byte
		if err := rows.Scan(
			&sensor.ID, &sensor.StableID, &sensor.AssetID, &sensor.AssetName, &sensor.Source,
			&sensor.SourcePath, &sensor.RawName, &sensor.DisplayAlias, &sensor.Category,
			&sensor.Unit, &sensor.Scale, &sensor.Monotonic, &sensor.HardwareWarning,
			&sensor.HardwareCritical, &sensor.UserWarning, &sensor.UserCritical,
			&sensor.IntervalSeconds, &sensor.DiscoveryStatus, &sensor.MissingDiscoveries,
			&sensor.Quality, &sensor.LastSeenAt, &sensor.CurrentValue, &sensor.CurrentText,
			&sensor.ObservedAt, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan sensor: %w", err)
		}
		sensor.Metadata = domain.JSONMap(metadata)
		sensors = append(sensors, sensor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sensors: %w", err)
	}
	return sensors, nil
}

func (r *Repository) Metrics(ctx context.Context, sensorID string, since time.Time) ([]domain.MetricPoint, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			date_bin('15 minutes', sm.observed_at, timestamptz '2001-01-01') AS bucket,
			min(sm.value), avg(sm.value), max(sm.value), count(*)
		FROM metric_series series
		JOIN metric_samples sm ON sm.series_id = series.id
		WHERE series.sensor_id = $1::uuid
		  AND sm.observed_at >= $2
		  AND sm.value IS NOT NULL
		GROUP BY bucket
		ORDER BY bucket`, sensorID, since)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()
	points := make([]domain.MetricPoint, 0)
	for rows.Next() {
		var point domain.MetricPoint
		if err := rows.Scan(&point.At, &point.Min, &point.Avg, &point.Max, &point.Count); err != nil {
			return nil, fmt.Errorf("scan metric point: %w", err)
		}
		points = append(points, point)
	}
	return points, rows.Err()
}

func (r *Repository) ListAlerts(ctx context.Context, activeOnly bool, limit int) ([]domain.Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT al.id::text, al.fingerprint, al.rule_name, al.asset_id::text, a.name,
			al.sensor_id::text, al.severity, al.state, al.summary, al.value, al.unit,
			al.started_at, al.updated_at, al.resolved_at
		FROM alerts al
		JOIN assets a ON a.id = al.asset_id
		WHERE (NOT $1 OR al.state IN ('Pending', 'Firing'))
		ORDER BY
			CASE al.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
			al.updated_at DESC
		LIMIT $2`, activeOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]domain.Alert, 0)
	for rows.Next() {
		var alert domain.Alert
		if err := rows.Scan(
			&alert.ID, &alert.Fingerprint, &alert.RuleName, &alert.AssetID, &alert.AssetName,
			&alert.SensorID, &alert.Severity, &alert.State, &alert.Summary, &alert.Value,
			&alert.Unit, &alert.StartedAt, &alert.UpdatedAt, &alert.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (r *Repository) ListIncidents(ctx context.Context, limit int) ([]domain.Incident, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.id::text, i.title, i.severity, i.state,
			count(DISTINCT al.asset_id), count(ia.alert_id), i.diagnosis,
			i.opened_at, i.updated_at, i.resolved_at
		FROM incidents i
		LEFT JOIN incident_alerts ia ON ia.incident_id = i.id
		LEFT JOIN alerts al ON al.id = ia.alert_id
		GROUP BY i.id
		ORDER BY i.updated_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer rows.Close()
	incidents := make([]domain.Incident, 0)
	for rows.Next() {
		var incident domain.Incident
		if err := rows.Scan(
			&incident.ID, &incident.Title, &incident.Severity, &incident.State,
			&incident.AssetCount, &incident.AlertCount, &incident.Diagnosis,
			&incident.OpenedAt, &incident.UpdatedAt, &incident.ResolvedAt,
		); err != nil {
			return nil, fmt.Errorf("scan incident: %w", err)
		}
		incidents = append(incidents, incident)
	}
	return incidents, rows.Err()
}

func (r *Repository) ListDiscoveryRuns(ctx context.Context, limit int) ([]domain.DiscoveryRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, agent_id, kind, state, requested_by, started_at, finished_at,
			assets_found, sensors_found, unknown_found, progress, details
		FROM discovery_runs
		ORDER BY started_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query discovery runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.DiscoveryRun, 0)
	for rows.Next() {
		var run domain.DiscoveryRun
		var details []byte
		if err := rows.Scan(
			&run.ID, &run.AgentID, &run.Kind, &run.State, &run.RequestedBy,
			&run.StartedAt, &run.FinishedAt, &run.AssetsFound, &run.SensorsFound,
			&run.UnknownFound, &run.Progress, &details,
		); err != nil {
			return nil, fmt.Errorf("scan discovery run: %w", err)
		}
		run.Details = domain.JSONMap(details)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *Repository) CreateDiscoveryRun(ctx context.Context, actor, agentID, kind string) (domain.DiscoveryRun, error) {
	if kind == "" {
		kind = "full"
	}
	var run domain.DiscoveryRun
	var details []byte
	err := r.pool.QueryRow(ctx, `
		INSERT INTO discovery_runs (agent_id, kind, state, requested_by, progress, details)
		VALUES (NULLIF($1, ''), $2, 'running', $3, 8, '{"phase":"capability-check"}')
		RETURNING id::text, agent_id, kind, state, requested_by, started_at, finished_at,
			assets_found, sensors_found, unknown_found, progress, details`,
		agentID, kind, actor,
	).Scan(
		&run.ID, &run.AgentID, &run.Kind, &run.State, &run.RequestedBy,
		&run.StartedAt, &run.FinishedAt, &run.AssetsFound, &run.SensorsFound,
		&run.UnknownFound, &run.Progress, &details,
	)
	if err != nil {
		return domain.DiscoveryRun{}, fmt.Errorf("create discovery run: %w", err)
	}
	run.Details = domain.JSONMap(details)
	if err := r.AppendAudit(ctx, actor, "discovery.started", "discovery_run", run.ID, "", map[string]any{
		"agentId": agentID,
		"kind":    kind,
	}); err != nil {
		return domain.DiscoveryRun{}, err
	}
	return run, nil
}

func (r *Repository) CompleteDiscoveryRun(ctx context.Context, id string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE discovery_runs
		SET state = 'succeeded', progress = 100, finished_at = now(),
			assets_found = (SELECT count(*) FROM assets),
			sensors_found = (SELECT count(*) FROM sensor_definitions),
			unknown_found = (SELECT count(*) FROM sensor_definitions WHERE display_alias IS NULL),
			details = '{"phase":"complete","degradedCollectors":["ipmi"],"enhancedCollectors":["nvml","redfish","ethtool"]}'
		WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("complete discovery run: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) ListSuggestions(ctx context.Context, status string, limit int) ([]domain.LLMSuggestion, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.id::text, s.sensor_id::text, sd.raw_name, a.name,
			s.suggested_name, s.suggested_asset_kind, s.suggested_category,
			s.suggested_unit, s.confidence, s.evidence, s.status, s.provider,
			s.model, s.created_at, s.reviewed_at, s.reviewed_by, s.context
		FROM llm_suggestions s
		JOIN sensor_definitions sd ON sd.id = s.sensor_id
		JOIN assets a ON a.id = sd.asset_id
		WHERE ($1 = '' OR s.status = $1)
		ORDER BY s.created_at DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query suggestions: %w", err)
	}
	defer rows.Close()
	suggestions := make([]domain.LLMSuggestion, 0)
	for rows.Next() {
		var suggestion domain.LLMSuggestion
		var evidenceJSON, contextJSON []byte
		if err := rows.Scan(
			&suggestion.ID, &suggestion.SensorID, &suggestion.RawName, &suggestion.AssetName,
			&suggestion.SuggestedName, &suggestion.SuggestedAssetKind,
			&suggestion.SuggestedCategory, &suggestion.SuggestedUnit,
			&suggestion.Confidence, &evidenceJSON, &suggestion.Status,
			&suggestion.Provider, &suggestion.Model, &suggestion.CreatedAt,
			&suggestion.ReviewedAt, &suggestion.ReviewedBy, &contextJSON,
		); err != nil {
			return nil, fmt.Errorf("scan suggestion: %w", err)
		}
		if err := json.Unmarshal(evidenceJSON, &suggestion.Evidence); err != nil {
			suggestion.Evidence = []string{}
		}
		suggestion.Context = domain.JSONMap(contextJSON)
		suggestions = append(suggestions, suggestion)
	}
	return suggestions, rows.Err()
}

func (r *Repository) ReviewSuggestion(ctx context.Context, id, decision, actor string) error {
	if decision != "Accepted" && decision != "Rejected" {
		return fmt.Errorf("unsupported decision %q", decision)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin suggestion review: %w", err)
	}
	defer tx.Rollback(ctx)

	var sensorID, suggestedName, suggestedCategory, currentStatus string
	err = tx.QueryRow(ctx, `
		SELECT sensor_id::text, suggested_name, suggested_category, status
		FROM llm_suggestions
		WHERE id = $1::uuid
		FOR UPDATE`, id).Scan(&sensorID, &suggestedName, &suggestedCategory, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock suggestion: %w", err)
	}
	if currentStatus != "Pending" {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `
		UPDATE llm_suggestions
		SET status = $2, reviewed_at = now(), reviewed_by = $3
		WHERE id = $1::uuid`, id, decision, actor); err != nil {
		return fmt.Errorf("update suggestion: %w", err)
	}
	if decision == "Accepted" {
		if _, err = tx.Exec(ctx, `
			UPDATE sensor_definitions
			SET display_alias = $2, category = $3, updated_at = now()
			WHERE id = $1::uuid`, sensorID, suggestedName, suggestedCategory); err != nil {
			return fmt.Errorf("apply accepted suggestion: %w", err)
		}
	}
	if err := appendAuditTx(ctx, tx, actor, "llm_suggestion."+strings.ToLower(decision), "llm_suggestion", id, "", map[string]any{
		"sensorId": sensorID,
		"decision": decision,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListActions(ctx context.Context, state string, limit int) ([]domain.ActionRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ar.id::text, ar.asset_id::text, a.name, ar.kind, ar.title, ar.command,
			ar.working_directory, ar.run_identity, ar.timeout_seconds, ar.risk, ar.state,
			ar.immutable_hash, ar.requested_by, ar.requested_at, ar.approved_by,
			ar.approved_at, ar.result
		FROM action_requests ar
		JOIN assets a ON a.id = ar.asset_id
		WHERE ($1 = '' OR ar.state = $1)
		ORDER BY ar.requested_at DESC
		LIMIT $2`, state, limit)
	if err != nil {
		return nil, fmt.Errorf("query actions: %w", err)
	}
	defer rows.Close()
	actions := make([]domain.ActionRequest, 0)
	for rows.Next() {
		var action domain.ActionRequest
		var resultJSON []byte
		if err := rows.Scan(
			&action.ID, &action.AssetID, &action.AssetName, &action.Kind, &action.Title,
			&action.Command, &action.WorkingDirectory, &action.Identity,
			&action.TimeoutSeconds, &action.Risk, &action.State, &action.ImmutableHash,
			&action.RequestedBy, &action.RequestedAt, &action.ApprovedBy,
			&action.ApprovedAt, &resultJSON,
		); err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		action.Result = domain.JSONMap(resultJSON)
		actions = append(actions, action)
	}
	return actions, rows.Err()
}

func (r *Repository) ReviewAction(ctx context.Context, id, decision, actor, remoteAddr string) error {
	if decision != "approve" && decision != "reject" {
		return fmt.Errorf("unsupported action decision %q", decision)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin action review: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentState, immutableHash string
	err = tx.QueryRow(ctx, `
		SELECT state, immutable_hash
		FROM action_requests
		WHERE id = $1::uuid
		FOR UPDATE`, id).Scan(&currentState, &immutableHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock action: %w", err)
	}
	if currentState != "pending" {
		return ErrConflict
	}

	nextState := "rejected"
	var tokenHash *string
	if decision == "approve" {
		nextState = "approved"
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return fmt.Errorf("create one-time action token: %w", err)
		}
		hash := sha256.Sum256(token)
		value := hex.EncodeToString(hash[:])
		tokenHash = &value
	}
	_, err = tx.Exec(ctx, `
		UPDATE action_requests
		SET state = $2, approved_by = $3, approved_at = now(), one_time_token_hash = $4
		WHERE id = $1::uuid`, id, nextState, actor, tokenHash)
	if err != nil {
		return fmt.Errorf("review action: %w", err)
	}
	if err := appendAuditTx(ctx, tx, actor, "action."+decision, "action_request", id, remoteAddr, map[string]any{
		"immutableHash": immutableHash,
		"nextState":     nextState,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ListAudit(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, actor, action, resource_type, resource_id, occurred_at,
			remote_addr, details, previous_hash, entry_hash
		FROM audit_log
		ORDER BY id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()
	entries := make([]domain.AuditEntry, 0)
	for rows.Next() {
		var entry domain.AuditEntry
		var detailsJSON []byte
		if err := rows.Scan(
			&entry.ID, &entry.Actor, &entry.Action, &entry.ResourceType,
			&entry.ResourceID, &entry.OccurredAt, &entry.RemoteAddr,
			&detailsJSON, &entry.PreviousHash, &entry.EntryHash,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		entry.Details = domain.JSONMap(detailsJSON)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *Repository) AppendAudit(
	ctx context.Context,
	actor, action, resourceType, resourceID, remoteAddr string,
	details map[string]any,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin audit append: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := appendAuditTx(ctx, tx, actor, action, resourceType, resourceID, remoteAddr, details); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func appendAuditTx(
	ctx context.Context,
	tx pgx.Tx,
	actor, action, resourceType, resourceID, remoteAddr string,
	details map[string]any,
) error {
	var previousHash string
	err := tx.QueryRow(ctx, `
		SELECT entry_hash
		FROM audit_log
		ORDER BY id DESC
		LIMIT 1
		FOR UPDATE`).Scan(&previousHash)
	if errors.Is(err, pgx.ErrNoRows) {
		previousHash = ""
	} else if err != nil {
		return fmt.Errorf("read audit chain head: %w", err)
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	occurredAt := time.Now().UTC()
	payload := strings.Join([]string{
		previousHash,
		actor,
		action,
		resourceType,
		resourceID,
		occurredAt.Format(time.RFC3339Nano),
		string(detailsJSON),
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	entryHash := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (
			actor, action, resource_type, resource_id, occurred_at, remote_addr,
			details, previous_hash, entry_hash
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)`,
		actor, action, resourceType, resourceID, occurredAt, remoteAddr,
		detailsJSON, previousHash, entryHash,
	); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}
	return nil
}
