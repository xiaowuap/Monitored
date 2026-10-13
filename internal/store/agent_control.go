package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/monitored-io/monitored/internal/agentupdate"
	"github.com/monitored-io/monitored/internal/domain"
)

func (r *Repository) RegisterAgent(ctx context.Context, agent domain.Agent) error {
	capabilities, err := json.Marshal(agent.Capabilities)
	if err != nil {
		return err
	}
	tools, err := json.Marshal(agent.Tools)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(agent.Metadata)
	if err != nil {
		return err
	}
	var state string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO agents (
			id, stable_host_id, hostname, os, architecture, version, state,
			certificate_id, capabilities, tools, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'online', $7, $8::jsonb, $9::jsonb, $10::jsonb, now())
		ON CONFLICT (id) DO UPDATE SET
			stable_host_id = EXCLUDED.stable_host_id,
			hostname = EXCLUDED.hostname,
			os = EXCLUDED.os,
			architecture = EXCLUDED.architecture,
			version = EXCLUDED.version,
			state = CASE WHEN agents.state = 'revoked' THEN 'revoked' ELSE 'online' END,
			certificate_id = EXCLUDED.certificate_id,
			capabilities = EXCLUDED.capabilities,
			tools = EXCLUDED.tools,
			metadata = agents.metadata || EXCLUDED.metadata,
			last_update_status = CASE
				WHEN agents.desired_version <> '' AND agents.desired_version = EXCLUDED.version THEN 'succeeded'
				ELSE agents.last_update_status
			END,
			last_update_at = CASE
				WHEN agents.desired_version <> '' AND agents.desired_version = EXCLUDED.version THEN now()
				ELSE agents.last_update_at
			END,
			last_update_error = CASE
				WHEN agents.desired_version <> '' AND agents.desired_version = EXCLUDED.version THEN ''
				ELSE agents.last_update_error
			END,
			last_seen_at = now()
		WHERE agents.state <> 'revoked'
		RETURNING state`,
		agent.ID, agent.StableHostID, agent.Hostname, agent.OS, agent.Architecture,
		agent.Version, agent.CertificateID, capabilities, tools, metadata,
	).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("register agent: %w", err)
	}
	if agent.Version != "" {
		tag, err := r.pool.Exec(ctx, `
			UPDATE agent_tasks
			SET state = 'succeeded', acknowledged_at = COALESCE(acknowledged_at, now()),
				finished_at = now(),
				result = result || jsonb_build_object('confirmedByRegistration', true, 'version', $2::text)
			WHERE agent_id = $1 AND kind = 'update'
			  AND state IN ('queued', 'dispatched', 'acknowledged')
			  AND payload->>'version' = $2`,
			agent.ID, agent.Version,
		)
		if err != nil {
			return fmt.Errorf("confirm Agent update registration: %w", err)
		}
		if tag.RowsAffected() > 0 {
			_ = r.AppendAudit(ctx, "agent:"+agent.ID, "agent.update_confirmed", "agent", agent.ID, "", map[string]any{
				"version": agent.Version, "confirmedBy": "registration",
			})
		}
	}
	return nil
}

func (r *Repository) UpdateAgentCapabilities(ctx context.Context, agentID string, capabilities, tools []string, metadata map[string]any) error {
	capabilityJSON, _ := json.Marshal(capabilities)
	toolJSON, _ := json.Marshal(tools)
	metadataJSON, _ := json.Marshal(metadata)
	tag, err := r.pool.Exec(ctx, `
		UPDATE agents SET capabilities = $2::jsonb, tools = $3::jsonb,
			metadata = metadata || $4::jsonb, last_seen_at = now()
		WHERE id = $1 AND state <> 'revoked'`,
		agentID, capabilityJSON, toolJSON, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("update agent capabilities: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) AgentHeartbeat(ctx context.Context, agentID string, walBytes int64, oldest *time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE agents SET state = 'online', last_seen_at = now(), wal_bytes = $2, oldest_wal_at = $3
		WHERE id = $1 AND state <> 'revoked'`, agentID, walBytes, oldest)
	if err != nil {
		return fmt.Errorf("agent heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) RecordAgentGap(ctx context.Context, agentID string, from, to time.Time, reason string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE agents SET state = 'degraded', last_seen_at = now(),
			last_gap_from = $2, last_gap_to = $3, last_gap_reason = $4
		WHERE id = $1 AND state <> 'revoked'`, agentID, from, to, reason)
	if err != nil {
		return fmt.Errorf("record agent gap: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return r.AppendAudit(ctx, agentID, "agent.gap_reported", "agent", agentID, "", map[string]any{
		"from": from, "to": to, "reason": reason,
	})
}

func (r *Repository) ListAgents(ctx context.Context, limit int) ([]domain.Agent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, stable_host_id, hostname, os, architecture, version,
			CASE WHEN state <> 'revoked' AND last_seen_at < now() - interval '2 minutes' THEN 'offline' ELSE state END,
			certificate_id, capabilities, tools, metadata, last_seen_at, wal_bytes,
			oldest_wal_at, last_gap_from, last_gap_to, last_gap_reason, registered_at
			, desired_version, last_update_status, last_update_at, last_update_error
		FROM agents ORDER BY last_seen_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var result []domain.Agent
	for rows.Next() {
		var item domain.Agent
		var capabilities, tools, metadata []byte
		if err := rows.Scan(
			&item.ID, &item.StableHostID, &item.Hostname, &item.OS, &item.Architecture,
			&item.Version, &item.State, &item.CertificateID, &capabilities, &tools, &metadata,
			&item.LastSeenAt, &item.WALBytes, &item.OldestWALAt, &item.LastGapFrom,
			&item.LastGapTo, &item.LastGapReason, &item.RegisteredAt,
			&item.DesiredVersion, &item.LastUpdateStatus, &item.LastUpdateAt, &item.LastUpdateError,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(capabilities, &item.Capabilities)
		_ = json.Unmarshal(tools, &item.Tools)
		item.Metadata = domain.JSONMap(metadata)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) QueueAgentUpdate(
	ctx context.Context,
	actor, agentID string,
	update agentupdate.Package,
	downloadURL string,
) (domain.AgentTask, error) {
	payload := map[string]any{
		"packageId": update.ID, "version": update.Version, "strategy": update.Strategy,
		"targetOs": update.OS, "architecture": update.Architecture,
		"downloadUrl": downloadURL, "sha256": update.SHA256, "sizeBytes": update.SizeBytes,
		"image": update.Image, "imageDigest": update.ImageDigest,
	}
	payloadJSON, _ := json.Marshal(payload)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTask{}, err
	}
	defer tx.Rollback(ctx)
	var agentOS, architecture, state, lastUpdateStatus string
	var lastUpdateAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT os, architecture, state, last_update_status, last_update_at
		FROM agents WHERE id = $1 FOR UPDATE`,
		agentID,
	).Scan(&agentOS, &architecture, &state, &lastUpdateStatus, &lastUpdateAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTask{}, ErrNotFound
		}
		return domain.AgentTask{}, fmt.Errorf("read Agent update target: %w", err)
	}
	restartPending := lastUpdateStatus == "restart_pending" && lastUpdateAt != nil &&
		lastUpdateAt.After(time.Now().Add(-15*time.Minute))
	if state == "revoked" || agentOS != update.OS || architecture != update.Architecture || restartPending {
		return domain.AgentTask{}, ErrConflict
	}
	var task domain.AgentTask
	var rawPayload, rawResult []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO agent_tasks (agent_id, kind, payload, expires_at)
		VALUES ($1, 'update', $2::jsonb, now() + interval '24 hours')
		RETURNING id::text, agent_id, kind, state, payload, immutable_hash, attempt,
			not_before, expires_at, dispatched_at, acknowledged_at, finished_at, result, created_at`,
		agentID, payloadJSON,
	).Scan(
		&task.ID, &task.AgentID, &task.Kind, &task.State, &rawPayload, &task.ImmutableHash,
		&task.Attempt, &task.NotBefore, &task.ExpiresAt, &task.DispatchedAt,
		&task.AcknowledgedAt, &task.FinishedAt, &rawResult, &task.CreatedAt,
	)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.ConstraintName == "agent_tasks_one_active_update_idx" {
			return domain.AgentTask{}, ErrConflict
		}
		return domain.AgentTask{}, fmt.Errorf("queue Agent update: %w", err)
	}
	task.Payload, task.Result = domain.JSONMap(rawPayload), domain.JSONMap(rawResult)
	if _, err := tx.Exec(ctx, `
		UPDATE agents SET desired_version = $2, last_update_status = 'queued',
			last_update_error = '', last_update_at = now()
		WHERE id = $1`,
		agentID, update.Version,
	); err != nil {
		return domain.AgentTask{}, fmt.Errorf("mark Agent update queued: %w", err)
	}
	if err := appendAuditTx(ctx, tx, actor, "agent.update_queued", "agent", agentID, "", map[string]any{
		"taskId": task.ID, "packageId": update.ID, "version": update.Version,
		"strategy": update.Strategy, "os": update.OS, "architecture": update.Architecture,
	}); err != nil {
		return domain.AgentTask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentTask{}, err
	}
	return task, nil
}

func (r *Repository) CreateAgentEnrollmentToken(
	ctx context.Context,
	actor, agentID string,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO agent_enrollment_tokens (token_hash, agent_id, requested_by, expires_at)
		VALUES ($1, $2, $3, $4)`,
		tokenHash, agentID, actor, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create agent enrollment token: %w", err)
	}
	_ = r.AppendAudit(ctx, actor, "agent.enrollment_token_created", "agent", agentID, "", map[string]any{
		"expiresAt": expiresAt,
	})
	return nil
}

