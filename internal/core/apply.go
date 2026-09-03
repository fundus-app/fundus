package core

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/ids"
	"github.com/fundus-app/fundus/internal/model"
)

// workspace is a copy-on-write overlay over the committed state. Ops mutate
// clones inside it; nothing reaches the state until commit.
type workspace struct {
	st      *state
	changed map[string]model.Object
	removed map[string]bool
	created map[string]bool
	before  map[string]json.RawMessage
	touched []string
	// affected: topics whose membership changed in this transaction (see
	// model.Txn.Affected).
	affected []string
	now      time.Time
	// replay skips validation whose outcome depends on tunables (name
	// normalization, size limits): what was committed once stays valid.
	replay bool
}

func newWorkspace(st *state) *workspace {
	return &workspace{st: st, changed: map[string]model.Object{}, removed: map[string]bool{},
		created: map[string]bool{}, before: map[string]json.RawMessage{}}
}

func (w *workspace) get(id string) (model.Object, bool) {
	if w.removed[id] {
		return nil, false
	}
	if o, ok := w.changed[id]; ok {
		return o, true
	}
	o, ok := w.st.objects[id]
	return o, ok
}

// touch records the before-image of id exactly once.
// affect records topics whose member lists changed so that their pages can
// be refreshed, without treating the topics as written (no before-image, no
// undo conflict).
func (w *workspace) affect(topics ...string) {
	for _, t := range topics {
		if t == "" {
			continue
		}
		dup := false
		for _, a := range w.affected {
			if a == t {
				dup = true
				break
			}
		}
		if !dup {
			w.affected = append(w.affected, t)
		}
	}
}

func (w *workspace) touch(id string) {
	if _, seen := w.before[id]; seen {
		return
	}
	if o, ok := w.st.objects[id]; ok {
		raw, _ := model.Marshal(o)
		w.before[id] = raw
	} else {
		w.before[id] = json.RawMessage("null")
	}
	w.touched = append(w.touched, id)
}

// mutable returns a clone of id registered for modification.
func (w *workspace) mutable(id string, expectedRev int, want model.Type) (model.Object, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: missing id", ErrInvalid)
	}
	o, ok := w.get(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	m := o.GetMeta()
	if want != "" && m.Type != want {
		return nil, fmt.Errorf("%w: %s is a %s, not a %s", ErrInvalid, id, m.Type, want)
	}
	if expectedRev > 0 && m.Rev != expectedRev {
		return nil, fmt.Errorf("%w: %s is at rev %d, expected %d", ErrConflict, id, m.Rev, expectedRev)
	}
	if c, ok := w.changed[id]; ok {
		return c, nil
	}
	w.touch(id)
	c := o.Clone()
	w.changed[id] = c
	return c, nil
}

func (w *workspace) create(obj model.Object) error {
	m := obj.GetMeta()
	if _, exists := w.get(m.ID); exists {
		return fmt.Errorf("%w: %s already exists", ErrInvalid, m.ID)
	}
	w.touch(m.ID)
	m.CreatedAt = w.now
	m.UpdatedAt = w.now
	m.Rev = 0
	w.created[m.ID] = true
	w.changed[m.ID] = obj
	delete(w.removed, m.ID)
	return nil
}

func (w *workspace) remove(id string) error {
	if _, ok := w.get(id); !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	w.touch(id)
	delete(w.changed, id)
	w.removed[id] = true
	return nil
}

// commit moves the overlay into the state and bumps revisions.
func (w *workspace) commit(st *state, txn *model.Txn) {
	for id := range w.removed {
		delete(st.objects, id)
		for k, tid := range st.topicNames {
			if tid == id {
				delete(st.topicNames, k)
			}
		}
	}
	for id, obj := range w.changed {
		m := obj.GetMeta()
		if old, ok := st.objects[id]; ok && !w.created[id] {
			m.Rev = old.GetMeta().Rev + 1
		} else if !w.created[id] {
			// restore of a removed object
			m.Rev = m.Rev + 1
		} else {
			m.Rev = 1
		}
		m.UpdatedAt = txn.At
		st.objects[id] = obj
	}
}

