package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/chat"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
	"github.com/fundus-app/fundus/internal/setup"
	"github.com/fundus-app/fundus/internal/triage"
	"github.com/fundus-app/fundus/internal/webui"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	warnings := append([]string(nil), s.Warnings...)
	if s.core.Recovery() != nil {
		warnings = append(warnings, "The data log was damaged at the end. Fundus kept a copy of the damaged part and continued.")
	}
	if warnings == nil {
		warnings = []string{}
	}
	cfg := s.config()
	if cfg.SetupNeeded() {
		warnings = append(warnings, "No model connected yet. Captures wait in the inbox until you connect one.")
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "version": Version, "seq": s.core.Seq(),
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"triage":         cfg.Triage.Provider + "/" + cfg.Triage.Model,
		"chat":           cfg.Chat.Provider + "/" + cfg.Chat.Model,
		"configured":     map[string]string{"triage": cfg.Triage.Provider + "/" + cfg.Triage.Model, "chat": cfg.Chat.Provider + "/" + cfg.Chat.Model},
		"setup_needed":   cfg.SetupNeeded(),
		"instance":       s.core.InstanceID(),
		"timezone":       s.core.Location().String(),
		"ui":             webui.Built(),
		"dictation":      cfg.DictationAvailable(),
		"recovery":       s.core.Recovery(),
		"warnings":       warnings,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.core.Stats())
}

// ---------------------------------------------------------------------------
// Captures

type captureRequest struct {
	ID             string `json:"id,omitempty"`
	Text           string `json:"text"`
	Source         string `json:"source,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	var req captureRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, 400, "bad_request", "text is required")
		return
	}
	// Client-supplied ids make retries idempotent.
	if req.ID != "" {
		if obj, err := s.core.Get(req.ID); err == nil {
			cap, ok := obj.(*model.Capture)
			if !ok {
				writeError(w, http.StatusConflict, "conflict", req.ID+" exists and is not a capture")
				return
			}
			writeJSON(w, 200, s.captureWithReceipts(cap))
			return
		}
	}
	src := clientName(req.Source)
	if src == "" {
		src = strings.TrimPrefix(actorFor(r), "user:")
	}
	// Subscribe before committing so the processed event cannot be missed.
	wait := time.Duration(intParam(r, "wait", 0)) * time.Millisecond
	if wait > 60*time.Second {
		wait = 60 * time.Second
	}
	var events <-chan core.Event
	var cancel func()
	if wait > 0 {
		events, cancel = s.core.Subscribe()
		if events != nil {
			defer cancel()
		}
	}
	ops := []model.Op{{Op: "capture.create", ID: req.ID, Text: req.Text, Source: src, ConversationID: req.ConversationID}}
	if _, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "user"}, ops); err != nil {
		writeCoreError(w, err)
		return
	}
	if s.worker != nil {
		s.worker.Kick()
	}
	id := ops[0].ID
	if events != nil {
		deadline := time.NewTimer(wait)
		defer deadline.Stop()
	waiting:
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					break waiting
				}
				if cap, isCap := ev.Payload.(*model.Capture); isCap && cap.ID == id &&
					cap.Status != model.CapturePending && cap.Status != model.CaptureProcessing {
					break waiting
				}
			case <-deadline.C:
				break waiting
			case <-r.Context().Done():
				break waiting
			}
		}
	}
	obj, err := s.core.Get(id)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.captureWithReceipts(obj.(*model.Capture)))
}

type captureResponse struct {
	*model.Capture
	Receipts []*model.Receipt `json:"receipts"`
}

func (s *Server) captureWithReceipts(c *model.Capture) captureResponse {
	recs := s.core.ReceiptsForCause("capture", c.ID)
	if recs == nil {
		recs = []*model.Receipt{}
	}
	return captureResponse{Capture: c, Receipts: recs}
}

func (s *Server) handleCaptures(w http.ResponseWriter, r *http.Request) {
	status := model.CaptureStatus(r.URL.Query().Get("status"))
	limit := intParam(r, "limit", 50)
	caps := s.core.Captures(status, limit)
	out := make([]captureResponse, 0, len(caps))
	for _, c := range caps {
		out = append(out, s.captureWithReceipts(c))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	caps := s.core.Inbox()
	out := make([]captureResponse, 0, len(caps))
	for _, c := range caps {
		out = append(out, s.captureWithReceipts(c))
	}
	writeJSON(w, 200, out)
}

func (s *Server) getCapture(w http.ResponseWriter, id string) (*model.Capture, bool) {
	obj, err := s.core.Get(id)
	if err != nil {
		writeCoreError(w, err)
		return nil, false
	}
	c, ok := obj.(*model.Capture)
	if !ok {
		writeError(w, 400, "invalid", id+" is not a capture")
		return nil, false
	}
	return c, true
}

func (s *Server) handleCaptureGet(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getCapture(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, 200, s.captureWithReceipts(c))
}

func (s *Server) handleCaptureRetry(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getCapture(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Answer string `json:"answer"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if c.Status == model.CaptureProcessing {
		writeError(w, http.StatusConflict, "processing", "this capture is being processed right now")
		return
	}
	op := model.Op{Op: "capture.set_status", ID: c.ID, ExpectedRev: c.Rev, Status: string(model.CapturePending)}
	if strings.TrimSpace(req.Answer) != "" {
		a := strings.TrimSpace(req.Answer)
		op.Answer = &a
	}
	if _, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "user"}, []model.Op{op}); err != nil {
		writeCoreError(w, err)
		return
	}
	if s.worker != nil {
		s.worker.Kick()
	}
	c, ok = s.getCapture(w, c.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, s.captureWithReceipts(c))
}

