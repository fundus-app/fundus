// Package llm abstracts chat-completion providers behind one small interface.
//
// Fundus never gives a model free write access. Providers only produce text
// or JSON; the triage and conversation runtimes validate every result against
// schemas before anything touches the core.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Message is one chat turn. Tool calls and tool results use the OpenAI shape
// because it is the common denominator across providers.
type Message struct {
	Role       string     `json:"role"` // system|user|assistant|tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Tool describes a callable function.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Schema requests JSON output that matches a JSON schema.
type Schema struct {
	Name   string
	Schema json.RawMessage
}

// Request is a single completion call.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Schema      *Schema
	Tools       []Tool
	MaxTokens   int
	Temperature *float64
	// ReasoningEffort is passed through to providers that support it.
	ReasoningEffort string
}

// Usage reports token consumption when the provider returns it.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is the model output.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	Model        string
	FinishReason string
	Usage        Usage
}

// Provider is implemented by every backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req *Request) (*Response, error)
}

// TranscribeRequest carries one recording to turn into text.
type TranscribeRequest struct {
	Model    string
	Audio    []byte
	MIME     string // audio/wav, audio/mpeg, audio/webm …
	Language string // BCP-47 hint, optional
	// Hints are words the speaker is likely to use (topic names, product
	// names) so that proper nouns come out right.
	Hints []string
}

// Transcriber is implemented by providers that can turn speech into text.
type Transcriber interface {
	Transcribe(ctx context.Context, req *TranscribeRequest) (string, error)
}

// Error classifies provider failures so callers can decide about retries.
type Error struct {
	Provider  string
	Status    int
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Message)
}

// Retryable reports whether an error is worth retrying (network, 429, 5xx).
func Retryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Retryable
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// ExtractJSON pulls a JSON object out of model text that may be wrapped in
// prose or code fences. Returns "" if no object-like span exists.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

// SearchResult is one hit of a web search.
type SearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// WebSearcher is implemented by providers that can search the web
// themselves (OpenAI search models, OpenRouter's web plugin).
type WebSearcher interface {
	SearchWeb(ctx context.Context, model, query string, n int) ([]SearchResult, error)
}
