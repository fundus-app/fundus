package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/model"
)

func str(s string) *string { return &s }

func openTest(t *testing.T, dir string) *Core {
	t.Helper()
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	n := 0
	c, err := Open(dir, Options{Now: func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return c
}

// newCapture commits a capture and returns its id (capture.create receipts
// are quiet, so the id comes from the op).
func newCapture(t *testing.T, c *Core, text string) string {
	t.Helper()
	ops := []model.Op{{Op: "capture.create", Text: text, Source: "test"}}
	if _, err := c.Commit(context.Background(), "user:test", &model.Cause{Kind: "user"}, ops); err != nil {
		t.Fatalf("capture: %v", err)
	}
	return ops[0].ID
}

func mustCommit(t *testing.T, c *Core, actor string, ops ...model.Op) *model.Receipt {
	t.Helper()
	rec, err := c.Commit(context.Background(), actor, &model.Cause{Kind: "user"}, ops)
	if err != nil {
		t.Fatalf("commit %v: %v", ops[0].Op, err)
	}
	return rec
}

func seed(t *testing.T, c *Core) (topicID, noteID, taskID, capID string) {
	t.Helper()
	rec := mustCommit(t, c, "user:test", model.Op{Op: "topic.create", Name: str("Solaranlage"), Aliases: []string{"PV", "Deye"}})
	topicID = rec.Lines[0].ObjectID
	capID = newCapture(t, c, "Deye String 2 prüfen")
	rec = mustCommit(t, c, "llm:triage/fake/x",
		model.Op{Op: "note.create", Kind: "note", Title: str("Deye Wechselrichter"), Markdown: "# Fakten\n\nString 2 fällt manchmal aus.\n\n- Modul A\n- Modul B", Topics: []string{topicID}, Origins: []string{capID}},
		model.Op{Op: "task.create", Text: "Deye: zweiten PV-String prüfen", Topics: []string{topicID}, Origins: []string{capID}},
		model.Op{Op: "capture.set_status", ID: capID, Status: "processed"},
	)
	for _, l := range rec.Lines {
		switch l.ObjectType {
		case model.TypeNote:
			noteID = l.ObjectID
		case model.TypeTask:
			taskID = l.ObjectID
		}
	}
	return
}

func TestCommitReplayDeterministic(t *testing.T) {
	dir := t.TempDir()
	c := openTest(t, dir)
	topicID, noteID, taskID, _ := seed(t, c)
	mustCommit(t, c, "user:test", model.Op{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "append", Markdown: "Nachtrag: Schnee war schuld."}}})
	mustCommit(t, c, "user:test", model.Op{Op: "task.update", ID: taskID, State: "done"})
	before := snapshotObjects(t, c)
	seq := c.Seq()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen from snapshot.
	c2 := openTest(t, dir)
	if c2.Seq() != seq {
		t.Fatalf("seq after reopen %d, want %d", c2.Seq(), seq)
	}
	after := snapshotObjects(t, c2)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state differs after snapshot reopen\nbefore=%s\nafter=%s", before, after)
	}
	c2.Close()

	// Remove the snapshot and replay the log from scratch; block ids must match.
	if err := removeSnapshot(dir); err != nil {
		t.Fatal(err)
	}
	c3 := openTest(t, dir)
	defer c3.Close()
	replayed := snapshotObjects(t, c3)
	if !reflect.DeepEqual(before, replayed) {
		t.Fatalf("state differs after full replay\nbefore=%s\nafter=%s", before, replayed)
	}
	n, _ := c3.Get(noteID)
	note := n.(*model.Note)
	if len(note.Body.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(note.Body.Blocks))
	}
	if !strings.HasPrefix(note.Body.Blocks[0].ID, "b_") {
		t.Fatalf("block id %q", note.Body.Blocks[0].ID)
	}
	if note.Body.Blocks[0].Sources[0] == "" {
		t.Fatal("block provenance missing")
	}
	if tp, ok := c3.FindTopic("pv"); !ok || tp.ID != topicID {
		t.Fatalf("alias lookup failed after replay")
	}
}

func snapshotObjects(t *testing.T, c *Core) map[string]string {
	t.Helper()
	out := map[string]string{}
	c.Each(nil, func(o model.Object) bool {
		raw, _ := json.Marshal(o)
		out[o.GetMeta().ID] = string(raw)
		return true
	})
	return out
}

