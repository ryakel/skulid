package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AIConversation states.
const (
	ConvStateIdle                 = "idle"
	ConvStateAwaitingConfirmation = "awaiting_confirmation"
	ConvStateThinking             = "thinking"
)

// AIPendingAction statuses.
const (
	PendingStatusPending  = "pending"
	PendingStatusApplied  = "applied"
	PendingStatusRejected = "rejected"
)

// Message roles (mirrors Anthropic API).
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type AIConversation struct {
	ID        int64
	Title     string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AIMessage struct {
	ID             int64
	ConversationID int64
	Role           string
	Content        json.RawMessage // array of Anthropic content blocks
	CreatedAt      time.Time
}

type AIPendingAction struct {
	ID             int64
	ConversationID int64
	MessageID      int64
	ToolUseID      string
	ToolName       string
	ToolInput      json.RawMessage
	Status         string
	Result         json.RawMessage
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}

// ---------------------------------------------------------------------------

type AIConversationRepo struct{ pool *pgxpool.Pool }

func NewAIConversationRepo(pool *pgxpool.Pool) *AIConversationRepo {
	return &AIConversationRepo{pool: pool}
}

func (r *AIConversationRepo) Create(ctx context.Context, title string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ai_conversation (title) VALUES ($1) RETURNING id`, title).Scan(&id)
	return id, err
}

func (r *AIConversationRepo) Get(ctx context.Context, id int64) (*AIConversation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, title, state, created_at, updated_at
		FROM ai_conversation WHERE id = $1`, id)
	var c AIConversation
	if err := row.Scan(&c.ID, &c.Title, &c.State, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *AIConversationRepo) List(ctx context.Context, limit int) ([]AIConversation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, state, created_at, updated_at
		FROM ai_conversation ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIConversation
	for rows.Next() {
		var c AIConversation
		if err := rows.Scan(&c.ID, &c.Title, &c.State, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AIConversationRepo) Touch(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE ai_conversation SET updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *AIConversationRepo) SetState(ctx context.Context, id int64, state string) error {
	_, err := r.pool.Exec(ctx, `UPDATE ai_conversation SET state = $2, updated_at = NOW() WHERE id = $1`, id, state)
	return err
}

func (r *AIConversationRepo) SetTitle(ctx context.Context, id int64, title string) error {
	_, err := r.pool.Exec(ctx, `UPDATE ai_conversation SET title = $2, updated_at = NOW() WHERE id = $1`, id, title)
	return err
}

func (r *AIConversationRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM ai_conversation WHERE id = $1`, id)
	return err
}

// DeleteOlderThan removes conversations whose updated_at is older than the cutoff.
// Returns the number of conversations deleted.
func (r *AIConversationRepo) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cmd, err := r.pool.Exec(ctx, `
		DELETE FROM ai_conversation WHERE updated_at < NOW() - $1::interval`, age)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// ---------------------------------------------------------------------------

type AIMessageRepo struct{ pool *pgxpool.Pool }

func NewAIMessageRepo(pool *pgxpool.Pool) *AIMessageRepo { return &AIMessageRepo{pool: pool} }

func (r *AIMessageRepo) Insert(ctx context.Context, m *AIMessage) (int64, error) {
	if len(m.Content) == 0 {
		m.Content = json.RawMessage(`[]`)
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ai_message (conversation_id, role, content_jsonb)
		VALUES ($1, $2, $3) RETURNING id`,
		m.ConversationID, m.Role, m.Content).Scan(&id)
	return id, err
}

func (r *AIMessageRepo) ListByConversation(ctx context.Context, convID int64) ([]AIMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, role, content_jsonb, created_at
		FROM ai_message WHERE conversation_id = $1 ORDER BY created_at, id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIMessage
	for rows.Next() {
		var m AIMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------

type AIPendingActionRepo struct{ pool *pgxpool.Pool }

func NewAIPendingActionRepo(pool *pgxpool.Pool) *AIPendingActionRepo {
	return &AIPendingActionRepo{pool: pool}
}

func (r *AIPendingActionRepo) Insert(ctx context.Context, p *AIPendingAction) (int64, error) {
	if len(p.ToolInput) == 0 {
		p.ToolInput = json.RawMessage(`{}`)
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ai_pending_action (conversation_id, message_id, tool_use_id, tool_name, tool_input_jsonb, status)
		VALUES ($1, $2, $3, $4, $5, 'pending') RETURNING id`,
		p.ConversationID, p.MessageID, p.ToolUseID, p.ToolName, p.ToolInput).Scan(&id)
	return id, err
}

// InsertResolved is for read tools that we executed eagerly during a turn that
// also staged writes. Their results need to ride alongside the writes when we
// finally call back to Anthropic.
func (r *AIPendingActionRepo) InsertResolved(ctx context.Context, p *AIPendingAction, status string, result json.RawMessage) (int64, error) {
	if len(p.ToolInput) == 0 {
		p.ToolInput = json.RawMessage(`{}`)
	}
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ai_pending_action (conversation_id, message_id, tool_use_id, tool_name, tool_input_jsonb,
		                               status, result_jsonb, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW()) RETURNING id`,
		p.ConversationID, p.MessageID, p.ToolUseID, p.ToolName, p.ToolInput, status, result).Scan(&id)
	return id, err
}

func (r *AIPendingActionRepo) Get(ctx context.Context, id int64) (*AIPendingAction, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, conversation_id, message_id, tool_use_id, tool_name, tool_input_jsonb,
		       status, result_jsonb, created_at, resolved_at
		FROM ai_pending_action WHERE id = $1`, id)
	return scanPendingAction(row)
}

func (r *AIPendingActionRepo) ListPendingByConversation(ctx context.Context, convID int64) ([]AIPendingAction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, message_id, tool_use_id, tool_name, tool_input_jsonb,
		       status, result_jsonb, created_at, resolved_at
		FROM ai_pending_action
		WHERE conversation_id = $1 AND status = 'pending'
		ORDER BY created_at, id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPendingActions(rows)
}

// ListForMessage returns every pending action attached to a particular assistant
// message (regardless of status). Used to assemble tool_results once they're all
// resolved.
func (r *AIPendingActionRepo) ListForMessage(ctx context.Context, messageID int64) ([]AIPendingAction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, message_id, tool_use_id, tool_name, tool_input_jsonb,
		       status, result_jsonb, created_at, resolved_at
		FROM ai_pending_action
		WHERE message_id = $1
		ORDER BY created_at, id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPendingActions(rows)
}

func (r *AIPendingActionRepo) Resolve(ctx context.Context, id int64, status string, result json.RawMessage) error {
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE ai_pending_action SET status = $2, result_jsonb = $3, resolved_at = NOW()
		WHERE id = $1`, id, status, result)
	return err
}

func collectPendingActions(rows pgx.Rows) ([]AIPendingAction, error) {
	var out []AIPendingAction
	for rows.Next() {
		p, err := scanPendingAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPendingAction(row rowScanner) (*AIPendingAction, error) {
	var p AIPendingAction
	if err := row.Scan(&p.ID, &p.ConversationID, &p.MessageID, &p.ToolUseID, &p.ToolName,
		&p.ToolInput, &p.Status, &p.Result, &p.CreatedAt, &p.ResolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}
