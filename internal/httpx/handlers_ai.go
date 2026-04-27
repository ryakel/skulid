package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ryakel/skulid/internal/ai"
)

// agentTimeout is the deadline given to a single agent advance, including
// every round trip to Anthropic and every tool call. Generous because some
// users will ask multi-step questions that loop several times.
const agentTimeout = 5 * time.Minute

func (s *Server) handleAssistantList(w http.ResponseWriter, r *http.Request) {
	convs, err := s.AIConversations.List(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := s.pageData(r, "Assistant")
	data["Conversations"] = convs
	s.render(w, "assistant_list", data)
}

func (s *Server) handleAssistantNew(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	text := r.FormValue("text")
	if text == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	convID, err := s.AIConversations.Create(r.Context(), "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.runAgentSend(convID, text); err != nil {
		s.Log.Error("assistant send failed", "conv_id", convID, "err", err)
		http.Error(w, "assistant call failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/assistant/"+strconv.FormatInt(convID, 10), http.StatusFound)
}

func (s *Server) handleAssistantChat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	conv, err := s.AIConversations.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if conv == nil {
		http.NotFound(w, r)
		return
	}
	msgs, err := s.AIMessages.ListByConversation(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pending, err := s.AIPending.ListPendingByConversation(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	turns := make([]chatTurn, 0, len(msgs))
	for _, m := range msgs {
		var blocks []ai.ContentBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			continue
		}
		var text string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				if text != "" {
					text += "\n\n"
				}
				text += b.Text
			}
		}
		turns = append(turns, chatTurn{Role: m.Role, Text: text})
	}

	pendingRows := make([]pendingRow, 0, len(pending))
	for _, p := range pending {
		pretty := prettyJSON(p.ToolInput)
		pendingRows = append(pendingRows, pendingRow{
			ID:          p.ID,
			ToolName:    p.ToolName,
			Description: ai.Describe(p.ToolName, p.ToolInput),
			InputPretty: pretty,
		})
	}

	data := s.pageData(r, "Assistant")
	data["Conversation"] = conv
	data["Turns"] = turns
	data["Pending"] = pendingRows
	s.render(w, "assistant_chat", data)
}

func (s *Server) handleAssistantSend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	text := r.FormValue("text")
	if text == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if err := s.runAgentSend(id, text); err != nil {
		s.Log.Error("assistant send failed", "conv_id", id, "err", err)
		http.Error(w, "assistant call failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/assistant/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func (s *Server) handleAssistantApply(w http.ResponseWriter, r *http.Request) {
	s.resolvePendingAction(w, r, true)
}

func (s *Server) handleAssistantReject(w http.ResponseWriter, r *http.Request) {
	s.resolvePendingAction(w, r, false)
}

func (s *Server) resolvePendingAction(w http.ResponseWriter, r *http.Request, apply bool) {
	convID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	actionID, err := strconv.ParseInt(chi.URLParam(r, "aid"), 10, 64)
	if err != nil {
		http.Error(w, "bad aid", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()
	if err := s.Agent.ResolvePendingAction(ctx, actionID, apply); err != nil {
		s.Log.Error("resolve action failed", "action_id", actionID, "err", err)
		http.Error(w, "resolve failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/assistant/"+strconv.FormatInt(convID, 10), http.StatusFound)
}

func (s *Server) handleAssistantDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.AIConversations.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/assistant", http.StatusFound)
}

// runAgentSend wraps SendUserMessage with the agent timeout. Run synchronously
// so the redirect to the conversation page lands after the model has responded.
func (s *Server) runAgentSend(convID int64, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()
	return s.Agent.SendUserMessage(ctx, convID, text)
}

// ---------------------------------------------------------------------------
// view types
// ---------------------------------------------------------------------------

type chatTurn struct {
	Role string
	Text string
}

type pendingRow struct {
	ID          int64
	ToolName    string
	Description string
	InputPretty string
}

func prettyJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(pretty)
}

