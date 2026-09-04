// Package triage turns a capture into validated core operations using an LLM.
//
// The model sees the capture plus retrieved context and answers with one JSON
// document (single shot, no tool loop). The runtime validates the document,
// resolves topic names, maps the model vocabulary onto core ops, applies the
// autonomy policy and commits everything in one transaction. Nothing is
// written when validation fails.
package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/ids"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// Triager processes captures.
type Triager struct {
	core *core.Core
	lg   *slog.Logger
	now  func() time.Time

	mu       sync.RWMutex
	provider llm.Provider
	role     config.Role
	policy   config.Policy
	ready    bool
	search   SearchFunc
}

// New builds a Triager. A nil provider parks the worker until Configure is
// called with a usable provider (first run without a model).
func New(c *core.Core, p llm.Provider, role config.Role, policy config.Policy, lg *slog.Logger) *Triager {
	if lg == nil {
		lg = slog.Default()
	}
	return &Triager{core: c, provider: p, role: role, policy: policy, lg: lg, now: time.Now, ready: p != nil}
}

// Configure swaps provider, role and policy at runtime (settings changed in
// the UI). A nil provider marks the triager as not ready.
// SearchFunc searches the store for context; hybrid when embeddings exist.
type SearchFunc func(ctx context.Context, q string, limit int, types []model.Type, includeAll bool) []core.Hit

// SetSearch replaces the lexical search used to gather context.
func (t *Triager) SetSearch(fn SearchFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.search = fn
}

func (t *Triager) searchFor(ctx context.Context, q string, limit int, types []model.Type) []core.Hit {
	t.mu.RLock()
	fn := t.search
	t.mu.RUnlock()
	if fn != nil {
		return fn(ctx, q, limit, types, false)
	}
	return t.core.Search(q, limit, types, false)
}

func (t *Triager) Configure(p llm.Provider, role config.Role, policy config.Policy) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.provider, t.role, t.policy = p, role, policy
	t.ready = p != nil
}

// Ready reports whether captures can be processed.
func (t *Triager) Ready() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ready
}

func (t *Triager) current() (llm.Provider, config.Role, config.Policy) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.provider, t.role, t.policy
}

// Actor names the triage model in the audit log.
func (t *Triager) Actor() string {
	p, role, _ := t.current()
	name := "none"
	if p != nil {
		name = p.Name()
	}
	return "llm:triage/" + name + "/" + role.Model
}

// ValidationError means the model output was rejected before any write.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "invalid model output: " + e.Msg }

