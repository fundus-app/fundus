package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
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
	transcription   string
	webSearch       string
	// noReasoningTools lists models that reject function tools unless
	// reasoning_effort is "none" (gpt-5.6 on chat completions).
	noReasoningTools map[string]bool
}

// OpenAIOptions configures NewOpenAI.
type OpenAIOptions struct {
	Name       string
	BaseURL    string
	APIKey     string
	Headers    map[string]string
	Structured StructuredMode
	HTTPClient *http.Client
	// Transcription is "audio" (POST /audio/transcriptions), "chat" (the
	// recording as an input_audio part) or "none".
	Transcription string
	// WebSearch is "chat_completions" (web_search_options on a search model),
	// "openrouter" (the web plugin) or "none".
	WebSearch string
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

		transcription: o.Transcription,
		webSearch:     o.WebSearch,
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
	Role        string         `json:"role"`
	Content     string         `json:"content"`
	ToolCalls   []oaToolCall   `json:"tool_calls,omitempty"`
	ToolCallID  string         `json:"tool_call_id,omitempty"`
	Annotations []oaAnnotation `json:"annotations,omitempty"`
}

// oaAnnotation is a citation attached by web search models.
type oaAnnotation struct {
	Type        string `json:"type"`
	URLCitation struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		StartIndex int    `json:"start_index"`
		EndIndex   int    `json:"end_index"`
	} `json:"url_citation"`
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
	// Web search: OpenAI search models take web_search_options, OpenRouter
	// takes the "web" plugin.
	WebSearchOptions json.RawMessage `json:"web_search_options,omitempty"`
	Plugins          json.RawMessage `json:"plugins,omitempty"`
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
		// Newer OpenAI models refuse function tools together with reasoning
		// on chat completions unless reasoning_effort is "none": remember
		// that per model and retry once.
		if errors.As(err, &e) && e.Status == 400 && len(req.Tools) > 0 && strings.Contains(e.Message, "reasoning_effort") {
			p.mu.Lock()
			if p.noReasoningTools == nil {
				p.noReasoningTools = map[string]bool{}
			}
			already := p.noReasoningTools[req.Model]
			p.noReasoningTools[req.Model] = true
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
	if len(req.Tools) > 0 && p.noReasoningTools[req.Model] {
		body.ReasoningEffort = "none"
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

// Transcribe implements Transcriber. With mode "audio" it posts the recording
// to /audio/transcriptions (OpenAI); with "chat" it sends the recording as an
// input_audio part of a chat completion (Gemini, OpenRouter). A 404 from the
// audio endpoint falls back to the chat path once, so an OpenAI-compatible
// server without a transcription endpoint still works when its model hears.
func (p *OpenAI) Transcribe(ctx context.Context, req *TranscribeRequest) (string, error) {
	switch p.transcription {
	case "none":
		return "", &Error{Provider: p.name, Message: "dictation is not available with this provider"}
	case "chat":
		return p.transcribeChat(ctx, req)
	}
	text, err := p.transcribeAudio(ctx, req)
	var e *Error
	if err != nil && errors.As(err, &e) && e.Status == 404 {
		return p.transcribeChat(ctx, req)
	}
	return text, err
}

func (p *OpenAI) transcribeAudio(ctx context.Context, req *TranscribeRequest) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", req.Model)
	if req.Language != "" {
		_ = mw.WriteField("language", req.Language)
	}
	if len(req.Hints) > 0 {
		// The prompt steers spelling of names; it is not an instruction.
		_ = mw.WriteField("prompt", strings.Join(req.Hints, ", "))
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="`+audioFilename(req.MIME)+`"`)
	hdr.Set("Content-Type", req.MIME)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(req.Audio); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	raw, err := p.post(ctx, "/audio/transcriptions", mw.FormDataContentType(), buf.Bytes())
	if err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", &Error{Provider: p.name, Message: "decode transcription: " + err.Error()}
	}
	return strings.TrimSpace(out.Text), nil
}

func (p *OpenAI) transcribeChat(ctx context.Context, req *TranscribeRequest) (string, error) {
	format := "wav"
	switch req.MIME {
	case "audio/mpeg", "audio/mp3":
		format = "mp3"
	case "audio/webm":
		format = "webm"
	case "audio/ogg":
		format = "ogg"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		format = "m4a"
	}
	system := "You are a transcription engine. Write down exactly what is said in the recording, in its original language, with normal punctuation. Output only the transcript: no quotes, no preamble, no commentary. If the recording is silent or unintelligible, output an empty string."
	if len(req.Hints) > 0 {
		system += " Names that may occur: " + strings.Join(req.Hints, ", ") + "."
	}
	if req.Language != "" {
		system += " The language is " + req.Language + "."
	}
	body := map[string]any{
		"model": req.Model,
		"messages": []any{
			map[string]any{"role": "system", "content": system},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Transcribe this recording."},
				map[string]any{"type": "input_audio", "input_audio": map[string]any{
					"data": base64.StdEncoding.EncodeToString(req.Audio), "format": format}},
			}},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	raw, err := p.post(ctx, "/chat/completions", "application/json", buf)
	if err != nil {
		return "", err
	}
	var or oaResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return "", &Error{Provider: p.name, Message: "decode response: " + err.Error()}
	}
	if len(or.Choices) == 0 {
		return "", &Error{Provider: p.name, Message: "no choices in response"}
	}
	return strings.TrimSpace(or.Choices[0].Message.Content), nil
}

// post sends one request and returns the body, mapping transport and HTTP
// failures to *Error the same way completions do.
func (p *OpenAI) post(ctx context.Context, path, contentType string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		httpReq.Header.Set(k, v)
	}
	res, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &Error{Provider: p.name, Message: err.Error(), Retryable: errors.Is(ctx.Err(), context.DeadlineExceeded)}
		}
		var nerr net.Error
		return nil, &Error{Provider: p.name, Message: err.Error(), Retryable: errors.As(err, &nerr) || errors.Is(err, io.EOF)}
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
	return raw, nil
}

func audioFilename(mime string) string {
	switch mime {
	case "audio/mpeg", "audio/mp3":
		return "audio.mp3"
	case "audio/webm":
		return "audio.webm"
	case "audio/ogg":
		return "audio.ogg"
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return "audio.m4a"
	}
	return "audio.wav"
}

// SearchWeb implements WebSearcher through the provider's own search: a chat
// completion on a search-capable model whose answer carries url_citation
// annotations. The model's prose is only used for snippets.
func (p *OpenAI) SearchWeb(ctx context.Context, model, query string, n int) ([]SearchResult, error) {
	if n <= 0 {
		n = 8
	}
	body := oaRequest{Model: model, Messages: []oaMessage{
		{Role: "system", Content: "You are a web search engine. For the query, find the most relevant current web pages and answer with a short list: for each page one line with its title, a one-sentence summary and the URL. Cite every page."},
		{Role: "user", Content: query},
	}}
	switch p.webSearch {
	case "chat_completions":
		body.WebSearchOptions = json.RawMessage(`{"search_context_size":"low"}`)
	case "openrouter":
		body.Plugins = json.RawMessage(fmt.Sprintf(`[{"id":"web","max_results":%d}]`, n))
	default:
		return nil, &Error{Provider: p.name, Message: "this provider has no web search of its own"}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	raw, err := p.post(ctx, "/chat/completions", "application/json", buf)
	if err != nil {
		return nil, err
	}
	var or oaResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, &Error{Provider: p.name, Message: "decode response: " + err.Error()}
	}
	if len(or.Choices) == 0 {
		return nil, &Error{Provider: p.name, Message: "no choices in response"}
	}
	msg := or.Choices[0].Message
	var out []SearchResult
	seen := map[string]bool{}
	for _, a := range msg.Annotations {
		u := strings.TrimSpace(a.URLCitation.URL)
		if a.Type != "url_citation" || u == "" || seen[u] {
			continue
		}
		seen[u] = true
		r := SearchResult{URL: u, Title: strings.TrimSpace(a.URLCitation.Title)}
		if s, e := a.URLCitation.StartIndex, a.URLCitation.EndIndex; s >= 0 && e > s && e <= len(msg.Content) {
			r.Snippet = strings.TrimSpace(msg.Content[s:e])
		}
		out = append(out, r)
		if len(out) >= n {
			break
		}
	}
	if len(out) == 0 {
		// No annotations: fall back to links written into the text.
		for _, u := range markdownLinks(msg.Content) {
			if seen[u] {
				continue
			}
			seen[u] = true
			out = append(out, SearchResult{URL: u})
			if len(out) >= n {
				break
			}
		}
	}
	return out, nil
}

// markdownLinks returns the http(s) URLs found in text, in order.
func markdownLinks(text string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '(' || r == ')' || r == '<' || r == '>' || r == '[' || r == ']'
	}) {
		if strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://") {
			out = append(out, strings.TrimRight(f, ".,;"))
		}
	}
	return out
}
