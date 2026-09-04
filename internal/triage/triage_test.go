package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func newCore(t *testing.T) *core.Core {
	t.Helper()
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func capture(t *testing.T, c *core.Core, text string) string {
	t.Helper()
	ops := []model.Op{{Op: "capture.create", Text: text, Source: "test"}}
	if _, err := c.Commit(context.Background(), "user:test", &model.Cause{Kind: "user"}, ops); err != nil {
		t.Fatal(err)
	}
	return ops[0].ID
}

func scripted(responses ...string) *llm.Fake {
	i := 0
	return &llm.Fake{Fn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
		if i >= len(responses) {
			return nil, errors.New("no more scripted responses")
		}
		r := responses[i]
		i++
		return &llm.Response{Content: r, Model: "scripted"}, nil
	}}
}

func triager(c *core.Core, p llm.Provider) *Triager {
	cfg := config.Default()
	return New(c, p, cfg.Triage, cfg.Autonomy, quiet)
}

func result(res Result) string {
	b, _ := json.Marshal(res)
	return string(b)
}

func TestProcessCreatesObjectsAndTopics(t *testing.T) {
	c := newCore(t)
	capID := capture(t, c, "Ich muss beim Deye noch prüfen, warum der zweite String manchmal keinen Strom liefert.")
	p := scripted(result(Result{Classification: "task", Confidence: 0.9, Summary: "Aufgabe angelegt.", Operations: []Operation{
		{Op: "task.create", Text: "Deye: zweiten PV-String prüfen", Topics: []string{"Deye"}},
		{Op: "note.create", Kind: "note", Title: "Deye String 2", Markdown: "String 2 liefert manchmal keinen Strom.", Topics: []string{"deye", "Solaranlage"}},
	}}))
	rec, err := triager(c, p).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Summary, `Created task “Deye: zweiten PV-String prüfen” in Deye.`) {
		t.Fatalf("summary %q", rec.Summary)
	}
	topics := c.Topics(false)
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics (Deye, Solaranlage), got %d", len(topics))
	}
	obj, _ := c.Get(capID)
	cap := obj.(*model.Capture)
	if cap.Status != model.CaptureProcessed || cap.Result == nil || cap.Result.Classification != "task" {
		t.Fatalf("capture %+v", cap)
	}
	// Provenance.
	for _, n := range c.Notes("", false) {
		if len(n.Origins) != 1 || n.Origins[0] != capID || n.Body.Blocks[0].Sources[0] != capID {
			t.Fatalf("note provenance %+v", n.Note)
		}
	}
	// The prompt must contain the capture wrapped in tags and the context.
	req := p.Calls[0]
	if !strings.Contains(req.Messages[0].Content, "<capture id=\""+capID+"\"") || req.Schema == nil || req.Schema.Name != SchemaName {
		t.Fatalf("request shape wrong: %+v", req)
	}
}

func TestInvalidOutputIsCorrectedOnce(t *testing.T) {
	c := newCore(t)
	capID := capture(t, c, "Grafana kann Loki-Logs als Panel anzeigen.")
	p := scripted("I think this is a note about Grafana!", result(Result{Classification: "note", Confidence: 0.8, Summary: "Notiz.", Operations: []Operation{
		{Op: "note.create", Title: "Grafana Loki Panel", Markdown: "Grafana kann Loki-Logs als Panel anzeigen."}}}))
	rec, err := triager(c, p).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 2 || len(p.Calls[1].Messages) != 3 || !strings.Contains(p.Calls[1].Messages[2].Content, "invalid") {
		t.Fatalf("corrective round trip missing: %d calls", len(p.Calls))
	}
	if !strings.HasPrefix(rec.Summary, `Created note “Grafana Loki Panel”.`) {
		t.Fatalf("summary %q", rec.Summary)
	}
}

func TestReferencesToMissingObjectsWriteNothing(t *testing.T) {
	c := newCore(t)
	capID := capture(t, c, "Nachtrag zu Grafana")
	p := scripted(
		result(Result{Classification: "info", Confidence: 0.9, Summary: "x", Operations: []Operation{{Op: "note.append", NoteID: "note_01DOESNOTEXIST", Markdown: "…"}}}),
		result(Result{Classification: "info", Confidence: 0.9, Summary: "x", Operations: []Operation{{Op: "note.append", NoteID: "note_01DOESNOTEXIST", Markdown: "…"}}}),
	)
	before := c.Stats()
	_, err := triager(c, p).Process(context.Background(), capID)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	after := c.Stats()
	if after.Notes != before.Notes || after.Topics != before.Topics {
		t.Fatal("objects were written despite invalid plan")
	}
	obj, _ := c.Get(capID)
	if cap := obj.(*model.Capture); cap.Status != model.CaptureFailed || cap.Result.Error == "" {
		t.Fatalf("capture should be failed with error: %+v", cap)
	}
	if len(c.Inbox()) != 1 {
		t.Fatal("failed capture must stay in the inbox")
	}
}

