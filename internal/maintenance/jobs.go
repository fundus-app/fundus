package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/model"
)

// integrity removes links to topics that no longer exist and reports what
// it cannot fix. Deterministic, no model.
func integrity(ctx context.Context, e *jobEnv) JobReport {
	rep := JobReport{}
	c := e.w.core
	live := map[string]bool{}
	for _, tv := range c.Topics(true) {
		if !tv.Topic.Archived {
			live[tv.Topic.ID] = true
		}
	}
	var ops []model.Op
	var notes []string
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		rep.Checked++
		switch v := o.(type) {
		case *model.Note:
			if v.Archived {
				return true
			}
			var dead []string
			for _, t := range v.Topics {
				if !live[t] {
					dead = append(dead, t)
				}
			}
			if len(dead) > 0 && len(ops) < 200 {
				ops = append(ops, model.Op{Op: "note.update", ID: v.ID, ExpectedRev: v.Rev, RemoveTopics: dead})
			}
		case *model.Task:
			if v.Archived {
				return true
			}
			var dead []string
			for _, t := range v.Topics {
				if !live[t] {
					dead = append(dead, t)
				}
			}
			if len(dead) > 0 && len(ops) < 200 {
				ops = append(ops, model.Op{Op: "task.update", ID: v.ID, ExpectedRev: v.Rev, RemoveTopics: dead})
			}
			for _, n := range v.Notes {
				if e.get(n) == nil {
					notes = append(notes, fmt.Sprintf("task %s links to a missing note %s", v.ID, n))
				}
			}
			if v.State == model.TaskDone && v.CompletedAt == nil {
				notes = append(notes, fmt.Sprintf("task %s is done without a completion time", v.ID))
			}
		}
		return true
	})
	seen := map[string]int{}
	c.Each([]model.Type{model.TypeSource}, func(o model.Object) bool {
		if s, ok := o.(*model.Source); ok {
			seen[s.URL]++
		}
		return true
	})
	dupURLs := 0
	for _, n := range seen {
		if n > 1 {
			dupURLs++
		}
	}
	if dupURLs > 0 {
		notes = append(notes, fmt.Sprintf("%d URLs are stored as more than one source", dupURLs))
	}
	if len(ops) > 0 {
		rec, err := c.Commit(ctx, systemActor, e.cause(), ops)
		if err != nil {
			rep.Error = err.Error()
		} else {
			rep.Changed = len(ops)
			rep.TxnIDs = append(rep.TxnIDs, rec.TxnID)
			notes = append(notes, fmt.Sprintf("removed links to deleted topics from %d objects", len(ops)))
		}
	}
	rep.Notes = notes
	return rep
}