func (s *Server) handleCaptureDismiss(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getCapture(w, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "user"},
		[]model.Op{{Op: "capture.set_status", ID: c.ID, ExpectedRev: c.Rev, Status: string(model.CaptureDismissed)}}); err != nil {
		writeCoreError(w, err)
		return
	}
	c, ok = s.getCapture(w, c.ID)
	if !ok {
		return
	}
	writeJSON(w, 200, s.captureWithReceipts(c))
}

// handleCaptureAccept applies the operations a parked capture proposes (or
// an edited set sent by the client) without another model call. The user is
// the actor: accepting is their decision.
func (s *Server) handleCaptureAccept(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getCapture(w, r.PathValue("id"))
	if !ok {
		return
	}
	if c.Status != model.CaptureNeedsReview && c.Status != model.CaptureFailed {
		writeError(w, http.StatusConflict, "conflict", "only parked or failed captures can be accepted")
		return
	}
	var req struct {
		Operations []triage.Operation `json:"operations"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if req.Operations == nil {
		if c.Result == nil || len(c.Result.Proposal) == 0 {
			writeError(w, 400, "bad_request", "this capture has no proposal; send operations")
			return
		}
		if err := json.Unmarshal(c.Result.Proposal, &req.Operations); err != nil {
			writeError(w, 500, "internal", "stored proposal is unreadable: "+err.Error())
			return
		}
	}
	classification := ""
	if c.Result != nil {
		classification = c.Result.Classification
	}
	ops, err := triage.Plan(s.core, s.config().Autonomy, c.ID, c.Text, classification, req.Operations, nil)
	if err != nil {
		writeError(w, 400, "invalid", err.Error())
		return
	}
	result := &model.CaptureResult{Summary: "Accepted by the user", ProcessedAt: time.Now().UTC()}
	if c.Result != nil {
		result.Classification = c.Result.Classification
		result.Confidence = c.Result.Confidence
		result.Provider, result.Model = c.Result.Provider, c.Result.Model
		if c.Result.Summary != "" {
			result.Summary = "Accepted: " + c.Result.Summary
		}
	}
	ops = append(ops, model.Op{Op: "capture.set_status", ID: c.ID, ExpectedRev: c.Rev, Status: string(model.CaptureProcessed), Result: result})
	if _, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "capture", ID: c.ID}, ops); err != nil {
		writeCoreError(w, err)
		return
	}
	c, ok = s.getCapture(w, c.ID)
	if !ok {
		return
	}
	writeJSON(w, 200, s.captureWithReceipts(c))
}

// ---------------------------------------------------------------------------
// Objects and views

type objectResponse struct {
	Object    model.Object     `json:"object"`
	Receipts  []*model.Receipt `json:"receipts"`
	Backlinks []linkRef        `json:"backlinks"`
	Topics    []linkRef        `json:"topics,omitempty"`
	Markdown  string           `json:"markdown,omitempty"`
}

type linkRef struct {
	ID        string     `json:"id"`
	Type      model.Type `json:"type"`
	Title     string     `json:"title"`
	Preview   string     `json:"preview,omitempty"`
	State     string     `json:"state,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

func refFor(o model.Object) linkRef {
	m := o.GetMeta()
	at := m.CreatedAt
	ref := linkRef{ID: m.ID, Type: m.Type, Title: o.Title(), CreatedAt: &at}
	switch v := o.(type) {
	case *model.Capture:
		ref.Preview = model.Shorten(v.Text, 160)
		ref.State = string(v.Status)
	case *model.Note:
		ref.Preview = model.Shorten(v.Body.PlainText(), 160)
		ref.State = string(v.Kind)
	case *model.Task:
		ref.State = string(v.State)
	case *model.Topic:
		ref.State = string(v.Kind)
	}
	return ref
}

// handleObjects resolves many ids at once (for citation chips).
func (s *Server) handleObjects(w http.ResponseWriter, r *http.Request) {
	var out []linkRef
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if len(out) >= 200 {
			break
		}
		if o, err := s.core.Get(id); err == nil {
			out = append(out, refFor(o))
		}
	}
	if out == nil {
		out = []linkRef{}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obj, err := s.core.Get(id)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	resp := objectResponse{Object: obj, Receipts: s.core.ReceiptsForObject(id, 20), Backlinks: []linkRef{}}
	for _, b := range s.core.Backlinks(id) {
		resp.Backlinks = append(resp.Backlinks, linkRef{ID: b.GetMeta().ID, Type: b.GetMeta().Type, Title: b.Title()})
	}
	var topics []string
	switch v := obj.(type) {
	case *model.Note:
		topics = v.Topics
		resp.Markdown = v.Body.Markdown()
	case *model.Task:
		topics = v.Topics
	case *model.Topic:
		resp.Markdown = v.Summary.Markdown()
	}
	for _, t := range topics {
		if o, err := s.core.Get(t); err == nil {
			resp.Topics = append(resp.Topics, linkRef{ID: t, Type: model.TypeTopic, Title: o.Title()})
		}
	}
	if resp.Receipts == nil {
		resp.Receipts = []*model.Receipt{}
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	kind := model.NoteKind(r.URL.Query().Get("kind"))
	notes := s.core.Notes(kind, boolParam(r, "archived"))
	if notes == nil {
		notes = []core.NoteView{}
	}
	writeJSON(w, 200, notes)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	var states []model.TaskState
	if v := r.URL.Query().Get("state"); v != "" {
		for _, st := range strings.Split(v, ",") {
			states = append(states, model.TaskState(strings.TrimSpace(st)))
		}
	}
	tasks := s.core.Tasks(states, boolParam(r, "archived"))
	if tasks == nil {
		tasks = []core.TaskView{}
	}
	writeJSON(w, 200, tasks)
}

func (s *Server) handleRelevant(w http.ResponseWriter, r *http.Request) {
	tasks := s.core.Relevant(intParam(r, "limit", 10))
	if tasks == nil {
		tasks = []core.TaskView{}
	}
	writeJSON(w, 200, tasks)
}

func (s *Server) handleTopics(w http.ResponseWriter, r *http.Request) {
	topics := s.core.Topics(boolParam(r, "archived"))
	if topics == nil {
		topics = []core.TopicView{}
	}
	writeJSON(w, 200, topics)
}

func (s *Server) handleTopic(w http.ResponseWriter, r *http.Request) {
	page, err := s.core.Topic(r.PathValue("id"))
	if err != nil {
		writeCoreError(w, err)
		return
	}
	if page.Notes == nil {
		page.Notes = []core.NoteView{}
	}
	if page.Tasks == nil {
		page.Tasks = []core.TaskView{}
	}
	writeJSON(w, 200, page)
}

type searchHit struct {
	ID      string     `json:"id"`
	Type    model.Type `json:"type"`
	Title   string     `json:"title"`
	Score   float64    `json:"score"`
	Preview string     `json:"preview"`
	Kind    string     `json:"kind,omitempty"`
	State   string     `json:"state,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, []searchHit{})
		return
	}
	var types []model.Type
	if v := r.URL.Query().Get("types"); v != "" {
		for _, t := range strings.Split(v, ",") {
			types = append(types, model.Type(strings.TrimSpace(t)))
		}
	}
	hits := s.core.Search(q, intParam(r, "limit", 20), types, boolParam(r, "all"))
	out := make([]searchHit, 0, len(hits))
	for _, h := range hits {
		sh := searchHit{ID: h.ID, Type: h.Type, Title: h.Title, Score: h.Score}
		switch v := h.Object.(type) {
		case *model.Note:
			sh.Preview = model.Shorten(v.Body.PlainText(), 200)
			sh.Kind = string(v.Kind)
		case *model.Task:
			sh.State = string(v.State)
		case *model.Topic:
			sh.Kind = string(v.Kind)
			sh.Preview = model.Shorten(v.Summary.PlainText(), 200)
		case *model.Capture:
			sh.Preview = model.Shorten(v.Text, 200)
			sh.State = string(v.Status)
		}
		out = append(out, sh)
	}
	writeJSON(w, 200, out)
}

// ---------------------------------------------------------------------------
// Changes, undo, commands

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	recs := s.core.Changes(core.ChangesQuery{Limit: intParam(r, "limit", 50), Before: before, After: after,
		Ascending: r.URL.Query().Has("after"), IncludeQuiet: boolParam(r, "all")})
	if recs == nil {
		recs = []*model.Receipt{}
	}
	writeJSON(w, 200, recs)
}

func (s *Server) handleChange(w http.ResponseWriter, r *http.Request) {
	txn, ok := s.core.Txn(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "not_found", "transaction not found")
		return
	}
	rec, _ := s.core.ReceiptFor(txn.ID)
	writeJSON(w, 200, map[string]any{"receipt": rec, "txn": txn})
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	rec, err := s.core.Undo(r.Context(), actorFor(r), r.PathValue("id"), req.Force)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ops []model.Op `json:"ops"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if len(req.Ops) > maxOpsPerCommand {
		writeError(w, 400, "bad_request", fmt.Sprintf("at most %d ops per command", maxOpsPerCommand))
		return
	}
	for i, op := range req.Ops {
		switch op.Op {
		case "note.revise", "note.update", "note.set_markdown", "task.update", "topic.update", "topic.set_summary", "topic.merge", "object.archive", "object.unarchive":
			if op.ExpectedRev <= 0 {
				writeError(w, 400, "bad_request", fmt.Sprintf("op %d (%s): expected_rev is required for edits", i, op.Op))
				return
			}
		}
	}
	rec, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "user"}, req.Ops)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	if s.worker != nil {
		s.worker.Kick()
	}
	writeJSON(w, 200, rec)
}

// ---------------------------------------------------------------------------
// Conversations

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	convs := s.core.Conversations(intParam(r, "limit", 50))
	type item struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Messages  int       `json:"messages"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	out := make([]item, 0, len(convs))
	for _, c := range convs {
		at := c.LastMessageAt
		if at.IsZero() {
			at = c.CreatedAt
		}
		out = append(out, item{ID: c.ID, Title: c.Title(), Messages: c.MessageCount, UpdatedAt: at})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	ops := []model.Op{{Op: "conversation.create"}}
	if req.Title != "" {
		ops[0].Title = &req.Title
	}
	if _, err := s.core.Commit(r.Context(), actorFor(r), &model.Cause{Kind: "user"}, ops); err != nil {
		writeCoreError(w, err)
		return
	}
	obj, err := s.core.Get(ops[0].ID)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, 201, obj)
}

func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	view, err := s.core.Conversation(r.PathValue("id"))
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) handleConversationMessage(w http.ResponseWriter, r *http.Request) {
	if s.chat == nil {
		writeError(w, 503, "unavailable", "No chat model is connected. Connect one in Settings.")
		return
	}
	var req struct {
		Text string `json:"text"`
		// ID is an optional client-generated capture id ("cap_…") that makes
		// the turn idempotent across retries.
		ID string `json:"id"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, 400, "bad_request", "text is required")
		return
	}
	id := r.PathValue("id")
	if !s.chatSlots.acquire(id) {
		writeError(w, http.StatusConflict, "busy", "a turn is already running for this conversation, or too many turns are in flight")
		return
	}
	defer s.chatSlots.release(id)
	// Long model turns: detach from the request context so a client
	// disconnect does not abort a half-finished tool loop, but stop with
	// the server.
	ctx, cancel := context.WithTimeout(s.baseCtx, 10*time.Minute)
	defer cancel()
	reply, err := s.chat.Send(ctx, id, req.Text, req.ID, actorFor(r), func(step chat.Step) {
		s.core.Publish(core.Event{Type: "chat.step", At: time.Now(), Payload: map[string]any{"conversation_id": id, "step": step}})
	})
	if err != nil {
		switch {
		case errors.Is(err, chat.ErrNoModel):
			writeError(w, 503, "unavailable", err.Error())
		case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrConflict):
			writeCoreError(w, err)
		default:
			writeError(w, 502, "chat_failed", err.Error())
		}
		return
	}
	writeJSON(w, 200, reply)
}

