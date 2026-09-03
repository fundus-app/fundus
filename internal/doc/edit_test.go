package doc

import (
	"errors"
	"reflect"
	"testing"
)

// baseMarkdown yields four blocks: heading, paragraph, list, quote.
const baseMarkdown = "# Title\n\nfirst para\n\n- a\n- b\n\n> quoted"

func baseDoc(t *testing.T) Document {
	t.Helper()
	d := ParseMarkdown(baseMarkdown, []string{"cap_base"})
	if len(d.Blocks) != 4 {
		t.Fatalf("base doc has %d blocks, want 4", len(d.Blocks))
	}
	return d
}

func TestApply(t *testing.T) {
	const missing = "b_missing"
	tests := []struct {
		name        string
		pin         []int // block indexes to pin before applying
		action      string
		target      int // block index for BlockID; -1 means a missing ID
		markdown    string
		allowPinned bool
		wantErr     error
		wantMD      string
	}{
		{
			name: "append", action: "append", target: -2, markdown: "new para",
			wantMD: baseMarkdown + "\n\nnew para",
		},
		{
			name: "append several blocks", action: "append", target: -2, markdown: "x\n\n1. y\n2. z",
			wantMD: baseMarkdown + "\n\nx\n\n1. y\n2. z",
		},
		{
			name: "append empty markdown", action: "append", target: -2, markdown: "  \n\n",
			wantErr: ErrBadEdit,
		},
		{
			name: "prepend", action: "prepend", target: -2, markdown: "> [!info] lead",
			wantMD: "> [!info] lead\n\n" + baseMarkdown,
		},
		{
			name: "prepend empty markdown", action: "prepend", target: -2, markdown: "",
			wantErr: ErrBadEdit,
		},
		{
			name: "insert_after first block", action: "insert_after", target: 0, markdown: "after title",
			wantMD: "# Title\n\nafter title\n\nfirst para\n\n- a\n- b\n\n> quoted",
		},
		{
			name: "insert_after last block", action: "insert_after", target: 3, markdown: "[[task_1]]",
			wantMD: baseMarkdown + "\n\n[[task_1]]",
		},
		{
			name: "insert_after pinned block is allowed", pin: []int{0},
			action: "insert_after", target: 0, markdown: "ok",
			wantMD: "# Title\n\nok\n\nfirst para\n\n- a\n- b\n\n> quoted",
		},
		{
			name: "insert_after missing block", action: "insert_after", target: -1, markdown: "x",
			wantErr: ErrNoBlock,
		},
		{
			name: "insert_after empty markdown", action: "insert_after", target: 0, markdown: "",
			wantErr: ErrBadEdit,
		},
		{
			name: "replace with several blocks", action: "replace", target: 1, markdown: "replaced\n\n```\ncode\n```",
			wantMD: "# Title\n\nreplaced\n\n```\ncode\n```\n\n- a\n- b\n\n> quoted",
		},
		{
			name: "replace missing block", action: "replace", target: -1, markdown: "x",
			wantErr: ErrNoBlock,
		},
		{
			name: "replace with empty markdown", action: "replace", target: 1, markdown: "\n",
			wantErr: ErrBadEdit,
		},
		{
			name: "replace pinned block is rejected", pin: []int{1},
			action: "replace", target: 1, markdown: "x",
			wantErr: ErrPinned,
		},
		{
			name: "replace pinned block with allowPinned", pin: []int{1},
			action: "replace", target: 1, markdown: "x", allowPinned: true,
			wantMD: "# Title\n\nx\n\n- a\n- b\n\n> quoted",
		},
		{
			name: "delete", action: "delete", target: 2,
			wantMD: "# Title\n\nfirst para\n\n> quoted",
		},
		{
			name: "delete missing block", action: "delete", target: -1,
			wantErr: ErrNoBlock,
		},
		{
			name: "delete pinned block is rejected", pin: []int{2},
			action: "delete", target: 2,
			wantErr: ErrPinned,
		},
		{
			name: "delete pinned block with allowPinned", pin: []int{2},
			action: "delete", target: 2, allowPinned: true,
			wantMD: "# Title\n\nfirst para\n\n> quoted",
		},
		{
			name: "pin", action: "pin", target: 3,
			wantMD: baseMarkdown,
		},
		{
			name: "pin missing block", action: "pin", target: -1,
			wantErr: ErrNoBlock,
		},
		{
			name: "unpin", pin: []int{3}, action: "unpin", target: 3,
			wantMD: baseMarkdown,
		},
		{
			name: "unpin missing block", action: "unpin", target: -1,
			wantErr: ErrNoBlock,
		},
		{
			name: "unknown action", action: "rewrite", target: 0, markdown: "x",
			wantErr: ErrBadEdit,
		},
		{
			name: "empty action", action: "", target: -2,
			wantErr: ErrBadEdit,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := baseDoc(t)
			for _, i := range tc.pin {
				d.Blocks[i].Pinned = true
			}
			e := Edit{Action: tc.action, Markdown: tc.markdown}
			switch {
			case tc.target == -1:
				e.BlockID = missing
			case tc.target >= 0:
				e.BlockID = d.Blocks[tc.target].ID
			}
			before := d.Clone()

			err := d.Apply(e, tc.allowPinned)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Apply error = %v, want %v", err, tc.wantErr)
				}
				if !reflect.DeepEqual(d, before) {
					t.Errorf("document changed despite error\n got: %#v\nwant: %#v", d, before)
				}
				return
			}
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := d.Markdown(); got != tc.wantMD {
				t.Errorf("Markdown after edit\n got: %q\nwant: %q", got, tc.wantMD)
			}
			if err := d.Validate(); err != nil {
				t.Errorf("Validate after edit: %v", err)
			}
			checkIDs(t, d)
			switch tc.action {
			case "pin":
				if !d.Blocks[tc.target].Pinned {
					t.Error("block not pinned")
				}
			case "unpin":
				if d.Blocks[tc.target].Pinned {
					t.Error("block still pinned")
				}
			}
		})
	}
}