// untagged asks the model which existing topics untagged notes and tasks
// belong to, keeps only assignments the text gives evidence for, and links
// them (additive, undoable).
func untagged(ctx context.Context, e *jobEnv) JobReport {
	rep := JobReport{}
	if !e.hasModel() {
		rep.Skipped = "no model"
		return rep
	}
	c := e.w.core
	topics := c.Topics(false)
	if len(topics) == 0 {
		rep.Skipped = "no topics yet"
		return rep
	}
	byID := map[string]*model.Topic{}
	type tctx struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Kind    string   `json:"kind"`
		Aliases []string `json:"aliases,omitempty"`
	}
	var tlist []tctx
	for i, tv := range topics {
		if i >= 200 {
			break
		}
		byID[tv.Topic.ID] = tv.Topic
		tlist = append(tlist, tctx{ID: tv.Topic.ID, Name: tv.Topic.Name, Kind: string(tv.Topic.Kind), Aliases: tv.Topic.Aliases})
	}
	cutoff := e.now().Add(-time.Duration(max(e.cfg.UntaggedAfterDays, 0)) * 24 * time.Hour)
	type item struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	var items []item
	objs := map[string]model.Object{}
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		m := o.GetMeta()
		if m.Archived || m.CreatedAt.After(cutoff) {
			return true
		}
		var topicsOf []string
		var kind string
		switch v := o.(type) {
		case *model.Note:
			topicsOf, kind = v.Topics, string(v.Kind)
		case *model.Task:
			if v.State == model.TaskDone {
				return true
			}
			topicsOf, kind = v.Topics, "task"
		}
		if len(topicsOf) > 0 {
			return true
		}
		rep.Checked++
		if len(items) < 40 {
			items = append(items, item{ID: m.ID, Kind: kind, Text: model.Shorten(objectText(o), 500)})
			objs[m.ID] = o
		}
		return true
	})
	if len(items) == 0 {
		return rep
	}
	var ops []model.Op
	for i := 0; i < len(items); i += 20 {
		batch := items[i:min(i+20, len(items))]
		payload, _ := json.MarshalIndent(map[string]any{"topics": tlist, "items": batch}, "", " ")
		var out struct {
			Assignments []struct {
				ID     string   `json:"id"`
				Topics []string `json:"topics"`
			} `json:"assignments"`
		}
		err := e.askJSON(ctx, "You file a personal knowledge base. For each item, name the existing topics it clearly belongs to, by id. Only assign a topic when the item is plainly about that subject; a shared word or a vague resemblance is not enough. Leave items without a clear topic out. "+untrusted,
			"<items>\n"+string(payload)+"\n</items>\nAnswer with JSON: {\"assignments\":[{\"id\":\"…\",\"topics\":[\"topic_…\"]}]}.",
			"topic_assignments", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["assignments"],"properties":{"assignments":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["id","topics"],"properties":{"id":{"type":"string"},"topics":{"type":"array","items":{"type":"string"}}}}}}}`), &out)
		if err != nil {
			rep.Error = err.Error()
			break
		}
		for _, a := range out.Assignments {
			o, ok := objs[a.ID]
			if !ok {
				continue
			}
			text := objectText(o)
			var keep []string
			for _, tid := range a.Topics {
				tp, ok := byID[tid]
				if ok && topicEvidenced(tp, text) {
					keep = append(keep, tid)
				}
			}
			if len(keep) == 0 {
				continue
			}
			switch v := o.(type) {
			case *model.Note:
				ops = append(ops, model.Op{Op: "note.update", ID: v.ID, ExpectedRev: v.Rev, AddTopics: keep})
			case *model.Task:
				ops = append(ops, model.Op{Op: "task.update", ID: v.ID, ExpectedRev: v.Rev, AddTopics: keep})
			}
		}
	}
	if len(ops) > 0 {
		rec, err := c.Commit(ctx, e.modelActor(), e.cause(), ops)
		if err != nil {
			rep.Error = err.Error()
		} else {
			rep.Changed = len(ops)
			rep.TxnIDs = append(rep.TxnIDs, rec.TxnID)
		}
	}
	return rep
}

// duplicates finds notes, topics and tasks that say the same thing. Likely
// note pairs get a related link right away (additive); merges and deletions
// go to the inbox as proposals.
func duplicates(ctx context.Context, e *jobEnv) JobReport {
	rep := JobReport{}
	c := e.w.core
	type pair struct {
		A, B   model.Object
		Reason string
	}
	proposedRecently := func(a, b string) bool {
		if at, ok := e.w.state.Proposed[pairKey(a, b)]; ok && e.now().Sub(at) < 60*24*time.Hour {
			return true
		}
		return false
	}
	// Notes.
	notes := c.Notes("", false)
	noteByID := map[string]*model.Note{}
	for _, nv := range notes {
		noteByID[nv.ID] = nv.Note
	}
	seenPair := map[string]bool{}
	var notePairs []pair
	for i, nv := range notes {
		if i >= 300 {
			break
		}
		rep.Checked++
		n := nv.Note
		// Candidates by meaning (embeddings) and by title words; the model
		// decides, so a generous net is fine.
		var cands []string
		if e.index != nil && e.index.Len() > 0 {
			for _, h := range e.index.Similar(n.ID, 3, func(id string) bool { _, ok := noteByID[id]; return ok }) {
				if h.Score >= 0.80 {
					cands = append(cands, h.ID)
				}
			}
		}
		for _, h := range c.Search(n.NoteTitle, 4, []model.Type{model.TypeNote}, false) {
			if h.ID != n.ID && !contains(cands, h.ID) {
				if other, ok := noteByID[h.ID]; ok && jaccard(n.NoteTitle, other.NoteTitle) >= 0.5 {
					cands = append(cands, h.ID)
				}
			}
		}
		for _, id := range cands {
			key := pairKey(n.ID, id)
			if seenPair[key] || proposedRecently(n.ID, id) {
				continue
			}
			other := noteByID[id]
			if contains(n.Related, id) || contains(other.Related, n.ID) {
				continue // already linked: the user knows
			}
			seenPair[key] = true
			notePairs = append(notePairs, pair{A: n, B: other, Reason: "similar"})
		}
	}
	// Topics: names that contain each other or share most words.
	topics := c.Topics(false)
	var topicPairs []pair
	for i := range topics {
		for j := i + 1; j < len(topics); j++ {
			a, b := topics[i].Topic, topics[j].Topic
			key := pairKey(a.ID, b.ID)
			if proposedRecently(a.ID, b.ID) {
				continue
			}
			na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if mentionsWord(na, nb) || mentionsWord(nb, na) || jaccard(a.Name, b.Name) >= 0.6 ||
				(e.index != nil && e.index.Len() > 0 && similarScore(e, a.ID, b.ID) >= 0.9) {
				seenPair[key] = true
				topicPairs = append(topicPairs, pair{A: a, B: b, Reason: "names"})
			}
		}
		rep.Checked++
	}
	// Tasks: the same text twice among open tasks.
	open := c.Tasks([]model.TaskState{model.TaskOpen, model.TaskLater, model.TaskWaiting}, false)
	byNorm := map[string]*model.Task{}
	var taskPairs []pair
	for _, tv := range open {
		rep.Checked++
		norm := normalizeText(tv.Text)
		if norm == "" {
			continue
		}
		if first, ok := byNorm[norm]; ok {
			if !proposedRecently(first.ID, tv.ID) {
				taskPairs = append(taskPairs, pair{A: first, B: tv.Task, Reason: "identical"})
			}
			continue
		}
		byNorm[norm] = tv.Task
	}
	// The model confirms note and topic pairs; identical tasks need no model.
	type verdict struct {
		A    string `json:"a"`
		B    string `json:"b"`
		Same bool   `json:"same"`
		Keep string `json:"keep"`
	}
	confirm := func(pairs []pair, what string) []verdict {
		if len(pairs) == 0 {
			return nil
		}
		if !e.hasModel() {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%d possible duplicate %s found, no model to confirm them", len(pairs), what))
			return nil
		}
		var verdicts []verdict
		for i := 0; i < len(pairs); i += 10 {
			batch := pairs[i:min(i+10, len(pairs))]
			type side struct {
				ID   string `json:"id"`
				Text string `json:"text"`
			}
			type pj struct {
				A side `json:"a"`
				B side `json:"b"`
			}
			var pl []pj
			for _, p := range batch {
				pl = append(pl, pj{A: side{p.A.GetMeta().ID, model.Shorten(objectText(p.A), 500)}, B: side{p.B.GetMeta().ID, model.Shorten(objectText(p.B), 500)}})
			}
			payload, _ := json.MarshalIndent(pl, "", " ")
			var out struct {
				Pairs []verdict `json:"pairs"`
			}
			err := e.askJSON(ctx, "You curate a personal knowledge base. For each pair of "+what+", decide whether they are about the same thing and should be merged (same = true) or merely related (same = false). Merging is only right when one would be redundant after the other absorbed its content. keep names the side to keep: the more complete or better named one. "+untrusted,
				"<pairs>\n"+string(payload)+"\n</pairs>\nAnswer with JSON: {\"pairs\":[{\"a\":\"id\",\"b\":\"id\",\"same\":true,\"keep\":\"a\"}]}.",
				"duplicate_verdicts", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["pairs"],"properties":{"pairs":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["a","b","same","keep"],"properties":{"a":{"type":"string"},"b":{"type":"string"},"same":{"type":"boolean"},"keep":{"type":"string","enum":["a","b"]}}}}}}`), &out)
			if err != nil {
				rep.Error = err.Error()
				return verdicts
			}
			verdicts = append(verdicts, out.Pairs...)
		}
		return verdicts
	}
	noteVerdicts := confirm(notePairs, "notes")
	// Related links first (cheap, additive, undoable), one op per note so
	// revisions stay consistent; the merge proposals then carry fresh revs.
	var relOps []model.Op
	seenNote := map[string]bool{}
	for _, v := range noteVerdicts {
		a, b := noteByID[v.A], noteByID[v.B]
		if a == nil || b == nil {
			continue
		}
		keep, drop := a, b
		if v.Keep == "b" {
			keep, drop = b, a
		}
		if !seenNote[keep.ID] {
			seenNote[keep.ID] = true
			relOps = append(relOps, model.Op{Op: "note.update", ID: keep.ID, ExpectedRev: keep.Rev, AddRelated: []string{drop.ID}})
		}
	}
	if len(relOps) > 0 {
		if rec, err := c.Commit(ctx, e.modelActor(), e.cause(), relOps); err != nil {
			rep.Error = err.Error()
		} else {
			rep.Changed += len(relOps)
			rep.TxnIDs = append(rep.TxnIDs, rec.TxnID)
		}
	}
	for _, v := range noteVerdicts {
		if !v.Same {
			e.w.state.Proposed[pairKey(v.A, v.B)] = e.now()
			continue
		}
		ka, kb := e.get(v.A), e.get(v.B)
		a, ok1 := ka.(*model.Note)
		b, ok2 := kb.(*model.Note)
		if !ok1 || !ok2 {
			continue
		}
		keep, drop := a, b
		if v.Keep == "b" {
			keep, drop = b, a
		}
		text := fmt.Sprintf("Merge note “%s” into “%s”?", drop.NoteTitle, keep.NoteTitle)
		if e.pendingProposal(text) {
			continue
		}
		ops := []model.Op{
			{Op: "note.revise", ID: keep.ID, ExpectedRev: keep.Rev, Origins: drop.Origins,
				Edits: []doc.Edit{{Action: "append", Markdown: drop.Body.Markdown(), Sources: drop.Origins}}},
			{Op: "object.archive", ID: drop.ID, ExpectedRev: drop.Rev},
		}
		if _, err := e.propose(ctx, text, []string{fmt.Sprintf("Append the text of “%s” to “%s”", drop.NoteTitle, keep.NoteTitle), fmt.Sprintf("Delete “%s” (undo restores it)", drop.NoteTitle)}, ops); err != nil {
			rep.Error = err.Error()
		} else {
			rep.Proposed++
			e.w.state.Proposed[pairKey(a.ID, b.ID)] = e.now()
		}
	}
	for _, v := range confirm(topicPairs, "topics") {
		if !v.Same {
			e.w.state.Proposed[pairKey(v.A, v.B)] = e.now()
			continue
		}
		a, b := e.get(v.A), e.get(v.B)
		ta, ok1 := a.(*model.Topic)
		tb, ok2 := b.(*model.Topic)
		if !ok1 || !ok2 {
			continue
		}
		keep, from := ta, tb
		if v.Keep == "b" {
			keep, from = tb, ta
		}
		text := fmt.Sprintf("Merge topic “%s” into “%s”?", from.Name, keep.Name)
		if e.pendingProposal(text) {
			continue
		}
		ops := []model.Op{{Op: "topic.merge", ID: keep.ID, ExpectedRev: keep.Rev, From: from.ID}}
		if _, err := e.propose(ctx, text, []string{fmt.Sprintf("Move the notes and tasks of “%s” to “%s”", from.Name, keep.Name), fmt.Sprintf("Keep “%s” as an alias", from.Name)}, ops); err != nil {
			rep.Error = err.Error()
		} else {
			rep.Proposed++
			e.w.state.Proposed[pairKey(ta.ID, tb.ID)] = e.now()
		}
	}
	for _, p := range taskPairs {
		first, second := p.A.(*model.Task), p.B.(*model.Task)
		newer := second
		if first.CreatedAt.After(second.CreatedAt) || (first.CreatedAt.Equal(second.CreatedAt) && first.ID > second.ID) {
			newer = first
		}
		text := fmt.Sprintf("Delete the duplicate task “%s”?", newer.Text)
		if e.pendingProposal(text) {
			continue
		}
		ops := []model.Op{{Op: "object.archive", ID: newer.ID, ExpectedRev: newer.Rev}}
		if _, err := e.propose(ctx, text, []string{"The same task exists twice; this removes the newer one (undo restores it)"}, ops); err != nil {
			rep.Error = err.Error()
		} else {
			rep.Proposed++
			e.w.state.Proposed[pairKey(first.ID, second.ID)] = e.now()
		}
	}
	return rep
}