// ---------------------------------------------------------------------------
// Export, probe

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	switch format {
	case "", "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="fundus-export.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", " ")
		_ = enc.Encode(s.core.ExportJSON())
	case "markdown", "md":
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="fundus-export.zip"`)
		if err := s.core.ExportMarkdownZip(w); err != nil {
			s.lg.Error("export", "err", err)
		}
	default:
		writeError(w, 400, "bad_request", "format must be json or markdown")
	}
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="fundus-backup.zip"`)
	if err := s.core.Backup(w); err != nil {
		s.lg.Error("backup", "err", err)
	}
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	role := r.URL.Query().Get("role")
	rc := cfg.Triage
	if role == "chat" {
		rc = cfg.Chat
	}
	if pn := r.URL.Query().Get("provider"); pn != "" {
		rc.Provider = pn
	}
	if m := r.URL.Query().Get("model"); m != "" {
		rc.Model = m
	}
	s.cfgMu.RLock()
	reg := s.reg
	s.cfgMu.RUnlock()
	p, err := reg.Get(rc.Provider)
	if err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	writeJSON(w, 200, llm.Probe(ctx, p, rc.Model))
}

// ---------------------------------------------------------------------------
// Settings and setup

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, setup.BuildView(s.config()))
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var patch setup.Patch
	if err := decode(w, r, &patch); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	next, err := setup.Apply(s.config(), patch)
	if err != nil {
		writeError(w, 400, "invalid", err.Error())
		return
	}
	if err := s.applyConfig(next); err != nil {
		writeError(w, 500, "internal", err.Error())
		return
	}
	s.lg.Info("settings updated", "triage", next.Triage.Provider+"/"+next.Triage.Model, "chat", next.Chat.Provider+"/"+next.Chat.Model)
	writeJSON(w, 200, setup.BuildView(next))
}

