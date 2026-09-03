package doc

import (
	"reflect"
	"strings"
	"testing"
)

// stripIDs returns a copy of d with every block ID blanked, so documents can
// be compared structurally.
func stripIDs(d Document) Document {
	c := d.Clone()
	for i := range c.Blocks {
		c.Blocks[i].ID = ""
	}
	return c
}

// checkIDs asserts that every block has a fresh, unique block ID.
func checkIDs(t *testing.T, d Document) {
	t.Helper()
	seen := map[string]bool{}
	for i, b := range d.Blocks {
		if !strings.HasPrefix(b.ID, "b_") {
			t.Errorf("block %d: id %q lacks b_ prefix", i, b.ID)
		}
		if seen[b.ID] {
			t.Errorf("block %d: duplicate id %q", i, b.ID)
		}
		seen[b.ID] = true
	}
}

func TestParseMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		md      string
		sources []string
		want    []Block
	}{
		{name: "empty", md: "", want: []Block{}},
		{name: "whitespace only", md: " \n\t\n   \n", want: []Block{}},
		{
			name: "headings clamp to level 3",
			md:   "# One\n## Two\n### Three\n#### Four\n###### Six  ",
			want: []Block{
				{Type: Heading, Level: 1, Text: "One"},
				{Type: Heading, Level: 2, Text: "Two"},
				{Type: Heading, Level: 3, Text: "Three"},
				{Type: Heading, Level: 3, Text: "Four"},
				{Type: Heading, Level: 3, Text: "Six"},
			},
		},
		{
			name: "hash without space is a paragraph",
			md:   "#tag\n#",
			want: []Block{{Type: Paragraph, Text: "#tag\n#"}},
		},
		{
			name: "multi-line paragraph keeps soft breaks and trims trailing space",
			md:   "line one   \nline two\t\n\nsecond para",
			want: []Block{
				{Type: Paragraph, Text: "line one\nline two"},
				{Type: Paragraph, Text: "second para"},
			},
		},
		{
			name: "windows and bare CR line endings",
			md:   "a\r\nb\r\n\r\n# H\rtail\r\n",
			want: []Block{
				{Type: Paragraph, Text: "a\nb"},
				{Type: Heading, Level: 1, Text: "H"},
				{Type: Paragraph, Text: "tail"},
			},
		},
		{
			name: "unordered list accepts all markers",
			md:   "- a\n* b\n+ c",
			want: []Block{{Type: List, Items: []string{"a", "b", "c"}}},
		},
		{
			name: "ordered list accepts dot and paren",
			md:   "1. a\n2) b\n10. c",
			want: []Block{{Type: List, Ordered: true, Items: []string{"a", "b", "c"}}},
		},
		{
			name: "list kind change splits the list",
			md:   "- a\n1. b\n- c",
			want: []Block{
				{Type: List, Items: []string{"a"}},
				{Type: List, Ordered: true, Items: []string{"b"}},
				{Type: List, Items: []string{"c"}},
			},
		},
		{
			name: "nested bullets and continuation lines fold into the item",
			md:   "- a\n  - a1\n    - a11\n\tcont\n- b\n  more b\n1. x\n  1. x1",
			want: []Block{
				{Type: List, Items: []string{"a\n- a1\n- a11\ncont", "b\nmore b"}},
				{Type: List, Ordered: true, Items: []string{"x\n1. x1"}},
			},
		},
		{
			name: "list ends at unindented text",
			md:   "- a\ntext",
			want: []Block{
				{Type: List, Items: []string{"a"}},
				{Type: Paragraph, Text: "text"},
			},
		},
		{
			name: "blank line ends a list before indented text",
			md:   "- a\n\n  not continuation",
			want: []Block{
				{Type: List, Items: []string{"a"}},
				{Type: Paragraph, Text: "  not continuation"},
			},
		},
		{
			name: "quote with bare and empty lines",
			md:   "> a\n>b\n>\n>   indented",
			want: []Block{{Type: Quote, Text: "a\nb\n\n  indented"}},
		},
		{
			name: "callout info",
			md:   "> [!info] Note this",
			want: []Block{{Type: Callout, Kind: "info", Text: "Note this"}},
		},
		{
			name: "callout kind is case-insensitive and body continues",
			md:   "> [!WARNING] careful\n> second line",
			want: []Block{{Type: Callout, Kind: "warning", Text: "careful\nsecond line"}},
		},
		{
			name: "callout body on following lines only",
			md:   "> [!question]\n> why?\n> because",
			want: []Block{{Type: Callout, Kind: "question", Text: "why?\nbecause"}},
		},
		{
			name: "callout with no text",
			md:   "> [!external]",
			want: []Block{{Type: Callout, Kind: "external", Text: ""}},
		},
		{
			name: "unknown callout kind stays a quote",
			md:   "> [!note] hi",
			want: []Block{{Type: Quote, Text: "[!note] hi"}},
		},
		{
			name: "callout marker after the first line stays quote text",
			md:   "> plain\n> [!info] not a callout",
			want: []Block{{Type: Quote, Text: "plain\n[!info] not a callout"}},
		},
		{
			name: "code with language keeps content verbatim",
			md:   "```go\nfunc main() {}\n\n\t// x  \n```",
			want: []Block{{Type: Code, Lang: "go", Text: "func main() {}\n\n\t// x  "}},
		},
		{
			name: "code without language ignores block syntax inside",
			md:   "```\n# not heading\n- not list\n> not quote\n```",
			want: []Block{{Type: Code, Text: "# not heading\n- not list\n> not quote"}},
		},
		{
			name: "code language is the first word after the fence",
			md:   "```python title=x\nprint(1)\n```",
			want: []Block{{Type: Code, Lang: "python", Text: "print(1)"}},
		},
		{
			name: "unterminated code fence runs to end of input",
			md:   "```sh\necho hi\n\n\n",
			want: []Block{{Type: Code, Lang: "sh", Text: "echo hi"}},
		},
		{
			name: "empty code block",
			md:   "```\n```",
			want: []Block{{Type: Code, Text: ""}},
		},
		{
			name: "task ref",
			md:   "[[task_01ABC]]  ",
			want: []Block{{Type: TaskRef, Ref: "task_01ABC"}},
		},
		{
			name: "task ref with trailing text is a paragraph",
			md:   "[[task_01ABC]] do it",
			want: []Block{{Type: Paragraph, Text: "[[task_01ABC]] do it"}},
		},
		{
			name: "source ref with excerpt",
			md:   "[[src_9]]   quoted excerpt  ",
			want: []Block{{Type: SourceRef, Ref: "src_9", Text: "quoted excerpt"}},
		},
		{
			name: "source ref without excerpt",
			md:   "[[src_9]]",
			want: []Block{{Type: SourceRef, Ref: "src_9", Text: ""}},
		},
		{
			name: "other refs are paragraphs",
			md:   "[[note_1]]\n[[task_1]] x",
			want: []Block{{Type: Paragraph, Text: "[[note_1]]\n[[task_1]] x"}},
		},
		{
			name: "kind change without blank lines",
			md:   "# T\npara\n- item\n> q\n[[task_1]]\n[[src_2]] x\n```\nc\n```\ntail",
			want: []Block{
				{Type: Heading, Level: 1, Text: "T"},
				{Type: Paragraph, Text: "para"},
				{Type: List, Items: []string{"item"}},
				{Type: Quote, Text: "q"},
				{Type: TaskRef, Ref: "task_1"},
				{Type: SourceRef, Ref: "src_2", Text: "x"},
				{Type: Code, Text: "c"},
				{Type: Paragraph, Text: "tail"},
			},
		},
		{
			name: "paragraph ends when a block starts",
			md:   "text\n# H\nmore\n[[src_1]]",
			want: []Block{
				{Type: Paragraph, Text: "text"},
				{Type: Heading, Level: 1, Text: "H"},
				{Type: Paragraph, Text: "more"},
				{Type: SourceRef, Ref: "src_1"},
			},
		},
		{
			name:    "sources copied onto every block",
			md:      "p\n\n- a",
			sources: []string{"cap_1", "src_2"},
			want: []Block{
				{Type: Paragraph, Text: "p", Sources: []string{"cap_1", "src_2"}},
				{Type: List, Items: []string{"a"}, Sources: []string{"cap_1", "src_2"}},
			},
		},
		{
			name:    "empty sources become nil",
			md:      "p",
			sources: []string{},
			want:    []Block{{Type: Paragraph, Text: "p"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMarkdown(tc.md, tc.sources)
			if got.Blocks == nil {
				t.Fatal("Blocks is nil; want non-nil slice")
			}
			checkIDs(t, got)
			if err := got.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
			if g := stripIDs(got).Blocks; !reflect.DeepEqual(g, tc.want) {
				t.Errorf("blocks mismatch\n got: %#v\nwant: %#v", g, tc.want)
			}
		})
	}
}