func TestUnclearAndLowConfidenceArePark(t *testing.T) {
	c := newCore(t)
	id1 := capture(t, c, "hm")
	id2 := capture(t, c, "vielleicht morgen")
	p := scripted(
		result(Result{Classification: "unclear", Confidence: 0.3, Summary: "Unklar.", Question: "Was meinst du mit hm?", Operations: []Operation{{Op: "note.create", Title: "should be dropped"}}}),
		result(Result{Classification: "task", Confidence: 0.4, Summary: "Vage.", Operations: []Operation{{Op: "task.create", Text: "morgen?"}}}),
	)
	tr := triager(c, p)
	if rec, err := tr.Process(context.Background(), id1); err != nil || rec != nil {
		t.Fatalf("unclear: rec=%v err=%v", rec, err)
	}
	if rec, err := tr.Process(context.Background(), id2); err != nil || rec != nil {
		t.Fatalf("low confidence: rec=%v err=%v", rec, err)
	}
	if st := c.Stats(); st.Notes+st.Ideas+st.OpenTasks != 0 {
		t.Fatalf("nothing should be written: %+v", st)
	}
	obj, _ := c.Get(id1)
	r1 := obj.(*model.Capture).Result
	if obj.(*model.Capture).Status != model.CaptureNeedsReview || r1.Reason != "unclear" || r1.Question != "Was meinst du mit hm?" {
		t.Fatalf("unclear park: %+v", r1)
	}
	obj, _ = c.Get(id2)
	r2 := obj.(*model.Capture).Result
	if obj.(*model.Capture).Status != model.CaptureNeedsReview || r2.Reason != "low_confidence" || r2.Question != "" || r2.Proposal == nil {
		t.Fatalf("low confidence park: %+v", r2)
	}
}

func TestAppendCompleteAndMentionUseExistingObjects(t *testing.T) {
	c := newCore(t)
	// Seed a topic, a note and a task.
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "topic.create", Name: str("Solaranlage"), Aliases: []string{"PV"}}})
	topicID := rec.Lines[0].ObjectID
	rec, _ = c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "note.create", Title: str("Deye"), Markdown: "Erster Absatz.", Topics: []string{topicID}},
		{Op: "task.create", Text: "String 2 prüfen", Topics: []string{topicID}},
	})
	noteID, taskID := rec.Lines[0].ObjectID, rec.Lines[1].ObjectID
	capID := capture(t, c, "Der String liefert wieder, war Schnee. PV läuft.")
	p := scripted(result(Result{Classification: "info", Confidence: 0.95, Summary: "Ergänzt.", Operations: []Operation{
		{Op: "note.append", NoteID: noteID, Markdown: "Schnee war die Ursache.", Topics: []string{"pv"}},
		{Op: "task.complete", TaskID: taskID},
	}}))
	out, err := triager(c, p).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Summary, `Added to note “Deye”.`) || !strings.Contains(out.Summary, `Completed task “String 2 prüfen”.`) {
		t.Fatalf("summary %q", out.Summary)
	}
	if len(c.Topics(false)) != 1 {
		t.Fatal("alias 'pv' must resolve to the existing topic, not create one")
	}
	obj, _ := c.Get(noteID)
	n := obj.(*model.Note)
	if len(n.Body.Blocks) != 2 || n.Body.Blocks[1].Sources[0] != capID || n.Origins[0] != capID {
		t.Fatalf("append provenance: %+v", n)
	}
	// The context offered to the model listed the existing task.
	if !strings.Contains(p.Calls[0].Messages[0].Content, taskID) {
		t.Fatal("open task missing from context")
	}
}

func TestProviderFailureMarksFailedAndRetries(t *testing.T) {
	c := newCore(t)
	capID := capture(t, c, "x")
	calls := 0
	p := &llm.Fake{Fn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
		calls++
		return nil, &llm.Error{Provider: "fake", Status: 503, Message: "down", Retryable: true}
	}}
	cfg := config.Default()
	cfg.Triage.MaxAttempts = 2
	tr := New(c, p, cfg.Triage, cfg.Autonomy, quiet)
	start := time.Now()
	_, err := tr.Process(context.Background(), capID)
	if err == nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if time.Since(start) < 2*time.Second {
		t.Fatal("expected backoff between attempts")
	}
	obj, _ := c.Get(capID)
	if cap := obj.(*model.Capture); cap.Status != model.CaptureFailed || cap.Attempts != 1 {
		t.Fatalf("capture %+v", cap)
	}
}