func TestApplyInsertedBlocksAreFresh(t *testing.T) {
	d := baseDoc(t)
	old := map[string]bool{}
	for _, b := range d.Blocks {
		old[b.ID] = true
	}
	e := Edit{Action: "append", Markdown: "one\n\ntwo", Sources: []string{"src_new"}}
	if err := d.Apply(e, false); err != nil {
		t.Fatal(err)
	}
	added := d.Blocks[4:]
	if len(added) != 2 {
		t.Fatalf("added %d blocks, want 2", len(added))
	}
	for _, b := range added {
		if old[b.ID] {
			t.Errorf("appended block reuses id %q", b.ID)
		}
		if !reflect.DeepEqual(b.Sources, []string{"src_new"}) {
			t.Errorf("appended block sources = %v, want [src_new]", b.Sources)
		}
		if b.Pinned {
			t.Error("appended block is pinned")
		}
	}
	// Existing blocks keep their own sources.
	if !reflect.DeepEqual(d.Blocks[0].Sources, []string{"cap_base"}) {
		t.Errorf("existing block sources = %v, want [cap_base]", d.Blocks[0].Sources)
	}
}

func TestApplyReplaceUnionsSources(t *testing.T) {
	d := baseDoc(t)
	d.Blocks[1].Sources = []string{"cap_1", "src_2"}
	d.Blocks[1].Pinned = true
	oldID := d.Blocks[1].ID

	e := Edit{Action: "replace", BlockID: oldID, Markdown: "p1\n\np2", Sources: []string{"src_2", "cap_3", "cap_3"}}
	if err := d.Apply(e, true); err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 5 {
		t.Fatalf("got %d blocks, want 5", len(d.Blocks))
	}
	want := []string{"cap_1", "src_2", "cap_3"}
	for _, b := range d.Blocks[1:3] {
		if !reflect.DeepEqual(b.Sources, want) {
			t.Errorf("replacement sources = %v, want %v", b.Sources, want)
		}
		if b.Pinned {
			t.Error("replacement block inherited the pinned flag")
		}
		if b.ID == oldID {
			t.Error("replacement block reused the old id")
		}
	}
	if d.Find(oldID) != -1 {
		t.Error("old block still present")
	}
	// The two replacement blocks must not share a sources slice.
	d.Blocks[1].Sources[0] = "changed"
	if d.Blocks[2].Sources[0] != "cap_1" {
		t.Error("replacement blocks share one sources slice")
	}
}

func TestApplyReplaceWithoutSources(t *testing.T) {
	d := ParseMarkdown("a", nil)
	if err := d.Apply(Edit{Action: "replace", BlockID: d.Blocks[0].ID, Markdown: "b"}, false); err != nil {
		t.Fatal(err)
	}
	if d.Blocks[0].Sources != nil {
		t.Errorf("sources = %#v, want nil", d.Blocks[0].Sources)
	}
	if d.Blocks[0].Text != "b" {
		t.Errorf("text = %q, want b", d.Blocks[0].Text)
	}
}