// handleSettingsTest probes a provider/model, optionally with a key that is
// not saved yet (so the wizard can test before committing).
func (s *Server) handleSettingsTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	cfg := s.config().Clone()
	pc, ok := cfg.Providers[req.Provider]
	if !ok {
		writeError(w, 400, "bad_request", "unknown provider "+req.Provider)
		return
	}
	if req.BaseURL != "" && strings.TrimRight(req.BaseURL, "/") != pc.BaseURL {
		// Never send a stored key to an endpoint the user just typed in.
		if req.APIKey == "" {
			writeError(w, 400, "bad_request", "testing a different endpoint requires the key for it in the same request")
			return
		}
		pc.APIKey, pc.APIKeyEnv = "", ""
		pc.BaseURL = strings.TrimRight(req.BaseURL, "/")
	}
	if req.APIKey != "" {
		pc.APIKey = req.APIKey
	}
	cfg.Providers[req.Provider] = pc
	if req.Model == "" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		req.Model = setup.ListModels(ctx, req.Provider, pc, nil).Suggested.Triage
		cancel()
	}
	if pc.Type == "openai" && req.Model == "" {
		writeError(w, 400, "bad_request", "no model given and none could be discovered")
		return
	}
	reg, err := llm.NewRegistry(cfg, triage.NewHeuristic)
	if err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	p, err := reg.Get(req.Provider)
	if err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	writeJSON(w, 200, llm.Probe(ctx, p, req.Model))
}

