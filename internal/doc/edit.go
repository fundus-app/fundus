package doc

import (
	"errors"
	"fmt"
)

// Errors returned by Apply and ApplyAll. Callers should test them with
// errors.Is; the returned values carry extra context.
var (
	ErrPinned  = errors.New("block is pinned")
	ErrNoBlock = errors.New("block not found")
	ErrBadEdit = errors.New("invalid edit")
)

// Apply performs one edit on the document. The edit's Markdown, where the
// action takes one, is parsed with ParseMarkdown and must yield at least one
// block. Blocks targeted by replace or delete must not be pinned unless
// allowPinned is set; pin, unpin and insert_after ignore the pinned flag
// because they do not alter the block's content.
//
// Apply is atomic: when it returns an error the document is unchanged.
func (d *Document) Apply(e Edit, allowPinned bool) error {
	return d.ApplyWith(nil, e, allowPinned)
}

// ApplyWith is Apply with an explicit block-ID generator for new blocks.
func (d *Document) ApplyWith(gen IDGen, e Edit, allowPinned bool) error {
	switch e.Action {
	case "append":
		nb, err := editBlocks(gen, e)
		if err != nil {
			return err
		}
		d.Blocks = splice(d.Blocks, len(d.Blocks), 0, nb)
	case "prepend":
		nb, err := editBlocks(gen, e)
		if err != nil {
			return err
		}
		d.Blocks = splice(d.Blocks, 0, 0, nb)
	case "insert_after":
		idx, err := d.locate(e.BlockID)
		if err != nil {
			return err
		}
		nb, err := editBlocks(gen, e)
		if err != nil {
			return err
		}
		d.Blocks = splice(d.Blocks, idx+1, 0, nb)
	case "replace":
		idx, err := d.locateWritable(e.BlockID, allowPinned)
		if err != nil {
			return err
		}
		nb, err := editBlocks(gen, e)
		if err != nil {
			return err
		}
		// The replacement inherits the provenance of what it replaces, but
		// not its pinned state: a user replacing a pinned block gets a new,
		// unpinned block.
		srcs := unionStrings(d.Blocks[idx].Sources, e.Sources)
		for i := range nb {
			nb[i].Sources = append([]string(nil), srcs...)
			nb[i].Pinned = false
		}
		d.Blocks = splice(d.Blocks, idx, 1, nb)
	case "delete":
		idx, err := d.locateWritable(e.BlockID, allowPinned)
		if err != nil {
			return err
		}
		d.Blocks = splice(d.Blocks, idx, 1, nil)
	case "pin", "unpin":
		idx, err := d.locate(e.BlockID)
		if err != nil {
			return err
		}
		d.Blocks[idx].Pinned = e.Action == "pin"
	default:
		return fmt.Errorf("%w: unknown action %q", ErrBadEdit, e.Action)
	}
	return nil
}

// ApplyAll applies edits in order. It is atomic as a whole: if any edit
// fails, the document is left exactly as it was and the error names the
// failing edit by index.
func (d *Document) ApplyAll(edits []Edit, allowPinned bool) error {
	return d.ApplyAllWith(nil, edits, allowPinned)
}

// ApplyAllWith is ApplyAll with an explicit block-ID generator.
func (d *Document) ApplyAllWith(gen IDGen, edits []Edit, allowPinned bool) error {
	c := d.Clone()
	for i, e := range edits {
		if err := c.ApplyWith(gen, e, allowPinned); err != nil {
			return fmt.Errorf("edit %d (%s): %w", i, e.Action, err)
		}
	}
	*d = c
	return nil
}

// editBlocks parses the edit's Markdown and rejects edits that carry no
// content.
func editBlocks(gen IDGen, e Edit) ([]Block, error) {
	blocks := ParseMarkdownWith(gen, e.Markdown, e.Sources).Blocks
	if len(blocks) == 0 {
		return nil, fmt.Errorf("%w: %s produced no blocks", ErrBadEdit, e.Action)
	}
	return blocks, nil
}

func (d *Document) locate(id string) (int, error) {
	idx := d.Find(id)
	if idx < 0 {
		return -1, fmt.Errorf("%w: %q", ErrNoBlock, id)
	}
	return idx, nil
}

func (d *Document) locateWritable(id string, allowPinned bool) (int, error) {
	idx, err := d.locate(id)
	if err != nil {
		return -1, err
	}
	if d.Blocks[idx].Pinned && !allowPinned {
		return -1, fmt.Errorf("%w: %q", ErrPinned, id)
	}
	return idx, nil
}

// splice returns a new slice equal to blocks with del elements at position i
// removed and ins inserted in their place. The input slice is never mutated.
func splice(blocks []Block, i, del int, ins []Block) []Block {
	out := make([]Block, 0, len(blocks)-del+len(ins))
	out = append(out, blocks[:i]...)
	out = append(out, ins...)
	out = append(out, blocks[i+del:]...)
	return out
}

// unionStrings concatenates a and b, dropping duplicates while keeping first
// occurrence order. It returns nil when the result would be empty.
func unionStrings(a, b []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range [][]string{a, b} {
		for _, v := range s {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// SetMarkdown replaces the whole document with the parse of md while keeping
// the identity of unchanged blocks: a new block whose content equals an
// existing block (matched in order, each old block used at most once) keeps
// that block's ID, provenance and pinned flag. Changed and new blocks get
// fresh IDs and sources. Pinned blocks that would disappear make the call
// fail with ErrPinned unless allowPinned is set. The document is unchanged on
// error.
func (d *Document) SetMarkdown(gen IDGen, md string, sources []string, allowPinned bool) error {
	next := ParseMarkdownWith(gen, md, sources)
	used := make([]bool, len(d.Blocks))
	for i := range next.Blocks {
		for j := range d.Blocks {
			if used[j] || !next.Blocks[i].Equal(d.Blocks[j]) {
				continue
			}
			used[j] = true
			old := d.Blocks[j]
			next.Blocks[i].ID = old.ID
			next.Blocks[i].Sources = append([]string(nil), old.Sources...)
			next.Blocks[i].Pinned = old.Pinned
			break
		}
	}
	if !allowPinned {
		for j, b := range d.Blocks {
			if b.Pinned && !used[j] {
				return fmt.Errorf("%w: block %s would be changed or removed", ErrPinned, b.ID)
			}
		}
	}
	*d = next
	return nil
}
