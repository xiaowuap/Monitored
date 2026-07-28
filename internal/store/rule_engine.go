package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/monitored-io/monitored/internal/domain"
	enginerules "github.com/monitored-io/monitored/internal/rules"
)

type ruleCandidate struct {
	ID, Name, Type, Severity, SensorID, AssetID, AssetName, AssetKind, Unit string
	Expression, Recovery                                                    map[string]any
	Duration                                                                time.Duration
	MissingMode                                                             string
	Maintenance                                                             bool
}

func (r *Repository) EvaluateRules(ctx context.Context, now time.Time) error {
	candidates, err := r.ruleCandidates(ctx, now)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := r.evaluateCandidate(ctx, now, candidate); err != nil {
			return fmt.Errorf("evaluate rule %s: %w", candidate.ID, err)
		}
	}
	return nil
}

func (r *Repository) ruleCandidates(ctx context.Context, now time.Time) ([]ruleCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT rule.id::text, rule.name, rule.rule_type, rule.severity,
			sensor.id::text, asset.id::text, asset.name, asset.kind, sensor.unit,
			rule.expression, rule.recovery_expression, rule.duration_seconds,
			rule.missing_data_mode,
			EXISTS (
				SELECT 1 FROM maintenance_windows maintenance
				WHERE maintenance.starts_at <= $1 AND maintenance.ends_at > $1
				  AND (
					maintenance.id = rule.maintenance_window_id OR
					maintenance.rule_id = rule.id OR
					maintenance.asset_id = asset.id
				  )
			)
		FROM alert_rules rule
		JOIN sensor_definitions sensor ON (
			sensor.id = rule.sensor_id OR
			(rule.sensor_id IS NULL AND rule.asset_kind <> '')
		)
		JOIN assets asset ON asset.id = sensor.asset_id
		WHERE rule.enabled
		  AND (rule.sensor_id IS NOT NULL OR asset.kind = rule.asset_kind)`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ruleCandidate
	for rows.Next() {
		var item ruleCandidate
		var expression, recovery []byte
		var duration int
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Type, &item.Severity, &item.SensorID,
			&item.AssetID, &item.AssetName, &item.AssetKind, &item.Unit, &expression, &recovery,
			&duration, &item.MissingMode, &item.Maintenance,
		); err != nil {
			return nil, err
		}
		item.Expression, item.Recovery = domain.JSONMap(expression), domain.JSONMap(recovery)
		item.Duration = time.Duration(duration) * time.Second
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) evaluateCandidate(ctx context.Context, now time.Time, candidate ruleCandidate) error {
	points, err := r.rulePoints(ctx, candidate.SensorID, now.Add(-2*time.Hour))
	if err != nil {
		return err
	}
	fingerprint := ruleFingerprint(candidate.ID, candidate.AssetID, candidate.SensorID)
	memory := enginerules.Memory{State: enginerules.StateOK}
	var alertID, previousState string
	err = r.pool.QueryRow(ctx, `
		SELECT id::text, state, started_at FROM alerts WHERE fingerprint = $1`,
		fingerprint,
	).Scan(&alertID, &previousState, &memory.PendingFrom)
	if err == nil {
		memory.State = enginerules.State(previousState)
	} else if err != pgx.ErrNoRows {
		return err
	} else {
		memory.PendingFrom = time.Time{}
	}
	if len(points) > 1 {
		previous := points[len(points)-2]
		memory.LastPoint = &previous
	}
	rule := enginerules.Rule{
		Type: candidate.Type, Condition: condition(candidate.Expression),
		For: candidate.Duration, MissingMode: candidate.MissingMode,
		StaleAfter: staleAfter(candidate.Expression),
	}
	if len(candidate.Recovery) > 0 {
		value := condition(candidate.Recovery)
		rule.Recovery = &value
	}
	evaluation, err := enginerules.Evaluate(now, rule, points, memory, candidate.Maintenance)
	if err != nil {
		return err
	}
	if alertID == "" && evaluation.State == enginerules.StateOK {
		return nil
	}
	summary := fmt.Sprintf("%s · %s", candidate.AssetName, evaluation.Reason)
	startedAt := now
	if !evaluation.PendingFrom.IsZero() {
		startedAt = evaluation.PendingFrom
	}
	var resolvedAt *time.Time
	if evaluation.State == enginerules.StateResolved || evaluation.State == enginerules.StateOK {
		value := now
		resolvedAt = &value
	}
	err = r.pool.QueryRow(ctx, `
		INSERT INTO alerts (
			fingerprint, rule_id, rule_name, asset_id, sensor_id, severity,
			state, summary, value, unit, started_at, updated_at, resolved_at
		)
		VALUES ($1, $2::uuid, $3, $4::uuid, $5::uuid, $6, $7, $8, $9, $10, $11, now(), $12)
		ON CONFLICT (fingerprint) DO UPDATE SET
			state = EXCLUDED.state, summary = EXCLUDED.summary, value = EXCLUDED.value,
			started_at = CASE
				WHEN alerts.state NOT IN ('Pending', 'Firing', 'Silenced')
				  AND EXCLUDED.state IN ('Pending', 'Firing', 'Silenced')
				THEN EXCLUDED.started_at
				ELSE alerts.started_at
			END,
			updated_at = now(), resolved_at = EXCLUDED.resolved_at
		RETURNING id::text`,
		fingerprint, candidate.ID, candidate.Name, candidate.AssetID, candidate.SensorID,
		candidate.Severity, string(evaluation.State), summary, evaluation.Value,
		candidate.Unit, startedAt, resolvedAt,
	).Scan(&alertID)
	if err != nil {
		return err
	}
	if evaluation.Changed {
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO alert_transitions (alert_id, previous_state, next_state, reason, value)
			VALUES ($1::uuid, $2, $3, $4, $5)`,
			alertID, string(evaluation.Previous), string(evaluation.State),
			evaluation.Reason, evaluation.Value,
		); err != nil {
			return err
		}
		if evaluation.State == enginerules.StateFiring || evaluation.State == enginerules.StateResolved {
			incidentID, err := r.correlateIncident(
				ctx, alertID, candidate.AssetID, candidate.AssetName,
				candidate.Severity, string(evaluation.State),
			)
			if err != nil {
				return err
			}
			if evaluation.State == enginerules.StateFiring && incidentID != "" {
				if err := r.enqueueAutomaticDiagnosis(ctx, incidentID, candidate); err != nil {
					return err
				}
			}
			if _, err := r.pool.Exec(ctx, `
				INSERT INTO notification_deliveries (channel_id, alert_id, dedup_key, not_before, payload)
				SELECT route.channel_id, $1::uuid, $2 || ':' || route.channel_id::text || ':' || $3,
					now() + make_interval(secs => route.group_window_seconds),
					jsonb_build_object(
						'alertId', $1::text, 'state', $3::text, 'summary', $4::text,
						'severity', $5::text, 'assetKind', $6::text, 'locale', route.locale
					)
				FROM notification_routes route
				JOIN notification_channels channel ON channel.id = route.channel_id
				WHERE route.enabled AND channel.enabled
				  AND ($3::text <> 'Resolved' OR route.send_recovery)
				  AND CASE $5::text
					WHEN 'critical' THEN 3 WHEN 'warning' THEN 2 ELSE 1
				  END >= CASE route.min_severity
					WHEN 'critical' THEN 3 WHEN 'warning' THEN 2 ELSE 1
				  END
				  AND (route.asset_kinds = '[]'::jsonb OR route.asset_kinds ? $6::text)
				ON CONFLICT (dedup_key) DO NOTHING`,
				alertID, fingerprint, string(evaluation.State), summary, candidate.Severity, candidate.AssetKind,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repository) correlateIncident(
	ctx context.Context,
	alertID, assetID, assetName, severity, state string,
) (string, error) {
	if state == string(enginerules.StateFiring) {
		var incidentID string
		err := r.pool.QueryRow(ctx, `
			INSERT INTO incidents (title, severity, state, correlation_key)
			VALUES ($1, $2, 'open', $3)
			ON CONFLICT (correlation_key)
				WHERE correlation_key <> '' AND state <> 'resolved'
			DO UPDATE SET
				severity = CASE
					WHEN EXCLUDED.severity = 'critical' THEN 'critical'
					WHEN incidents.severity = 'critical' THEN 'critical'
					WHEN EXCLUDED.severity = 'warning' THEN 'warning'
					ELSE incidents.severity
				END,
				updated_at = now()
			RETURNING id::text`,
			assetName+" · active hardware alerts", severity, "asset:"+assetID,
		).Scan(&incidentID)
		if err != nil {
			return "", fmt.Errorf("upsert incident: %w", err)
		}
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO incident_alerts (incident_id, alert_id)
			VALUES ($1::uuid, $2::uuid)
			ON CONFLICT DO NOTHING`, incidentID, alertID); err != nil {
			return "", fmt.Errorf("link alert to incident: %w", err)
		}
		return incidentID, nil
	}
	if state == string(enginerules.StateResolved) {
		if _, err := r.pool.Exec(ctx, `
			UPDATE incidents incident
			SET state = 'resolved', resolved_at = now(), updated_at = now()
			WHERE incident.id IN (
				SELECT link.incident_id FROM incident_alerts link
				WHERE link.alert_id = $1::uuid
			)
			  AND incident.state <> 'resolved'
			  AND NOT EXISTS (
				SELECT 1
				FROM incident_alerts link
				JOIN alerts alert ON alert.id = link.alert_id
				WHERE link.incident_id = incident.id
				  AND alert.state IN ('Pending', 'Firing', 'Silenced')
			  )`, alertID); err != nil {
			return "", fmt.Errorf("resolve incident: %w", err)
		}
	}
	return "", nil
}

func (r *Repository) enqueueAutomaticDiagnosis(ctx context.Context, incidentID string, candidate ruleCandidate) error {
	raw, ok := candidate.Expression["autoDiagnosis"].(map[string]any)
	if !ok || !boolAny(raw["enabled"]) {
		return nil
	}
	provider := stringAny(raw["provider"])
	model := stringAny(raw["model"])
	if (provider != "openai-compatible" && provider != "ollama") || model == "" {
		return nil
	}
	minSeverity := stringAny(raw["minSeverity"])
	if minSeverity == "" {
		minSeverity = "critical"
	}
	if severityRank(candidate.Severity) < severityRank(minSeverity) {
		return nil
	}
	cooldown := int(numberAny(raw["cooldownSeconds"]))
	if cooldown < 60 {
		cooldown = 3600
	}
	if cooldown > 30*24*60*60 {
		cooldown = 30 * 24 * 60 * 60
	}
	tokenBudget := int(numberAny(raw["tokenBudget"]))
	if tokenBudget < 256 {
		tokenBudget = 8000
	}
	if tokenBudget > 100000 {
		tokenBudget = 100000
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"trigger": "automatic-rule-diagnosis", "ruleId": candidate.ID,
		"ruleName": candidate.Name, "tokenBudget": tokenBudget,
	})
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO diagnoses (
			incident_id, asset_id, provider, model, requested_by, context
		)
		SELECT $1::uuid, $2::uuid, $3, $4, $5, $6::jsonb
		WHERE NOT EXISTS (
			SELECT 1 FROM diagnoses
			WHERE incident_id = $1::uuid AND provider = $3 AND model = $4
			  AND requested_at > now() - make_interval(secs => $7)
		)`,
		incidentID, candidate.AssetID, provider, model, "rule:"+candidate.Name,
		contextJSON, cooldown,
	)
	if err != nil {
		return fmt.Errorf("queue automatic diagnosis: %w", err)
	}
	if tag.RowsAffected() > 0 {
		_ = r.AppendAudit(ctx, "rule:"+candidate.Name, "diagnosis.auto_queued", "incident", incidentID, "", map[string]any{
			"provider": provider, "model": model, "cooldownSeconds": cooldown,
			"tokenBudget": tokenBudget,
		})
	}
	return nil
}

func severityRank(value string) int {
	switch value {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func boolAny(value any) bool {
	result, _ := value.(bool)
	return result
}

func (r *Repository) rulePoints(ctx context.Context, sensorID string, since time.Time) ([]enginerules.Point, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT sample.observed_at, sample.value, sample.text_value
		FROM metric_series series
		JOIN metric_samples sample ON sample.series_id = series.id
		WHERE series.sensor_id = $1::uuid AND sample.observed_at >= $2
		ORDER BY sample.observed_at`, sensorID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []enginerules.Point
	for rows.Next() {
		var item enginerules.Point
		var text *string
		if err := rows.Scan(&item.At, &item.Value, &text); err != nil {
			return nil, err
		}
		if text != nil {
			item.Text = *text
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func condition(value map[string]any) enginerules.Condition {
	operator := stringAny(value["operator"])
	expected := numberAny(value["value"])
	if operator == "" {
		for _, key := range []string{"delta", "rate"} {
			if raw := stringAny(value[key]); raw != "" {
				operator, expected = splitComparison(raw)
				break
			}
		}
	}
	return enginerules.Condition{
		Operator: operator, Value: expected, Text: stringAny(value["text"]),
		Window: durationAny(value["window"]),
	}
}

func staleAfter(value map[string]any) time.Duration {
	result := durationAny(value["staleAfter"])
	if result <= 0 {
		result = 2 * time.Minute
	}
	return result
}

func splitComparison(value string) (string, float64) {
	value = strings.TrimSpace(value)
	for _, operator := range []string{">=", "<=", "!=", ">", "<", "=", "=="} {
		if strings.HasPrefix(value, operator) {
			number, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(value, operator)), 64)
			return operator, number
		}
	}
	number, _ := strconv.ParseFloat(value, 64)
	return ">", number
}

func durationAny(value any) time.Duration {
	switch typed := value.(type) {
	case string:
		result, _ := time.ParseDuration(typed)
		return result
	case float64:
		return time.Duration(typed) * time.Second
	default:
		return 0
	}
}

func numberAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	default:
		return 0
	}
}

func stringAny(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func ruleFingerprint(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}