// Process runs the pipeline for one capture. The capture must be pending or
// needs_review (re-triage after an answer). It returns the receipt of the
// committed transaction, or nil when the capture was parked, dismissed or
// yielded nothing to write.
func (t *Triager) Process(ctx context.Context, captureID string) (*model.Receipt, error) {
	obj, err := t.core.Get(captureID)
	if err != nil {
		return nil, err
	}
	cap, ok := obj.(*model.Capture)
	if !ok {
		return nil, fmt.Errorf("%s is not a capture", captureID)
	}
	provider, role, policy := t.current()
	if provider == nil {
		return nil, errors.New("no model configured")
	}
	if _, err := t.core.Commit(ctx, "system", &model.Cause{Kind: "capture", ID: cap.ID},
		[]model.Op{{Op: "capture.set_status", ID: cap.ID, ExpectedRev: cap.Rev, Status: string(model.CaptureProcessing)}}); err != nil {
		return nil, err
	}
	// Everything below carries the capture's revision so that a dismiss or
	// retry issued by the user while the model thinks wins over us.
	obj, _ = t.core.Get(cap.ID)
	cap = obj.(*model.Capture)
	rev := cap.Rev

	res, plan, providerName, modelName, err := t.ask(ctx, cap, provider, role, policy)
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown, not a model problem: hand the capture back to the queue.
			_, _ = t.core.Commit(context.Background(), "system", &model.Cause{Kind: "capture", ID: cap.ID},
				[]model.Op{{Op: "capture.set_status", ID: cap.ID, ExpectedRev: rev, Status: string(model.CapturePending)}})
			return nil, err
		}
		t.fail(ctx, cap.ID, rev, err, providerName, modelName)
		return nil, err
	}
	result := &model.CaptureResult{Classification: res.Classification, Confidence: res.Confidence, Summary: res.Summary,
		Question: res.Question, Provider: providerName, Model: modelName, ProcessedAt: t.now().UTC()}
	if len(res.Operations) > 0 {
		result.Proposal, _ = json.Marshal(res.Operations)
	}

	setStatus := func(status model.CaptureStatus) (*model.Receipt, error) {
		_, err := t.core.Commit(ctx, t.Actor(), &model.Cause{Kind: "capture", ID: cap.ID},
			[]model.Op{{Op: "capture.set_status", ID: cap.ID, ExpectedRev: rev, Status: string(status), Result: result}})
		return nil, t.userWon(cap.ID, rev, err)
	}
	park := func(reason string) (*model.Receipt, error) {
		result.Reason = reason
		return setStatus(model.CaptureNeedsReview)
	}
	switch {
	case res.Classification == "discard":
		result.Reason = "discard"
		return setStatus(model.CaptureDismissed)
	case res.Classification == "unclear":
		if result.Question == "" {
			result.Question = "What did you mean?"
		}
		return park("unclear")
	case res.Confidence < policy.MinConfidence:
		// The model's summary says what it would have done; the client
		// explains that it was parked for being unsure.
		result.Question = ""
		return park("low_confidence")
	case len(plan) == 0:
		result.Proposal = nil
		return setStatus(model.CaptureProcessed)
	case !policy.AutoCreate:
		result.Question = ""
		return park("proposal")
	}
	ops := append(plan, model.Op{Op: "capture.set_status", ID: cap.ID, ExpectedRev: rev, Status: string(model.CaptureProcessed), Result: result})
	rec, err := t.core.Commit(ctx, t.Actor(), &model.Cause{Kind: "capture", ID: cap.ID}, ops)
	if err != nil && errors.Is(err, core.ErrConflict) && !t.captureChanged(cap.ID, rev) {
		// Another object moved under us (a user edit during the model call).
		// The model's plan is still valid; rebuild it against fresh state once.
		plan2, perr := Plan(t.core, policy, cap.ID, cap.Text, res.Classification, res.Operations, nil)
		if perr == nil {
			ops = append(plan2, model.Op{Op: "capture.set_status", ID: cap.ID, ExpectedRev: rev, Status: string(model.CaptureProcessed), Result: result})
			rec, err = t.core.Commit(ctx, t.Actor(), &model.Cause{Kind: "capture", ID: cap.ID}, ops)
		}
	}
	if err != nil {
		if uerr := t.userWon(cap.ID, rev, err); uerr == nil {
			return nil, nil
		}
		verr := &ValidationError{Msg: err.Error()}
		t.fail(ctx, cap.ID, rev, verr, providerName, modelName)
		return nil, verr
	}
	return rec, nil
}

// captureChanged reports whether the capture left the revision we hold, i.e.
// the user retried or dismissed it while we were working.
func (t *Triager) captureChanged(id string, rev int) bool {
	obj, err := t.core.Get(id)
	if err != nil {
		return true
	}
	return obj.GetMeta().Rev != rev
}

// userWon turns a conflict caused by the capture having moved on (the user
// retried or dismissed it meanwhile) into a silent no-op: the user's intent
// supersedes the model's.
func (t *Triager) userWon(id string, rev int, err error) error {
	if err != nil && errors.Is(err, core.ErrConflict) && t.captureChanged(id, rev) {
		t.lg.Info("capture changed during triage; model result discarded", "capture", id)
		return nil
	}
	return err
}

func (t *Triager) fail(ctx context.Context, id string, rev int, cause error, provider, modelName string) {
	res := &model.CaptureResult{Error: classifyError(cause), Retryable: retryable(cause), Provider: provider, Model: modelName, ProcessedAt: t.now().UTC()}
	t.lg.Debug("triage failure detail", "capture", id, "err", cause)
	if _, err := t.core.Commit(ctx, "system", &model.Cause{Kind: "capture", ID: id},
		[]model.Op{{Op: "capture.set_status", ID: id, ExpectedRev: rev, Status: string(model.CaptureFailed), Result: res}}); err != nil {
		if t.userWon(id, rev, err) != nil {
			t.lg.Error("could not record triage failure", "capture", id, "err", err)
		}
	}
}

// retryable is true for transient provider problems.
func retryable(err error) bool {
	var v *ValidationError
	if errors.As(err, &v) {
		return false
	}
	return llm.Retryable(err) || errors.Is(err, context.DeadlineExceeded)
}

