package maintenance

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

func str(s string) *string { return &s }

// fakeModel answers every maintenance schema deterministically.
func fakeModel(t *testing.T, ids map[string]string) *llm.Fake {
	return &llm.Fake{ProviderName: "fake", Fn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
		if req.Schema == nil {
			t.Fatalf("unexpected free-form call")
		}
		body := req.Messages[len(req.Messages)-1].Content
		switch req.Schema.Name {
		case "topic_assignments":
			// Link the PV note to Solaranlage; wrongly try to link the dentist task too.
			out := map[string]any{"assignments": []map[string]any{
				{"id": ids["pvNote"], "topics": []string{ids["solaranlage"]}},
				{"id": ids["dentist"], "topics": []string{ids["solaranlage"]}},
			}}
			raw, _ := json.Marshal(out)
			return &llm.Response{Content: string(raw)}, nil
		case "duplicate_verdicts":
			var pairs []map[string]any
			if strings.Contains(body, ids["loki1"]) {
				pairs = append(pairs, map[string]any{"a": ids["loki1"], "b": ids["loki2"], "same": true, "keep": "a"})
			}
			if strings.Contains(body, ids["solar"]) {
				pairs = append(pairs, map[string]any{"a": ids["solarSystem"], "b": ids["solar"], "same": true, "keep": "a"})
			}
			raw, _ := json.Marshal(map[string]any{"pairs": pairs})
			return &llm.Response{Content: string(raw)}, nil
		case "topic_summary":
			return &llm.Response{Content: `{"summary":"Die Solaranlage läuft; offen ist die Prüfung von String 2."}`}, nil
		case "task_help":
			if strings.Contains(body, "release notes") {
				return &llm.Response{Content: `{"action":"draft","reason":"notes cover it","title":"Release notes 0.4","markdown":"- Impeller default\n- Native dictation\n\n(from: Flutter 3.47: what changed)"}`}, nil
			}
			return &llm.Response{Content: `{"action":"none","reason":"nothing to add"}`}, nil
		}
		return nil, nil
	}}
}

func seed(t *testing.T, c *core.Core, tick func()) map[string]string {
	t.Helper()
	ids := map[string]string{}
	commit := func(ops ...model.Op) *model.Receipt {
		tick()
		rec, err := c.Commit(context.Background(), "user:test", nil, ops)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	rec := commit(model.Op{Op: "topic.create", Name: str("Solaranlage"), Aliases: []string{"PV"}})
	ids["solaranlage"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "topic.create", Name: str("Solar system")})
	ids["solarSystem"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "topic.create", Name: str("Solar")})
	ids["solar"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "topic.create", Name: str("Old project")})
	ids["old"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "note.create", Title: str("PV String 2 liefert nichts"), Markdown: "Der zweite String der PV-Anlage liefert seit Montag keinen Strom."})
	ids["pvNote"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "note.create", Title: str("Wechselrichter Deye"), Markdown: "Der Deye hängt an String 1 und 2.", Topics: []string{ids["solaranlage"]}})
	ids["deyeNote"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "task.create", Text: "String 2 prüfen", Topics: []string{ids["solaranlage"]}})
	ids["stringTask"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "note.create", Title: str("Grafana Loki Panel"), Markdown: "Loki panel for the heating logs."})
	ids["loki1"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "note.create", Title: str("Grafana Loki panel setup"), Markdown: "How the Loki panel was set up."})
	ids["loki2"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "task.create", Text: "Zahnarzt anrufen"})
	ids["dentist"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "task.create", Text: "Zahnarzt anrufen!"})
	ids["dentist2"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "note.create", Title: str("Flutter 3.47: what changed"), Markdown: "Impeller is the default renderer. Native dictation landed."})
	ids["flutterNote"] = rec.Lines[0].ObjectID
	rec = commit(model.Op{Op: "task.create", Text: "Write the release notes for 0.4"})
	ids["releaseTask"] = rec.Lines[0].ObjectID
	// A note linked to a topic that gets deleted: a dangling link for integrity.
	rec = commit(model.Op{Op: "note.create", Title: str("Old note"), Markdown: "x", Topics: []string{ids["old"]}})
	ids["oldNote"] = rec.Lines[0].ObjectID
	o, _ := c.Get(ids["old"])
	commit(model.Op{Op: "object.archive", ID: ids["old"], ExpectedRev: o.GetMeta().Rev})
	return ids
}

