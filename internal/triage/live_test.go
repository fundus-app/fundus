package triage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// TestLiveProvider runs a few real captures through the configured provider.
// It is skipped unless FUNDUS_LIVE_TESTS=1 and an OPENAI_API_KEY (or
// FUNDUS_LIVE_BASE_URL + FUNDUS_LIVE_KEY) are set, because it costs money and
// depends on the network. Run it before a release:
//
//	FUNDUS_LIVE_TESTS=1 OPENAI_API_KEY=… go test ./internal/triage -run Live -v
//
// liveTriager builds a triager against the real provider named by the
// environment, or skips the test.
func liveTriager(t *testing.T) (*core.Core, *Triager) {
	t.Helper()
	if os.Getenv("FUNDUS_LIVE_TESTS") != "1" {
		t.Skip("set FUNDUS_LIVE_TESTS=1 to run against a real provider")
	}
	key := os.Getenv("FUNDUS_LIVE_KEY")
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		t.Skip("no FUNDUS_LIVE_KEY or OPENAI_API_KEY")
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
	return c, New(c, p, cfg.Triage, cfg.Autonomy, quiet)
}

func TestLiveProvider(t *testing.T) {
	c, tr := liveTriager(t)

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
		{"Finde heraus, welche E-Ink-Displays am Raspberry Pi Zero laufen.", "research", func(t *testing.T) {
			var found bool
			for _, tv := range c.Tasks([]model.TaskState{model.TaskOpen}, false) {
				if tv.Kind == model.TaskKindResearch {
					found = true
					if strings.HasPrefix(strings.ToLower(tv.Text), "research") || !strings.Contains(tv.Text, "E-Ink") {
						t.Errorf("research task text should be the German question without a prefix: %q", tv.Text)
					}
				}
			}
			if !found {
				t.Error("a German research request must become a research task by kind")
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

// TestLiveTopicCatchUp replays what a user saw: three captures about the same
// new project. The model may create the topic on any of them; by the end,
// every note and open task must be attached to it, which requires the model to
// link the earlier objects with "link" when the topic finally appears.
func TestLiveTopicCatchUp(t *testing.T) {
	c, tr := liveTriager(t)
	texts := []string{
		"Fundus zum Einhorn machen: Backlinks kassieren.",
		"Fundus auf Y Combinator, Hacker News, Indie Hackers und Product Hunt veröffentlichen und crossverlinken.",
		"Fundus braucht eine Landingpage mit Screenshots und einem Download-Button.",
	}
	for _, text := range texts {
		id := capture(t, c, text)
		rec, err := tr.Process(context.Background(), id)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		t.Logf("%-40s → %s", model.Shorten(text, 40), rec.Summary)
	}
	var fundus string
	for _, tv := range c.Topics(false) {
		if strings.Contains(strings.ToLower(tv.Topic.Name), "fundus") {
			fundus = tv.Topic.ID
		}
	}
	if fundus == "" {
		t.Fatalf("no Fundus topic after three captures: %+v", c.Topics(false))
	}
	linked := func(topics []string) bool {
		for _, id := range topics {
			if id == fundus {
				return true
			}
		}
		return false
	}
	for _, n := range c.Notes("", false) {
		if !linked(n.Topics) {
			t.Errorf("note %q not linked to the Fundus topic", n.NoteTitle)
		}
	}
	for _, tk := range c.Tasks([]model.TaskState{model.TaskOpen, model.TaskLater, model.TaskWaiting}, false) {
		if !linked(tk.Topics) {
			t.Errorf("task %q not linked to the Fundus topic", tk.Text)
		}
	}
}

// TestLiveNoSpuriousTopic replays a mis-filing the user saw: with an
// unrelated topic in the store, a capture about UI updates must not be linked
// to it (the model had attached "UI-Updates" to "RPG mit Godot Engine").
func TestLiveNoSpuriousTopic(t *testing.T) {
	c, tr := liveTriager(t)
	rec, err := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "topic.create", Name: str("RPG mit Godot Engine"), Kind: "project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rpg := rec.Lines[0].ObjectID
	if _, err := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "note.create", Kind: "idea", Title: str("RPG mit Godot Engine bauen"), Markdown: "Kleines Rollenspiel als Nebenprojekt.", Topics: []string{rpg}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Die UI Updates klappen noch nicht richtig.", "Verstehen, warum Tage in Fundus nicht geupdated werden."} {
		id := capture(t, c, text)
		out, err := tr.Process(context.Background(), id)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		t.Logf("%-40s → %s", model.Shorten(text, 40), out.Summary)
		for _, touched := range out.Touched {
			obj, _ := c.Get(touched)
			var topics []string
			switch o := obj.(type) {
			case *model.Note:
				topics = o.Topics
			case *model.Task:
				topics = o.Topics
			}
			for _, tp := range topics {
				if tp == rpg {
					t.Errorf("%q was linked to the unrelated RPG topic", text)
				}
			}
		}
	}
}
