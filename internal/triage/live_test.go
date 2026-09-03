package triage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// TestLiveProvider runs a few real captures through the configured provider.
// It is skipped unless FUNDUS_LIVE_TESTS=1 and an OPENAI_API_KEY (or
// FUNDUS_LIVE_BASE_URL + FUNDUS_LIVE_KEY) are set, because it costs money and
// depends on the network. Run it before a release:
//
//	FUNDUS_LIVE_TESTS=1 OPENAI_API_KEY=… go test ./internal/triage -run Live -v
func TestLiveProvider(t *testing.T) {
	if os.Getenv("FUNDUS_LIVE_TESTS") != "1" {
		t.Skip("set FUNDUS_LIVE_TESTS=1 to run against a real provider")
	}
	key := os.Getenv("FUNDUS_LIVE_KEY")
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		t.Skip("no API key")
	}
	base := os.Getenv("FUNDUS_LIVE_BASE_URL")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	modelName := os.Getenv("FUNDUS_LIVE_MODEL")
	if modelName == "" {
		modelName = "gpt-5.4-mini"
	}
	p := llm.NewOpenAI(llm.OpenAIOptions{Name: "live", BaseURL: base, APIKey: key})
	c := newCore(t)
	cfg := config.Default()
	cfg.Triage.Model = modelName
	tr := New(c, p, cfg.Triage, cfg.Autonomy, quiet)

	cases := []struct {
		text  string
		want  string // classification
		check func(t *testing.T)
	}{
		{"I must send the 2025 tax return by 15 October, otherwise there is a late fee. Important!", "task", func(t *testing.T) {
			tasks := c.Tasks([]model.TaskState{model.TaskOpen}, false)
			if len(tasks) != 1 || tasks[0].Due != "2026-10-15" || tasks[0].Importance < 2 {
				t.Errorf("tax task: %+v", tasks)
			}
			if len(tasks) == 1 {
				low := strings.ToLower(tasks[0].Text)
				if !strings.Contains(low, "tax") {
					t.Errorf("task text should stay in the capture's language: %q", tasks[0].Text)
				}
			}
		}},
		{"Ich sollte mir irgendwann ansehen, ob man die Heizungsdaten mit Grafana visualisieren kann. Vielleicht als kleines Projekt.", "idea", func(t *testing.T) {
			if len(c.Notes(model.NoteKindIdea, false)) != 1 {
				t.Error("idea not filed")
			}
			if len(c.Tasks([]model.TaskState{model.TaskOpen}, false)) != 1 {
				t.Error("a vague 'maybe someday' must not become a task")
			}
		}},
		{"hm", "", func(t *testing.T) {}},
	}
	for _, tc := range cases {
		id := capture(t, c, tc.text)
		if _, err := tr.Process(context.Background(), id); err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		obj, _ := c.Get(id)
		cap := obj.(*model.Capture)
		if tc.want != "" && cap.Result.Classification != tc.want {
			t.Errorf("%q: classification %q, want %q (summary: %s)", tc.text, cap.Result.Classification, tc.want, cap.Result.Summary)
		}
		if tc.text == "hm" && cap.Status != model.CaptureNeedsReview && cap.Status != model.CaptureDismissed {
			t.Errorf("'hm' should be parked or dismissed, got %s", cap.Status)
		}
		// The receipt summary must be in the capture's language: English
		// captures get English summaries (a live run once produced Dutch).
		if strings.HasPrefix(tc.text, "I must") {
			low := strings.ToLower(cap.Result.Summary)
			if !strings.Contains(low, "tax") && !strings.Contains(low, "return") {
				t.Errorf("summary not in the capture's language: %q", cap.Result.Summary)
			}
		}
		t.Logf("%-40s → %s | %s", strings.TrimSpace(model.Shorten(tc.text, 40)), cap.Status, cap.Result.Summary)
		tc.check(t)
	}
}