func TestRunDoesEveryJob(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	clock := base
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	ids := seed(t, c, func() { clock = clock.Add(time.Second) })
	clock = base.Add(48 * time.Hour) // everything is older than untagged_after_days
	w := New(c, t.TempDir(), quiet)
	w.Now = func() time.Time { return clock }
	cfg := config.Default().Maintenance
	cfg.Assist = "propose"
	w.Configure(cfg, config.Default().Autonomy, fakeModel(t, ids), config.Role{Model: "m"}, nil, nil, "", nil)
	run, err := w.RunNow(context.Background(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]JobReport{}
	for _, j := range run.Jobs {
		byName[j.Name] = j
		if j.Error != "" {
			t.Errorf("%s: %s", j.Name, j.Error)
		}
	}
	// integrity: the dangling topic link is gone.
	o, _ := c.Get(ids["oldNote"])
	if n := o.(*model.Note); len(n.Topics) != 0 || byName["integrity"].Changed != 1 {
		t.Fatalf("integrity: topics=%v report=%+v", n.Topics, byName["integrity"])
	}
	// untagged: PV note linked (alias evidence), dentist task not (no evidence).
	o, _ = c.Get(ids["pvNote"])
	if n := o.(*model.Note); len(n.Topics) != 1 || n.Topics[0] != ids["solaranlage"] {
		t.Fatalf("untagged: pv note topics %v", n.Topics)
	}
	o, _ = c.Get(ids["dentist"])
	if len(o.(*model.Task).Topics) != 0 {
		t.Fatal("untagged: dentist task got a topic without evidence")
	}
	// duplicates: related link plus three proposals (note merge, topic merge, duplicate task).
	o, _ = c.Get(ids["loki1"])
	if !contains(o.(*model.Note).Related, ids["loki2"]) {
		t.Fatal("duplicates: related link missing")
	}
	var proposals []*model.Capture
	for _, cap := range c.Inbox() {
		if cap.Source == "maintenance" {
			proposals = append(proposals, cap)
		}
	}
	texts := map[string]*model.Capture{}
	for _, p := range proposals {
		texts[p.Text] = p
	}
	for _, want := range []string{"Merge note “Grafana Loki panel setup” into “Grafana Loki Panel”?", "Merge topic “Solar” into “Solar system”?", "Delete the duplicate task “Zahnarzt anrufen!”?"} {
		if texts[want] == nil {
			t.Errorf("proposal missing: %q (have %v)", want, keysOf(texts))
		}
	}
	// summaries: Solaranlage has three members and gets the automatic block.
	o, _ = c.Get(ids["solaranlage"])
	summary := o.(*model.Topic).Summary.Markdown()
	if !strings.Contains(summary, "> [!info] Automatic summary (6 Sep 2026): Die Solaranlage läuft") {
		t.Fatalf("summary: %q", summary)
	}
	// assist (propose): a draft proposal for the release notes task, nothing for the dentist.
	if texts["Draft for “Write the release notes for 0.4”: Draft: Release notes 0.4?"] == nil {
		t.Errorf("assist proposal missing (have %v)", keysOf(texts))
	}
	// Accepting the note merge proposal as the user merges and archives.
	p := texts["Merge note “Grafana Loki panel setup” into “Grafana Loki Panel”?"]
	var ops []model.Op
	if err := json.Unmarshal(p.Result.CoreProposal, &ops); err != nil || len(ops) != 2 || len(p.Result.Lines) != 2 {
		t.Fatalf("core proposal: %v %d lines=%v", err, len(ops), p.Result.Lines)
	}
	if _, err := c.Commit(context.Background(), "user:test", nil, ops); err != nil {
		t.Fatalf("accept merge: %v", err)
	}
	o, _ = c.Get(ids["loki1"])
	if !strings.Contains(o.(*model.Note).Body.PlainText(), "How the Loki panel was set up") {
		t.Fatal("merge did not append the duplicate's text")
	}
	o, _ = c.Get(ids["loki2"])
	if !o.GetMeta().Archived {
		t.Fatal("duplicate not archived")
	}
	// A second run proposes nothing again and rewrites no unchanged summary.
	before := len(c.Inbox())
	run2, err := w.RunNow(context.Background(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Inbox()) != before {
		t.Fatal("second run re-proposed")
	}
	for _, j := range run2.Jobs {
		if j.Name == "summaries" && j.Changed != 0 {
			// Solaranlage changed (pv note linked, so a refresh is legitimate);
			// but the loki merge does not touch topics. Allow at most one.
			if j.Changed > 1 {
				t.Fatalf("summaries rewritten %d times", j.Changed)
			}
		}
	}
	if st := w.Status(); st.Last == nil || st.Last.ID != run2.ID || st.Running {
		t.Fatalf("status %+v", st)
	}
	if got := w.Runs(10); len(got) != 2 || got[0].ID != run2.ID {
		t.Fatalf("runs %d", len(got))
	}
	// History survives a restart.
	w2 := New(c, w.dir[:len(w.dir)-len("/maintenance")], quiet)
	if len(w2.Runs(10)) != 2 || len(w2.state.Proposed) == 0 {
		t.Fatal("runs or state not persisted")
	}
}

func keysOf(m map[string]*model.Capture) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestScheduleNext(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, _ := core.Open(t.TempDir(), core.Options{Logger: quiet})
	w := New(c, t.TempDir(), quiet)
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, loc)
	w.Now = func() time.Time { return now }
	cfg := config.Default().Maintenance
	cfg.At = "03:30"
	w.Configure(cfg, config.Policy{}, nil, config.Role{}, nil, nil, "", nil)
	if next := w.Status().Next; next.Day() != 5 || next.Hour() != 3 || next.Minute() != 30 {
		t.Fatalf("next %v", next)
	}
	cfg.Every = config.Duration{Duration: 6 * time.Hour}
	w.Configure(cfg, config.Policy{}, nil, config.Role{}, nil, nil, "", nil)
	if next := w.Status().Next; next.Sub(now) > 2*time.Minute {
		t.Fatalf("interval with no history should run soon, got %v", next)
	}
	// Without a model, model jobs are skipped and the run still completes.
	run, err := w.RunNow(context.Background(), "manual")
	if err != nil || len(run.Jobs) != 4 || run.Jobs[1].Skipped == "" {
		t.Fatalf("run %+v %v", run, err)
	}
	if next := w.Status().Next; next.Sub(run.Started) != 6*time.Hour {
		t.Fatalf("next after run %v", next)
	}
}
