package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/monitored-io/monitored/internal/domain"
)

func (r *Repository) ListConnections(ctx context.Context, limit int) ([]domain.Connection, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, kind, endpoint, agent_id, enabled, tls_mode,
			capability, status, status_message, last_tested_at, last_success_at,
			created_at, updated_at, encrypted_secret IS NOT NULL
		FROM connections ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Connection
	for rows.Next() {
		var item domain.Connection
		var capability []byte
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Kind, &item.Endpoint, &item.AgentID,
			&item.Enabled, &item.TLSMode, &capability, &item.Status, &item.StatusMessage,
			&item.LastTestedAt, &item.LastSuccessAt, &item.CreatedAt, &item.UpdatedAt,
			&item.HasCredentials,
		); err != nil {
			return nil, err
		}
		item.Capability = domain.JSONMap(capability)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateConnection(ctx context.Context, actor string, item domain.Connection, config map[string]any, encryptedSecret []byte) (domain.Connection, error) {
	configJSON, _ := json.Marshal(config)
	var capability []byte
	err := r.pool.QueryRow(ctx, `
		INSERT INTO connections (name, kind, endpoint, agent_id, enabled, tls_mode, config, encrypted_secret)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		RETURNING id::text, name, kind, endpoint, agent_id, enabled, tls_mode,
			capability, status, status_message, last_tested_at, last_success_at,
			created_at, updated_at, encrypted_secret IS NOT NULL`,
		item.Name, item.Kind, item.Endpoint, item.AgentID, item.Enabled, item.TLSMode,
		configJSON, encryptedSecret,
	).Scan(
		&item.ID, &item.Name, &item.Kind, &item.Endpoint, &item.AgentID, &item.Enabled,
		&item.TLSMode, &capability, &item.Status, &item.StatusMessage, &item.LastTestedAt,
		&item.LastSuccessAt, &item.CreatedAt, &item.UpdatedAt, &item.HasCredentials,
	)
	if err != nil {
		return domain.Connection{}, fmt.Errorf("create connection: %w", err)
	}
	item.Capability = domain.JSONMap(capability)
	_ = r.AppendAudit(ctx, actor, "connection.created", "connection", item.ID, "", map[string]any{
		"kind": item.Kind, "endpoint": item.Endpoint, "hasCredentials": len(encryptedSecret) > 0,
	})
	return item, nil
}

func (r *Repository) ListRules(ctx context.Context, limit int) ([]domain.Rule, error) {
	limit = bounded(limit, 500)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, sensor_id::text, asset_kind, rule_type, severity,
			expression, duration_seconds, recovery_expression, missing_data_mode,
			maintenance_window_id::text, enabled, created_at, updated_at
		FROM alert_rules ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Rule
	for rows.Next() {
		var item domain.Rule
		var expression, recovery []byte
		if err := rows.Scan(
			&item.ID, &item.Name, &item.SensorID, &item.AssetKind, &item.RuleType,
			&item.Severity, &expression, &item.DurationSeconds, &recovery,
			&item.MissingDataMode, &item.MaintenanceWindow, &item.Enabled,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Expression, item.RecoveryExpression = domain.JSONMap(expression), domain.JSONMap(recovery)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateRule(ctx context.Context, actor string, item domain.Rule) (domain.Rule, error) {
	expression, _ := json.Marshal(item.Expression)
	recovery, _ := json.Marshal(item.RecoveryExpression)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO alert_rules (
			name, sensor_id, asset_kind, rule_type, severity, expression,
			duration_seconds, recovery_expression, missing_data_mode,
			maintenance_window_id, enabled
		)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6::jsonb, $7, $8::jsonb,
			$9, NULLIF($10, '')::uuid, $11)
		RETURNING id::text, created_at, updated_at`,
		item.Name, stringPointer(item.SensorID), item.AssetKind, item.RuleType, item.Severity,
		expression, item.DurationSeconds, recovery, item.MissingDataMode,
		stringPointer(item.MaintenanceWindow), item.Enabled,
	).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.Rule{}, fmt.Errorf("create rule: %w", err)
	}
	_ = r.AppendAudit(ctx, actor, "rule.created", "rule", item.ID, "", map[string]any{"name": item.Name})
	return item, nil
}

func (r *Repository) ListMaintenanceWindows(ctx context.Context, limit int) ([]domain.MaintenanceWindow, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, starts_at, ends_at, asset_id::text, rule_id::text,
			created_by, created_at
		FROM maintenance_windows ORDER BY starts_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MaintenanceWindow
	for rows.Next() {
		var item domain.MaintenanceWindow
		if err := rows.Scan(&item.ID, &item.Name, &item.StartsAt, &item.EndsAt, &item.AssetID, &item.RuleID, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateMaintenanceWindow(ctx context.Context, actor string, item domain.MaintenanceWindow) (domain.MaintenanceWindow, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO maintenance_windows (name, starts_at, ends_at, asset_id, rule_id, created_by)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6)
		RETURNING id::text, created_at`,
		item.Name, item.StartsAt, item.EndsAt, stringPointer(item.AssetID), stringPointer(item.RuleID), actor,
	).Scan(&item.ID, &item.CreatedAt)
	item.CreatedBy = actor
	if err != nil {
		return domain.MaintenanceWindow{}, fmt.Errorf("create maintenance window: %w", err)
	}
	_ = r.AppendAudit(ctx, actor, "maintenance.created", "maintenance_window", item.ID, "", map[string]any{"name": item.Name})
	return item, nil
}

func (r *Repository) ListNotificationChannels(ctx context.Context, limit int) ([]domain.NotificationChannel, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, kind, enabled, config, encrypted_secret IS NOT NULL,
			last_tested_at, last_status, created_at, updated_at
		FROM notification_channels ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.NotificationChannel
	for rows.Next() {
		var item domain.NotificationChannel
		var config []byte
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Kind, &item.Enabled, &config, &item.HasSecret,
			&item.LastTestedAt, &item.LastStatus, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Config = domain.JSONMap(config)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateNotificationChannel(ctx context.Context, actor string, item domain.NotificationChannel, encryptedSecret []byte) (domain.NotificationChannel, error) {
	configJSON, _ := json.Marshal(item.Config)
	var returnedConfig []byte
	err := r.pool.QueryRow(ctx, `
		INSERT INTO notification_channels (name, kind, enabled, config, encrypted_secret)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		RETURNING id::text, name, kind, enabled, config, encrypted_secret IS NOT NULL,
			last_tested_at, last_status, created_at, updated_at`,
		item.Name, item.Kind, item.Enabled, configJSON, encryptedSecret,
	).Scan(
		&item.ID, &item.Name, &item.Kind, &item.Enabled, &returnedConfig,
		&item.HasSecret, &item.LastTestedAt, &item.LastStatus, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return domain.NotificationChannel{}, fmt.Errorf("create notification channel: %w", err)
	}
	item.Config = domain.JSONMap(returnedConfig)
	_ = r.AppendAudit(ctx, actor, "notification_channel.created", "notification_channel", item.ID, "", map[string]any{
		"kind": item.Kind, "hasSecret": item.HasSecret,
	})
	return item, nil
}

func (r *Repository) ListNotificationRoutes(ctx context.Context, limit int) ([]domain.NotificationRoute, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT route.id::text, route.channel_id::text, channel.name,
			route.min_severity, route.asset_kinds, route.group_window_seconds,
			route.repeat_interval_seconds, route.send_recovery, route.locale, route.enabled
		FROM notification_routes route
		JOIN notification_channels channel ON channel.id = route.channel_id
		ORDER BY channel.name, route.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.NotificationRoute
	for rows.Next() {
		var item domain.NotificationRoute
		var assetKindsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ChannelID, &item.ChannelName, &item.MinSeverity,
			&assetKindsJSON, &item.GroupWindowSeconds, &item.RepeatIntervalSeconds,
			&item.SendRecovery, &item.Locale, &item.Enabled,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(assetKindsJSON, &item.AssetKinds)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateNotificationRoute(ctx context.Context, actor string, item domain.NotificationRoute) (domain.NotificationRoute, error) {
	assetKindsJSON, _ := json.Marshal(item.AssetKinds)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO notification_routes (
			channel_id, min_severity, asset_kinds, group_window_seconds,
			repeat_interval_seconds, send_recovery, locale, enabled
		)
		VALUES ($1::uuid, $2, $3::jsonb, $4, $5, $6, $7, $8)
		RETURNING id::text`,
		item.ChannelID, item.MinSeverity, assetKindsJSON, item.GroupWindowSeconds,
		item.RepeatIntervalSeconds, item.SendRecovery, item.Locale, item.Enabled,
	).Scan(&item.ID)
	if err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("create notification route: %w", err)
	}
	_ = r.AppendAudit(ctx, actor, "notification_route.created", "notification_route", item.ID, "", map[string]any{
		"channelId": item.ChannelID, "minSeverity": item.MinSeverity,
	})
	return item, nil
}

func (r *Repository) EnqueueNotificationTest(ctx context.Context, actor, channelID string) error {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO notification_deliveries (channel_id, dedup_key, payload)
		SELECT id, 'test:' || id::text || ':' || gen_random_uuid()::text,
			jsonb_build_object(
				'test', true,
				'state', 'Test',
				'summary', 'Monitored notification channel test',
				'severity', 'info',
				'locale', 'zh-CN'
			)
		FROM notification_channels
		WHERE id = $1::uuid`, channelID)
	if err != nil {
		return fmt.Errorf("queue notification test: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return r.AppendAudit(ctx, actor, "notification_channel.test_queued", "notification_channel", channelID, "", nil)
}

func (r *Repository) ClaimNotificationDeliveries(ctx context.Context, limit int) ([]domain.NotificationDelivery, error) {
	limit = bounded(limit, 50)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		WITH selected AS (
			SELECT delivery.id FROM notification_deliveries delivery
			WHERE delivery.status IN ('queued', 'retrying') AND delivery.not_before <= now()
			ORDER BY delivery.created_at LIMIT $1 FOR UPDATE SKIP LOCKED
		)
		UPDATE notification_deliveries delivery
		SET status = 'sending', attempt = attempt + 1
		FROM selected, notification_channels channel
		WHERE delivery.id = selected.id AND channel.id = delivery.channel_id
		RETURNING delivery.id::text, channel.id::text, channel.name, channel.kind,
			channel.config, channel.encrypted_secret, delivery.payload, delivery.attempt`, limit)
	if err != nil {
		return nil, err
	}
	var result []domain.NotificationDelivery
	for rows.Next() {
		var item domain.NotificationDelivery
		var configJSON, payloadJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ChannelID, &item.ChannelName, &item.Kind, &configJSON,
			&item.EncryptedSecret, &payloadJSON, &item.Attempt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		item.Config, item.Payload = domain.JSONMap(configJSON), domain.JSONMap(payloadJSON)
		result = append(result, item)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) FinishNotificationDelivery(ctx context.Context, id string, sendErr error) error {
	if sendErr == nil {
		_, err := r.pool.Exec(ctx, `
			WITH completed AS (
				UPDATE notification_deliveries
				SET status = 'sent', sent_at = now(), last_error = ''
				WHERE id = $1::uuid
				RETURNING channel_id, payload
			)
			UPDATE notification_channels channel
			SET last_tested_at = now(), last_status = 'healthy', updated_at = now()
			FROM completed
			WHERE channel.id = completed.channel_id
			  AND completed.payload->>'test' = 'true'`, id)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		WITH completed AS (
			UPDATE notification_deliveries SET
				status = CASE WHEN attempt >= 8 THEN 'failed' ELSE 'retrying' END,
				not_before = now() + make_interval(secs => LEAST(3600, power(2, attempt)::integer * 15)),
				last_error = left($2, 4096)
			WHERE id = $1::uuid
			RETURNING channel_id, payload
		)
		UPDATE notification_channels channel
		SET last_tested_at = now(), last_status = left('failed: ' || $2, 1024), updated_at = now()
		FROM completed
		WHERE channel.id = completed.channel_id
		  AND completed.payload->>'test' = 'true'`, id, sendErr.Error())
	return err
}

func (r *Repository) ListDiagnoses(ctx context.Context, limit int) ([]domain.Diagnosis, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, incident_id::text, asset_id::text, status, provider, model,
			summary, hypotheses, evidence, data_gaps, checks, actions, rollback,
			confidence, token_usage, requested_by, requested_at, completed_at, context
		FROM diagnoses ORDER BY requested_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Diagnosis
	for rows.Next() {
		var item domain.Diagnosis
		var hypotheses, evidence, gaps, checks, actions, contextJSON []byte
		if err := rows.Scan(
			&item.ID, &item.IncidentID, &item.AssetID, &item.Status, &item.Provider,
			&item.Model, &item.Summary, &hypotheses, &evidence, &gaps, &checks,
			&actions, &item.Rollback, &item.Confidence, &item.TokenUsage,
			&item.RequestedBy, &item.RequestedAt, &item.CompletedAt, &contextJSON,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(hypotheses, &item.Hypotheses)
		_ = json.Unmarshal(evidence, &item.Evidence)
		_ = json.Unmarshal(gaps, &item.DataGaps)
		_ = json.Unmarshal(checks, &item.Checks)
		_ = json.Unmarshal(actions, &item.Actions)
		item.Context = domain.JSONMap(contextJSON)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateDiagnosis(ctx context.Context, actor string, item domain.Diagnosis) (domain.Diagnosis, error) {
	contextJSON, _ := json.Marshal(item.Context)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO diagnoses (incident_id, asset_id, provider, model, requested_by, context)
		VALUES (NULLIF($1, '')::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6::jsonb)
		RETURNING id::text, status, requested_at`,
		stringPointer(item.IncidentID), stringPointer(item.AssetID), item.Provider,
		item.Model, actor, contextJSON,
	).Scan(&item.ID, &item.Status, &item.RequestedAt)
	if err != nil {
		return domain.Diagnosis{}, fmt.Errorf("create diagnosis: %w", err)
	}
	item.RequestedBy = actor
	_ = r.AppendAudit(ctx, actor, "diagnosis.queued", "diagnosis", item.ID, "", map[string]any{
		"provider": item.Provider, "model": item.Model,
	})
	return item, nil
}

func (r *Repository) ClaimDiagnoses(ctx context.Context, limit int) ([]domain.Diagnosis, error) {
	limit = bounded(limit, 10)
	rows, err := r.pool.Query(ctx, `
		WITH selected AS (
			SELECT id FROM diagnoses
			WHERE status = 'queued'
			ORDER BY requested_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE diagnoses diagnosis
		SET status = 'running'
		FROM selected
		WHERE diagnosis.id = selected.id
		RETURNING diagnosis.id::text, diagnosis.incident_id::text,
			diagnosis.asset_id::text, diagnosis.provider, diagnosis.model,
			diagnosis.requested_by, diagnosis.requested_at, diagnosis.context`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Diagnosis
	for rows.Next() {
		var item domain.Diagnosis
		var contextJSON []byte
		if err := rows.Scan(
			&item.ID, &item.IncidentID, &item.AssetID, &item.Provider, &item.Model,
			&item.RequestedBy, &item.RequestedAt, &contextJSON,
		); err != nil {
			return nil, err
		}
		item.Status = "running"
		item.Context = domain.JSONMap(contextJSON)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) FinishDiagnosis(ctx context.Context, item domain.Diagnosis, status string, diagnosisErr error) error {
	if status != "succeeded" && status != "failed" && status != "budget_exceeded" {
		return fmt.Errorf("unsupported diagnosis status %q", status)
	}
	hypotheses, _ := json.Marshal(item.Hypotheses)
	evidence, _ := json.Marshal(item.Evidence)
	gaps, _ := json.Marshal(item.DataGaps)
	checks, _ := json.Marshal(item.Checks)
	actions, _ := json.Marshal(item.Actions)
	errorText := ""
	if diagnosisErr != nil {
		errorText = diagnosisErr.Error()
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE diagnoses
		SET status = $2, summary = $3, hypotheses = $4::jsonb,
			evidence = $5::jsonb, data_gaps = $6::jsonb, checks = $7::jsonb,
			actions = $8::jsonb, rollback = $9, confidence = $10,
			token_usage = $11, error = left($12, 4096), completed_at = now()
		WHERE id = $1::uuid AND status = 'running'`,
		item.ID, status, item.Summary, hypotheses, evidence, gaps, checks, actions,
		item.Rollback, item.Confidence, item.TokenUsage, errorText,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return r.AppendAudit(ctx, "worker", "diagnosis."+status, "diagnosis", item.ID, "", map[string]any{
		"provider": item.Provider, "model": item.Model, "tokenUsage": item.TokenUsage,
	})
}

func (r *Repository) ListUsers(ctx context.Context, limit int) ([]domain.User, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, email::text, role, enabled, totp_secret_encrypted IS NOT NULL,
			last_login_at, created_at FROM users ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.User
	for rows.Next() {
		var item domain.User
		if err := rows.Scan(&item.ID, &item.Email, &item.Role, &item.Enabled, &item.TOTPEnabled, &item.LastLoginAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateUser(ctx context.Context, actor, email, role, passwordHash string) (domain.User, error) {
	var item domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, email::text, role, enabled, false, last_login_at, created_at`,
		email, passwordHash, role,
	).Scan(&item.ID, &item.Email, &item.Role, &item.Enabled, &item.TOTPEnabled, &item.LastLoginAt, &item.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	_ = r.AppendAudit(ctx, actor, "user.created", "user", item.ID, "", map[string]any{"email": item.Email, "role": role})
	return item, nil
}

func (r *Repository) EnsureBootstrapUser(ctx context.Context, email, passwordHash string) error {
	if email == "" || passwordHash == "" {
		return fmt.Errorf("bootstrap email and password hash are required")
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, role, enabled)
		VALUES ($1, $2, 'admin', true)
		ON CONFLICT (email) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			role = 'admin',
			enabled = true,
			updated_at = now()`,
		email, passwordHash,
	)
	if err != nil {
		return fmt.Errorf("ensure bootstrap administrator: %w", err)
	}
	return nil
}

func (r *Repository) UserCredential(ctx context.Context, email string) (passwordHash, role string, enabled bool, err error) {
	err = r.pool.QueryRow(ctx, `SELECT password_hash, role, enabled FROM users WHERE email = $1`, email).
		Scan(&passwordHash, &role, &enabled)
	return
}

func bounded(value, maximum int) int {
	if value <= 0 || value > maximum {
		return maximum
	}
	return value
}

func stringPointer(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