// ask builds the context, calls the model, parses the result and plans the
// core ops. One corrective round trip is attempted when the output is
// structurally invalid or references objects that do not exist; nothing is
// written in any of these steps.
func (t *Triager) ask(ctx context.Context, cap *model.Capture, provider llm.Provider, role config.Role, policy config.Policy) (*Result, []model.Op, string, string, error) {
	tctx := t.buildContext(ctx, cap)
	shown := tctx.ids()
	req := &llm.Request{Model: role.Model, System: systemPrompt, MaxTokens: role.MaxTokens,
		Temperature: role.Temperature, ReasoningEffort: role.ReasoningEffort,
		Schema:   &llm.Schema{Name: SchemaName, Schema: Schema},
		Messages: []llm.Message{{Role: "user", Content: userMessage(tctx)}}}
	if req.MaxTokens == 0 {
		req.MaxTokens = 4000
	}
	timeout := role.Timeout.Duration
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	var lastErr error
	attempts := role.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	corrected := false
	for attempt := 0; attempt < attempts; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := provider.Complete(cctx, req)
		cancel()
		if err != nil {
			lastErr = err
			if !llm.Retryable(err) || ctx.Err() != nil {
				break
			}
			sleep(ctx, time.Duration(attempt+1)*2*time.Second)
			continue
		}
		res, perr := parseResult(resp.Content)
		var plan []model.Op
		if perr == nil {
			plan, perr = Plan(t.core, policy, cap.ID, cap.Text, res.Classification, res.Operations, shown)
		}
		if perr == nil {
			return res, plan, provider.Name(), firstNonEmpty(resp.Model, role.Model), nil
		}
		lastErr = perr
		if corrected {
			break
		}
		// Feed the error back once; still no write happened.
		corrected = true
		req.Messages = append(req.Messages,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: "That output was invalid: " + perr.Error() + "\nRespond again with a corrected JSON object only. Only use ids that appear in the context."})
		attempt-- // the corrective turn does not count as a retry
	}
	if lastErr == nil {
		lastErr = errors.New("no response")
	}
	return nil, nil, provider.Name(), role.Model, lastErr
}

func (t *Triager) buildContext(ctx context.Context, cap *model.Capture) *Context {
	loc := t.core.Location()
	now := t.now().In(loc)
	tctx := &Context{Now: now.Format("2006-01-02 15:04 MST"), Weekday: now.Weekday().String(), Answer: cap.Answer,
		Capture: CaptureCtx{ID: cap.ID, Text: cap.Text, Source: cap.Source, At: cap.CreatedAt.In(loc).Format("2006-01-02 15:04")}}
	if cap.Result != nil && cap.Answer != "" {
		tctx.Question = cap.Result.Question
	}
	for _, tp := range t.core.TopicsMentionedIn(cap.Text) {
		tctx.MentionedTopics = append(tctx.MentionedTopics, topicCtx(tp))
	}
	topics := t.core.Topics(false)
	if len(topics) > 300 {
		topics = topics[:300]
	}
	for _, tv := range topics {
		tctx.Topics = append(tctx.Topics, topicCtx(tv.Topic))
	}
	sort.Slice(tctx.Topics, func(i, j int) bool { return tctx.Topics[i].Name < tctx.Topics[j].Name })
	query := cap.Text
	if cap.Answer != "" {
		query += " " + cap.Answer
	}
	for _, h := range t.searchFor(ctx, query, 8, []model.Type{model.TypeNote}) {
		n := h.Object.(*model.Note)
		tctx.Notes = append(tctx.Notes, NoteCtx{ID: n.ID, Kind: string(n.Kind), Title: n.NoteTitle,
			Preview: model.Shorten(n.Body.PlainText(), 300), Topics: n.Topics, Updated: n.UpdatedAt.In(t.core.Location()).Format("2006-01-02")})
	}
	seen := map[string]bool{}
	for _, h := range t.searchFor(ctx, query, 8, []model.Type{model.TypeTask}) {
		tk := h.Object.(*model.Task)
		if tk.State == model.TaskDone {
			continue
		}
		seen[tk.ID] = true
		tctx.Tasks = append(tctx.Tasks, TaskCtx{ID: tk.ID, Text: tk.Text, State: string(tk.State), Due: tk.Due, Topics: tk.Topics})
	}
	// Also show notes and tasks linked to mentioned topics, so "I did X" can
	// complete a task and new facts land in the right note even when the
	// wording does not overlap.
	seenNotes := map[string]bool{}
	for _, n := range tctx.Notes {
		seenNotes[n.ID] = true
	}
	for _, mt := range tctx.MentionedTopics {
		page, err := t.core.Topic(mt.ID)
		if err != nil {
			continue
		}
		for _, tv := range page.Tasks {
			if tv.State != model.TaskDone && !seen[tv.ID] && len(tctx.Tasks) < 20 {
				seen[tv.ID] = true
				tctx.Tasks = append(tctx.Tasks, TaskCtx{ID: tv.ID, Text: tv.Text, State: string(tv.State), Due: tv.Due, Topics: tv.Topics})
			}
		}
		for _, n := range page.Notes {
			if !seenNotes[n.ID] && len(tctx.Notes) < 16 {
				seenNotes[n.ID] = true
				tctx.Notes = append(tctx.Notes, NoteCtx{ID: n.ID, Kind: string(n.Kind), Title: n.NoteTitle,
					Preview: model.Shorten(n.Body.PlainText(), 300), Topics: n.Topics, Updated: n.UpdatedAt.In(t.core.Location()).Format("2006-01-02")})
			}
		}
	}
	if tctx.Notes == nil {
		tctx.Notes = []NoteCtx{}
	}
	if tctx.Tasks == nil {
		tctx.Tasks = []TaskCtx{}
	}
	if tctx.Topics == nil {
		tctx.Topics = []TopicCtx{}
	}
	return tctx
}

