// Package doc defines Fundus's typed document model and its lossless mapping
// to a strict Markdown subset.
//
// Long-form content (note bodies, topic summaries, research results) is stored
// as a flat list of blocks. Every block carries a stable ID so that the LLM can
// revise a document with block-level operations ("replace block b_…") instead
// of rewriting the whole text, and so that provenance can be tracked per block.
//
// Inline formatting inside a block (bold, italic, code, links, object refs) is
// kept as a Markdown-subset string; clients parse it for rendering. This keeps
// the core small and the wire format readable.
package doc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fundus-app/fundus/internal/ids"
)

// BlockType enumerates the block kinds of the document model.
type BlockType string

const (
	Heading   BlockType = "heading"
	Paragraph BlockType = "paragraph"
	List      BlockType = "list"
	Quote     BlockType = "quote"
	Code      BlockType = "code"
	Callout   BlockType = "callout"
	TaskRef   BlockType = "task_ref"
	SourceRef BlockType = "source_ref"
)

// Block is one unit of a document.
//
// Which fields are used depends on Type:
//
//	heading:    Level (1-3), Text
//	paragraph:  Text
//	list:       Items, Ordered
//	quote:      Text (may contain newlines)
//	code:       Lang, Text (verbatim)
//	callout:    Kind (info|warning|question|external), Text
//	task_ref:   Ref (task id)
//	source_ref: Ref (source id), Text (optional quoted excerpt)
type Block struct {
	ID      string    `json:"id"`
	Type    BlockType `json:"type"`
	Text    string    `json:"text,omitempty"`
	Level   int       `json:"level,omitempty"`
	Items   []string  `json:"items,omitempty"`
	Ordered bool      `json:"ordered,omitempty"`
	Lang    string    `json:"lang,omitempty"`
	Kind    string    `json:"kind,omitempty"`
	Ref     string    `json:"ref,omitempty"`

	// Sources lists the capture or source IDs this block was derived from.
	Sources []string `json:"sources,omitempty"`
	// Pinned blocks are never modified by the LLM until the user unpins them.
	Pinned bool `json:"pinned,omitempty"`
}

// Document is an ordered list of blocks.
type Document struct {
	Blocks []Block `json:"blocks"`
}

// IDGen produces block IDs. The core passes a deterministic generator per
// transaction so that replaying the event log reproduces identical block IDs;
// nil means random IDs.
type IDGen func() string

// NewBlockID returns a fresh random block ID.
func NewBlockID() string { return ids.New(ids.PrefixBlock) }

func (g IDGen) next() string {
	if g == nil {
		return NewBlockID()
	}
	return g()
}

// Equal reports whether two blocks have the same content (ignoring ID,
// provenance and pinned state).
func (b Block) Equal(o Block) bool {
	if b.Type != o.Type || b.Text != o.Text || b.Level != o.Level || b.Ordered != o.Ordered ||
		b.Lang != o.Lang || b.Kind != o.Kind || b.Ref != o.Ref || len(b.Items) != len(o.Items) {
		return false
	}
	for i := range b.Items {
		if b.Items[i] != o.Items[i] {
			return false
		}
	}
	return true
}

// Clone returns a deep copy.
func (d Document) Clone() Document {
	out := Document{Blocks: make([]Block, len(d.Blocks))}
	for i, b := range d.Blocks {
		out.Blocks[i] = b.Clone()
	}
	return out
}

// Clone returns a deep copy of the block.
func (b Block) Clone() Block {
	c := b
	if b.Items != nil {
		c.Items = append([]string(nil), b.Items...)
	}
	if b.Sources != nil {
		c.Sources = append([]string(nil), b.Sources...)
	}
	return c
}

// Find returns the index of the block with the given ID, or -1.
func (d Document) Find(id string) int {
	for i, b := range d.Blocks {
		if b.ID == id {
			return i
		}
	}
	return -1
}

// PlainText renders the document as unformatted text, used for indexing and
// short previews. Refs render as their ID.
func (d Document) PlainText() string {
	var sb strings.Builder
	for i, b := range d.Blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch b.Type {
		case List:
			for _, it := range b.Items {
				sb.WriteString(stripInline(it))
				sb.WriteString("\n")
			}
		case TaskRef, SourceRef:
			sb.WriteString(b.Ref)
			if b.Text != "" {
				sb.WriteString(" ")
				sb.WriteString(stripInline(b.Text))
			}
		case Code:
			sb.WriteString(b.Text)
		default:
			sb.WriteString(stripInline(b.Text))
		}
	}
	return sb.String()
}

// Validate checks structural invariants: unique non-empty IDs, known types,
// and per-type required fields.
func (d Document) Validate() error {
	seen := make(map[string]struct{}, len(d.Blocks))
	for i, b := range d.Blocks {
		if b.ID == "" {
			return fmt.Errorf("block %d: empty id", i)
		}
		if _, dup := seen[b.ID]; dup {
			return fmt.Errorf("block %d: duplicate id %q", i, b.ID)
		}
		seen[b.ID] = struct{}{}
		switch b.Type {
		case Heading:
			if b.Level < 1 || b.Level > 3 {
				return fmt.Errorf("block %s: heading level %d out of range", b.ID, b.Level)
			}
		case Paragraph, Quote, Code:
		case List:
			if len(b.Items) == 0 {
				return fmt.Errorf("block %s: empty list", b.ID)
			}
		case Callout:
			switch b.Kind {
			case "info", "warning", "question", "external":
			default:
				return fmt.Errorf("block %s: unknown callout kind %q", b.ID, b.Kind)
			}
		case TaskRef, SourceRef:
			if b.Ref == "" {
				return fmt.Errorf("block %s: empty ref", b.ID)
			}
		default:
			return fmt.Errorf("block %s: unknown type %q", b.ID, b.Type)
		}
	}
	return nil
}

// MarshalJSON keeps an empty document as {"blocks":[]} rather than null.
func (d Document) MarshalJSON() ([]byte, error) {
	type alias Document
	a := alias(d)
	if a.Blocks == nil {
		a.Blocks = []Block{}
	}
	return json.Marshal(a)
}

// stripInline removes the inline markup characters that the Markdown subset
// uses, leaving readable text. It is intentionally approximate.
func stripInline(s string) string {
	r := strings.NewReplacer("**", "", "__", "", "`", "", "*", "", "_", " ")
	return r.Replace(s)
}

// Edit is one block-level modification of a document.
//
//	append        Markdown, Sources
//	prepend       Markdown, Sources
//	insert_after  BlockID, Markdown, Sources
//	replace       BlockID, Markdown, Sources
//	delete        BlockID
//	pin           BlockID
//	unpin         BlockID
type Edit struct {
	Action   string   `json:"action"`
	BlockID  string   `json:"block_id,omitempty"`
	Markdown string   `json:"markdown,omitempty"`
	Sources  []string `json:"sources,omitempty"`
}
