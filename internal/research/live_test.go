package research

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// TestLiveResearch runs one real research task: OpenAI's own web search as
// the backend and the filing model as the reader. Skipped unless
// FUNDUS_LIVE_TESTS=1 and OPENAI_API_KEY are set; costs a few cents.
func TestLiveResearch(t *testing.T) {
	if os.Getenv("FUNDUS_LIVE_TESTS") != "1" {
		t.Skip("set FUNDUS_LIVE_TESTS=1 to run against a real provider")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("no OPENAI_API_KEY")
	}
	cfg := config.Default()
	cfg.Providers["openai"] = config.Provider{Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: key, WebSearch: "chat_completions"}
	if m := os.Getenv("FUNDUS_LIVE_MODEL"); m != "" {
		cfg.Chat.Model = m
	} else {
		cfg.Chat.Model = "gpt-5.6-luna"
	}
	provider := llm.NewOpenAI(llm.OpenAIOptions{Name: "openai", BaseURL: cfg.Providers["openai"].BaseURL, APIKey: key, WebSearch: "chat_completions"})
	searcher := NewSearcher(cfg, provider, nil)
	if searcher == nil {
		t.Fatal("no searcher")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	w := New(c, quiet, "test")
	w.Configure(provider, cfg.ResearchRole(), cfg.Research, searcher)
	events, unsub := c.Subscribe()
	defer unsub()
	taskID, err := w.StartQuestion(context.Background(), "What is the current stable version of the Go programming language, and when was it released?", "user:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	var noteID string
	for noteID == "" {
		ev := <-events
		if ev.Type != "research.progress" {
			continue
		}
		p := ev.Payload.(Progress)
		t.Logf("%-6s %s", p.Step, model.Shorten(p.Summary, 120))
		switch p.Step {
		case "error":
			t.Fatalf("research failed: %s", p.Summary)
		case "done":
			noteID = p.NoteID
		}
	}
	w.Stop()
	obj, _ := c.Get(noteID)
	n := obj.(*model.Note)
	md := n.Body.Markdown()
	t.Logf("note:\n%s", md)
	if !strings.Contains(md, "[!external]") || !strings.Contains(md, "[[src_") || !strings.Contains(strings.ToLower(md), "go 1.") {
		t.Fatalf("note missing external callout, sources or a Go version:\n%s", md)
	}
	obj, _ = c.Get(taskID)
	if obj.(*model.Task).State != model.TaskDone {
		t.Fatal("task not completed")
	}
}