// parseResult decodes and structurally validates the model output.
func parseResult(content string) (*Result, error) {
	raw := llm.ExtractJSON(content)
	if raw == "" {
		return nil, &ValidationError{Msg: "no JSON object in response"}
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var res Result
	if err := dec.Decode(&res); err != nil {
		return nil, &ValidationError{Msg: err.Error()}
	}
	if !contains(Classifications, res.Classification) {
		return nil, &ValidationError{Msg: fmt.Sprintf("classification %q unknown", res.Classification)}
	}
	if res.Confidence < 0 || res.Confidence > 1 {
		return nil, &ValidationError{Msg: "confidence out of range"}
	}
	if strings.TrimSpace(res.Summary) == "" {
		return nil, &ValidationError{Msg: "summary missing"}
	}
	if res.Classification == "unclear" {
		if strings.TrimSpace(res.Question) == "" {
			return nil, &ValidationError{Msg: "unclear classification requires a question"}
		}
		res.Operations = nil
	}
	if res.Classification == "discard" {
		res.Operations = nil
	}
	for i, op := range res.Operations {
		switch op.Op {
		case "note.create":
			if strings.TrimSpace(op.Title) == "" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: note.create needs a title", i)}
			}
			if op.Kind != "" && op.Kind != "note" && op.Kind != "idea" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: note kind %q", i, op.Kind)}
			}
		case "note.append":
			if op.NoteID == "" || strings.TrimSpace(op.Markdown) == "" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: note.append needs note_id and markdown", i)}
			}
		case "task.create":
			if strings.TrimSpace(op.Text) == "" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: task.create needs text", i)}
			}
			if op.Kind != "" && op.Kind != string(model.TaskKindResearch) {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: task kind %q", i, op.Kind)}
			}
			if op.State != "" && op.State != "open" && op.State != "later" && op.State != "waiting" && op.State != "done" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: task state %q", i, op.State)}
			}
		case "task.complete", "task.mention", "task.update":
			if op.TaskID == "" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: %s needs task_id", i, op.Op)}
			}
			if op.State != "" && op.State != "open" && op.State != "later" && op.State != "waiting" && op.State != "done" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: task state %q", i, op.State)}
			}
		case "link":
			if (op.NoteID == "") == (op.TaskID == "") {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: link needs exactly one of note_id or task_id", i)}
			}
			if len(op.Topics) == 0 {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: link needs topics", i)}
			}
		case "topic.create":
			if strings.TrimSpace(op.Name) == "" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: topic.create needs name", i)}
			}
			if op.Kind != "" && op.Kind != "topic" && op.Kind != "person" && op.Kind != "project" {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: topic kind %q", i, op.Kind)}
			}
		default:
			return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: unknown op %q", i, op.Op)}
		}
		if op.Due != nil && *op.Due != "" {
			if _, err := time.Parse("2006-01-02", *op.Due); err != nil {
				return nil, &ValidationError{Msg: fmt.Sprintf("operation %d: due %q is not YYYY-MM-DD", i, *op.Due)}
			}
		}
	}
	return &res, nil
}