func (r *Repository) ConsumeAgentEnrollmentToken(
	ctx context.Context,
	agentID string,
	tokenHash []byte,
	remoteAddress string,
) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_enrollment_tokens
		SET used_at = now()
		WHERE token_hash = $1 AND agent_id = $2
		  AND used_at IS NULL AND expires_at > now()`,
		tokenHash, agentID,
	)
	if err != nil {
		return fmt.Errorf("consume agent enrollment token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_ = r.AppendAudit(ctx, "agent:"+agentID, "agent.enrolled", "agent", agentID, remoteAddress, map[string]any{
		"credential": "short-lived-mtls",
	})
	return nil
}

func (r *Repository) ValidateAgentEnrollmentToken(
	ctx context.Context,
	agentID string,
	tokenHash []byte,
) error {
	var valid bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_enrollment_tokens
			WHERE token_hash = $1 AND agent_id = $2
			  AND used_at IS NULL AND expires_at > now()
		)`,
		tokenHash, agentID,
	).Scan(&valid); err != nil {
		return fmt.Errorf("validate agent enrollment token: %w", err)
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) AuthorizeAgentCertificateRenewal(ctx context.Context, agentID string) error {
	var allowed bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM agents WHERE id = $1 AND state <> 'revoked'
		)`,
		agentID,
	).Scan(&allowed); err != nil {
		return fmt.Errorf("authorize agent certificate renewal: %w", err)
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) NextAgentTasks(ctx context.Context, agentID string, limit int) ([]domain.AgentTask, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE agent_tasks
		SET state = 'expired', finished_at = now(),
			result = result || '{"error":"task expired before completion"}'::jsonb
		WHERE agent_id = $1 AND state IN ('queued', 'dispatched', 'acknowledged')
		  AND expires_at <= now()`,
		agentID,
	); err != nil {
		return nil, fmt.Errorf("expire Agent tasks: %w", err)
	}
	rows, err := tx.Query(ctx, `
		WITH selected AS (
			SELECT id, payload FROM agent_tasks
			WHERE agent_id = $1
			  AND (
			    state = 'queued'
			    OR (
			      state = 'dispatched' AND kind = 'update'
			      AND dispatched_at < now() - interval '1 hour'
			      AND attempt < 3
			    )
			  )
			  AND not_before <= now() AND expires_at > now()
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE agent_tasks task
		SET state = 'dispatched', dispatched_at = now(), attempt = attempt + 1,
			payload = task.payload - 'oneTimeToken'
		FROM selected
		WHERE task.id = selected.id
		RETURNING task.id::text, task.agent_id, task.kind, task.state, selected.payload,
			task.immutable_hash, task.attempt, task.not_before, task.expires_at,
			task.dispatched_at, task.acknowledged_at, task.finished_at, task.result, task.created_at`,
		agentID, limit,
	)
	if err != nil {
		return nil, err
	}
	var tasks []domain.AgentTask
	for rows.Next() {
		var task domain.AgentTask
		var payload, result []byte
		if err := rows.Scan(
			&task.ID, &task.AgentID, &task.Kind, &task.State, &payload, &task.ImmutableHash,
			&task.Attempt, &task.NotBefore, &task.ExpiresAt, &task.DispatchedAt,
			&task.AcknowledgedAt, &task.FinishedAt, &result, &task.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		task.Payload, task.Result = domain.JSONMap(payload), domain.JSONMap(result)
		tasks = append(tasks, task)
	}
	rows.Close()
	for _, task := range tasks {
		if task.Kind == "discovery" {
			runID, _ := task.Payload["runId"].(string)
			if runID == "" {
				return nil, errors.New("discovery task is missing its run identity")
			}
			if _, err := tx.Exec(ctx, `
				UPDATE discovery_runs
				SET state = 'running', progress = 10,
					details = jsonb_build_object('phase', 'collecting', 'taskId', $2::text)
				WHERE id = $1::uuid AND state = 'queued'`, runID, task.ID); err != nil {
				return nil, fmt.Errorf("mark discovery running: %w", err)
			}
		}
		if task.Kind != "action" {
			if task.Kind == "update" {
				if _, err := tx.Exec(ctx, `
						UPDATE agents SET last_update_status = 'dispatched', last_update_at = now()
						WHERE id = $1`, agentID); err != nil {
					return nil, fmt.Errorf("mark Agent update dispatched: %w", err)
				}
			}
			continue
		}
		actionRequestID, _ := task.Payload["actionRequestId"].(string)
		oneTimeToken, _ := task.Payload["oneTimeToken"].(string)
		if actionRequestID == "" || oneTimeToken == "" {
			return nil, errors.New("approved action task is missing its request identity or execution token")
		}
		var expectedHash string
		if err := tx.QueryRow(ctx, `
			SELECT one_time_token_hash
			FROM action_requests
			WHERE id = $1::uuid AND state = 'approved'
			  AND execution_token_expires_at > now()
			FOR UPDATE`, actionRequestID).Scan(&expectedHash); err != nil {
			return nil, fmt.Errorf("validate approved action token: %w", err)
		}
		actualHash := fmt.Sprintf("%x", sha256.Sum256([]byte(oneTimeToken)))
		if subtle.ConstantTimeCompare([]byte(actualHash), []byte(expectedHash)) != 1 {
			return nil, errors.New("approved action token hash does not match")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE action_requests
			SET state = 'running'
			WHERE id = $1::uuid AND state = 'approved'`, actionRequestID); err != nil {
			return nil, fmt.Errorf("mark action running: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *Repository) CompleteAgentTask(ctx context.Context, agentID, taskID, status string, result map[string]any) error {
	if status != "succeeded" && status != "failed" {
		return errors.New("task result status must be succeeded or failed")
	}
	resultJSON, _ := json.Marshal(result)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind string
	var payloadJSON []byte
	err = tx.QueryRow(ctx, `
		UPDATE agent_tasks SET state = $3, acknowledged_at = COALESCE(acknowledged_at, now()),
			finished_at = now(), result = $4::jsonb
		WHERE id = $2::uuid AND agent_id = $1 AND state IN ('dispatched', 'acknowledged')
		RETURNING kind, payload`,
		agentID, taskID, status, resultJSON,
	).Scan(&kind, &payloadJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("complete agent task: %w", err)
	}
	if kind == "action" {
		var payload map[string]any
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return fmt.Errorf("decode completed action task: %w", err)
		}
		actionRequestID, _ := payload["actionRequestId"].(string)
		if actionRequestID == "" {
			return errors.New("completed action task is missing its request identity")
		}
		tag, err := tx.Exec(ctx, `
			UPDATE action_requests
			SET state = $2, result = $3::jsonb, one_time_token_hash = NULL,
				execution_token_expires_at = NULL
			WHERE id = $1::uuid AND state = 'running'`,
			actionRequestID, status, resultJSON,
		)
		if err != nil {
			return fmt.Errorf("complete action request: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
		if err := appendAuditTx(ctx, tx, "agent:"+agentID, "action."+status, "action_request", actionRequestID, "", map[string]any{
			"taskId": taskID,
		}); err != nil {
			return err
		}
	}
	if kind == "discovery" {
		var payload map[string]any
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return fmt.Errorf("decode discovery task payload: %w", err)
		}
		runID, _ := payload["runId"].(string)
		if runID == "" {
			return errors.New("completed discovery task is missing its run identity")
		}
		structured := map[string]any{}
		if raw, ok := result["structured"].(string); ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &structured)
		}
		assetsFound := integerFromJSON(structured["assets"])
		sensorsFound := integerFromJSON(structured["sensors"])
		unknownFound := integerFromJSON(structured["unknown"])
		tag, err := tx.Exec(ctx, `
			UPDATE discovery_runs
			SET state = $2, progress = 100, finished_at = now(),
				assets_found = $3, sensors_found = $4, unknown_found = $5,
				details = $6::jsonb
			WHERE id = $1::uuid AND state IN ('queued', 'running')`,
			runID, status, assetsFound, sensorsFound, unknownFound, resultJSON,
		)
		if err != nil {
			return fmt.Errorf("complete discovery run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
		if err := appendAuditTx(ctx, tx, "agent:"+agentID, "discovery."+status, "discovery_run", runID, "", map[string]any{
			"taskId": taskID, "assets": assetsFound, "sensors": sensorsFound,
			"unknown": unknownFound,
		}); err != nil {
			return err
		}
	}
	if kind == "update" {
		var payload map[string]any
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return fmt.Errorf("decode completed Agent update task: %w", err)
		}
		version, _ := payload["version"].(string)
		updateStatus := status
		lastError, _ := result["error"].(string)
		if status == "succeeded" {
			structured := map[string]any{}
			if raw, ok := result["structured"].(string); ok && raw != "" {
				_ = json.Unmarshal([]byte(raw), &structured)
			}
			switch {
			case boolValue(structured["restartScheduled"]):
				updateStatus = "restart_pending"
			case boolValue(structured["activationPending"]):
				updateStatus = "staged"
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agents
			SET desired_version = $2, last_update_status = $3,
				last_update_at = now(), last_update_error = $4
			WHERE id = $1`,
			agentID, version, updateStatus, lastError,
		); err != nil {
			return fmt.Errorf("complete Agent update status: %w", err)
		}
		if err := appendAuditTx(ctx, tx, "agent:"+agentID, "agent.update_"+updateStatus, "agent", agentID, "", map[string]any{
			"taskId": taskID, "version": version,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func integerFromJSON(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func (r *Repository) ApplyDiscoverySnapshot(ctx context.Context, runID, agentID string, batch domain.IngestBatch) error {
	if err := r.Ingest(ctx, batch); err != nil {
		return err
	}
	observed := make([]string, 0, len(batch.Sensors))
	for _, sensor := range batch.Sensors {
		observed = append(observed, sensor.StableID)
	}
	observedJSON, _ := json.Marshal(observed)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO discovery_snapshots (run_id, agent_id, observed_stable_ids)
		VALUES (NULLIF($1, '')::uuid, $2, $3::jsonb)`, runID, agentID, observedJSON); err != nil {
		return fmt.Errorf("store discovery snapshot: %w", err)
	}
	// Removed sensors are a terminal missing state until ingestion sees them
	// again and resets their lifecycle. Do not rewrite them on every scan.
	if _, err := tx.Exec(ctx, `
		WITH observed AS (
			SELECT jsonb_array_elements_text($2::jsonb) stable_id
		)
		UPDATE sensor_definitions sensor
		SET missing_discoveries = missing_discoveries + 1,
			discovery_status = CASE
				WHEN missing_discoveries + 1 >= 6 THEN 'removed'
				WHEN missing_discoveries + 1 >= 3 THEN 'stale'
				ELSE discovery_status
			END,
			updated_at = now()
		FROM assets asset
		WHERE sensor.asset_id = asset.id
		  AND asset.metadata->>'agentId' = $1
		  AND (sensor.missing_discoveries < 6 OR sensor.discovery_status <> 'removed')
		  AND NOT EXISTS (SELECT 1 FROM observed WHERE observed.stable_id = sensor.stable_id)`,
		agentID, observedJSON,
	); err != nil {
		return fmt.Errorf("advance discovery lifecycle: %w", err)
	}
	return tx.Commit(ctx)
}