// handleSetupModels lists a provider's models. GET uses the stored key;
// POST {provider, api_key?, base_url?} lists with an unsaved key (never in a
// URL). A new base_url requires its own key, as in settings/test.
func (s *Server) handleSetupModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if r.Method == http.MethodPost {
		if err := decode(w, r, &req); err != nil {
			writeError(w, 400, "bad_request", err.Error())
			return
		}
	} else {
		req.Provider = r.URL.Query().Get("provider")
	}
	cfg := s.config()
	pc, ok := cfg.Providers[req.Provider]
	if !ok {
		writeError(w, 400, "bad_request", "unknown provider "+req.Provider)
		return
	}
	if req.BaseURL != "" && strings.TrimRight(req.BaseURL, "/") != pc.BaseURL {
		if req.APIKey == "" {
			writeError(w, 400, "bad_request", "listing a different endpoint requires the key for it in the same request")
			return
		}
		pc.APIKey, pc.APIKeyEnv = "", ""
		pc.BaseURL = strings.TrimRight(req.BaseURL, "/")
	}
	if req.APIKey != "" {
		pc.APIKey = req.APIKey
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, 200, setup.ListModels(ctx, req.Provider, pc, nil))
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
	}
	if err := decode(w, r, &req); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callback := scheme + "://" + r.Host + "/setup/oauth/callback"
	u, err := s.oauth.Start(req.Provider, callback)
	if err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"url": u, "provider": req.Provider})
}