func TestApplyOnEmptyDocument(t *testing.T) {
	var d Document
	if err := d.Apply(Edit{Action: "append", Markdown: "hello"}, false); err != nil {
		t.Fatal(err)
	}
	if d.Markdown() != "hello" {
		t.Errorf("Markdown = %q, want hello", d.Markdown())
	}
	if err := d.Apply(Edit{Action: "delete", BlockID: d.Blocks[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 0 {
		t.Errorf("got %d blocks after delete, want 0", len(d.Blocks))
	}
	if err := d.Apply(Edit{Action: "delete", BlockID: "b_none"}, false); !errors.Is(err, ErrNoBlock) {
		t.Errorf("delete on empty doc: err = %v, want ErrNoBlock", err)
	}
}

func TestApplyAll(t *testing.T) {
	t.Run("applies in order", func(t *testing.T) {
		d := baseDoc(t)
		edits := []Edit{
			{Action: "append", Markdown: "tail"},
			{Action: "delete", BlockID: d.Blocks[0].ID},
			{Action: "prepend", Markdown: "## New title"},
			{Action: "pin", BlockID: d.Blocks[3].ID},
		}
		if err := d.ApplyAll(edits, false); err != nil {
			t.Fatal(err)
		}
		want := "## New title\n\nfirst para\n\n- a\n- b\n\n> quoted\n\ntail"
		if got := d.Markdown(); got != want {
			t.Errorf("Markdown\n got: %q\nwant: %q", got, want)
		}
		if !d.Blocks[3].Pinned {
			t.Error("quote block not pinned")
		}
		if err := d.Validate(); err != nil {
			t.Error(err)
		}
		checkIDs(t, d)
	})

	t.Run("empty edit list is a no-op", func(t *testing.T) {
		d := baseDoc(t)
		before := d.Clone()
		if err := d.ApplyAll(nil, false); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d, before) {
			t.Error("document changed")
		}
	})

	t.Run("failure rolls back earlier edits", func(t *testing.T) {
		d := baseDoc(t)
		before := d.Clone()
		edits := []Edit{
			{Action: "append", Markdown: "ok"},
			{Action: "delete", BlockID: "b_missing"},
		}
		err := d.ApplyAll(edits, false)
		if !errors.Is(err, ErrNoBlock) {
			t.Fatalf("err = %v, want ErrNoBlock", err)
		}
		if !reflect.DeepEqual(d, before) {
			t.Errorf("document changed despite error\n got: %#v\nwant: %#v", d, before)
		}
	})

	t.Run("pinned rejection rolls back", func(t *testing.T) {
		d := baseDoc(t)
		d.Blocks[1].Pinned = true
		before := d.Clone()
		edits := []Edit{
			{Action: "delete", BlockID: d.Blocks[0].ID},
			{Action: "replace", BlockID: d.Blocks[1].ID, Markdown: "x"},
		}
		if err := d.ApplyAll(edits, false); !errors.Is(err, ErrPinned) {
			t.Fatalf("err = %v, want ErrPinned", err)
		}
		if !reflect.DeepEqual(d, before) {
			t.Error("document changed despite error")
		}
		if err := d.ApplyAll(edits, true); err != nil {
			t.Fatalf("with allowPinned: %v", err)
		}
		if got, want := d.Markdown(), "x\n\n- a\n- b\n\n> quoted"; got != want {
			t.Errorf("Markdown\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("bad edit rolls back", func(t *testing.T) {
		d := baseDoc(t)
		before := d.Clone()
		edits := []Edit{
			{Action: "prepend", Markdown: "ok"},
			{Action: "bogus"},
		}
		if err := d.ApplyAll(edits, false); !errors.Is(err, ErrBadEdit) {
			t.Fatalf("err = %v, want ErrBadEdit", err)
		}
		if !reflect.DeepEqual(d, before) {
			t.Error("document changed despite error")
		}
	})
}

func TestSetMarkdownKeepsUnchangedBlocks(t *testing.T) {
	d := ParseMarkdown("# Titel\n\nErster Absatz.\n\n- a\n- b", []string{"cap_1"})
	d.Blocks[1].Pinned = true
	ids := []string{d.Blocks[0].ID, d.Blocks[1].ID, d.Blocks[2].ID}
	// Change the list, keep heading and paragraph, add a new paragraph.
	if err := d.SetMarkdown(nil, "# Titel\n\nErster Absatz.\n\n- a\n- b\n- c\n\nNeu.", nil, false); err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 4 {
		t.Fatalf("blocks %d", len(d.Blocks))
	}
	if d.Blocks[0].ID != ids[0] || d.Blocks[1].ID != ids[1] || !d.Blocks[1].Pinned || d.Blocks[1].Sources[0] != "cap_1" {
		t.Fatalf("unchanged blocks lost identity: %+v", d.Blocks[:2])
	}
	if d.Blocks[2].ID == ids[2] || len(d.Blocks[2].Sources) != 0 {
		t.Fatalf("changed list should be a new block: %+v", d.Blocks[2])
	}
	// Removing the pinned paragraph is refused unless allowed.
	if err := d.SetMarkdown(nil, "# Titel", nil, false); err == nil {
		t.Fatal("expected ErrPinned")
	}
	if err := d.SetMarkdown(nil, "# Titel", nil, true); err != nil || len(d.Blocks) != 1 {
		t.Fatalf("allowPinned: %v %d", err, len(d.Blocks))
	}
}

func TestParseMarkdownWithGenerator(t *testing.T) {
	n := 0
	gen := func() string { n++; return "b_" + string(rune('a'+n)) }
	d := ParseMarkdownWith(gen, "x\n\ny", nil)
	if d.Blocks[0].ID != "b_b" || d.Blocks[1].ID != "b_c" {
		t.Fatalf("ids %v %v", d.Blocks[0].ID, d.Blocks[1].ID)
	}
}