func TestHeuristicEndToEnd(t *testing.T) {
	c := newCore(t)
	tr := triager(c, NewHeuristic("fake"))
	cases := map[string]string{
		"Ich muss die Steuererklärung abschicken.": "task",
		"Vielleicht wäre ein E-Ink Display cool.":  "idea",
		"Warum ist der Himmel blau?":               "question",
		"Grafana 11 kann Loki-Panels.":             "note",
	}
	for text, want := range cases {
		id := capture(t, c, text)
		if _, err := tr.Process(context.Background(), id); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		obj, _ := c.Get(id)
		if got := obj.(*model.Capture).Result.Classification; got != want {
			t.Errorf("%q: classification %q, want %q", text, got, want)
		}
	}
	if st := c.Stats(); st.OpenTasks != 1 || st.Ideas != 1 || st.Notes != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestWorkerProcessesPendingAndResetsStale(t *testing.T) {
	c := newCore(t)
	stale := capture(t, c, "stale one")
	if _, err := c.Commit(context.Background(), "system", nil, []model.Op{{Op: "capture.set_status", ID: stale, Status: "processing"}}); err != nil {
		t.Fatal(err)
	}
	fresh := capture(t, c, "Ich muss X erledigen")
	w := NewWorker(c, triager(c, NewHeuristic("fake")), quiet)
	w.Poll = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := c.Get(stale)
		b, _ := c.Get(fresh)
		if a.(*model.Capture).Status == model.CaptureProcessed && b.(*model.Capture).Status == model.CaptureProcessed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// New capture while running: the worker must wake via the event.
	late := capture(t, c, "noch was")
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		o, _ := c.Get(late)
		if o.(*model.Capture).Status == model.CaptureProcessed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	for _, id := range []string{stale, fresh, late} {
		o, _ := c.Get(id)
		if o.(*model.Capture).Status != model.CaptureProcessed {
			t.Fatalf("%s not processed: %s", id, o.(*model.Capture).Status)
		}
	}
	if len(c.Inbox()) != 0 {
		t.Fatalf("inbox not empty: %d", len(c.Inbox()))
	}
	_ = fmt.Sprint
}

func TestParseResultRejectsBadShapes(t *testing.T) {
	bad := []string{
		``,
		`{"classification":"weird","confidence":0.5,"summary":"x","operations":[]}`,
		`{"classification":"task","confidence":1.5,"summary":"x","operations":[]}`,
		`{"classification":"task","confidence":0.9,"summary":"","operations":[]}`,
		`{"classification":"unclear","confidence":0.2,"summary":"x","operations":[]}`,
		`{"classification":"task","confidence":0.9,"summary":"x","operations":[{"op":"task.create"}]}`,
		`{"classification":"task","confidence":0.9,"summary":"x","operations":[{"op":"task.create","text":"a","due":"tomorrow"}]}`,
		`{"classification":"task","confidence":0.9,"summary":"x","operations":[{"op":"note.delete","note_id":"x"}]}`,
		`{"classification":"task","confidence":0.9,"summary":"x","operations":[],"extra":1}`,
	}
	for _, b := range bad {
		if _, err := parseResult(b); err == nil {
			t.Errorf("accepted %q", b)
		}
	}
	good := "```json\n" + `{"classification":"task","confidence":0.9,"summary":"x","question":null,"operations":[{"op":"task.create","text":"a","due":"2026-09-10"}]}` + "\n```"
	if _, err := parseResult(good); err != nil {
		t.Fatalf("rejected fenced JSON: %v", err)
	}
}

func TestLinkAttachesShownObjectsToTopics(t *testing.T) {
	c := newCore(t)
	// Two earlier captures produced a note and a task without any topic.
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "note.create", Title: str("Fundus zum Einhorn machen"), Markdown: "Backlinks kassieren."},
		{Op: "task.create", Text: "Fundus auf Product Hunt veröffentlichen"},
	})
	noteID, taskID := rec.Lines[0].ObjectID, rec.Lines[1].ObjectID
	capID := capture(t, c, "Fundus braucht eine Landingpage.")
	good := result(Result{Classification: "note", Confidence: 0.9, Summary: "Abgelegt.", Operations: []Operation{
		{Op: "note.create", Kind: "note", Title: "Fundus Landingpage", Markdown: "Braucht eine Landingpage.", Topics: []string{"Fundus"}},
		{Op: "link", NoteID: noteID, Topics: []string{"Fundus"}},
		{Op: "link", TaskID: taskID, Topics: []string{"Fundus"}},
	}})
	out, err := triager(c, scripted(good, good)).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	topics := c.Topics(false)
	if len(topics) != 1 {
		t.Fatalf("topics %d", len(topics))
	}
	for _, id := range []string{noteID, taskID} {
		obj, _ := c.Get(id)
		var got []string
		switch o := obj.(type) {
		case *model.Note:
			got = o.Topics
		case *model.Task:
			got = o.Topics
		}
		if len(got) != 1 || got[0] != topics[0].Topic.ID {
			t.Fatalf("%s topics %v, want %s", id, got, topics[0].Topic.ID)
		}
	}
	for _, want := range []string{`Linked note “Fundus zum Einhorn machen” to Fundus.`, `Linked task “Fundus auf Product Hunt veröffentlichen” to Fundus.`} {
		if !strings.Contains(out.Summary, want) {
			t.Fatalf("summary %q lacks %q", out.Summary, want)
		}
	}

	// A link to an object the model was never shown is refused and writes nothing.
	rec, _ = c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "task.create", Text: "Steuer machen"}})
	hidden := rec.Lines[0].ObjectID
	capID = capture(t, c, "Fundus Logo überarbeiten.")
	bad := result(Result{Classification: "note", Confidence: 0.9, Summary: "x", Operations: []Operation{{Op: "link", TaskID: hidden, Topics: []string{"Fundus"}}}})
	before := c.Stats()
	if _, err := triager(c, scripted(bad, bad)).Process(context.Background(), capID); err == nil {
		t.Fatal("expected the hidden id to be refused")
	}
	if after := c.Stats(); after.Notes != before.Notes || after.Topics != before.Topics {
		t.Fatalf("stats changed: %+v -> %+v", before, after)
	}
	if obj, _ := c.Get(hidden); len(obj.(*model.Task).Topics) != 0 {
		t.Fatal("hidden task was linked")
	}
}

