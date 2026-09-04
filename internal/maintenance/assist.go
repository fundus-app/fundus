package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/ids"
	"github.com/fundus-app/fundus/internal/model"
	"github.com/fundus-app/fundus/internal/research"
)

// assist lets the model work on open tasks. It may draft a note from what
// the knowledge base already holds, or start research when the answer is
// out on the web. "propose" files drafts in the inbox; "auto" writes them
// as notes linked to the task and starts research by itself.
func assist(ctx context.Context, e *jobEnv) JobReport {
	rep := JobReport{}
	if !e.hasModel() {
		rep.Skipped = "no model"
		return rep
	}
	c := e.w.core
	auto := e.cfg.Assist == "auto"
	helped := 0
	for _, tv := range c.Tasks([]model.TaskState{model.TaskOpen}, false) {
		if helped >= 5 || ctx.Err() != nil {
			break
		}
		task := tv.Task
		if len(task.Notes) > 0 || research.IsResearchTask(task) {
			continue
		}
		if at, ok := e.w.state.Assisted[task.ID]; ok && e.now().Sub(at) < 14*24*time.Hour {
			continue
		}
		rep.Checked++
		type nctx struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Preview string `json:"preview"`
		}
		var notes []nctx
		for _, h := range c.Search(task.Text, 8, []model.Type{model.TypeNote}, false) {
			if n, ok := h.Object.(*model.Note); ok {
				notes = append(notes, nctx{n.ID, n.NoteTitle, model.Shorten(n.Body.PlainText(), 400)})
			}
		}
		payload, _ := json.MarshalIndent(map[string]any{"task": task.Text, "topics": tv.TopicNames, "due": task.Due, "notes": notes}, "", " ")
		var out struct {
			Action   string `json:"action"`
			Reason   string `json:"reason"`
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		}
		err := e.askJSON(ctx, "You help with one open task of a personal knowledge base, using only what the notes already say. Choose: \"draft\" when the notes hold enough to produce something useful for the task (a checklist, an outline, a short draft, a summary of what is known and what is missing); \"research\" when the task needs facts from the web that the notes lack; \"none\" otherwise (most tasks: do not force help). A draft is at most 300 words of plain Markdown (paragraphs, \"- \" lists), states nothing the notes do not contain, and names the notes it used by title. Write in the language of the task. "+untrusted,
			"<notes>\n"+string(payload)+"\n</notes>\nAnswer with JSON: {\"action\":\"none|draft|research\",\"reason\":\"…\",\"title\":\"…\",\"markdown\":\"…\"}.",
			"task_help", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["action","reason"],"properties":{"action":{"type":"string","enum":["none","draft","research"]},"reason":{"type":"string"},"title":{"type":"string"},"markdown":{"type":"string"}}}`), &out)
		if err != nil {
			rep.Error = err.Error()
			break
		}
		e.w.state.Assisted[task.ID] = e.now()
		switch out.Action {
		case "draft":
			title := strings.TrimSpace(out.Title)
			body := strings.TrimSpace(out.Markdown)
			if title == "" || body == "" {
				continue
			}
			if !strings.HasPrefix(strings.ToLower(title), "draft") {
				title = "Draft: " + title
			}
			noteID := ids.New(ids.PrefixNote)
			ops := []model.Op{
				{Op: "note.create", ID: noteID, Kind: string(model.NoteKindNote), Title: &title, Markdown: body, Topics: task.Topics},
				{Op: "task.update", ID: task.ID, ExpectedRev: task.Rev, AddNotes: []string{noteID}},
			}
			if auto {
				rec, err := c.Commit(ctx, e.modelActor(), e.cause(), ops)
				if err != nil {
					rep.Error = err.Error()
					continue
				}
				rep.Changed++
				rep.TxnIDs = append(rep.TxnIDs, rec.TxnID)
			} else {
				text := fmt.Sprintf("Draft for “%s”: %s?", task.Text, title)
				if e.pendingProposal(text) {
					continue
				}
				if _, err := e.propose(ctx, text, []string{"Create the note “" + title + "” from your own notes", "Link it to the task"}, ops); err != nil {
					rep.Error = err.Error()
					continue
				}
				rep.Proposed++
			}
			helped++
		case "research":
			if auto && e.research != nil && e.research.Available() {
				if err := e.research.Start(task.ID); err == nil {
					rep.Changed++
					rep.Notes = append(rep.Notes, "started research for “"+task.Text+"”")
					helped++
				}
			} else {
				rep.Notes = append(rep.Notes, "research could help with “"+task.Text+"”: "+strings.TrimSpace(out.Reason))
			}
		}
	}
	return rep
}