// handleOAuthCallback receives the browser redirect, exchanges the code for
// a key, stores it and shows a small page.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	provider, key, err := s.oauth.Finish(ctx, q.Get("state"), q.Get("code"))
	if err != nil {
		s.lg.Warn("oauth callback failed", "err", err)
		oauthPage(w, http.StatusBadRequest, "Connection failed", err.Error()+" Go back to Fundus and try again.")
		return
	}
	next, err := setup.Apply(s.config(), setup.Patch{Providers: map[string]setup.ProviderPatch{provider: {APIKey: &key}}})
	if err == nil {
		err = s.applyConfig(next)
	}
	if err != nil {
		oauthPage(w, http.StatusInternalServerError, "Could not save the key", err.Error())
		return
	}
	s.lg.Info("provider connected via oauth", "provider", provider)
	oauthPage(w, 200, "Connected", provider+" is connected. You can close this tab and return to Fundus.")
}

func oauthPage(w http.ResponseWriter, status int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Fundus</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:6rem auto;line-height:1.5;color:#222}h1{font-weight:600}</style>
<h1>%s</h1><p>%s</p>`, htmlEscape(title), htmlEscape(text))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func intParam(r *http.Request, name string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return v
}

func boolParam(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || v == "true"
}

const maxOpsPerCommand = 100

// clientName keeps only [a-z0-9_-], at most 32 chars.
func clientName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, strings.ToLower(strings.TrimSpace(s)))
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

// chatSlots limits in-flight chat turns: one per conversation, a few overall.
type chatSlots struct {
	mu     sync.Mutex
	active map[string]bool
	max    int
}

func newChatSlots(max int) *chatSlots { return &chatSlots{active: map[string]bool{}, max: max} }

func (c *chatSlots) acquire(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active[id] || len(c.active) >= c.max {
		return false
	}
	c.active[id] = true
	return true
}

func (c *chatSlots) release(id string) {
	c.mu.Lock()
	delete(c.active, id)
	c.mu.Unlock()
}

// handleTranscribe turns one uploaded recording into text with the dictation
// provider. The text is returned to the client for review; nothing is
// captured here, so a bad transcript never reaches the log.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if !cfg.DictationAvailable() {
		writeError(w, http.StatusServiceUnavailable, "dictation_unavailable", "dictation is not set up: choose a provider with a transcription model in Settings")
		return
	}
	const maxAudio = 25 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxAudio+1<<20)
	if err := r.ParseMultipartForm(maxAudio); err != nil {
		writeError(w, http.StatusBadRequest, "bad_audio", "could not read the upload (25 MB limit): "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_audio", "multipart field \"audio\" is required")
		return
	}
	defer f.Close()
	audio, err := io.ReadAll(io.LimitReader(f, maxAudio+1))
	if err != nil || len(audio) == 0 || len(audio) > maxAudio {
		writeError(w, http.StatusBadRequest, "bad_audio", "the recording is empty or larger than 25 MB")
		return
	}
	mimeType := hdr.Header.Get("Content-Type")
	switch {
	case bytes.HasPrefix(audio, []byte("RIFF")):
		mimeType = "audio/wav"
	case mimeType == "" || mimeType == "application/octet-stream":
		mimeType = "audio/wav"
	}
	prov, err := s.reg.Get(cfg.Dictation.Provider)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "dictation_unavailable", err.Error())
		return
	}
	tr, ok := prov.(llm.Transcriber)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "dictation_unavailable", "the dictation provider cannot transcribe")
		return
	}
	// Topic names steer the spelling of proper nouns; the model never sees
	// anything else from the store.
	hints := []string{"Fundus"}
	for _, tv := range s.core.Topics(false) {
		if len(hints) >= 40 {
			break
		}
		hints = append(hints, tv.Topic.Name)
	}
	timeout := cfg.Dictation.Timeout.Duration
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	text, err := tr.Transcribe(ctx, &llm.TranscribeRequest{Model: cfg.Dictation.Model, Audio: audio, MIME: mimeType,
		Language: strings.TrimSpace(r.FormValue("language")), Hints: hints})
	if err != nil {
		s.lg.Warn("transcription failed", "err", err)
		writeError(w, http.StatusBadGateway, "provider_error", "transcription failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "model": cfg.Dictation.Model})
}