func TestExpectedRevConflict(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	_, _, taskID, _ := seed(t, c)
	_, err := c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "task.update", ID: taskID, ExpectedRev: 5, State: "done"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	obj, _ := c.Get(taskID)
	if obj.(*model.Task).State != model.TaskOpen {
		t.Fatal("task must be unchanged after a failed commit")
	}
	mustCommit(t, c, "user:test", model.Op{Op: "task.update", ID: taskID, ExpectedRev: 1, State: "done"})
	obj, _ = c.Get(taskID)
	if obj.(*model.Task).State != model.TaskDone || obj.GetMeta().Rev != 2 {
		t.Fatalf("task not updated: %+v", obj)
	}
}

func TestAtomicity(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	seq := c.Seq()
	_, err := c.Commit(context.Background(), "user:test", nil, []model.Op{
		{Op: "topic.create", Name: str("Alpha")},
		{Op: "note.create", Title: str("x"), Markdown: "y", Topics: []string{"topic_missing"}},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if c.Seq() != seq {
		t.Fatal("failed commit must not advance seq")
	}
	if _, ok := c.FindTopic("Alpha"); ok {
		t.Fatal("topic from failed transaction leaked into state")
	}
}

func TestUndoRestoresAndConflicts(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	_, noteID, _, _ := seed(t, c)
	rec := mustCommit(t, c, "user:test", model.Op{Op: "note.update", ID: noteID, Title: str("Neuer Titel")})
	obj, _ := c.Get(noteID)
	if obj.Title() != "Neuer Titel" {
		t.Fatal("rename failed")
	}
	undo, err := c.Undo(context.Background(), "user:test", rec.TxnID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(undo.Summary, "Undid:") {
		t.Fatalf("summary %q", undo.Summary)
	}
	obj, _ = c.Get(noteID)
	if obj.Title() != "Deye Wechselrichter" || obj.GetMeta().Rev != 3 {
		t.Fatalf("undo did not restore: title=%q rev=%d", obj.Title(), obj.GetMeta().Rev)
	}
	if _, err := c.Undo(context.Background(), "user:test", rec.TxnID, false); !errors.Is(err, ErrUndone) {
		t.Fatalf("second undo: %v", err)
	}
	changes := c.Changes(ChangesQuery{Limit: 10})
	var found bool
	for _, r := range changes {
		if r.TxnID == rec.TxnID && r.UndoneBy == undo.TxnID && !r.Undoable {
			found = true
		}
	}
	if !found {
		t.Fatal("changes view does not mark the undone transaction")
	}

	// Conflict: modify after, then undo the earlier change.
	rec2 := mustCommit(t, c, "user:test", model.Op{Op: "note.update", ID: noteID, Title: str("A")})
	mustCommit(t, c, "user:test", model.Op{Op: "note.update", ID: noteID, Title: str("B")})
	_, err = c.Undo(context.Background(), "user:test", rec2.TxnID, false)
	var uc *UndoConflict
	if !errors.As(err, &uc) || len(uc.Objects) != 1 {
		t.Fatalf("want UndoConflict, got %v", err)
	}
	if _, err := c.Undo(context.Background(), "user:test", rec2.TxnID, true); err != nil {
		t.Fatalf("forced undo: %v", err)
	}
	obj, _ = c.Get(noteID)
	if obj.Title() != "Deye Wechselrichter" {
		t.Fatalf("forced undo restored %q", obj.Title())
	}
}

func TestUndoOfCreateRemovesAndParksCapture(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	capID := newCapture(t, c, "Idee: E-Ink Fernbedienung")
	mustCommit(t, c, "system", model.Op{Op: "capture.set_status", ID: capID, Status: "processing"})
	rec := mustCommit(t, c, "llm:triage/fake/x",
		model.Op{Op: "note.create", Kind: "idea", Title: str("E-Ink Fernbedienung"), Markdown: "…", Origins: []string{capID}},
		model.Op{Op: "capture.set_status", ID: capID, Status: "processed", Result: &model.CaptureResult{Summary: "Saved."}})
	noteID := rec.Lines[0].ObjectID
	if _, err := c.Undo(context.Background(), "user:test", rec.TxnID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(noteID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("note should be removed, got %v", err)
	}
	obj, _ := c.Get(capID)
	cap := obj.(*model.Capture)
	if cap.Status != model.CaptureNeedsReview {
		t.Fatalf("capture status %q, want needs_review (undo must not re-trigger processing)", cap.Status)
	}
	if len(c.Inbox()) != 1 {
		t.Fatal("capture should be back in the inbox")
	}
	if len(c.Search("Fernbedienung", 5, nil, false)) != 0 {
		t.Fatal("removed note still in index")
	}
}

func TestPinnedBlocks(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	_, noteID, _, _ := seed(t, c)
	obj, _ := c.Get(noteID)
	blockID := obj.(*model.Note).Body.Blocks[1].ID
	mustCommit(t, c, "user:test", model.Op{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "pin", BlockID: blockID}}})
	_, err := c.Commit(context.Background(), "llm:triage/fake/x", nil, []model.Op{{Op: "note.revise", ID: noteID,
		Edits: []doc.Edit{{Action: "replace", BlockID: blockID, Markdown: "rewritten"}}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("model must not edit pinned block: %v", err)
	}
	mustCommit(t, c, "user:test", model.Op{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "replace", BlockID: blockID, Markdown: "user rewrite"}}})
	obj, _ = c.Get(noteID)
	if obj.(*model.Note).Body.Blocks[1].Text != "user rewrite" {
		t.Fatal("user edit not applied")
	}
}

func TestTopicUniquenessAndMentions(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	seed(t, c)
	if _, err := c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "topic.create", Name: str("solaranlage")}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate topic accepted: %v", err)
	}
	if _, err := c.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "topic.create", Name: str("deye")}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("alias duplicate accepted: %v", err)
	}
	found := c.TopicsMentionedIn("Der Deye macht heute wieder Probleme mit der PV")
	if len(found) != 1 || found[0].Name != "Solaranlage" {
		t.Fatalf("mentions: %+v", found)
	}
}

