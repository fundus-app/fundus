package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/model"
)

// TestRandomOpsReplayIsDeterministic drives the core with a long random but
// valid sequence of operations (creates, edits, links, completions, undos),
// then checks three invariants: the snapshot equals a full replay from seq 1,
// a truncated last log line is recovered without losing committed state, and
// the search index after replay finds what it found before.
func TestRandomOpsReplayIsDeterministic(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			dir := t.TempDir()
			c := openTest(t, dir)
			rng := rand.New(rand.NewSource(seed))
			var topics, notes, tasks, caps, txns []string
			actors := []string{"user:test", "llm:triage/fake/x", "user:app"}
			for i := 0; i < 300; i++ {
				actor := actors[rng.Intn(len(actors))]
				var ops []model.Op
				switch rng.Intn(9) {
				case 0:
					name := fmt.Sprintf("Topic %d", i)
					ops = []model.Op{{Op: "topic.create", Name: &name, Aliases: []string{fmt.Sprintf("t%d", i)}}}
				case 1:
					ops = []model.Op{{Op: "capture.create", Text: fmt.Sprintf("capture %d with words alpha beta", i), Source: "test"}}
				case 2:
					title := fmt.Sprintf("Note %d", i)
					op := model.Op{Op: "note.create", Kind: []string{"note", "idea"}[rng.Intn(2)], Title: &title,
						Markdown: fmt.Sprintf("# Heading\n\nParagraph %d.\n\n- a\n- b", i)}
					if len(topics) > 0 {
						op.Topics = []string{topics[rng.Intn(len(topics))]}
					}
					if len(caps) > 0 {
						op.Origins = []string{caps[rng.Intn(len(caps))]}
					}
					ops = []model.Op{op}
				case 3:
					op := model.Op{Op: "task.create", Text: fmt.Sprintf("Task %d", i)}
					if rng.Intn(2) == 0 {
						due := fmt.Sprintf("2026-10-%02d", 1+rng.Intn(28))
						op.Due = &due
					}
					if len(topics) > 0 {
						op.Topics = []string{topics[rng.Intn(len(topics))]}
					}
					ops = []model.Op{op}
				case 4:
					if len(notes) == 0 {
						continue
					}
					id := notes[rng.Intn(len(notes))]
					ops = []model.Op{{Op: "note.revise", ID: id, Edits: []doc.Edit{{Action: "append", Markdown: fmt.Sprintf("Appended %d.", i), Sources: []string{"cap_x"}}}}}
				case 5:
					if len(tasks) == 0 {
						continue
					}
					id := tasks[rng.Intn(len(tasks))]
					ops = []model.Op{{Op: "task.update", ID: id, State: []string{"done", "waiting", "later"}[rng.Intn(3)], Mention: rng.Intn(2) == 0}}
					actor = "user:test" // models may not reopen; keep it simple
				case 6:
					if len(notes) == 0 || len(topics) == 0 {
						continue
					}
					ops = []model.Op{{Op: "note.update", ID: notes[rng.Intn(len(notes))], AddTopics: []string{topics[rng.Intn(len(topics))]}}}
				case 7:
					if len(txns) == 0 {
						continue
					}
					// Undo a random earlier transaction; conflicts are fine.
					_, _ = c.Undo(context.Background(), "user:test", txns[rng.Intn(len(txns))], rng.Intn(3) == 0)
					continue
				case 8:
					if len(notes) == 0 {
						continue
					}
					id := notes[rng.Intn(len(notes))]
					ops = []model.Op{{Op: "note.set_markdown", ID: id, Markdown: fmt.Sprintf("# Heading\n\nRewritten %d.", i)}}
					actor = "user:test"
				}
				rec, err := c.Commit(context.Background(), actor, &model.Cause{Kind: "user"}, ops)
				if err != nil {
					continue // conflicts and forbidden model edits are expected in a random walk
				}
				txns = append(txns, rec.TxnID)
				for _, op := range ops {
					switch op.Op {
					case "topic.create":
						topics = append(topics, op.ID)
					case "capture.create":
						caps = append(caps, op.ID)
					case "note.create":
						notes = append(notes, op.ID)
					case "task.create":
						tasks = append(tasks, op.ID)
					}
				}
			}
			before := snapshotObjects(t, c)
			hits := len(c.Search("alpha", 0, nil, true))
			seq := c.Seq()
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}

			// 1. Snapshot vs full replay.
			if err := removeSnapshot(dir); err != nil {
				t.Fatal(err)
			}
			c2 := openTest(t, dir)
			if c2.Seq() != seq {
				t.Fatalf("seq %d after replay, want %d", c2.Seq(), seq)
			}
			if !reflect.DeepEqual(before, snapshotObjects(t, c2)) {
				t.Fatal("full replay differs from the state at close")
			}
			if got := len(c2.Search("alpha", 0, nil, true)); got != hits {
				t.Fatalf("index after replay: %d hits, want %d", got, hits)
			}
			c2.Close()

			// 2. Crash: cut the last line of the last segment in half.
			seg := lastSegment(t, dir)
			raw, _ := os.ReadFile(seg)
			parts := splitKeepLast(raw)
			prefix, last := parts[0], parts[1]
			cut := append(append([]byte{}, prefix...), last[:len(last)/2]...)
			if err := os.WriteFile(seg, cut, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := removeSnapshot(dir); err != nil {
				t.Fatal(err)
			}
			c3 := openTest(t, dir)
			if c3.Recovery() == nil {
				t.Fatal("expected a recovery report after a cut tail")
			}
			if c3.Seq() != seq-1 {
				t.Fatalf("seq after crash recovery %d, want %d", c3.Seq(), seq-1)
			}
			// It keeps working after recovery.
			name := "After crash"
			if _, err := c3.Commit(context.Background(), "user:test", nil, []model.Op{{Op: "topic.create", Name: &name}}); err != nil {
				t.Fatalf("commit after recovery: %v", err)
			}
			c3.Close()
		})
	}
}

func lastSegment(t *testing.T, dir string) string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "events", "*.jsonl"))
	if err != nil || len(entries) == 0 {
		t.Fatal("no segments")
	}
	return entries[len(entries)-1]
}

// splitKeepLast returns [everything before the last line, the last line].
func splitKeepLast(raw []byte) [][]byte {
	body := raw
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	i := len(body) - 1
	for i >= 0 && body[i] != '\n' {
		i--
	}
	return [][]byte{raw[:i+1], raw[i+1:]}
}

var _ = json.Marshal
