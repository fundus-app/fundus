package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StructuredMode selects how JSON output is requested from an OpenAI-style API.
type StructuredMode string

const (
	ModeAuto       StructuredMode = "auto"        // try json_schema, fall back
	ModeJSONSchema StructuredMode = "json_schema" // response_format json_schema
	ModeJSONObject StructuredMode = "json_object" // response_format json_object + schema in prompt
	ModePrompt     StructuredMode = "prompt"      // schema in prompt only
)

// OpenAI talks to any OpenAI-compatible chat completions endpoint: OpenAI,
// OpenRouter, Ollama, LM Studio, vLLM, Anthropic's compatibility layer, …
type OpenAI struct {
	name    string
	baseURL string
	apiKey  string
	headers map[string]string
	client  *http.Client

	mu   sync.Mutex
	mode StructuredMode // effective mode after fallbacks
	auto bool
	// legacyMaxTokens is set when the endpoint rejects max_completion_tokens.
	legacyMaxTokens bool
}

// OpenAIOptions configures NewOpenAI.
type OpenAIOptions struct {
	Name       string
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Structured StructuredMode
	HTTPClient *http.Client
}

// NewOpenAI builds a provider.
func NewOpenAI(o OpenAIOptions) *OpenAI {
	p := &OpenAI{
		name:    o.Name,
		baseURL: strings.TrimRight(o.BaseURL, "/"),
		apiKey:  o.APIKey,
		headers: o.Headers,
		client:  o.HTTPClient,
		mode:    o.Structured,
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: 10 * time.Minute}
	}
	if p.mode == "" || p.mode == ModeAuto {
		p.mode = ModeJSONSchema
		p.auto = true
	}
	return p
}

func (p *OpenAI) Name() string { return p.name }

// Mode returns the currently effective structured-output mode.
func (p *OpenAI) Mode() StructuredMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mode
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaRequest struct {
	Model           string          `json:"model"`
	Messages        []oaMessage     `json:"messages"`
	Tools           []oaTool        `json:"tools,omitempty"`
	ResponseFormat  json.RawMessage `json:"response_format,omitempty"`
	MaxTokens       int             `json:"max_completion_tokens,omitempty"`
	LegacyMaxTokens int             `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// Complete implements Provider.
func (p *OpenAI) Complete(ctx context.Context, req *Request) (*Response, error) {
	mode := p.Mode()
	for {
		resp, err := p.complete(ctx, req, mode)
		if err == nil {
			return resp, nil
		}
		var e *Error
		// Older OpenAI-compatible servers only know max_tokens.
		if errors.As(err, &e) && e.Status == 400 && strings.Contains(e.Message, "max_completion_tokens") {
			p.mu.Lock()
			already := p.legacyMaxTokens
			p.legacyMaxTokens = true
			p.mu.Unlock()
			if !already {
				continue
			}
		}
		// Downgrade the structured mode when the endpoint rejects it.
		if p.auto && req.Schema != nil && errors.As(err, &e) && e.Status == 400 && mode != ModePrompt &&
			(strings.Contains(strings.ToLower(e.Message), "response_format") || strings.Contains(strings.ToLower(e.Message), "json_schema") || strings.Contains(strings.ToLower(e.Message), "schema")) {
			next := ModeJSONObject
			if mode == ModeJSONObject {
				next = ModePrompt
			}
			p.mu.Lock()
			p.mode = next
			p.mu.Unlock()
			mode = next
			continue
		}
		return nil, err
	}
}

func (p *OpenAI) complete(ctx context.Context, req *Request, mode StructuredMode) (*Response, error) {
	body := oaRequest{Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature, ReasoningEffort: req.ReasoningEffort}
	p.mu.Lock()
	if p.legacyMaxTokens {
		body.LegacyMaxTokens, body.MaxTokens = req.MaxTokens, 0
	}
	p.mu.Unlock()
	system := req.System
	if req.Schema != nil {
		switch mode {
		case ModeJSONSchema:
			rf, _ := json.Marshal(map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   req.Schema.Name,
					"schema": req.Schema.Schema,
					"strict": false,
				},
			})
			body.ResponseFormat = rf
		case ModeJSONObject:
			body.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
			system += schemaInstruction(req.Schema)
		default:
			system += schemaInstruction(req.Schema)
		}
	}
	if system != "" {
		body.Messages = append(body.Messages, oaMessage{Role: "system", Content: system})
	}
	for _, m := range req.Messages {
		om := oaMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			var otc oaToolCall
			otc.ID = tc.ID
			otc.Type = "function"
			otc.Function.Name = tc.Name
			otc.Function.Arguments = string(tc.Args)
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		body.Messages = append(body.Messages, om)
	}
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Parameters
		body.Tools = append(body.Tools, ot)
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}
	res, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			// Cancelled or timed out by the caller: not the provider's fault,
			// and not something a blind retry should mask.
			return nil, &Error{Provider: p.name, Message: err.Error(), Retryable: errors.Is(ctx.Err(), context.DeadlineExceeded)}
		}
		var nerr net.Error
		retry := errors.As(err, &nerr) || errors.Is(err, io.EOF)
		return nil, &Error{Provider: p.name, Message: err.Error(), Retryable: retry}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, &Error{Provider: p.name, Message: "read body: " + err.Error(), Retryable: true}
	}
	if res.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		var er oaResponse
		if json.Unmarshal(raw, &er) == nil && er.Error != nil {
			msg = er.Error.Message
		}
		return nil, &Error{Provider: p.name, Status: res.StatusCode, Message: truncate(msg, 500),
			Retryable: res.StatusCode == 429 || res.StatusCode >= 500}
	}
	var or oaResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, &Error{Provider: p.name, Message: "decode response: " + err.Error()}
	}
	if len(or.Choices) == 0 {
		return nil, &Error{Provider: p.name, Message: "no choices in response"}
	}
	ch := or.Choices[0]
	out := &Response{Content: ch.Message.Content, Model: or.Model, FinishReason: ch.FinishReason,
		Usage: Usage{InputTokens: or.Usage.PromptTokens, OutputTokens: or.Usage.CompletionTokens}}
	for _, tc := range ch.Message.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(args) {
			args = json.RawMessage(`{}`)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	return out, nil
}

func schemaInstruction(s *Schema) string {
	return fmt.Sprintf("\n\nRespond with a single JSON object only, no prose, no code fences. It must validate against this JSON schema (%s):\n%s", s.Name, string(s.Schema))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