func TestParseMarkdownSourcesNotAliased(t *testing.T) {
	in := []string{"cap_1"}
	d := ParseMarkdown("a\n\nb", in)
	in[0] = "changed"
	if d.Blocks[0].Sources[0] != "cap_1" {
		t.Errorf("block sources alias the input slice: %v", d.Blocks[0].Sources)
	}
	d.Blocks[0].Sources[0] = "other"
	if d.Blocks[1].Sources[0] != "cap_1" {
		t.Errorf("blocks share one sources slice: %v", d.Blocks[1].Sources)
	}
}

func TestMarkdown(t *testing.T) {
	tests := []struct {
		name string
		doc  Document
		want string
	}{
		{name: "empty", doc: Document{}, want: ""},
		{name: "empty non-nil", doc: Document{Blocks: []Block{}}, want: ""},
		{
			name: "headings clamp level",
			doc: Document{Blocks: []Block{
				{Type: Heading, Level: 1, Text: "a"},
				{Type: Heading, Level: 2, Text: "b"},
				{Type: Heading, Level: 3, Text: "c"},
				{Type: Heading, Level: 0, Text: "d"},
				{Type: Heading, Level: 7, Text: "e"},
			}},
			want: "# a\n\n## b\n\n### c\n\n# d\n\n### e",
		},
		{
			name: "paragraph",
			doc:  Document{Blocks: []Block{{Type: Paragraph, Text: "one\ntwo"}}},
			want: "one\ntwo",
		},
		{
			name: "unordered list indents continuation lines",
			doc:  Document{Blocks: []Block{{Type: List, Items: []string{"a\n- a1\ncont", "b"}}}},
			want: "- a\n  - a1\n  cont\n- b",
		},
		{
			name: "ordered numbering restarts per list",
			doc: Document{Blocks: []Block{
				{Type: List, Ordered: true, Items: []string{"a", "b"}},
				{Type: List, Ordered: true, Items: []string{"c"}},
			}},
			want: "1. a\n2. b\n\n1. c",
		},
		{
			name: "quote uses bare marker for empty lines",
			doc:  Document{Blocks: []Block{{Type: Quote, Text: "a\n\nb"}}},
			want: "> a\n>\n> b",
		},
		{
			name: "callout single line",
			doc:  Document{Blocks: []Block{{Type: Callout, Kind: "info", Text: "note"}}},
			want: "> [!info] note",
		},
		{
			name: "callout multi line",
			doc:  Document{Blocks: []Block{{Type: Callout, Kind: "warning", Text: "first\nsecond\n\nfourth"}}},
			want: "> [!warning] first\n> second\n>\n> fourth",
		},
		{
			name: "callout empty",
			doc:  Document{Blocks: []Block{{Type: Callout, Kind: "question", Text: ""}}},
			want: "> [!question]",
		},
		{
			name: "code with and without lang",
			doc: Document{Blocks: []Block{
				{Type: Code, Lang: "go", Text: "x := 1\n\ny := 2"},
				{Type: Code, Text: ""},
			}},
			want: "```go\nx := 1\n\ny := 2\n```\n\n```\n\n```",
		},
		{
			name: "refs",
			doc: Document{Blocks: []Block{
				{Type: TaskRef, Ref: "task_1"},
				{Type: SourceRef, Ref: "src_2"},
				{Type: SourceRef, Ref: "src_3", Text: "excerpt"},
			}},
			want: "[[task_1]]\n\n[[src_2]]\n\n[[src_3]] excerpt",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.doc.Markdown(); got != tc.want {
				t.Errorf("Markdown()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// roundTripDocs are Markdown inputs, written in the subset, that must survive
// parse → render → parse without changing structure.
var roundTripDocs = map[string]string{
	"every block type": `# Title

Intro paragraph with **bold** and a [link](https://example.com)
that continues on a second line.

## Findings

- first
- second
- third

1. step one
2. step two

> A quotation
> across two lines

> [!info] Something worth knowing

` + "```go\nfunc main() {\n\tfmt.Println(“hi”)\n}\n```" + `

[[task_01HZX]]

[[src_01ABC]] "the quoted excerpt"

[[src_01DEF]]

### Closing`,

	"nested lists with tabs and deep indentation": `- parent
  - child one
    - grandchild
	tab continuation
  plain continuation
- second parent
    * four-space nested
1. ordered
   2. nested ordered
2. next`,

	"paragraphs with soft breaks and crlf": "First line\r\nsecond line   \r\nthird\r\n\r\nNew paragraph\r\n  indented line\r\n\r\n\r\nThird paragraph\r\n",

	"all callout kinds": `> [!info] plain info

> [!Warning] mixed case
> with body

> [!QUESTION]
> body only

> [!external]

> [!info]
>
> blank then body`,

	"unterminated fence at end": "Some text\n\n```python\nprint('unterminated')\n\nmore()\n\n\n",

	"adjacent blocks without blank lines": `# H1
para line
- a
- b
> q1
> q2
1. one
[[task_9]]
[[src_9]] text
` + "```\ncode\n```" + `
tail para
## H2`,

	"quotes with empties and unknown callouts": `> line
>
>   indented inside
> [!info] not a callout here

> [!note] unknown kind is a quote
> more`,

	"refs and near misses": `[[task_1]]

[[task_2]] trailing text is a paragraph

[[src_1]]

[[src_2]] with excerpt

[[note_3]] not a ref block`,

	"headings deep and edge cases": `#### Deep heading
##### Deeper
#tag not heading
#
## Empty-ish heading   `,

	"bare dash and mixed markers": `-
* b
+ c

1) paren
2. dot`,
}

func TestRoundTrip(t *testing.T) {
	for name, md := range roundTripDocs {
		t.Run(name, func(t *testing.T) {
			d1 := ParseMarkdown(md, []string{"cap_1"})
			if err := d1.Validate(); err != nil {
				t.Fatalf("Validate(parse): %v", err)
			}
			if len(d1.Blocks) == 0 {
				t.Fatal("parsed no blocks")
			}
			m1 := d1.Markdown()
			d2 := ParseMarkdown(m1, []string{"cap_1"})
			if err := d2.Validate(); err != nil {
				t.Fatalf("Validate(reparse): %v", err)
			}
			if a, b := stripIDs(d1).Blocks, stripIDs(d2).Blocks; !reflect.DeepEqual(a, b) {
				t.Errorf("parse(render(parse(md))) != parse(md)\nfirst:  %#v\nsecond: %#v\nrendered:\n%s", a, b, m1)
			}
			if m2 := d2.Markdown(); m2 != m1 {
				t.Errorf("render is not a fixed point\nfirst:\n%s\nsecond:\n%s", m1, m2)
			}
			if strings.HasSuffix(m1, "\n") {
				t.Errorf("rendered Markdown has trailing newline: %q", m1)
			}
			if strings.Contains(m1, "\n\n\n") {
				t.Errorf("rendered Markdown has more than one blank line between blocks:\n%s", m1)
			}
		})
	}
}

func TestRoundTripCanonicalDocument(t *testing.T) {
	d := Document{Blocks: []Block{
		{ID: "b_1", Type: Heading, Level: 2, Text: "Summary"},
		{ID: "b_2", Type: Paragraph, Text: "Two\nlines"},
		{ID: "b_3", Type: List, Items: []string{"a\n- a1", "b"}},
		{ID: "b_4", Type: List, Ordered: true, Items: []string{"x", "y"}},
		{ID: "b_5", Type: Quote, Text: "q\n\nq2"},
		{ID: "b_6", Type: Callout, Kind: "external", Text: "head\nbody"},
		{ID: "b_7", Type: Code, Lang: "sh", Text: "ls\n\n# comment"},
		{ID: "b_8", Type: TaskRef, Ref: "task_1"},
		{ID: "b_9", Type: SourceRef, Ref: "src_1", Text: "excerpt"},
	}}
	md := d.Markdown()
	back := ParseMarkdown(md, nil)
	if a, b := stripIDs(d).Blocks, stripIDs(back).Blocks; !reflect.DeepEqual(a, b) {
		t.Errorf("parse(render(d)) != d\n got: %#v\nwant: %#v", b, a)
	}
	if again := back.Markdown(); again != md {
		t.Errorf("render(parse(render(d))) != render(d)\n got: %q\nwant: %q", again, md)
	}
}
