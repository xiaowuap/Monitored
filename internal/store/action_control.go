package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/monitored-io/monitored/internal/domain"
)

func (r *Repository) CreateAction(ctx context.Context, actor string, item domain.ActionRequest, canonical map[string]any) (domain.ActionRequest, error) {
	canonicalJSON, _ := json.Marshal(canonical)
	environmentJSON, _ := json.Marshal(item.Environment)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO action_requests (
			asset_id, agent_id, kind, title, command, working_directory,
			environment, run_identity, timeout_seconds, risk, state,
			immutable_hash, canonical_payload, requested_by
		)
		VALUES ($1::uuid, NULLIF($2, ''), $3, $4, $5, $6, $7::jsonb, $8, $9,
			$10, 'pending', $11, $12::jsonb, $13)
		RETURNING id::text, requested_at`,
		item.AssetID, item.AgentID, item.Kind, item.Title, item.Command,
		item.WorkingDirectory, environmentJSON, item.Identity, item.TimeoutSeconds,
		item.Risk, item.ImmutableHash, canonicalJSON, actor,
	).Scan(&item.ID, &item.RequestedAt)
	if err != nil {
		return domain.ActionRequest{}, fmt.Errorf("create action request: %w", err)
	}
	item.RequestedBy, item.State = actor, "pending"
	_ = r.AppendAudit(ctx, actor, "action.requested", "action_request", item.ID, "", map[string]any{
		"immutableHash": item.ImmutableHash, "agentId": item.AgentID, "risk": item.Risk,
	})
	return item, nil
}