// blockIDs returns a deterministic block-id generator for (txn, op) so that
// replaying the log reproduces identical documents.
func blockIDs(txnID string, opIdx int) func() string {
	n := 0
	return func() string {
		n++
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%d", txnID, opIdx, n)))
		return ids.PrefixBlock + "_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])[:20])
	}
}

func opIndex(txn *model.Txn, op *model.Op) int {
	for i := range txn.Ops {
		if &txn.Ops[i] == op {
			return i
		}
	}
	return -1
}

// apply validates and applies one op. Generated ids are written back into the
// op so the log is replayable.
func (w *workspace) apply(op *model.Op, txn *model.Txn) error {
	w.now = txn.At
	gen := doc.IDGen(blockIDs(txn.ID, opIndex(txn, op)))
	userActor := strings.HasPrefix(txn.Actor, "user:")
	// Model actors only get additive powers: they may create, append, link
	// and complete, but never rewrite user-authored content, clear fields the
	// user set, rename, archive or delete. This is the "information
	// preserving" rule enforced where it cannot be bypassed.
	modelActor := strings.HasPrefix(txn.Actor, "llm:")

	switch op.Op {
	case "capture.create":
		text := strings.TrimSpace(op.Text)
		if text == "" {
			return fmt.Errorf("%w: empty capture", ErrInvalid)
		}
		if len(text) > 200_000 && !w.replay {
			return fmt.Errorf("%w: capture too long", ErrInvalid)
		}
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixCapture)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixCapture); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		src := op.Source
		if src == "" {
			src = "api"
		}
		if op.ConversationID != "" {
			if _, ok := w.get(op.ConversationID); !ok {
				return fmt.Errorf("%w: conversation %s", ErrNotFound, op.ConversationID)
			}
		}
		cap := &model.Capture{Meta: model.Meta{ID: op.ID, Type: model.TypeCapture}, Text: text, Source: src,
			Status: model.CapturePending, ConversationID: op.ConversationID}
		if op.Status != "" {
			if err := validCaptureStatus(op.Status); err != nil {
				return err
			}
			cap.Status = model.CaptureStatus(op.Status)
		}
		return w.create(cap)

	case "capture.set_status":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeCapture)
		if err != nil {
			return err
		}
		cap := o.(*model.Capture)
		if op.Status != "" {
			if err := validCaptureStatus(op.Status); err != nil {
				return err
			}
			if model.CaptureStatus(op.Status) == model.CaptureProcessing && cap.Status != model.CaptureProcessing {
				cap.Attempts++
			}
			cap.Status = model.CaptureStatus(op.Status)
		}
		if op.Result != nil {
			r := *op.Result
			cap.Result = &r
		}
		if op.Answer != nil {
			cap.Answer = *op.Answer
		}
		return nil

	case "note.create":
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixNote)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixNote); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		title := ""
		if op.Title != nil {
			title = strings.TrimSpace(*op.Title)
		}
		if title == "" {
			return fmt.Errorf("%w: note title required", ErrInvalid)
		}
		if len([]rune(title)) > 200 && !w.replay {
			return fmt.Errorf("%w: note title too long", ErrInvalid)
		}
		kind := model.NoteKindNote
		if op.Kind != "" {
			kind = model.NoteKind(op.Kind)
			if kind != model.NoteKindNote && kind != model.NoteKindIdea {
				return fmt.Errorf("%w: note kind %q", ErrInvalid, op.Kind)
			}
		}
		if err := w.checkTopics(op.Topics); err != nil {
			return err
		}
		if err := w.checkOrigins(op.Origins); err != nil {
			return err
		}
		body := doc.ParseMarkdownWith(gen, op.Markdown, op.Origins)
		if err := body.Validate(); err != nil {
			return fmt.Errorf("%w: body: %v", ErrInvalid, err)
		}
		n := &model.Note{Meta: model.Meta{ID: op.ID, Type: model.TypeNote}, Kind: kind, NoteTitle: title, Body: body,
			Topics: dedupe(op.Topics), Origins: dedupe(op.Origins)}
		w.affect(n.Topics...)
		return w.create(n)

	case "note.revise":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeNote)
		if err != nil {
			return err
		}
		n := o.(*model.Note)
		if len(op.Edits) == 0 {
			return fmt.Errorf("%w: no edits", ErrInvalid)
		}
		for i := range op.Edits {
			if len(op.Edits[i].Sources) == 0 && len(op.Origins) > 0 {
				op.Edits[i].Sources = dedupe(op.Origins)
			}
		}
		if err := w.checkOrigins(op.Origins); err != nil {
			return err
		}
		if modelActor {
			for _, e := range op.Edits {
				switch e.Action {
				case "delete", "pin", "unpin":
					return fmt.Errorf("%w: models may not %s blocks", ErrForbidden, e.Action)
				case "replace":
					i := n.Body.Find(e.BlockID)
					if i >= 0 && len(n.Body.Blocks[i].Sources) == 0 {
						return fmt.Errorf("%w: block %s was written by the user; models may only append", ErrForbidden, e.BlockID)
					}
				}
			}
		}
		if err := n.Body.ApplyAllWith(gen, op.Edits, userActor); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		n.Origins = dedupe(append(n.Origins, op.Origins...))
		return nil

	case "note.set_markdown":
		if modelActor {
			return fmt.Errorf("%w: models edit notes block by block (note.revise)", ErrForbidden)
		}
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeNote)
		if err != nil {
			return err
		}
		n := o.(*model.Note)
		if err := w.checkOrigins(op.Origins); err != nil {
			return err
		}
		if err := n.Body.SetMarkdown(gen, op.Markdown, op.Origins, userActor); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		n.Origins = dedupe(append(n.Origins, op.Origins...))
		return nil

	case "note.update":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeNote)
		if err != nil {
			return err
		}
		n := o.(*model.Note)
		if modelActor && (op.Title != nil || op.Kind != "" || len(op.RemoveTopics) > 0) {
			return fmt.Errorf("%w: models may only add links to notes", ErrForbidden)
		}
		if op.Title != nil {
			t := strings.TrimSpace(*op.Title)
			if t == "" {
				return fmt.Errorf("%w: empty title", ErrInvalid)
			}
			n.NoteTitle = t
		}
		if op.Kind != "" {
			k := model.NoteKind(op.Kind)
			if k != model.NoteKindNote && k != model.NoteKindIdea {
				return fmt.Errorf("%w: note kind %q", ErrInvalid, op.Kind)
			}
			n.Kind = k
		}
		if err := w.checkTopics(op.AddTopics); err != nil {
			return err
		}
		n.Topics = dedupe(append(n.Topics, op.AddTopics...))
		n.Topics = without(n.Topics, op.RemoveTopics)
		w.affect(op.AddTopics...)
		w.affect(op.RemoveTopics...)
		for _, r := range op.AddRelated {
			if r == n.ID {
				continue
			}
			if _, ok := w.get(r); !ok || ids.Prefix(r) != ids.PrefixNote {
				return fmt.Errorf("%w: related note %s", ErrNotFound, r)
			}
		}
		n.Related = dedupe(append(n.Related, op.AddRelated...))
		return nil

	case "task.create":
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixTask)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixTask); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		text := strings.TrimSpace(op.Text)
		if text == "" {
			return fmt.Errorf("%w: task text required", ErrInvalid)
		}
		if len([]rune(text)) > 500 && !w.replay {
			return fmt.Errorf("%w: task text too long", ErrInvalid)
		}
		if err := w.checkTopics(op.Topics); err != nil {
			return err
		}
		if err := w.checkOrigins(op.Origins); err != nil {
			return err
		}
		t := &model.Task{Meta: model.Meta{ID: op.ID, Type: model.TypeTask}, Text: text, State: model.TaskOpen,
			Topics: dedupe(op.Topics), Origins: dedupe(op.Origins)}
		w.affect(t.Topics...)
		if op.State != "" {
			if err := validTaskState(op.State); err != nil {
				return err
			}
			t.State = model.TaskState(op.State)
			if t.State == model.TaskDone {
				at := w.now
				t.CompletedAt = &at
			}
		}
		if err := applyTaskFields(t, op, w.now); err != nil {
			return err
		}
		return w.create(t)

	case "task.update":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeTask)
		if err != nil {
			return err
		}
		t := o.(*model.Task)
		if modelActor {
			if err := modelTaskUpdateAllowed(t, op); err != nil {
				return err
			}
		}
		if op.Text != "" {
			text := strings.TrimSpace(op.Text)
			if len([]rune(text)) > 500 && !w.replay {
				return fmt.Errorf("%w: task text too long", ErrInvalid)
			}
			t.Text = text
		}
		if op.State != "" {
			if err := validTaskState(op.State); err != nil {
				return err
			}
			ns := model.TaskState(op.State)
			if ns == model.TaskDone && t.State != model.TaskDone {
				at := w.now
				t.CompletedAt = &at
			}
			if ns != model.TaskDone {
				t.CompletedAt = nil
			}
			t.State = ns
		}
		if err := applyTaskFields(t, op, w.now); err != nil {
			return err
		}
		if err := w.checkTopics(op.AddTopics); err != nil {
			return err
		}
		t.Topics = without(dedupe(append(t.Topics, op.AddTopics...)), op.RemoveTopics)
		w.affect(op.AddTopics...)
		w.affect(op.RemoveTopics...)
		for _, nid := range op.AddNotes {
			if _, ok := w.get(nid); !ok || ids.Prefix(nid) != ids.PrefixNote {
				return fmt.Errorf("%w: note %s", ErrNotFound, nid)
			}
		}
		t.Notes = dedupe(append(t.Notes, op.AddNotes...))
		if op.Mention {
			t.Mentions++
		}
		return nil

	case "topic.create":
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixTopic)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixTopic); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		name := ""
		if op.Name != nil {
			name = strings.TrimSpace(*op.Name)
		}
		if name == "" {
			return fmt.Errorf("%w: topic name required", ErrInvalid)
		}
		if len([]rune(name)) > 120 && !w.replay {
			return fmt.Errorf("%w: topic name too long", ErrInvalid)
		}
		if existing := w.topicByName(name); existing != "" && !w.replay {
			return fmt.Errorf("%w: topic %q already exists as %s", ErrInvalid, name, existing)
		}
		if !w.replay {
			for _, a := range op.Aliases {
				if ex := w.topicByName(a); ex != "" {
					return fmt.Errorf("%w: alias %q already names topic %s", ErrInvalid, a, ex)
				}
			}
		}
		kind := model.TopicKindTopic
		if op.Kind != "" {
			kind = model.TopicKind(op.Kind)
			if kind != model.TopicKindTopic && kind != model.TopicKindPerson && kind != model.TopicKindProject {
				return fmt.Errorf("%w: topic kind %q", ErrInvalid, op.Kind)
			}
		}
		t := &model.Topic{Meta: model.Meta{ID: op.ID, Type: model.TypeTopic}, Kind: kind, Name: name,
			Aliases: cleanAliases(op.Aliases, name), Summary: doc.Document{Blocks: []doc.Block{}}}
		op.Aliases = t.Aliases // the log stores the effective values
		return w.create(t)

	case "topic.update":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeTopic)
		if err != nil {
			return err
		}
		t := o.(*model.Topic)
		if modelActor && (op.Name != nil || op.Kind != "" || op.Aliases != nil) {
			return fmt.Errorf("%w: models may only edit topic summaries", ErrForbidden)
		}
		if modelActor {
			for _, e := range op.Edits {
				switch e.Action {
				case "delete", "pin", "unpin":
					return fmt.Errorf("%w: models may not %s blocks", ErrForbidden, e.Action)
				case "replace":
					i := t.Summary.Find(e.BlockID)
					if i >= 0 && len(t.Summary.Blocks[i].Sources) == 0 {
						return fmt.Errorf("%w: block %s was written by the user; models may only append", ErrForbidden, e.BlockID)
					}
				}
			}
		}
		if op.Name != nil {
			name := strings.TrimSpace(*op.Name)
			if name == "" {
				return fmt.Errorf("%w: empty topic name", ErrInvalid)
			}
			if ex := w.topicByName(name); ex != "" && ex != t.ID && !w.replay {
				return fmt.Errorf("%w: topic %q already exists as %s", ErrInvalid, name, ex)
			}
			t.Name = name
		}
		if op.Kind != "" {
			k := model.TopicKind(op.Kind)
			if k != model.TopicKindTopic && k != model.TopicKindPerson && k != model.TopicKindProject {
				return fmt.Errorf("%w: topic kind %q", ErrInvalid, op.Kind)
			}
			t.Kind = k
		}
		if op.Aliases != nil {
			if !w.replay {
				for _, a := range op.Aliases {
					if ex := w.topicByName(a); ex != "" && ex != t.ID {
						return fmt.Errorf("%w: alias %q already names topic %s", ErrInvalid, a, ex)
					}
				}
			}
			t.Aliases = cleanAliases(op.Aliases, t.Name)
			op.Aliases = t.Aliases
		}
		if len(op.Edits) > 0 {
			if err := t.Summary.ApplyAllWith(gen, op.Edits, userActor); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalid, err)
			}
		}
		return nil

	case "topic.set_summary":
		if modelActor {
			return fmt.Errorf("%w: models edit summaries block by block (topic.update)", ErrForbidden)
		}
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeTopic)
		if err != nil {
			return err
		}
		t := o.(*model.Topic)
		if err := w.checkOrigins(op.Origins); err != nil {
			return err
		}
		if err := t.Summary.SetMarkdown(gen, op.Markdown, op.Origins, userActor); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return nil

	case "topic.merge":
		if modelActor {
			return fmt.Errorf("%w: models may not merge topics", ErrForbidden)
		}
		if op.From == "" || op.From == op.ID {
			return fmt.Errorf("%w: topic.merge needs a different source topic in from", ErrInvalid)
		}
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeTopic)
		if err != nil {
			return err
		}
		into := o.(*model.Topic)
		fo, err := w.mutable(op.From, 0, model.TypeTopic)
		if err != nil {
			return err
		}
		from := fo.(*model.Topic)
		// Relink everything that pointed at the old topic.
		for id, obj := range w.st.objects {
			var topics *[]string
			switch v := obj.(type) {
			case *model.Note:
				topics = &v.Topics
			case *model.Task:
				topics = &v.Topics
			default:
				continue
			}
			if !contains(*topics, from.ID) {
				continue
			}
			mo, err := w.mutable(id, 0, "")
			if err != nil {
				return err
			}
			switch v := mo.(type) {
			case *model.Note:
				v.Topics = dedupe(append(without(v.Topics, []string{from.ID}), into.ID))
			case *model.Task:
				v.Topics = dedupe(append(without(v.Topics, []string{from.ID}), into.ID))
			}
		}
		into.Aliases = cleanAliases(append(append(into.Aliases, from.Name), from.Aliases...), into.Name)
		op.Aliases = into.Aliases
		if len(from.Summary.Blocks) > 0 {
			into.Summary.Blocks = append(into.Summary.Blocks, from.Summary.Clone().Blocks...)
		}
		from.Archived = true
		from.Aliases = nil
		return nil

	case "source.create":
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixSource)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixSource); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if strings.TrimSpace(op.URL) == "" {
			return fmt.Errorf("%w: source url required", ErrInvalid)
		}
		s := &model.Source{Meta: model.Meta{ID: op.ID, Type: model.TypeSource}, URL: strings.TrimSpace(op.URL),
			FetchedAt: w.now, Excerpt: op.Text, Query: op.Query}
		if op.Title != nil {
			s.SrcTitle = strings.TrimSpace(*op.Title)
		}
		return w.create(s)

	case "conversation.create":
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixConv)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixConv); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		cv := &model.Conversation{Meta: model.Meta{ID: op.ID, Type: model.TypeConversation}}
		if op.Title != nil {
			cv.ConvTitle = strings.TrimSpace(*op.Title)
		}
		return w.create(cv)

	case "conversation.update":
		o, err := w.mutable(op.ID, op.ExpectedRev, model.TypeConversation)
		if err != nil {
			return err
		}
		cv := o.(*model.Conversation)
		if op.Title != nil {
			cv.ConvTitle = strings.TrimSpace(*op.Title)
		}
		return nil

	case "message.create", "conversation.append":
		// conversation.append is the pre-0.3.1 spelling (message embedded in
		// the conversation object). Logs are forever, so it stays readable.
		if op.Op == "conversation.append" && op.ConversationID == "" {
			// The legacy op carried no message id; derive one from the
			// transaction so every replay yields the same id.
			op.ConversationID = op.ID
			op.ID = ids.Derived(ids.PrefixMessage, txn.ID+"/"+fmt.Sprint(opIndex(txn, op)))
		}
		if op.Message == nil {
			return fmt.Errorf("%w: message required", ErrInvalid)
		}
		if op.ID == "" {
			op.ID = ids.New(ids.PrefixMessage)
		} else if err := ids.MustHavePrefix(op.ID, ids.PrefixMessage); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		convID := op.ConversationID
		if convID == "" {
			convID = op.Message.ConversationID
		}
		co, err := w.mutable(convID, 0, model.TypeConversation)
		if err != nil {
			return err
		}
		cv := co.(*model.Conversation)
		m := *op.Message
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("%w: message role %q", ErrInvalid, m.Role)
		}
		if m.CaptureID != "" {
			if _, ok := w.get(m.CaptureID); !ok {
				return fmt.Errorf("%w: capture %s", ErrNotFound, m.CaptureID)
			}
		}
		m.Meta = model.Meta{ID: op.ID, Type: model.TypeMessage}
		m.ConversationID = cv.ID
		m.Index = cv.MessageCount
		m.Blocks = doc.ParseMarkdownWith(gen, m.Text, nil)
		m.TxnIDs = dedupe(m.TxnIDs)
		m.Refs = dedupe(m.Refs)
		cv.MessageCount++
		cv.LastMessageAt = w.now
		if cv.ConvTitle == "" && m.Role == "user" {
			cv.ConvTitle = model.Shorten(strings.Join(strings.Fields(m.Blocks.PlainText()), " "), 60)
		}
		if op.Title != nil {
			cv.ConvTitle = strings.TrimSpace(*op.Title)
		}
		return w.create(&m)

	case "object.archive", "object.unarchive":
		if modelActor {
			return fmt.Errorf("%w: models may not archive", ErrForbidden)
		}
		o, err := w.mutable(op.ID, op.ExpectedRev, "")
		if err != nil {
			return err
		}
		m := o.GetMeta()
		if m.Type == model.TypeCapture {
			return fmt.Errorf("%w: captures cannot be archived; dismiss them instead", ErrInvalid)
		}
		if op.Op == "object.unarchive" && !w.replay {
			if t, ok := o.(*model.Topic); ok {
				if ex := w.topicByName(t.Name); ex != "" && ex != t.ID {
					return fmt.Errorf("%w: topic %q already exists as %s; merge instead", ErrInvalid, t.Name, ex)
				}
			}
		}
		m.Archived = op.Op == "object.archive"
		return nil

	case "object.restore":
		if len(op.Object) == 0 {
			return fmt.Errorf("%w: object image required", ErrInvalid)
		}
		obj, err := model.Unmarshal(op.Object)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if obj.GetMeta().ID != op.ID {
			return fmt.Errorf("%w: image id %s does not match %s", ErrInvalid, obj.GetMeta().ID, op.ID)
		}
		w.touch(op.ID)
		if cur, ok := w.get(op.ID); ok {
			// Keep the current revision so commit bumps monotonically.
			obj.GetMeta().Rev = cur.GetMeta().Rev
		}
		delete(w.removed, op.ID)
		w.changed[op.ID] = obj
		return nil

	case "object.remove":
		return w.remove(op.ID)
	}
	return fmt.Errorf("%w: unknown op %q", ErrInvalid, op.Op)
}