func TestReceiptsAndViews(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	_, _, taskID, capID := seed(t, c)
	recs := c.ReceiptsForCause("capture", "")
	_ = recs
	rec := mustCommit(t, c, "user:test", model.Op{Op: "task.update", ID: taskID, State: "waiting", WaitingOn: str("Ersatzteil")})
	if rec.Summary != `Task “Deye: zweiten PV-String prüfen” is now waiting on Ersatzteil.` {
		t.Fatalf("summary %q", rec.Summary)
	}
	if len(c.Tasks([]model.TaskState{model.TaskWaiting}, false)) != 1 {
		t.Fatal("waiting view")
	}
	if len(c.Relevant(10)) != 0 {
		t.Fatal("waiting tasks must not be relevant")
	}
	obj, _ := c.Get(capID)
	if obj.(*model.Capture).Status != model.CaptureProcessed {
		t.Fatal("capture status")
	}
	topics := c.Topics(false)
	if len(topics) != 1 || topics[0].NoteCount != 1 || topics[0].OpenTaskCount != 0 {
		t.Fatalf("topic counts %+v", topics[0])
	}
	page, err := c.Topic(topics[0].ID)
	if err != nil || len(page.Notes) != 1 || len(page.Tasks) != 1 {
		t.Fatalf("topic page %+v %v", page, err)
	}
	stats := c.Stats()
	if stats.Notes != 1 || stats.Captures != 1 || stats.Topics != 1 {
		t.Fatalf("stats %+v", stats)
	}
	ex := c.ExportJSON()
	if len(ex.Objects) != 4 {
		t.Fatalf("export objects %d", len(ex.Objects))
	}
}

func TestAttentionScore(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	mk := func(due string, imp int) *model.Task {
		return &model.Task{Meta: model.Meta{CreatedAt: now.Add(-30 * 24 * time.Hour)}, Due: due, Importance: imp}
	}
	overdue, _ := Score(mk("2026-09-01", 0), now, nil)
	today, _ := Score(mk("2026-09-03", 0), now, nil)
	week, _ := Score(mk("2026-09-08", 0), now, nil)
	none, _ := Score(mk("", 0), now, nil)
	important, reasons := Score(mk("", 3), now, nil)
	if !(overdue > today && today > week && week > none) {
		t.Fatalf("ordering: %v %v %v %v", overdue, today, week, none)
	}
	if important <= none || len(reasons) == 0 {
		t.Fatal("importance ignored")
	}
}

func TestEvents(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	ch, cancel := c.Subscribe()
	defer cancel()
	mustCommit(t, c, "user:test", model.Op{Op: "capture.create", Text: "hi", Source: "test"})
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got[ev.Type] = true
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for events")
		}
	}
	if !got["txn.committed"] || !got["capture.changed"] {
		t.Fatalf("events %v", got)
	}
}