// Plan maps the model vocabulary onto core ops, resolving topic names to ids
// (creating topics when needed) and attaching provenance to originID (a
// capture). It checks that every referenced object exists so that Commit
// cannot half-apply. fallbackText is used as note body when the model gives
// none. It is shared by triage, chat and proposal acceptance.
//
// shown lists the object ids the model was offered; note.append, link and
// task.* may only reference those, so injected text cannot target objects by id.
// A nil map means no restriction (a user accepted the operations).
func Plan(c *core.Core, policy config.Policy, originID, fallbackText, classification string, operations []Operation, shown map[string]bool) ([]model.Op, error) {
	if len(operations) > policy.MaxOpsPerCapture && policy.MaxOpsPerCapture > 0 {
		return nil, &ValidationError{Msg: fmt.Sprintf("%d operations exceed the limit of %d", len(operations), policy.MaxOpsPerCapture)}
	}
	allowed := func(id string) error {
		if shown != nil && !shown[id] {
			return &ValidationError{Msg: fmt.Sprintf("%s was not part of the context offered to the model", id)}
		}
		return nil
	}
	var ops []model.Op
	newTopics := map[string]string{}     // normalized name -> id created in this plan
	newTopicNames := map[string]string{} // id -> display name, for the catch-up below
	inPlan := map[string]bool{}          // topic ids created in this plan: always plausible
	newNotes := map[string]string{}      // normalized title -> note id created in this plan
	newTopicOps := 0
	maxNew := policy.MaxNewTopicsPerCapture
	createTopic := func(name, kind string, aliases []string) (string, bool) {
		norm := core.NormalizeName(name)
		if norm == "" {
			return "", false
		}
		if id, ok := newTopics[norm]; ok {
			return id, true
		}
		if maxNew > 0 && newTopicOps >= maxNew {
			return "", false
		}
		id := newTopicID()
		newTopics[norm] = id
		newTopicNames[id] = name
		inPlan[id] = true
		newTopicOps++
		n := name
		ops = append(ops, model.Op{Op: "topic.create", ID: id, Name: &n, Kind: kind, Aliases: aliases})
		return id, true
	}
	resolveTopics := func(names []string) ([]string, error) {
		var out []string
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if tp, ok := c.FindTopic(n); ok {
				out = append(out, tp.ID)
				continue
			}
			if ids.Valid(n) {
				// An id that is not a known topic: either a stale topic id or,
				// as seen live, a note id the model put into "topics". Never
				// turn it into a topic name; let the corrective round fix it.
				return nil, &ValidationError{Msg: fmt.Sprintf("%s is not a topic", n)}
			}
			if id, ok := createTopic(n, "", nil); ok {
				out = append(out, id)
			}
		}
		return dedupe(out), nil
	}
	for _, op := range operations {
		if op.Op == "topic.create" {
			name := strings.TrimSpace(op.Name)
			if _, ok := c.FindTopic(name); ok {
				continue // already exists; harmless
			}
			createTopic(name, op.Kind, op.Aliases)
		}
	}
	for _, op := range operations {
		switch op.Op {
		case "note.create":
			topics, err := resolveTopics(op.Topics)
			if err != nil {
				return nil, err
			}
			topics = plausibleTopics(c, topics, inPlan, fallbackText+"\n"+op.Title+"\n"+op.Markdown)
			title := trimSentence(op.Title)
			md := strings.TrimSpace(op.Markdown)
			if md == "" {
				md = fallbackText
			}
			// A note with the same title already exists: append instead of
			// creating a duplicate.
			if existing := findNoteByTitle(c, title); existing != nil {
				ops = append(ops, model.Op{Op: "note.revise", ID: existing.ID, ExpectedRev: existing.Rev, Origins: []string{originID},
					Edits: []doc.Edit{{Action: "append", Markdown: md, Sources: []string{originID}}}})
				if len(topics) > 0 {
					ops = append(ops, model.Op{Op: "note.update", ID: existing.ID, ExpectedRev: existing.Rev, AddTopics: topics})
				}
				continue
			}
			if id, dup := newNotes[core.NormalizeName(title)]; dup {
				ops = append(ops, model.Op{Op: "note.revise", ID: id, Origins: []string{originID},
					Edits: []doc.Edit{{Action: "append", Markdown: md, Sources: []string{originID}}}})
				continue
			}
			kind := op.Kind
			if kind == "" {
				if classification == "idea" {
					kind = "idea"
				} else {
					kind = "note"
				}
			}
			noteID := ids.New(ids.PrefixNote)
			newNotes[core.NormalizeName(title)] = noteID
			ops = append(ops, model.Op{Op: "note.create", ID: noteID, Kind: kind, Title: &title, Markdown: md, Topics: topics, Origins: []string{originID}})
		case "note.append":
			if err := allowed(op.NoteID); err != nil {
				return nil, err
			}
			obj, err := c.Get(op.NoteID)
			if err != nil {
				return nil, &ValidationError{Msg: fmt.Sprintf("note %s does not exist", op.NoteID)}
			}
			n, ok := obj.(*model.Note)
			if !ok {
				return nil, &ValidationError{Msg: fmt.Sprintf("%s is not a note", op.NoteID)}
			}
			topics, err := resolveTopics(op.Topics)
			if err != nil {
				return nil, err
			}
			topics = plausibleTopics(c, topics, inPlan, fallbackText+"\n"+n.NoteTitle+"\n"+op.Markdown)
			ops = append(ops, model.Op{Op: "note.revise", ID: n.ID, ExpectedRev: n.Rev, Origins: []string{originID},
				Edits: []doc.Edit{{Action: "append", Markdown: strings.TrimSpace(op.Markdown), Sources: []string{originID}}}})
			if len(topics) > 0 {
				ops = append(ops, model.Op{Op: "note.update", ID: n.ID, ExpectedRev: n.Rev, AddTopics: topics})
			}
		case "task.create":
			topics, err := resolveTopics(op.Topics)
			if err != nil {
				return nil, err
			}
			topics = plausibleTopics(c, topics, inPlan, fallbackText+"\n"+op.Text)
			o := model.Op{Op: "task.create", Text: trimSentence(op.Text), Topics: topics, Origins: []string{originID},
				Due: op.Due, EffortMinutes: op.EffortMinutes, Importance: op.Importance, State: op.State}
			// The classification decides what the task is, not its wording and
			// not a per-op flag (small models sprinkle "research" on ordinary
			// tasks): a research request in any language becomes a research task.
			if classification == "research" {
				o.Kind = string(model.TaskKindResearch)
				o.Text = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(o.Text, "Research:"), "Recherche:"))
			}
			if op.WaitingOn != "" {
				w := op.WaitingOn
				o.WaitingOn = &w
			}
			ops = append(ops, o)
		case "link":
			topics, err := resolveTopics(op.Topics)
			if err != nil {
				return nil, err
			}
			if len(topics) == 0 {
				return nil, &ValidationError{Msg: "link needs at least one topic"}
			}
			switch {
			case op.NoteID != "" && op.TaskID == "":
				if err := allowed(op.NoteID); err != nil {
					return nil, err
				}
				obj, err := c.Get(op.NoteID)
				if err != nil {
					return nil, &ValidationError{Msg: fmt.Sprintf("note %s does not exist", op.NoteID)}
				}
				n, ok := obj.(*model.Note)
				if !ok {
					return nil, &ValidationError{Msg: fmt.Sprintf("%s is not a note", op.NoteID)}
				}
				if topics = plausibleTopics(c, topics, inPlan, fallbackText+"\n"+n.NoteTitle+"\n"+n.Body.PlainText()); len(topics) > 0 {
					ops = append(ops, model.Op{Op: "note.update", ID: n.ID, ExpectedRev: n.Rev, AddTopics: topics})
				}
			case op.TaskID != "" && op.NoteID == "":
				if err := allowed(op.TaskID); err != nil {
					return nil, err
				}
				obj, err := c.Get(op.TaskID)
				if err != nil {
					return nil, &ValidationError{Msg: fmt.Sprintf("task %s does not exist", op.TaskID)}
				}
				tk, ok := obj.(*model.Task)
				if !ok {
					return nil, &ValidationError{Msg: fmt.Sprintf("%s is not a task", op.TaskID)}
				}
				if topics = plausibleTopics(c, topics, inPlan, fallbackText+"\n"+tk.Text); len(topics) > 0 {
					ops = append(ops, model.Op{Op: "task.update", ID: tk.ID, ExpectedRev: tk.Rev, AddTopics: topics})
				}
			default:
				return nil, &ValidationError{Msg: "link needs exactly one of note_id or task_id"}
			}
		case "task.complete", "task.mention", "task.update":
			if err := allowed(op.TaskID); err != nil {
				return nil, err
			}
			obj, err := c.Get(op.TaskID)
			if err != nil {
				return nil, &ValidationError{Msg: fmt.Sprintf("task %s does not exist", op.TaskID)}
			}
			tk, ok := obj.(*model.Task)
			if !ok {
				return nil, &ValidationError{Msg: fmt.Sprintf("%s is not a task", op.TaskID)}
			}
			o := model.Op{Op: "task.update", ID: tk.ID, ExpectedRev: tk.Rev}
			switch op.Op {
			case "task.complete":
				o.State = string(model.TaskDone)
			case "task.mention":
				o.Mention = true
			case "task.update":
				// Additive only: the core rejects rewording, clearing or
				// moving user-set fields for model actors anyway; the
				// vocabulary just never offers them.
				o.State = op.State
				if op.Due != nil && *op.Due != "" {
					o.Due = op.Due
				}
				o.EffortMinutes = op.EffortMinutes
				o.Importance = op.Importance
				if op.WaitingOn != "" {
					w := op.WaitingOn
					o.WaitingOn = &w
				}
				topics, err := resolveTopics(op.Topics)
				if err != nil {
					return nil, err
				}
				o.AddTopics = topics
			}
			ops = append(ops, o)
		}
	}
	ops = catchUp(c, ops, newTopicNames, shown)
	// Topic creations must precede their use: move them to the front.
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].Op == "topic.create" && ops[j].Op != "topic.create" })
	return ops, nil
}