func TestObjectIDsInTopicsAreRefused(t *testing.T) {
	c := newCore(t)
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "note.create", Title: str("Fundus"), Markdown: "x"}})
	noteID := rec.Lines[0].ObjectID
	capID := capture(t, c, "Fundus Landingpage bauen.")
	// A live run once put a note id into "topics", which became a topic named after the id.
	bad := result(Result{Classification: "task", Confidence: 0.9, Summary: "x", Operations: []Operation{{Op: "task.create", Text: "Landingpage bauen", Topics: []string{noteID}}}})
	if _, err := triager(c, scripted(bad, bad)).Process(context.Background(), capID); err == nil {
		t.Fatal("expected a validation error")
	}
	if n := len(c.Topics(false)); n != 0 {
		t.Fatalf("%d topics created from an object id", n)
	}
}

func TestNewTopicCatchesUpShownObjects(t *testing.T) {
	c := newCore(t)
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "note.create", Title: str("Fundus zum Einhorn machen"), Markdown: "Backlinks kassieren."},
		{Op: "task.create", Text: "Fundus auf Product Hunt veröffentlichen"},
		{Op: "note.create", Title: str("Einkaufsliste"), Markdown: "Fundus ist hier nur im Text, nicht im Titel."},
	})
	noteID, taskID, otherID := rec.Lines[0].ObjectID, rec.Lines[1].ObjectID, rec.Lines[2].ObjectID
	capID := capture(t, c, "Fundus braucht eine Landingpage.")
	// The model creates the topic and a note that names it, but links nothing.
	res := result(Result{Classification: "note", Confidence: 0.9, Summary: "Abgelegt.", Operations: []Operation{
		{Op: "topic.create", Name: "Fundus", Kind: "project"},
		{Op: "note.create", Kind: "note", Title: "Fundus Landingpage", Markdown: "Screenshots und Download-Button."},
	}})
	out, err := triager(c, scripted(res, res)).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	topics := c.Topics(false)
	if len(topics) != 1 {
		t.Fatalf("topics %d", len(topics))
	}
	want := topics[0].Topic.ID
	linked := func(id string) bool {
		obj, _ := c.Get(id)
		var ts []string
		switch o := obj.(type) {
		case *model.Note:
			ts = o.Topics
		case *model.Task:
			ts = o.Topics
		}
		for _, x := range ts {
			if x == want {
				return true
			}
		}
		return false
	}
	if !linked(noteID) || !linked(taskID) {
		t.Fatalf("earlier objects not caught up: %s", out.Summary)
	}
	if linked(otherID) {
		t.Fatalf("note that only mentions the name in its body must not be linked: %s", out.Summary)
	}
	var created string
	for _, n := range c.Notes("", false) {
		if n.NoteTitle == "Fundus Landingpage" {
			created = n.ID
		}
	}
	if created == "" || !linked(created) {
		t.Fatalf("note created alongside the topic not linked: %s", out.Summary)
	}
	if !strings.Contains(out.Summary, "Linked note “Fundus zum Einhorn machen” to Fundus.") {
		t.Fatalf("receipt: %s", out.Summary)
	}
}