func TestModelActorsAreAdditiveOnly(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	_, noteID, taskID, _ := seed(t, c)
	// User sets a due date and writes a block of their own.
	mustCommit(t, c, "user:test", model.Op{Op: "task.update", ID: taskID, Due: str("2026-09-10")})
	mustCommit(t, c, "user:test", model.Op{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "append", Markdown: "Vom Nutzer geschrieben."}}})
	obj, _ := c.Get(noteID)
	blocks := obj.(*model.Note).Body.Blocks
	userBlock := blocks[len(blocks)-1].ID
	derivedBlock := blocks[0].ID
	llm := "llm:chat/fake/x"
	forbidden := []model.Op{
		{Op: "task.update", ID: taskID, Text: "umformuliert"},
		{Op: "task.update", ID: taskID, Due: str("")},
		{Op: "task.update", ID: taskID, Due: str("2026-12-24")},
		{Op: "note.update", ID: noteID, Title: str("neu")},
		{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "delete", BlockID: derivedBlock}}},
		{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "replace", BlockID: userBlock, Markdown: "x"}}},
		{Op: "object.archive", ID: noteID},
	}
	for _, op := range forbidden {
		if _, err := c.Commit(context.Background(), llm, nil, []model.Op{op}); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s by model should be forbidden, got %v", op.Op, err)
		}
	}
	allowed := []model.Op{
		{Op: "task.update", ID: taskID, Due: str("2026-09-10"), Mention: true},
		{Op: "task.update", ID: taskID, State: "done"},
		{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "replace", BlockID: derivedBlock, Markdown: "neu formuliert", Sources: []string{"cap_x"}}}},
		{Op: "note.revise", ID: noteID, Edits: []doc.Edit{{Action: "append", Markdown: "Nachtrag"}}},
	}
	for _, op := range allowed {
		if _, err := c.Commit(context.Background(), llm, nil, []model.Op{op}); err != nil {
			t.Errorf("%s by model should be allowed, got %v", op.Op, err)
		}
	}
	if _, err := c.Commit(context.Background(), llm, nil, []model.Op{{Op: "task.update", ID: taskID, State: "open"}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("model must not reopen a done task: %v", err)
	}
	// The user can do all of it.
	for _, op := range forbidden[:4] {
		if _, err := c.Commit(context.Background(), "user:test", nil, []model.Op{op}); err != nil {
			t.Errorf("%s by user failed: %v", op.Op, err)
		}
	}
}

func TestCapturesCannotBeUndone(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	rec := mustCommit(t, c, "user:test", model.Op{Op: "capture.create", Text: "raw", Source: "test"})
	if _, err := c.Undo(context.Background(), "user:test", rec.TxnID, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undo of capture.create must be refused: %v", err)
	}
	if !rec.Quiet || rec.Undoable {
		t.Fatalf("capture receipts should be quiet and not undoable: %+v", rec)
	}
}

func TestMessagesAndConversationView(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	ops := []model.Op{{Op: "conversation.create"}}
	mustCommit(t, c, "user:test", ops...)
	convID := ops[0].ID
	capID := newCapture(t, c, "Hallo")
	msg := []model.Op{{Op: "message.create", ConversationID: convID, Message: &model.Message{Role: "user", Text: "Hallo **Welt**", CaptureID: capID}}}
	rec := mustCommit(t, c, "user:test", msg...)
	if !rec.Quiet {
		t.Fatal("message receipts are quiet")
	}
	mustCommit(t, c, "llm:chat/fake/x", model.Op{Op: "message.create", ConversationID: convID, Message: &model.Message{Role: "assistant", Text: "- a\n- b"}})
	view, err := c.Conversation(convID)
	if err != nil || len(view.Messages) != 2 || view.MessageCount != 2 || view.ConvTitle != "Hallo Welt" {
		t.Fatalf("view %+v err %v", view, err)
	}
	if view.Messages[1].Blocks.Blocks[0].Type != doc.List || view.Messages[0].Index != 0 {
		t.Fatalf("messages %+v", view.Messages)
	}
	// The conversation before-image stays small: only the counter changes.
	txn, _ := c.Txn(rec.TxnID)
	if len(txn.Touched) != 2 {
		t.Fatalf("touched %v", txn.Touched)
	}
}