func similarScore(e *jobEnv, a, b string) float64 {
	for _, h := range e.index.Similar(a, 20, nil) {
		if h.ID == b {
			return h.Score
		}
	}
	return 0
}

// summaries writes or refreshes one automatic summary block per topic whose
// members changed. The block carries its date and is replaced, never
// stacked; user-written blocks are left alone.
func summaries(ctx context.Context, e *jobEnv) JobReport {
	rep := JobReport{}
	if !e.hasModel() {
		rep.Skipped = "no model"
		return rep
	}
	c := e.w.core
	topics := c.Topics(false)
	sort.Slice(topics, func(i, j int) bool { return topics[i].LastActivity.After(topics[j].LastActivity) })
	written := 0
	for _, tv := range topics {
		if written >= 10 || ctx.Err() != nil {
			break
		}
		page, err := c.Topic(tv.Topic.ID)
		if err != nil || len(page.Notes)+len(page.Tasks)+len(page.DoneTasks) < 2 {
			continue
		}
		rep.Checked++
		// Members fingerprint: ids and revisions.
		var fp []string
		for _, n := range page.Notes {
			fp = append(fp, fmt.Sprintf("%s@%d", n.ID, n.Rev))
		}
		for _, t := range append(page.Tasks, page.DoneTasks...) {
			fp = append(fp, fmt.Sprintf("%s@%d", t.ID, t.Rev))
		}
		sort.Strings(fp)
		hash := strings.Join(fp, ",")
		prev := e.w.state.Summaries[tv.Topic.ID]
		if prev.Hash == hash {
			continue
		}
		type nctx struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Preview string `json:"preview"`
		}
		var nl []nctx
		for i, n := range page.Notes {
			if i >= 15 {
				break
			}
			nl = append(nl, nctx{n.ID, n.NoteTitle, model.Shorten(n.Body.PlainText(), 400)})
		}
		var open, done []string
		for i, t := range page.Tasks {
			if i < 15 {
				open = append(open, t.Text)
			}
		}
		for i, t := range page.DoneTasks {
			if i < 10 {
				done = append(done, t.Text)
			}
		}
		// The user's own summary blocks stay context, never get rewritten.
		var userText []string
		for _, b := range tv.Topic.Summary.Blocks {
			if b.ID != prev.Block {
				userText = append(userText, b.Text)
			}
		}
		payload, _ := json.MarshalIndent(map[string]any{"topic": tv.Topic.Name, "kind": tv.Topic.Kind, "aliases": tv.Topic.Aliases,
			"user_summary": strings.Join(userText, "\n"), "notes": nl, "open_tasks": open, "done_tasks": done}, "", " ")
		var out struct {
			Summary string `json:"summary"`
		}
		err = e.askJSON(ctx, "You maintain a personal knowledge base. Write a short summary of a topic from its notes and tasks: what it is about, the current state, what is open. Two to five plain sentences, no headings, no bullet points, no advice, in the language the notes are written in. Do not repeat the user's own summary; complement it. Do not invent anything the notes do not say. "+untrusted,
			"<topic>\n"+string(payload)+"\n</topic>\nAnswer with JSON: {\"summary\":\"…\"}.",
			"topic_summary", json.RawMessage(`{"type":"object","additionalProperties":false,"required":["summary"],"properties":{"summary":{"type":"string"}}}`), &out)
		if err != nil {
			rep.Error = err.Error()
			break
		}
		text := strings.TrimSpace(out.Summary)
		if text == "" {
			continue
		}
		md := "> [!info] Automatic summary (" + e.now().Format("2 Jan 2006") + "): " + strings.ReplaceAll(text, "\n", " ")
		var sources []string
		for i, n := range page.Notes {
			if i < 20 {
				sources = append(sources, n.ID)
			}
		}
		edit := doc.Edit{Action: "append", Markdown: md, Sources: sources}
		if i := tv.Topic.Summary.Find(prev.Block); prev.Block != "" && i >= 0 && !tv.Topic.Summary.Blocks[i].Pinned {
			edit = doc.Edit{Action: "replace", BlockID: prev.Block, Markdown: md, Sources: sources}
		}
		rec, err := c.Commit(ctx, e.modelActor(), e.cause(), []model.Op{{Op: "topic.update", ID: tv.Topic.ID, ExpectedRev: tv.Topic.Rev, Edits: []doc.Edit{edit}}})
		if err != nil {
			rep.Error = err.Error()
			continue
		}
		rep.Changed++
		rep.TxnIDs = append(rep.TxnIDs, rec.TxnID)
		written++
		// Remember which block is ours.
		if o := e.get(tv.Topic.ID); o != nil {
			for _, b := range o.(*model.Topic).Summary.Blocks {
				if strings.HasPrefix(b.Text, "Automatic summary (") {
					e.w.state.Summaries[tv.Topic.ID] = summaryState{Block: b.ID, At: e.now(), Hash: hash}
				}
			}
		}
	}
	return rep
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