// catchUp attaches a topic created in this plan to the objects that name it:
// the notes and tasks created alongside it, and the shown objects from earlier
// captures. A topic usually appears only on the second or third capture about
// a subject, and models link the earlier objects unreliably; this makes the
// catch-up deterministic. It only adds links (additive, undoable) and only for
// objects the model was shown, so injected text cannot reach further.
func catchUp(c *core.Core, ops []model.Op, newTopics map[string]string, shown map[string]bool) []model.Op {
	if len(newTopics) == 0 {
		return ops
	}
	has := func(topics []string, id string) bool {
		for _, t := range topics {
			if t == id {
				return true
			}
		}
		return false
	}
	for id, name := range newTopics {
		// Objects created in the same plan.
		for i := range ops {
			o := &ops[i]
			switch o.Op {
			case "note.create":
				if o.Title != nil && mentionsName(*o.Title, name) && !has(o.Topics, id) {
					o.Topics = append(o.Topics, id)
				}
			case "task.create":
				if mentionsName(o.Text, name) && !has(o.Topics, id) {
					o.Topics = append(o.Topics, id)
				}
			}
		}
		// Objects the model was shown.
		for objID := range shown {
			obj, err := c.Get(objID)
			if err != nil {
				continue
			}
			var text, updateOp string
			var current []string
			var rev int
			switch o := obj.(type) {
			case *model.Note:
				text, updateOp, current, rev = o.NoteTitle, "note.update", o.Topics, o.Rev
			case *model.Task:
				if o.State == model.TaskDone {
					continue
				}
				text, updateOp, current, rev = o.Text, "task.update", o.Topics, o.Rev
			default:
				continue
			}
			if !mentionsName(text, name) || has(current, id) {
				continue
			}
			merged := false
			for i := range ops {
				if ops[i].ID == objID && ops[i].Op == updateOp {
					if !has(ops[i].AddTopics, id) {
						ops[i].AddTopics = append(ops[i].AddTopics, id)
					}
					merged = true
					break
				}
			}
			if !merged {
				ops = append(ops, model.Op{Op: updateOp, ID: objID, ExpectedRev: rev, AddTopics: []string{id}})
			}
		}
	}
	return ops
}

