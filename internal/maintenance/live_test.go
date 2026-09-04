package maintenance

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/embed"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// TestLiveMaintenance runs the model-backed jobs against seeded data with a
// real model and real embeddings. Skipped unless FUNDUS_LIVE_TESTS=1 and
// OPENAI_API_KEY are set; costs a few cents.
func TestLiveMaintenance(t *testing.T) {
	if os.Getenv("FUNDUS_LIVE_TESTS") != "1" {
		t.Skip("set FUNDUS_LIVE_TESTS=1 to run against a real provider")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("no OPENAI_API_KEY")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := time.Now().Add(-72 * time.Hour)
	clock := base
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	ids := seed(t, c, func() { clock = clock.Add(time.Minute) })
	clock = time.Now()
	provider := llm.NewOpenAI(llm.OpenAIOptions{Name: "openai", BaseURL: "https://api.openai.com/v1", APIKey: key})
	modelName := os.Getenv("FUNDUS_LIVE_MODEL")
	if modelName == "" {
		modelName = "gpt-5.6-luna"
	}
	ix, _ := embed.Open(t.TempDir(), "text-embedding-3-small")
	emb := &embed.Client{Name: "openai", BaseURL: "https://api.openai.com/v1", APIKey: key}
	if n, err := embed.NewSyncer(c, ix, emb, "text-embedding-3-small", quiet).Backfill(context.Background()); err != nil || n == 0 {
		t.Fatalf("backfill: %v %d", err, n)
	}
	w := New(c, t.TempDir(), quiet)
	cfg := config.Default().Maintenance
	cfg.Assist = "propose"
	w.Configure(cfg, config.Default().Autonomy, provider, config.Role{Model: modelName}, ix, emb, "text-embedding-3-small", nil)
	run, err := w.RunNow(context.Background(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range run.Jobs {
		t.Logf("%-10s checked=%d changed=%d proposed=%d skipped=%q error=%q notes=%v", j.Name, j.Checked, j.Changed, j.Proposed, j.Skipped, j.Error, j.Notes)
		if j.Error != "" {
			t.Errorf("%s failed: %s", j.Name, j.Error)
		}
	}
	o, _ := c.Get(ids["pvNote"])
	if n := o.(*model.Note); len(n.Topics) != 1 || n.Topics[0] != ids["solaranlage"] {
		t.Errorf("untagged: PV note topics %v", n.Topics)
	}
	o, _ = c.Get(ids["dentist"])
	if len(o.(*model.Task).Topics) != 0 {
		t.Errorf("untagged: dentist task got a topic")
	}
	o, _ = c.Get(ids["solaranlage"])
	summary := o.(*model.Topic).Summary.Markdown()
	t.Logf("summary: %s", summary)
	if !strings.Contains(summary, "Automatic summary (") {
		t.Errorf("no automatic summary written")
	}
	var texts []string
	for _, cap := range c.Inbox() {
		if cap.Source == "maintenance" {
			texts = append(texts, cap.Text)
		}
	}
	t.Logf("proposals: %v", texts)
	joined := strings.Join(texts, " | ")
	if !strings.Contains(joined, "Grafana Loki") {
		t.Errorf("duplicate notes not proposed for merge")
	}
	if !strings.Contains(joined, "Zahnarzt") {
		t.Errorf("duplicate task not proposed")
	}
}