func TestMentionsName(t *testing.T) {
	for text, name := range map[string]string{"Fundus zum Einhorn machen": "Fundus", "Das FUNDUS-Logo": "fundus", "Solar system check": "Solar system"} {
		if !mentionsName(text, name) {
			t.Errorf("%q should mention %q", text, name)
		}
	}
	for text, name := range map[string]string{"Fundusaufnahme": "Fundus", "PV läuft": "PV", "nothing here": "Fundus"} {
		if mentionsName(text, name) {
			t.Errorf("%q should not mention %q", text, name)
		}
	}
}

func TestUnrelatedExistingTopicIsDropped(t *testing.T) {
	c := newCore(t)
	rec, _ := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "topic.create", Name: str("RPG mit Godot Engine"), Kind: "project", Aliases: []string{"godot"}},
		{Op: "topic.create", Name: str("Solaranlage"), Aliases: []string{"PV"}},
		{Op: "topic.create", Name: str("Heizung")},
	})
	rpg, solar, heizung := rec.Lines[0].ObjectID, rec.Lines[1].ObjectID, rec.Lines[2].ObjectID
	cases := []struct {
		text, title string
		topic       string
		keep        bool
	}{
		{"Die UI Updates klappen noch nicht richtig.", "UI-Updates", rpg, false},
		{"Godot Shader für das Inventar bauen.", "Inventar-Shader", rpg, true},
		{"PV läuft wieder, war Schnee.", "PV wieder da", solar, true},
		{"Heizungsdaten mit Grafana visualisieren.", "Grafana Heizung", heizung, true},
		{"Zahnarzt anrufen.", "Zahnarzt", solar, false},
	}
	for _, tc := range cases {
		capID := capture(t, c, tc.text)
		res := result(Result{Classification: "note", Confidence: 0.9, Summary: "x", Operations: []Operation{
			{Op: "note.create", Kind: "note", Title: tc.title, Markdown: tc.text, Topics: []string{tc.topic}},
		}})
		out, err := triager(c, scripted(res, res)).Process(context.Background(), capID)
		if err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}
		linked := strings.Contains(out.Summary, " in ")
		if linked != tc.keep {
			t.Errorf("%q: linked=%v want %v (%s)", tc.text, linked, tc.keep, out.Summary)
		}
	}
	// A link op to an unrelated topic is dropped without failing the plan.
	rec, _ = c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "task.create", Text: "Steuer machen"}})
	taskID := rec.Lines[0].ObjectID
	capID := capture(t, c, "Steuer machen nicht vergessen.")
	res := result(Result{Classification: "task", Confidence: 0.9, Summary: "x", Operations: []Operation{
		{Op: "task.mention", TaskID: taskID},
		{Op: "link", TaskID: taskID, Topics: []string{rpg}},
	}})
	if _, err := triager(c, scripted(res, res)).Process(context.Background(), capID); err != nil {
		t.Fatal(err)
	}
	obj, _ := c.Get(taskID)
	if len(obj.(*model.Task).Topics) != 0 {
		t.Fatal("unrelated link applied")
	}
}

func TestResearchClassificationMakesResearchTask(t *testing.T) {
	c := newCore(t)
	capID := capture(t, c, "Finde heraus, welche E-Ink-Displays am Raspberry Pi Zero laufen.")
	res := result(Result{Classification: "research", Confidence: 0.9, Summary: "Recherche angelegt.", Operations: []Operation{
		{Op: "task.create", Text: "Welche E-Ink-Displays laufen am Raspberry Pi Zero?"},
	}})
	out, err := triager(c, scripted(res, res)).Process(context.Background(), capID)
	if err != nil {
		t.Fatal(err)
	}
	tasks := c.Tasks([]model.TaskState{model.TaskOpen}, false)
	if len(tasks) != 1 || tasks[0].Kind != model.TaskKindResearch || strings.HasPrefix(tasks[0].Text, "Research:") {
		t.Fatalf("task %+v", tasks[0].Task)
	}
	if !strings.Contains(out.Summary, "Created research task “Welche E-Ink-Displays laufen am Raspberry Pi Zero?”") {
		t.Fatalf("receipt: %s", out.Summary)
	}
}
