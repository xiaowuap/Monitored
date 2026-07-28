package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (r *Repository) BeginTOTPEnrollment(ctx context.Context, email string, encryptedSecret []byte) error {
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO user_totp_enrollments (user_id, encrypted_secret, expires_at)
		SELECT id, $2, now() + interval '10 minutes'
		FROM users
		WHERE email = $1 AND enabled
		ON CONFLICT (user_id) DO UPDATE SET
			encrypted_secret = EXCLUDED.encrypted_secret,
			expires_at = EXCLUDED.expires_at,
			created_at = now()`, email, encryptedSecret)
	if err != nil {
		return fmt.Errorf("begin TOTP enrollment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return r.AppendAudit(ctx, email, "totp.enrollment_started", "user", email, "", nil)
}

func (r *Repository) PendingTOTPSecret(ctx context.Context, email string) ([]byte, error) {
	var encrypted []byte
	err := r.pool.QueryRow(ctx, `
		SELECT enrollment.encrypted_secret
		FROM user_totp_enrollments enrollment
		JOIN users ON users.id = enrollment.user_id
		WHERE users.email = $1 AND users.enabled
		  AND enrollment.expires_at > now()`, email).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return encrypted, err
}

func (r *Repository) CompleteTOTPEnrollment(ctx context.Context, email string, encryptedSecret []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET totp_secret_encrypted = $2, updated_at = now()
		WHERE email = $1 AND enabled`, email, encryptedSecret)
	if err != nil {
		return fmt.Errorf("activate TOTP: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM user_totp_enrollments
		WHERE user_id = (SELECT id FROM users WHERE email = $1)`, email); err != nil {
		return err
	}
	if err := appendAuditTx(ctx, tx, email, "totp.enabled", "user", email, "", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) UserTOTPSecret(ctx context.Context, email string) ([]byte, bool, error) {
	var encrypted []byte
	err := r.pool.QueryRow(ctx, `
		SELECT totp_secret_encrypted FROM users
		WHERE email = $1 AND enabled`, email).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	return encrypted, len(encrypted) > 0, nil
}

func (r *Repository) ActionRisk(ctx context.Context, id string) (string, error) {
	var risk string
	err := r.pool.QueryRow(ctx, `
		SELECT risk FROM action_requests WHERE id = $1::uuid`, id).Scan(&risk)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return risk, err
}
