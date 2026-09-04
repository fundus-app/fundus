// Package model defines Fundus's stable object types.
//
// Three layers exist (see docs/concept.md §8):
//
//   - Captures: unchanged user input, append-only.
//   - Objects: notes, tasks, topics, sources and conversations with stable IDs,
//     revisions and provenance.
//   - Views: derived, never stored here.
//
// Every state change is expressed as an Op inside a Txn (see ops.go). The
// core applies ops to objects; nothing else mutates them.
package model

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
)

// Type names an object type. The value doubles as the ID prefix owner.
type Type string

const (
	TypeCapture      Type = "capture"
	TypeNote         Type = "note"
	TypeTask         Type = "task"
	TypeTopic        Type = "topic"
	TypeSource       Type = "source"
	TypeConversation Type = "conversation"
	TypeMessage      Type = "message"
)

// Meta is shared by all objects.
type Meta struct {
	ID        string    `json:"id"`
	Type      Type      `json:"type"`
	Rev       int       `json:"rev"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Archived  bool      `json:"archived,omitempty"`
}

// Object is implemented by every stored entity.
type Object interface {
	GetMeta() *Meta
	Clone() Object
	// Title returns a short human label used in receipts and lists.
	Title() string
	// SearchFields returns the text fields to index, keyed by field name.
	SearchFields() map[string]string
}

// GetMeta implements Object.
func (m *Meta) GetMeta() *Meta { return m }

// ---------------------------------------------------------------------------
// Capture

// CaptureStatus tracks the processing state of a capture.
type CaptureStatus string

const (
	CapturePending     CaptureStatus = "pending"      // stored, not yet processed
	CaptureProcessing  CaptureStatus = "processing"   // a worker is on it
	CaptureProcessed   CaptureStatus = "processed"    // operations committed
	CaptureNeedsReview CaptureStatus = "needs_review" // model unsure; nothing written
	CaptureFailed      CaptureStatus = "failed"       // processing errors exhausted
	CaptureDismissed   CaptureStatus = "dismissed"    // user closed it without action
)

// CaptureResult records what processing produced. Kept on the capture so the
// inbox can show it without joining the audit log.
type CaptureResult struct {
	Classification string  `json:"classification,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Summary        string  `json:"summary,omitempty"`
	Question       string  `json:"question,omitempty"`
	// Reason says why a capture was parked or dismissed so clients can
	// phrase it: unclear | low_confidence | proposal | discard | undone.
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
	// Retryable marks transient failures (provider down, rate limit) that
	// the worker retries with backoff; validation failures are not retried.
	Retryable bool   `json:"retryable,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	// CoreProposal holds core operations proposed by maintenance (a topic
	// merge, an archive); accepting applies them as the user without a
	// plan step. Lines carries their human description for the inbox.
	CoreProposal json.RawMessage `json:"core_proposal,omitempty"`
	Lines        []string        `json:"lines,omitempty"`
	// Proposal holds the operations (model vocabulary) that were not
	// written because the capture was parked; POST /captures/{id}/accept
	// applies them.
	Proposal    json.RawMessage `json:"proposal,omitempty"`
	ProcessedAt time.Time       `json:"processed_at,omitempty"`
}

// Capture is unchanged user input with a timestamp. Text is never rewritten.
type Capture struct {
	Meta
	Text           string         `json:"text"`
	Source         string         `json:"source"` // cli|api|desktop|mobile|chat|voice|import|test
	Status         CaptureStatus  `json:"status"`
	Attempts       int            `json:"attempts,omitempty"`
	Result         *CaptureResult `json:"result,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	// Answer holds the user's reply to a clarification question, if any.
	Answer string `json:"answer,omitempty"`
}

func (c *Capture) Clone() Object {
	cp := *c
	if c.Result != nil {
		r := *c.Result
		r.Proposal = append(json.RawMessage(nil), c.Result.Proposal...)
		r.CoreProposal = append(json.RawMessage(nil), c.Result.CoreProposal...)
		r.Lines = cloneStrings(c.Result.Lines)
		cp.Result = &r
	}
	return &cp
}