func applyTaskFields(t *model.Task, op *model.Op, now time.Time) error {
	if op.Due != nil {
		if *op.Due == "" {
			t.Due = ""
		} else {
			if _, err := time.Parse("2006-01-02", *op.Due); err != nil {
				return fmt.Errorf("%w: due %q is not YYYY-MM-DD", ErrInvalid, *op.Due)
			}
			t.Due = *op.Due
		}
	}
	if op.EffortMinutes != nil {
		if *op.EffortMinutes < 0 || *op.EffortMinutes > 100_000 {
			return fmt.Errorf("%w: effort_minutes out of range", ErrInvalid)
		}
		t.EffortMinutes = *op.EffortMinutes
	}
	if op.Importance != nil {
		if *op.Importance < 0 || *op.Importance > 3 {
			return fmt.Errorf("%w: importance must be 0..3", ErrInvalid)
		}
		t.Importance = *op.Importance
	}
	if op.WaitingOn != nil {
		t.WaitingOn = strings.TrimSpace(*op.WaitingOn)
	}
	return nil
}

func (w *workspace) checkTopics(topics []string) error {
	if w.replay {
		return nil
	}
	for _, id := range topics {
		o, ok := w.get(id)
		if !ok || o.GetMeta().Type != model.TypeTopic {
			return fmt.Errorf("%w: topic %s", ErrNotFound, id)
		}
	}
	return nil
}

