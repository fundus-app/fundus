package doc

import "testing"

// FuzzParseMarkdownRoundTrip checks that any input parses without panicking,
// that the result validates, and that serialising and re-parsing is stable.
func FuzzParseMarkdownRoundTrip(f *testing.F) {
	for _, seed := range []string{"", "# H\n\ntext", "- a\n- b\n  cont", "> [!info] x\n> y", "```go\ncode\n", "[[task_A1]]", "[[src_X]] quote", "\r\nline\r\n", "####### deep", "1. a\n2) b", "> [!weird] z"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, md string) {
		d := ParseMarkdown(md, []string{"cap_1"})
		if err := d.Validate(); err != nil {
			t.Fatalf("invalid document from %q: %v", md, err)
		}
		once := d.Markdown()
		again := ParseMarkdown(once, nil).Markdown()
		if once != again {
			t.Fatalf("not stable:\n%q\n%q", once, again)
		}
	})
}

// FuzzSetMarkdown checks whole-body replacement never panics or corrupts ids.
func FuzzSetMarkdown(f *testing.F) {
	f.Add("a\n\nb", "a\n\nc")
	f.Add("# x\n\n- 1\n- 2", "")
	f.Fuzz(func(t *testing.T, first, second string) {
		d := ParseMarkdown(first, []string{"cap_1"})
		if len(d.Blocks) > 0 {
			d.Blocks[0].Pinned = true
		}
		_ = d.SetMarkdown(nil, second, nil, true)
		if err := d.Validate(); err != nil {
			t.Fatalf("invalid after SetMarkdown: %v", err)
		}
	})
}