func (c *Capture) Title() string { return Shorten(c.Text, 60) }

func (c *Capture) SearchFields() map[string]string {
	return map[string]string{"body": c.Text}
}

// ---------------------------------------------------------------------------
// Note

// NoteKind distinguishes loose ideas from information notes.
type NoteKind string

const (
	NoteKindNote NoteKind = "note"
	NoteKindIdea NoteKind = "idea"
)

// Note is a derived knowledge object. Body blocks carry provenance.
type Note struct {
	Meta
	Kind      NoteKind     `json:"kind"`
	NoteTitle string       `json:"title"`
	Body      doc.Document `json:"body"`
	Topics    []string     `json:"topics,omitempty"`
	Origins   []string     `json:"origins,omitempty"` // capture ids
	Related   []string     `json:"related,omitempty"` // note ids
}

func (n *Note) Clone() Object {
	cp := *n
	cp.Body = n.Body.Clone()
	cp.Topics = cloneStrings(n.Topics)
	cp.Origins = cloneStrings(n.Origins)
	cp.Related = cloneStrings(n.Related)
	return &cp
}

func (n *Note) Title() string { return n.NoteTitle }

func (n *Note) SearchFields() map[string]string {
	return map[string]string{"title": n.NoteTitle, "body": n.Body.PlainText()}
}

// ---------------------------------------------------------------------------
// Task

// TaskState is the minimal task lifecycle. Archiving uses Meta.Archived.
type TaskState string

const (
	TaskOpen    TaskState = "open"
	TaskWaiting TaskState = "waiting" // blocked on something external
	TaskLater   TaskState = "later"   // deliberately deferred
	TaskDone    TaskState = "done"
)

// Task needs only ID, text, state, creation time and origin. Everything else
// is optional evidence for the attention score.
// TaskKind marks what kind of work a task asks for. "" is an ordinary task;
// "research" asks Fundus to look something up on the web (concept §9).
type TaskKind string

const TaskKindResearch TaskKind = "research"

type Task struct {
	Meta
	Text          string     `json:"text"`
	Kind          TaskKind   `json:"kind,omitempty"`
	State         TaskState  `json:"state"`
	Due           string     `json:"due,omitempty"` // YYYY-MM-DD
	EffortMinutes int        `json:"effort_minutes,omitempty"`
	Importance    int        `json:"importance,omitempty"` // 0 unset, 1 low, 2 normal, 3 high
	WaitingOn     string     `json:"waiting_on,omitempty"`
	Topics        []string   `json:"topics,omitempty"`
	Origins       []string   `json:"origins,omitempty"`
	Notes         []string   `json:"notes,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	// Mentions counts how often captures referred to this task after creation.
	Mentions int `json:"mentions,omitempty"`
}

func (t *Task) Clone() Object {
	cp := *t
	cp.Topics = cloneStrings(t.Topics)
	cp.Origins = cloneStrings(t.Origins)
	cp.Notes = cloneStrings(t.Notes)
	if t.CompletedAt != nil {
		at := *t.CompletedAt
		cp.CompletedAt = &at
	}
	return &cp
}

func (t *Task) Title() string { return t.Text }

func (t *Task) SearchFields() map[string]string {
	return map[string]string{"title": t.Text, "body": t.WaitingOn}
}

// ---------------------------------------------------------------------------
// Topic

// TopicKind lets people and projects reuse the topic machinery.
type TopicKind string

const (
	TopicKindTopic   TopicKind = "topic"
	TopicKindPerson  TopicKind = "person"
	TopicKindProject TopicKind = "project"
)

// Topic is a hub object: notes and tasks link to it, and it carries an
// LLM-maintained summary whose blocks trace back to their sources.
type Topic struct {
	Meta
	Kind    TopicKind    `json:"kind"`
	Name    string       `json:"name"`
	Aliases []string     `json:"aliases,omitempty"`
	Summary doc.Document `json:"summary"`
}

func (t *Topic) Clone() Object {
	cp := *t
	cp.Aliases = cloneStrings(t.Aliases)
	cp.Summary = t.Summary.Clone()
	return &cp
}

func (t *Topic) Title() string { return t.Name }

func (t *Topic) SearchFields() map[string]string {
	return map[string]string{"title": t.Name, "aliases": joinStrings(t.Aliases), "body": t.Summary.PlainText()}
}

// ---------------------------------------------------------------------------
// Source

// Source is an external reference captured during research.
type Source struct {
	Meta
	URL       string    `json:"url"`
	SrcTitle  string    `json:"title,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Excerpt   string    `json:"excerpt,omitempty"`
	Query     string    `json:"query,omitempty"`
}