// mentionsName reports whether text contains name as a whole word, ignoring
// case. Names shorter than three letters never match.
func mentionsName(text, name string) bool {
	if len([]rune(strings.TrimSpace(name))) < 3 {
		return false
	}
	return mentionsWord(text, name)
}

// mentionsWord is mentionsName without the length rule.
func mentionsWord(text, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	low := strings.ToLower(text)
	for start := 0; ; {
		i := strings.Index(low[start:], name)
		if i < 0 {
			return false
		}
		i += start
		before := i == 0 || !isLetter(lastRune(low[:i]))
		after := i+len(name) >= len(low) || !isLetter(firstRune(low[i+len(name):]))
		if before && after {
			return true
		}
		start = i + len(name)
	}
}

func isLetter(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// findNoteByTitle returns a live note whose normalized title equals title.
func findNoteByTitle(c *core.Core, title string) *model.Note {
	want := core.NormalizeName(title)
	if want == "" {
		return nil
	}
	var found *model.Note
	c.Each([]model.Type{model.TypeNote}, func(o model.Object) bool {
		n := o.(*model.Note)
		if !n.Archived && core.NormalizeName(n.NoteTitle) == want {
			found = n
			return false
		}
		return true
	})
	return found
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// classifyError keeps a short, stable description of a failure for the
// durable record; raw provider bodies only go to the debug log.
func classifyError(err error) string {
	var e *llm.Error
	if errors.As(err, &e) {
		switch {
		case e.Status == 401 || e.Status == 403:
			return "The " + e.Provider + " key was rejected. Check it in Settings."
		case e.Status == 404:
			return "The model was not found at " + e.Provider + "."
		case e.Status == 429:
			return e.Provider + " is rate limiting. Fundus will retry."
		case e.Status >= 500:
			return fmt.Sprintf("%s reported an error (HTTP %d). Fundus will retry.", e.Provider, e.Status)
		case e.Status >= 400:
			return fmt.Sprintf("%s rejected the request (HTTP %d): %s", e.Provider, e.Status, model.Shorten(e.Message, 120))
		default:
			return "Could not reach " + e.Provider + ": " + model.Shorten(e.Message, 120)
		}
	}
	var v *ValidationError
	if errors.As(err, &v) {
		return model.Shorten(v.Error(), 300)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The model did not answer in time. Fundus will retry."
	}
	return model.Shorten(err.Error(), 200)
}

// trimSentence trims whitespace and one trailing full stop: titles and task
// texts are noun phrases, and models tend to copy the capture's final period
// into them, which then collides with the receipt's own punctuation.
func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "..") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	}
	return s
}

