package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ryakel/skulid/internal/db"
	syncengine "github.com/ryakel/skulid/internal/sync"
)

// Agent owns the conversation/turn lifecycle. It is safe to share across
// requests; one Agent backs every conversation.
type Agent struct {
	client        *Client
	conversations *db.AIConversationRepo
	messages      *db.AIMessageRepo
	pending       *db.AIPendingActionRepo
	accounts      *db.AccountRepo
	calendars     *db.CalendarRepo
	audit         *db.AuditRepo
	tasks         *db.TaskRepo
	habits        *db.HabitRepo
	occurrences   *db.HabitOccurrenceRepo
	scheduler     *syncengine.Scheduler
	clientFor     syncengine.ClientFor
	log           *slog.Logger
}

func NewAgent(client *Client,
	conversations *db.AIConversationRepo,
	messages *db.AIMessageRepo,
	pending *db.AIPendingActionRepo,
	accounts *db.AccountRepo,
	calendars *db.CalendarRepo,
	audit *db.AuditRepo,
	tasks *db.TaskRepo,
	habits *db.HabitRepo,
	occurrences *db.HabitOccurrenceRepo,
	scheduler *syncengine.Scheduler,
	clientFor syncengine.ClientFor,
	log *slog.Logger,
) *Agent {
	return &Agent{
		client:        client,
		conversations: conversations,
		messages:      messages,
		pending:       pending,
		accounts:      accounts,
		calendars:     calendars,
		audit:         audit,
		tasks:         tasks,
		habits:        habits,
		occurrences:   occurrences,
		scheduler:     scheduler,
		clientFor:     clientFor,
		log:           log,
	}
}

// maxLoopIterations bounds the auto-execute loop. Read tools can chain, but
// not infinitely: ten round trips is plenty for any reasonable request.
const maxLoopIterations = 10

// SendUserMessage appends a user-authored message to a conversation and runs
// the agent loop until either the model ends its turn or it stages destructive
// actions awaiting confirmation.
func (a *Agent) SendUserMessage(ctx context.Context, convID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("empty message")
	}
	conv, err := a.conversations.Get(ctx, convID)
	if err != nil {
		return err
	}
	if conv == nil {
		return errors.New("conversation not found")
	}
	if conv.State == db.ConvStateAwaitingConfirmation {
		return errors.New("conversation is awaiting confirmation; resolve pending actions first")
	}

	content := []ContentBlock{{Type: "text", Text: text}}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if _, err := a.messages.Insert(ctx, &db.AIMessage{
		ConversationID: convID,
		Role:           db.RoleUser,
		Content:        contentJSON,
	}); err != nil {
		return err
	}
	if conv.Title == "" {
		_ = a.conversations.SetTitle(ctx, convID, summarize(text))
	}
	return a.advance(ctx, convID)
}

// ResolvePendingAction is called when the user clicks Apply or Reject in the
// UI. It executes (or rejects) the staged tool, and once every pending action
// for the latest assistant message is resolved, sends the combined tool_results
// back to the model and continues the loop.
func (a *Agent) ResolvePendingAction(ctx context.Context, actionID int64, apply bool) error {
	action, err := a.pending.Get(ctx, actionID)
	if err != nil {
		return err
	}
	if action == nil {
		return errors.New("action not found")
	}
	if action.Status != db.PendingStatusPending {
		return errors.New("action already resolved")
	}

	var resultJSON json.RawMessage
	if apply {
		tb := NewToolbox(a.accounts, a.calendars, a.audit, a.tasks, a.habits, a.occurrences, a.scheduler, a.clientFor, action.ConversationID)
		resultStr, err := tb.Execute(ctx, action.ToolName, action.ToolInput)
		isError := err != nil
		if err != nil {
			resultStr = err.Error()
		}
		resultJSON, _ = json.Marshal(map[string]any{
			"content":  resultStr,
			"is_error": isError,
		})
		if err := a.pending.Resolve(ctx, actionID, db.PendingStatusApplied, resultJSON); err != nil {
			return err
		}
	} else {
		resultJSON, _ = json.Marshal(map[string]any{
			"content":  "User rejected this action; do not retry without further instruction.",
			"is_error": true,
		})
		if err := a.pending.Resolve(ctx, actionID, db.PendingStatusRejected, resultJSON); err != nil {
			return err
		}
	}

	// If there are still pending actions for this assistant turn, just wait.
	siblings, err := a.pending.ListForMessage(ctx, action.MessageID)
	if err != nil {
		return err
	}
	for _, s := range siblings {
		if s.Status == db.PendingStatusPending {
			return nil
		}
	}

	// All siblings resolved — synthesize the user message that carries every
	// tool_result back to the model, then continue the loop.
	results := make([]ContentBlock, 0, len(siblings))
	for _, s := range siblings {
		var r struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		}
		_ = json.Unmarshal(s.Result, &r)
		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: s.ToolUseID,
			Content:   r.Content,
			IsError:   r.IsError,
		})
	}
	contentJSON, err := json.Marshal(results)
	if err != nil {
		return err
	}
	if _, err := a.messages.Insert(ctx, &db.AIMessage{
		ConversationID: action.ConversationID,
		Role:           db.RoleUser,
		Content:        contentJSON,
	}); err != nil {
		return err
	}
	_ = a.conversations.SetState(ctx, action.ConversationID, db.ConvStateIdle)
	return a.advance(ctx, action.ConversationID)
}

