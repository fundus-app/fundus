package llm

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ProbeResult reports what a provider/model pair can do. Fundus refuses to
// use a model for triage unless it passes the structured-output probe.
type ProbeResult struct {
	Provider   string        `json:"provider"`
	Model      string        `json:"model"`
	Reachable  bool          `json:"reachable"`
	Structured bool          `json:"structured"` // valid JSON matching a schema
	Tools      bool          `json:"tools"`      // returned a tool call
	German     bool          `json:"german"`     // classified a German sentence correctly
	Latency    time.Duration `json:"latency"`
	Errors     []string      `json:"errors,omitempty"`
	Mode       string        `json:"mode,omitempty"`
}

// Probe runs three small requests against the provider.
func Probe(ctx context.Context, p Provider, model string) ProbeResult {
	r := ProbeResult{Provider: p.Name(), Model: model}
	start := time.Now()

	// 1. Plain reachability.
	resp, err := p.Complete(ctx, &Request{Model: model, MaxTokens: 50,
		Messages: []Message{{Role: "user", Content: "Reply with the single word OK."}}})
	if err != nil {
		r.Errors = append(r.Errors, "reachability: "+err.Error())
		r.Latency = time.Since(start)
		return r
	}
	r.Reachable = true
	_ = resp

	// 2. Structured output with a schema, on a German sentence.
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["kind","confidence"],"properties":{"kind":{"type":"string","enum":["task","idea","question"]},"confidence":{"type":"number"}}}`)
	resp, err = p.Complete(ctx, &Request{Model: model, MaxTokens: 200,
		Schema:   &Schema{Name: "probe", Schema: schema},
		System:   "Classify the user's sentence as task, idea or question.",
		Messages: []Message{{Role: "user", Content: "Ich muss morgen unbedingt die Steuererklärung abschicken."}}})
	if err != nil {
		r.Errors = append(r.Errors, "structured: "+err.Error())
	} else {
		var out struct {
			Kind       string  `json:"kind"`
			Confidence float64 `json:"confidence"`
		}
		if json.Unmarshal([]byte(ExtractJSON(resp.Content)), &out) == nil && out.Kind != "" {
			r.Structured = true
			r.German = out.Kind == "task"
		} else {
			r.Errors = append(r.Errors, "structured output failed: the answer was not the requested JSON: "+truncate(resp.Content, 200))
		}
	}
	if o, ok := p.(*OpenAI); ok {
		r.Mode = string(o.Mode())
	}

	// 3. Tool calling.
	tool := Tool{Name: "lookup", Description: "Look up a topic by name.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)}
	resp, err = p.Complete(ctx, &Request{Model: model, MaxTokens: 200, Tools: []Tool{tool},
		System:   "Use the lookup tool to look up the topic the user mentions.",
		Messages: []Message{{Role: "user", Content: "Look up Grafana."}}})
	if err != nil {
		r.Errors = append(r.Errors, "tools: "+err.Error())
	} else if len(resp.ToolCalls) > 0 && strings.EqualFold(resp.ToolCalls[0].Name, "lookup") {
		r.Tools = true
	} else {
		r.Errors = append(r.Errors, "tools: no tool call returned")
	}
	r.Latency = time.Since(start)
	return r
}
