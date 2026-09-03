package model

import (
	"encoding/json"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
)

// Op is one typed state change. It is a "wide" struct: every op kind uses a
// subset of the fields, documented per kind below. The flat shape keeps JSON
// simple for the audit log, the API and the LLM.
//
// Op kinds and the fields they read:
//
// Every op that modifies an existing object accepts ExpectedRev; when set,
// the commit fails with a conflict if the object's revision differs.
//
//	capture.create      ID?, Text, Source, ConversationID?, Status? (default pending)
//	capture.set_status  ID, ExpectedRev?, Status, Result?, Answer?
//	note.create         ID?, Kind, Title, Markdown, Topics, Origins
//	note.revise         ID, ExpectedRev, Edits
//	note.set_markdown   ID, ExpectedRev, Markdown, Origins   (whole body; unchanged blocks keep identity)
//	note.update         ID, ExpectedRev, Title?, Kind?, AddTopics, RemoveTopics, AddRelated
//	task.create         ID?, Text, Topics, Origins, Due?, EffortMinutes?, Importance?
//	task.update         ID, ExpectedRev, Text?, State?, Due?, EffortMinutes?, Importance?,
//	                    WaitingOn?, AddTopics, RemoveTopics, AddNotes, Mention
//	topic.create        ID?, Name, Kind, Aliases
//	topic.update        ID, ExpectedRev, Name?, Kind?, Aliases?, Edits (summary)
//	topic.set_summary   ID, ExpectedRev, Markdown, Origins
//	topic.merge         ID (survivor), ExpectedRev, From (topic id to fold in)
//	source.create       ID?, URL, Title, Text (excerpt), Query
//	conversation.create ID?, Title
//	conversation.update ID, ExpectedRev, Title
//	message.create      ID?, ConversationID, Message{Role, Text, CaptureID, TxnIDs, Refs, Interrupted}
//	object.archive      ID, ExpectedRev
//	object.unarchive    ID, ExpectedRev
//	object.restore      ID, Object   (undo only: put back a full prior image)
//	object.remove       ID           (undo only: reverse a create)
type Op struct {
	Op          string `json:"op"`
	ID          string `json:"id,omitempty"`
	ExpectedRev int    `json:"expected_rev,omitempty"`

	// Shared content fields.
	Text     string   `json:"text,omitempty"`
	Title    *string  `json:"title,omitempty"`
	Markdown string   `json:"markdown,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Topics   []string `json:"topics,omitempty"`
	Origins  []string `json:"origins,omitempty"`
	Source   string   `json:"source,omitempty"`

	// Link changes.
	AddTopics    []string `json:"add_topics,omitempty"`
	RemoveTopics []string `json:"remove_topics,omitempty"`
	AddRelated   []string `json:"add_related,omitempty"`
	AddNotes     []string `json:"add_notes,omitempty"`

	// Task fields. Pointers distinguish "unchanged" from "clear".
	State         string  `json:"state,omitempty"`
	Due           *string `json:"due,omitempty"`
	EffortMinutes *int    `json:"effort_minutes,omitempty"`
	Importance    *int    `json:"importance,omitempty"`
	WaitingOn     *string `json:"waiting_on,omitempty"`
	Mention       bool    `json:"mention,omitempty"`

	// Topic fields.
	Name    *string  `json:"name,omitempty"`
	Aliases []string `json:"aliases,omitempty"`

	// Document edits (note body or topic summary).
	Edits []doc.Edit `json:"edits,omitempty"`

	// Capture fields.
	Status         string         `json:"status,omitempty"`
	Result         *CaptureResult `json:"result,omitempty"`
	Answer         *string        `json:"answer,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`

	// Source fields.
	URL   string `json:"url,omitempty"`
	Query string `json:"query,omitempty"`

	// Conversation fields.
	Message *Message `json:"message,omitempty"`

	// topic.merge: the topic folded into ID.
	From string `json:"from,omitempty"`

	// Undo-only payload.
	Object json.RawMessage `json:"object,omitempty"`
}

// Cause records why a transaction happened.
type Cause struct {
	Kind string `json:"kind"` // capture|conversation|user|undo|maintenance|research|system
	ID   string `json:"id,omitempty"`
}

// Txn is an atomic group of ops. It is the unit of the event log, of undo and
// of the audit view.
type Txn struct {
	Seq   uint64    `json:"seq"`
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Actor string    `json:"actor"` // user:cli | user:app | llm:triage/<model> | system
	Cause *Cause    `json:"cause,omitempty"`
	Ops   []Op      `json:"ops"`
	// Before holds the full prior image of every touched object; a nil value
	// means the object did not exist before. This is what makes undo exact.
	Before map[string]json.RawMessage `json:"before"`
	// Touched lists touched object ids in order of first touch.
	Touched []string `json:"touched"`
	// Affected lists objects whose own record did not change but whose view
	// did: topics that gained or lost members. Clients refresh those pages;
	// undo ignores them because nothing was written to them.
	Affected []string `json:"affected,omitempty"`
	// Summary and Lines are the human receipt, stored so the audit view shows
	// what was said at the time, not a re-rendering against later state.
	Summary string        `json:"summary,omitempty"`
	Lines   []ReceiptLine `json:"lines,omitempty"`
	// UndoOf is set on transactions that revert another one.
	UndoOf string `json:"undo_of,omitempty"`
	// UndoneBy is not stored; it is derived when listing changes.
}

// ReceiptLine describes one visible effect of a transaction.
type ReceiptLine struct {
	Op         string `json:"op"`
	ObjectID   string `json:"object_id"`
	ObjectType Type   `json:"object_type"`
	Text       string `json:"text"`
}

// Receipt is returned for every committed transaction. It reflects what was
// actually written, not what a model claimed.
type Receipt struct {
	TxnID    string        `json:"txn_id"`
	Seq      uint64        `json:"seq"`
	At       time.Time     `json:"at"`
	Actor    string        `json:"actor"`
	Cause    *Cause        `json:"cause,omitempty"`
	Lines    []ReceiptLine `json:"lines"`
	Summary  string        `json:"summary"`
	Touched  []string      `json:"touched,omitempty"`
	Affected []string      `json:"affected,omitempty"`
	Undoable bool          `json:"undoable"`
	UndoOf   string        `json:"undo_of,omitempty"`
	UndoneBy string        `json:"undone_by,omitempty"`
	// Quiet marks bookkeeping transactions (status flips, conversation
	// appends) that the audit view hides by default.
	Quiet bool `json:"quiet,omitempty"`
}