func (w *workspace) checkOrigins(origins []string) error {
	for _, id := range origins {
		o, ok := w.get(id)
		if !ok || (o.GetMeta().Type != model.TypeCapture && o.GetMeta().Type != model.TypeSource) {
			return fmt.Errorf("%w: origin %s", ErrNotFound, id)
		}
	}
	return nil
}

// topicByName finds a non-archived topic by normalized name/alias, including
// topics created earlier in the same transaction.
func (w *workspace) topicByName(name string) string {
	n := NormalizeName(name)
	if n == "" {
		return ""
	}
	for _, o := range w.changed {
		if t, ok := o.(*model.Topic); ok && !t.Archived {
			if NormalizeName(t.Name) == n {
				return t.ID
			}
			for _, a := range t.Aliases {
				if NormalizeName(a) == n {
					return t.ID
				}
			}
		}
	}
	if id, ok := w.st.topicNames[n]; ok && !w.removed[id] {
		if _, changed := w.changed[id]; !changed {
			return id
		}
	}
	return ""
}

func validCaptureStatus(s string) error {
	switch model.CaptureStatus(s) {
	case model.CapturePending, model.CaptureProcessing, model.CaptureProcessed, model.CaptureNeedsReview, model.CaptureFailed, model.CaptureDismissed:
		return nil
	}
	return fmt.Errorf("%w: capture status %q", ErrInvalid, s)
}