// plausibleTopics keeps, from a model's topic choice, the topics the text
// gives evidence for: topics created in this same plan (the model named them
// for this capture) and existing topics whose name or alias the text mentions
// or shares a significant word with. Small models attach unrelated topics on a
// whim ("UI updates" → "RPG mit Godot Engine"); a wrong link misleads, a
// missing one is one click away.
func plausibleTopics(c *core.Core, topics []string, inPlan map[string]bool, evidence string) []string {
	if len(topics) == 0 {
		return topics
	}
	words := significantTokens(evidence)
	var out []string
	for _, id := range topics {
		if inPlan[id] {
			out = append(out, id)
			continue
		}
		obj, err := c.Get(id)
		if err != nil {
			continue
		}
		tp, ok := obj.(*model.Topic)
		if !ok {
			continue
		}
		if topicEvidenced(tp, evidence, words) {
			out = append(out, id)
		}
	}
	return out
}

func topicEvidenced(tp *model.Topic, evidence string, words map[string]bool) bool {
	// Aliases are deliberate abbreviations ("PV"), so they match as whole
	// words at any length; the name needs three letters like everywhere else.
	for _, a := range tp.Aliases {
		if mentionsWord(evidence, a) {
			return true
		}
	}
	names := append([]string{tp.Name}, tp.Aliases...)
	for _, n := range names {
		if mentionsName(evidence, n) {
			return true
		}
		for t := range significantTokens(n) {
			if words[t] {
				return true
			}
			// German compounds: "Heizung" evidences "Heizungsdaten".
			if len([]rune(t)) >= 5 {
				for w := range words {
					if strings.HasPrefix(w, t) {
						return true
					}
				}
			}
		}
	}
	return false
}

// significantTokens lowercases and splits text into words of four or more
// letters that are not function words.
func significantTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 4 {
			continue
		}
		skip := false
		for _, stop := range stopwords {
			if stop[w] {
				skip = true
				break
			}
		}
		if !skip {
			out[w] = true
		}
	}
	return out
}
