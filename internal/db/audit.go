package db

import (
	"context"
	"time"

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

// auditPruneBatch bounds how many rows one DELETE statement touches. The
// prune may first run against a table that has already grown for a year, and
// a single unbounded DELETE there would hold locks and bloat WAL for as long
// as it took.
const auditPruneBatch = 5000

// DeleteOlderThan removes audit rows older than the cutoff, in batches, and
// returns how many it deleted. Deleting is safe against the table's foreign
// keys: rule_id and smart_block_id are ON DELETE SET NULL, so rows outliving
// the rule they describe are expected rather than a constraint problem.
//
// The loop stops on a short batch, so a steady-state prune is a single
// statement touching almost nothing.
func (r *AuditRepo) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	if age <= 0 {
		return 0, nil
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		cmd, err := r.pool.Exec(ctx, `
			DELETE FROM audit_log
			WHERE id IN (
				SELECT id FROM audit_log
				WHERE ts < NOW() - $1::interval
				ORDER BY id
				LIMIT $2
			)`, age, auditPruneBatch)
		if err != nil {
			return total, err
		}
		n := cmd.RowsAffected()
		total += n
		if n < auditPruneBatch {
			return total, nil
		}
	}
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