func validTaskState(s string) error {
	switch model.TaskState(s) {
	case model.TaskOpen, model.TaskWaiting, model.TaskLater, model.TaskDone:
		return nil
	}
	return fmt.Errorf("%w: task state %q", ErrInvalid, s)
}

// cleanAliases dedupes aliases against the name and each other and caps
// their number and length so a single model call cannot hijack name
// resolution for unrelated words.
func cleanAliases(in []string, name string) []string {
	seen := map[string]bool{NormalizeName(name): true}
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(a)
		n := NormalizeName(a)
		if a == "" || n == "" || seen[n] || len([]rune(a)) > 60 {
			continue
		}
		seen[n] = true
		out = append(out, a)
		if len(out) == 8 {
			break
		}
	}
	return out
}

// modelTaskUpdateAllowed enforces the additive-only rule for model actors.
func modelTaskUpdateAllowed(t *model.Task, op *model.Op) error {
	if op.Text != "" && op.Text != t.Text {
		return fmt.Errorf("%w: models may not reword tasks", ErrForbidden)
	}
	if op.Due != nil {
		if *op.Due == "" {
			return fmt.Errorf("%w: models may not clear due dates", ErrForbidden)
		}
		if t.Due != "" && t.Due != *op.Due {
			return fmt.Errorf("%w: models may not move a due date the user set", ErrForbidden)
		}
	}
	if op.Importance != nil && t.Importance != 0 && *op.Importance != t.Importance {
		return fmt.Errorf("%w: models may not change importance once set", ErrForbidden)
	}
	if op.EffortMinutes != nil && t.EffortMinutes != 0 && *op.EffortMinutes != t.EffortMinutes {
		return fmt.Errorf("%w: models may not change effort once set", ErrForbidden)
	}
	if op.State != "" {
		if t.State == model.TaskDone && model.TaskState(op.State) != model.TaskDone {
			return fmt.Errorf("%w: models may not reopen completed tasks", ErrForbidden)
		}
	}
	if len(op.RemoveTopics) > 0 {
		return fmt.Errorf("%w: models may not unlink topics", ErrForbidden)
	}
	if op.WaitingOn != nil && t.WaitingOn != "" && *op.WaitingOn != t.WaitingOn {
		return fmt.Errorf("%w: models may not change what a task waits on", ErrForbidden)
	}
	return nil
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func without(in, remove []string) []string {
	if len(remove) == 0 {
		return in
	}
	rm := map[string]bool{}
	for _, r := range remove {
		rm[r] = true
	}
	var out []string
	for _, s := range in {
		if !rm[s] {
			out = append(out, s)
		}
	}
	return out
}
