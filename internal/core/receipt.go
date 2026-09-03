package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

func buildReceipt(txn *model.Txn, ws *workspace) *model.Receipt {
	lookup := func(id string) model.Object {
		if o, ok := ws.get(id); ok {
			return o
		}
		if o, ok := ws.st.objects[id]; ok {
			return o
		}
		return nil
	}
	before := func(id string) model.Object {
		if raw, ok := ws.before[id]; ok && len(raw) > 0 && string(raw) != "null" {
			if o, err := model.Unmarshal(raw); err == nil {
				return o
			}
		}
		return nil
	}
	return renderReceipt(txn, lookup, before)
}

// renderReceipt describes the visible effects of a transaction from the ops
// and the before/after objects, never from a model's claims. Ops that changed
// nothing (completing a done task, linking an already linked topic) say so.
func renderReceipt(txn *model.Txn, lookup, before func(string) model.Object) *model.Receipt {
	r := &model.Receipt{TxnID: txn.ID, Seq: txn.Seq, At: txn.At, Actor: txn.Actor, Cause: txn.Cause, UndoOf: txn.UndoOf, Touched: txn.Touched}
	quiet := 0
	for _, op := range txn.Ops {
		obj := lookup(op.ID)
		prev := before(op.ID)
		newTopics := func(add []string) []string {
			var had []string
			switch v := prev.(type) {
			case *model.Note:
				had = v.Topics
			case *model.Task:
				had = v.Topics
			}
			var out []string
			for _, id := range add {
				if !contains(had, id) {
					out = append(out, id)
				}
			}
			return out
		}
		name := func() string {
			if obj == nil {
				return op.ID
			}
			return obj.Title()
		}
		topicNames := func(ids []string) string {
			var names []string
			for _, id := range ids {
				if t := lookup(id); t != nil {
					names = append(names, t.Title())
				}
			}
			return strings.Join(names, ", ")
		}
		var text string
		var typ model.Type
		if obj != nil {
			typ = obj.GetMeta().Type
		}
		switch op.Op {
		case "capture.create":
			// Captures are listed in their own views; the audit log is for
			// what was done with them.
			quiet++
			continue
		case "capture.set_status":
			switch model.CaptureStatus(op.Status) {
			case model.CaptureNeedsReview:
				text = "Parked in the inbox"
				if op.Result != nil {
					switch op.Result.Reason {
					case "unclear":
						text += " with a question"
						if op.Result.Question != "" {
							text += ": " + strings.TrimRight(op.Result.Question, ".")
						}
					case "low_confidence":
						text += fmt.Sprintf(" because the model was unsure (%.0f%%)", op.Result.Confidence*100)
					case "proposal":
						text += " as a proposal"
						if op.Result.Summary != "" {
							text += ": " + strings.TrimRight(op.Result.Summary, ".")
						}
					case "undone":
						text += " after an undo"
					}
				}
				text += "."
			case model.CaptureFailed:
				text = "Processing failed"
				if op.Result != nil && op.Result.Error != "" {
					text += ": " + model.Shorten(op.Result.Error, 160)
				}
				text += "."
			case model.CaptureDismissed:
				text = "Dismissed capture."
			default:
				quiet++
				continue
			}
		case "note.create":
			kind := "note"
			if op.Kind == string(model.NoteKindIdea) {
				kind = "idea"
			}
			text = fmt.Sprintf("Created %s “%s”", kind, name())
			if tn := topicNames(op.Topics); tn != "" {
				text += " in " + tn
			}
			text += "."
		case "note.revise":
			appendOnly := true
			for _, e := range op.Edits {
				if e.Action != "append" {
					appendOnly = false
				}
			}
			if appendOnly {
				text = fmt.Sprintf("Added to note “%s”.", name())
			} else {
				text = fmt.Sprintf("Revised note “%s” (%d edit%s).", name(), len(op.Edits), plural(len(op.Edits)))
			}
		case "note.update":
			var parts []string
			if op.Title != nil {
				parts = append(parts, "renamed")
			}
			if op.Kind != "" {
				parts = append(parts, "now a "+op.Kind)
			}
			if tn := topicNames(newTopics(op.AddTopics)); tn != "" {
				parts = append(parts, "linked to "+tn)
			}
			if tn := topicNames(op.RemoveTopics); tn != "" {
				parts = append(parts, "unlinked from "+tn)
			}
			if len(op.AddRelated) > 0 {
				parts = append(parts, fmt.Sprintf("related to %d note%s", len(op.AddRelated), plural(len(op.AddRelated))))
			}
			if len(parts) == 0 {
				text = fmt.Sprintf("Note “%s” unchanged.", name())
			} else if tn := topicNames(newTopics(op.AddTopics)); len(parts) == 1 && tn != "" {
				text = fmt.Sprintf("Linked note “%s” to %s.", name(), tn)
			} else {
				text = fmt.Sprintf("Updated note “%s”: %s.", name(), strings.Join(parts, ", "))
			}
		case "note.set_markdown":
			text = fmt.Sprintf("Edited note “%s”.", name())
		case "task.create":
			// One sentence: Created task “X” in Fundus, due Fri 12 Sep, deferred to later.
			text = fmt.Sprintf("Created task “%s”", name())
			if tn := topicNames(op.Topics); tn != "" {
				text += " in " + tn
			}
			if op.Due != nil && *op.Due != "" {
				text += ", due " + humanDate(*op.Due)
			}
			switch op.State {
			case string(model.TaskLater):
				text += ", deferred to later"
			case string(model.TaskWaiting):
				text += ", waiting"
			case string(model.TaskDone):
				text += ", already done"
			}
			text += "."
		case "task.update":
			prevState := ""
			if pt, ok := prev.(*model.Task); ok {
				prevState = string(pt.State)
			}
			switch {
			case op.State != "" && op.State == prevState && op.Due == nil && op.Text == "" && op.EffortMinutes == nil && op.Importance == nil && len(newTopics(op.AddTopics)) == 0 && !op.Mention:
				text = fmt.Sprintf("Task “%s” was already %s.", name(), op.State)
			case op.State == string(model.TaskDone):
				text = fmt.Sprintf("Completed task “%s”.", name())
			case op.State == string(model.TaskWaiting):
				text = fmt.Sprintf("Task “%s” is now waiting", name())
				if op.WaitingOn != nil && *op.WaitingOn != "" {
					text += " on " + *op.WaitingOn
				}
				text += "."
			case op.State == string(model.TaskLater):
				text = fmt.Sprintf("Deferred task “%s” to later.", name())
			case op.State == string(model.TaskOpen):
				text = fmt.Sprintf("Reopened task “%s”.", name())
			case op.Mention && op.Due == nil && op.Text == "" && op.EffortMinutes == nil && op.Importance == nil:
				text = fmt.Sprintf("Noted another mention of task “%s”.", name())
			default:
				var parts []string
				if op.Text != "" {
					parts = append(parts, "reworded")
				}
				if op.Due != nil {
					if *op.Due == "" {
						parts = append(parts, "due date removed")
					} else {
						parts = append(parts, "due "+humanDate(*op.Due))
					}
				}
				if op.EffortMinutes != nil {
					parts = append(parts, fmt.Sprintf("effort %d min", *op.EffortMinutes))
				}
				if op.Importance != nil {
					parts = append(parts, "importance "+importanceWord(*op.Importance))
				}
				if tn := topicNames(newTopics(op.AddTopics)); tn != "" {
					parts = append(parts, "linked to "+tn)
				}
				if tn := topicNames(op.RemoveTopics); tn != "" {
					parts = append(parts, "unlinked from "+tn)
				}
				if len(op.AddNotes) > 0 {
					parts = append(parts, "linked to notes")
				}
				if op.Mention {
					parts = append(parts, "mentioned again")
				}
				if len(parts) == 0 {
					text = fmt.Sprintf("Task “%s” unchanged.", name())
				} else if tn := topicNames(newTopics(op.AddTopics)); len(parts) == 1 && tn != "" {
					text = fmt.Sprintf("Linked task “%s” to %s.", name(), tn)
				} else {
					text = fmt.Sprintf("Updated task “%s”: %s.", name(), strings.Join(parts, ", "))
				}
			}
		case "topic.create":
			switch op.Kind {
			case string(model.TopicKindPerson):
				text = fmt.Sprintf("Created person “%s”.", name())
			case string(model.TopicKindProject):
				text = fmt.Sprintf("Created project “%s”.", name())
			default:
				text = fmt.Sprintf("Created topic “%s”.", name())
			}
		case "topic.update":
			if len(op.Edits) > 0 && op.Name == nil && op.Aliases == nil {
				text = fmt.Sprintf("Updated summary of “%s”.", name())
			} else {
				text = fmt.Sprintf("Updated topic “%s”.", name())
			}
		case "topic.set_summary":
			text = fmt.Sprintf("Edited summary of “%s”.", name())
		case "topic.merge":
			from := op.From
			if o := lookup(op.From); o != nil {
				from = o.Title()
			}
			text = fmt.Sprintf("Merged topic “%s” into “%s”.", from, name())
		case "source.create":
			text = fmt.Sprintf("Saved source “%s”.", name())
		case "conversation.create", "conversation.update", "message.create", "conversation.append":
			quiet++
			continue
		case "object.archive":
			text = fmt.Sprintf("Archived %s “%s”.", typ, name())
		case "object.unarchive":
			text = fmt.Sprintf("Unarchived %s “%s”.", typ, name())
		case "object.restore":
			text = fmt.Sprintf("Restored %s “%s”.", typ, name())
		case "object.remove":
			text = fmt.Sprintf("Removed %s “%s”.", typ, name())
		default:
			text = op.Op + "."
		}
		r.Lines = append(r.Lines, model.ReceiptLine{Op: op.Op, ObjectID: op.ID, ObjectType: typ, Text: text})
	}
	if r.Lines == nil {
		r.Lines = []model.ReceiptLine{}
	}
	if len(r.Lines) == 0 && quiet > 0 {
		r.Summary = "No visible changes."
		r.Quiet = true
	} else {
		parts := make([]string, 0, len(r.Lines))
		for _, l := range r.Lines {
			parts = append(parts, l.Text)
		}
		r.Summary = strings.Join(parts, " ")
	}
	r.Undoable = !r.Quiet
	for _, op := range txn.Ops {
		if op.Op == "capture.create" {
			r.Undoable = false
		}
	}
	return r
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// humanDate renders YYYY-MM-DD as "10 Sep 2026" for receipts.
func humanDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("2 Jan 2006")
	}
	return iso
}

func importanceWord(n int) string {
	switch n {
	case 1:
		return "low"
	case 2:
		return "normal"
	case 3:
		return "high"
	}
	return "unset"
}