func TestTopicMergeAndSetMarkdown(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	topicID, noteID, taskID, _ := seed(t, c)
	other := mustCommit(t, c, "user:test", model.Op{Op: "topic.create", Name: str("Photovoltaik")}).Lines[0].ObjectID
	mustCommit(t, c, "user:test", model.Op{Op: "note.update", ID: noteID, AddTopics: []string{other}})
	obj, _ := c.Get(topicID)
	mustCommit(t, c, "user:test", model.Op{Op: "topic.merge", ID: topicID, ExpectedRev: obj.GetMeta().Rev, From: other})
	n, _ := c.Get(noteID)
	if topics := n.(*model.Note).Topics; len(topics) != 1 || topics[0] != topicID {
		t.Fatalf("note topics after merge: %v", topics)
	}
	if tp, ok := c.FindTopic("photovoltaik"); !ok || tp.ID != topicID {
		t.Fatal("merged name should resolve as alias")
	}
	if len(c.Topics(false)) != 1 {
		t.Fatal("merged topic should be archived")
	}
	// Whole-body edit keeps unchanged block identity.
	n, _ = c.Get(noteID)
	note := n.(*model.Note)
	first := note.Body.Blocks[0].ID
	mustCommit(t, c, "user:test", model.Op{Op: "note.set_markdown", ID: noteID, ExpectedRev: note.Rev, Markdown: "# Fakten\n\nGanz neu."})
	n, _ = c.Get(noteID)
	note = n.(*model.Note)
	if len(note.Body.Blocks) != 2 || note.Body.Blocks[0].ID != first || len(note.Body.Blocks[1].Sources) != 0 {
		t.Fatalf("set_markdown blocks: %+v", note.Body.Blocks)
	}
	if _, err := c.Commit(context.Background(), "llm:chat/x/y", nil, []model.Op{{Op: "note.set_markdown", ID: noteID, Markdown: "x"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("model must not use set_markdown: %v", err)
	}
	_ = taskID
}

func TestUndoRefusesResurrectionAndQuietTxns(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	create := mustCommit(t, c, "user:test", model.Op{Op: "topic.create", Name: str("Haus")})
	topicID := create.Lines[0].ObjectID
	edit := mustCommit(t, c, "user:test", model.Op{Op: "topic.update", ID: topicID, Aliases: []string{"Home"}})
	// Force-undo the creation (the edit came later, so it conflicts).
	if _, err := c.Undo(context.Background(), "user:test", create.TxnID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(topicID); !errors.Is(err, ErrNotFound) {
		t.Fatal("topic should be gone")
	}
	// Undoing the edit must not bring the topic back silently.
	var uc *UndoConflict
	if _, err := c.Undo(context.Background(), "user:test", edit.TxnID, false); !errors.As(err, &uc) {
		t.Fatalf("want UndoConflict, got %v", err)
	}
	// Bookkeeping transactions cannot be undone.
	capID := newCapture(t, c, "x")
	flip := mustCommit(t, c, "system", model.Op{Op: "capture.set_status", ID: capID, Status: "processing"})
	if _, err := c.Undo(context.Background(), "user:test", flip.TxnID, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("quiet txn undo: %v", err)
	}
}

func TestModelCannotReplaceUserSummaryBlock(t *testing.T) {
	c := openTest(t, t.TempDir())
	defer c.Close()
	topicID := mustCommit(t, c, "user:test", model.Op{Op: "topic.create", Name: str("Haus")}).Lines[0].ObjectID
	mustCommit(t, c, "user:test", model.Op{Op: "topic.update", ID: topicID, Edits: []doc.Edit{{Action: "append", Markdown: "Vom Nutzer."}}})
	obj, _ := c.Get(topicID)
	blockID := obj.(*model.Topic).Summary.Blocks[0].ID
	_, err := c.Commit(context.Background(), "llm:chat/x/y", nil, []model.Op{{Op: "topic.update", ID: topicID,
		Edits: []doc.Edit{{Action: "replace", BlockID: blockID, Markdown: "umgeschrieben"}}}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("model replaced a user block: %v", err)
	}
	if _, err := c.Commit(context.Background(), "llm:chat/x/y", nil, []model.Op{{Op: "topic.update", ID: topicID,
		Edits: []doc.Edit{{Action: "append", Markdown: "Ergänzung", Sources: []string{"cap_x"}}}}}); err != nil {
		t.Fatalf("model append should work: %v", err)
	}
}
