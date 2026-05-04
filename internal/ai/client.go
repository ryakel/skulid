// Package ai implements the optional Anthropic-powered assistant. It is
// dormant unless ANTHROPIC_API_KEY is set; the rest of the app makes no
// assumptions about its presence.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	apiURL           = "https://api.anthropic.com/v1/messages"
	apiVersion       = "2023-06-01"
	defaultMaxTokens = 4096
)

// Client talks to Anthropic's Messages API.
type Client struct {
	apiKey string
	model  string
	hc     *http.Client
}

func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = "claude-opus-4-7"
	}
	return &Client{
		apiKey: apiKey,
		model:  model,
		hc:     &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Model() string { return c.model }

// ContentBlock is a polymorphic wire type covering every block kind the API
// emits or accepts. Empty fields are omitted by json.Marshal so we can use
// one struct for text, tool_use, and tool_result.
type ContentBlock struct {
	Type string `json:"type"`

	// "text"
	Text string `json:"text,omitempty"`

	// "tool_use"
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// "tool_result"
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Message is one turn in the conversation as Anthropic sees it.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ToolDef advertises a tool the model is allowed to call.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Tools     []ToolDef `json:"tools,omitempty"`
	Messages  []Message `json:"messages"`
}

type Response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// APIError is what we return when Anthropic responds with non-2xx.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anthropic API error: HTTP %d: %s", e.Status, e.Body)
}

func (c *Client) Send(ctx context.Context, system string, tools []ToolDef, messages []Message) (*Response, error) {
	req := Request{
		Model:     c.model,
		MaxTokens: defaultMaxTokens,
		System:    system,
		Tools:     tools,
		Messages:  messages,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Body: string(respBody)}
	}

	var out Response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w (body=%s)", err, string(respBody))
	}
	return &out, nil
}