func (s *Source) Clone() Object { cp := *s; return &cp }

func (s *Source) Title() string {
	if s.SrcTitle != "" {
		return s.SrcTitle
	}
	return s.URL
}

func (s *Source) SearchFields() map[string]string {
	return map[string]string{"title": s.SrcTitle, "body": s.Excerpt + " " + s.URL}
}

// ---------------------------------------------------------------------------
// Conversation and Message

// Conversation is a persistent thread. Its messages are separate objects so
// that a long conversation does not make every append re-store the whole
// history in the event log.
type Conversation struct {
	Meta
	ConvTitle     string    `json:"title,omitempty"`
	MessageCount  int       `json:"message_count"`
	LastMessageAt time.Time `json:"last_message_at,omitempty"`
}

func (c *Conversation) Clone() Object { cp := *c; return &cp }

func (c *Conversation) Title() string {
	if c.ConvTitle != "" {
		return c.ConvTitle
	}
	return "Conversation"
}

func (c *Conversation) SearchFields() map[string]string {
	return map[string]string{"title": c.ConvTitle}
}

// Message is one turn in a conversation. User turns reference their capture;
// assistant turns reference the transactions they caused. Blocks are derived
// from Text by the core.
type Message struct {
	Meta
	ConversationID string       `json:"conversation_id"`
	Index          int          `json:"index"`
	Role           string       `json:"role"` // user|assistant
	Text           string       `json:"text,omitempty"`
	Blocks         doc.Document `json:"blocks"`
	CaptureID      string       `json:"capture_id,omitempty"`
	TxnIDs         []string     `json:"txn_ids,omitempty"`
	Refs           []string     `json:"refs,omitempty"` // object ids cited in the answer
	// Interrupted marks a user turn whose reply never arrived (restart).
	Interrupted bool `json:"interrupted,omitempty"`
}

func (m *Message) Clone() Object {
	cp := *m
	cp.Blocks = m.Blocks.Clone()
	cp.TxnIDs = cloneStrings(m.TxnIDs)
	cp.Refs = cloneStrings(m.Refs)
	return &cp
}

func (m *Message) Title() string { return Shorten(m.Text, 60) }

func (m *Message) SearchFields() map[string]string {
	if m.Role == "assistant" {
		return map[string]string{"body": m.Text}
	}
	return map[string]string{} // user turns are indexed via their capture
}

// ---------------------------------------------------------------------------
// Encoding helpers

// Unmarshal decodes an object by its "type" discriminator.
func Unmarshal(raw []byte) (Object, error) {
	var probe struct {
		Type Type `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	var obj Object
	switch probe.Type {
	case TypeCapture:
		obj = &Capture{}
	case TypeNote:
		obj = &Note{}
	case TypeTask:
		obj = &Task{}
	case TypeTopic:
		obj = &Topic{}
	case TypeSource:
		obj = &Source{}
	case TypeConversation:
		obj = &Conversation{}
	case TypeMessage:
		obj = &Message{}
	default:
		return nil, fmt.Errorf("decode object: unknown type %q", probe.Type)
	}
	if err := json.Unmarshal(raw, obj); err != nil {
		return nil, fmt.Errorf("decode %s: %w", probe.Type, err)
	}
	return obj, nil
}

// Marshal encodes an object as compact JSON.
func Marshal(obj Object) ([]byte, error) { return json.Marshal(obj) }

// Shorten cuts s to at most n runes, appending an ellipsis when cut.
func Shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

func joinStrings(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
