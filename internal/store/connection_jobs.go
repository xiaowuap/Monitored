package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/monitored-io/monitored/internal/connection"
)

func (r *Repository) ListConnectionJobs(ctx context.Context, limit int) ([]connection.Job, error) {
	limit = bounded(limit, 250)
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, kind, endpoint, tls_mode, config, encrypted_secret
		FROM connections
		WHERE enabled AND (agent_id IS NULL OR agent_id = '')
		ORDER BY last_tested_at NULLS FIRST, updated_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list Master connection jobs: %w", err)
	}
	defer rows.Close()
	var result []connection.Job
	for rows.Next() {
		var job connection.Job
		var configJSON []byte
		if err := rows.Scan(
			&job.ID, &job.Name, &job.Kind, &job.Endpoint, &job.TLSMode,
			&configJSON, &job.EncryptedSecret,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(configJSON, &job.Config)
		result = append(result, job)
	}
	return result, rows.Err()
}

func (r *Repository) FinishConnectionPoll(ctx context.Context, id string, capability map[string]any, pollErr error) error {
	capabilityJSON, _ := json.Marshal(capability)
	if pollErr == nil {
		_, err := r.pool.Exec(ctx, `
			UPDATE connections
			SET status = 'healthy', status_message = '', capability = $2::jsonb,
				last_tested_at = now(), last_success_at = now(), updated_at = now()
			WHERE id = $1::uuid`, id, capabilityJSON)
		return err
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE connections
		SET status = CASE WHEN last_success_at IS NULL THEN 'failed' ELSE 'degraded' END,
			status_message = left($2, 2048), capability = $3::jsonb,
			last_tested_at = now(), updated_at = now()
		WHERE id = $1::uuid`, id, pollErr.Error(), capabilityJSON)
	return err
}
