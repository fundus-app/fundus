package triage

import (
	"testing"

	"github.com/fundus-app/fundus/internal/llm"
)

// FuzzParseResult: whatever a model returns must be rejected cleanly or
// yield a structurally valid result, never a panic.
func FuzzParseResult(f *testing.F) {
	f.Add(`{"classification":"task","confidence":0.9,"summary":"x","question":null,"operations":[{"op":"task.create","text":"a"}]}`)
	f.Add("```json\n{\"classification\":\"unclear\",\"confidence\":0.1,\"summary\":\"?\",\"question\":\"why\",\"operations\":[]}\n```")
	f.Add(`{"classification":"note","confidence":2,"summary":"","operations":[{"op":"nope"}]}`)
	f.Add(`prose { "a": [ }`)
	f.Fuzz(func(t *testing.T, content string) {
		res, err := parseResult(content)
		if err == nil {
			if res.Confidence < 0 || res.Confidence > 1 || res.Summary == "" {
				t.Fatalf("accepted invalid result: %+v", res)
			}
			for _, op := range res.Operations {
				if op.Op == "" {
					t.Fatal("empty op accepted")
				}
			}
		}
		_ = llm.ExtractJSON(content)
	})
}
