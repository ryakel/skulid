package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditRepo struct {
	pool *pgxpool.Pool
}

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo { return &AuditRepo{pool: pool} }

type AuditWrite struct {
	Kind          string
	RuleID        *int64
	SmartBlockID  *int64
	SourceEventID string
	TargetEventID string
	Action        string
	Message       string
}

func (r *AuditRepo) Write(ctx context.Context, e AuditWrite) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_log (kind, rule_id, smart_block_id, source_event_id, target_event_id, action, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Kind, e.RuleID, e.SmartBlockID, e.SourceEventID, e.TargetEventID, e.Action, e.Message)
	return err
}

func (r *AuditRepo) Recent(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, ts, kind, rule_id, smart_block_id, source_event_id, target_event_id, action, message
		FROM audit_log ORDER BY ts DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.RuleID, &e.SmartBlockID, &e.SourceEventID, &e.TargetEventID, &e.Action, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