// advance runs the model + auto-tool loop until the conversation reaches a
// stopping state (idle, awaiting confirmation, or a hard error).
func (a *Agent) advance(ctx context.Context, convID int64) error {
	for i := 0; i < maxLoopIterations; i++ {
		_ = a.conversations.SetState(ctx, convID, db.ConvStateThinking)
		messages, err := a.loadMessagesForAPI(ctx, convID)
		if err != nil {
			return err
		}
		resp, err := a.client.Send(ctx, SystemPrompt(time.Now().UTC()), Defs(), messages)
		if err != nil {
			_ = a.conversations.SetState(ctx, convID, db.ConvStateIdle)
			a.log.Error("anthropic call failed", "conv_id", convID, "err", err)
			return fmt.Errorf("anthropic: %w", err)
		}

		assistantContent, err := json.Marshal(resp.Content)
		if err != nil {
			return err
		}
		assistantMsgID, err := a.messages.Insert(ctx, &db.AIMessage{
			ConversationID: convID,
			Role:           db.RoleAssistant,
			Content:        assistantContent,
		})
		if err != nil {
			return err
		}
		_ = a.conversations.Touch(ctx, convID)

		// Partition tool_use blocks: read tools execute now, write tools stage.
		var toolUses []ContentBlock
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				toolUses = append(toolUses, b)
			}
		}
		if len(toolUses) == 0 {
			// Pure text turn — we're done.
			_ = a.conversations.SetState(ctx, convID, db.ConvStateIdle)
			return nil
		}

		tb := NewToolbox(a.accounts, a.calendars, a.audit, a.tasks, a.habits, a.occurrences, a.scheduler, a.clientFor, convID)
		var stagedAny bool
		results := make([]ContentBlock, 0, len(toolUses))
		for _, tu := range toolUses {
			if IsWrite(tu.Name) {
				if _, err := a.pending.Insert(ctx, &db.AIPendingAction{
					ConversationID: convID,
					MessageID:      assistantMsgID,
					ToolUseID:      tu.ID,
					ToolName:       tu.Name,
					ToolInput:      tu.Input,
				}); err != nil {
					return err
				}
				stagedAny = true
				continue
			}
			out, err := tb.Execute(ctx, tu.Name, tu.Input)
			results = append(results, ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   firstNonEmpty(out, errString(err)),
				IsError:   err != nil,
			})
		}

		if stagedAny {
			// We must not call the API again until the user resolves writes; any
			// read tool_results computed in this same turn ride alongside as
			// pre-resolved pending actions so the eventual replay assembles them
			// into one user message.
			for _, r := range results {
				resultJSON, _ := json.Marshal(map[string]any{
					"content":  r.Content,
					"is_error": r.IsError,
				})
				if _, err := a.pending.InsertResolved(ctx, &db.AIPendingAction{
					ConversationID: convID,
					MessageID:      assistantMsgID,
					ToolUseID:      r.ToolUseID,
					ToolName:       "_auto_executed",
				}, db.PendingStatusApplied, resultJSON); err != nil {
					return err
				}
			}
			_ = a.conversations.SetState(ctx, convID, db.ConvStateAwaitingConfirmation)
			return nil
		}

		// Pure read iteration — feed results back as a new user message and loop.
		contentJSON, err := json.Marshal(results)
		if err != nil {
			return err
		}
		if _, err := a.messages.Insert(ctx, &db.AIMessage{
			ConversationID: convID,
			Role:           db.RoleUser,
			Content:        contentJSON,
		}); err != nil {
			return err
		}
	}
	_ = a.conversations.SetState(ctx, convID, db.ConvStateIdle)
	return errors.New("agent loop exceeded max iterations")
}

// loadMessagesForAPI hydrates the conversation into the wire format Anthropic
// expects. Empty content arrays are skipped (defensive — should not occur).
func (a *Agent) loadMessagesForAPI(ctx context.Context, convID int64) ([]Message, error) {
	rows, err := a.messages.ListByConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		var blocks []ContentBlock
		if err := json.Unmarshal(r.Content, &blocks); err != nil {
			return nil, fmt.Errorf("decode message #%d: %w", r.ID, err)
		}
		if len(blocks) == 0 {
			continue
		}
		out = append(out, Message{Role: r.Role, Content: blocks})
	}
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// summarize produces a short conversation title from the first user message.
func summarize(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(empty)"
	}
	const max = 60
	if len([]rune(text)) <= max {
		return text
	}
	r := []rune(text)
	return string(r[:max]) + "…"
}
