package doc

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// The Markdown subset.
//
// ParseMarkdown and Document.Markdown map between the block model and a
// deliberately small Markdown dialect. Blocks are separated by blank lines or
// by a change of block kind. Line-level syntax:
//
//	# / ## / ###      heading (deeper levels clamp to 3)
//	```lang … ```     fenced code, verbatim
//	> text            quote; "> [!kind] text" on the first line makes a callout
//	- item / 1. item  lists; lines indented by 2+ spaces or a tab continue the
//	                  previous item (nested bullets survive as text lines)
//	[[task_…]]        task reference (alone on its line)
//	[[src_…]] text    source reference with optional excerpt
//	anything else     paragraph; consecutive lines keep their soft breaks
//
// The mapping is stable: parsing the Markdown produced by Document.Markdown
// yields the same blocks (ignoring IDs), and re-serialising that document
// reproduces the same Markdown byte for byte.

var (
	reListItem  = regexp.MustCompile(`^([ \t]*)([-*+]|[0-9]+[.)]) (.*)$`)
	reTaskRef   = regexp.MustCompile(`^\[\[(task_[A-Za-z0-9]+)\]\]$`)
	reSourceRef = regexp.MustCompile(`^\[\[(src_[A-Za-z0-9]+)\]\](.*)$`)
	reCallout   = regexp.MustCompile(`^\[!([A-Za-z]+)\][ \t]*(.*)$`)
)

// ParseMarkdown parses md, written in the Markdown subset described above,
// into a Document. Every block receives a fresh ID and its own copy of
// sources (nil when sources is empty). Empty or whitespace-only input yields
// a document with a non-nil, empty block slice.
func ParseMarkdown(md string, sources []string) Document {
	return ParseMarkdownWith(nil, md, sources)
}

// ParseMarkdownWith is ParseMarkdown with an explicit block-ID generator.
func ParseMarkdownWith(gen IDGen, md string, sources []string) Document {
	p := parser{lines: splitLines(md), blocks: []Block{}, gen: gen}
	if len(sources) > 0 {
		p.sources = append([]string(nil), sources...)
	}
	for p.i < len(p.lines) {
		line := p.lines[p.i]
		switch {
		case isBlank(line):
			p.i++
		case isFence(line):
			p.parseCode()
		case isHeading(line):
			p.parseHeading()
		case isTaskRef(line):
			p.parseTaskRef()
		case isSourceRef(line):
			p.parseSourceRef()
		case isQuote(line):
			p.parseQuote()
		case isListItem(line):
			p.parseList()
		default:
			p.parseParagraph()
		}
	}
	return Document{Blocks: p.blocks}
}

// Markdown renders the document in the Markdown subset. Blocks are separated
// by exactly one blank line and the output carries no trailing newline. An
// empty document renders as "".
func (d Document) Markdown() string {
	parts := make([]string, len(d.Blocks))
	for i, b := range d.Blocks {
		parts[i] = blockMarkdown(b)
	}
	return strings.Join(parts, "\n\n")
}

func blockMarkdown(b Block) string {
	switch b.Type {
	case Heading:
		lvl := b.Level
		if lvl < 1 {
			lvl = 1
		} else if lvl > 3 {
			lvl = 3
		}
		return strings.Repeat("#", lvl) + " " + b.Text
	case List:
		var sb strings.Builder
		for i, it := range b.Items {
			if i > 0 {
				sb.WriteByte('\n')
			}
			if b.Ordered {
				sb.WriteString(strconv.Itoa(i + 1))
				sb.WriteString(". ")
			} else {
				sb.WriteString("- ")
			}
			lines := strings.Split(it, "\n")
			sb.WriteString(lines[0])
			for _, l := range lines[1:] {
				sb.WriteString("\n  ")
				sb.WriteString(l)
			}
		}
		return sb.String()
	case Quote:
		return quoteLines(b.Text)
	case Callout:
		head := "> [!" + b.Kind + "]"
		if b.Text == "" {
			return head
		}
		first, rest, more := strings.Cut(b.Text, "\n")
		if first == "" {
			// The body starts with an empty line: keep it as a bare ">" so
			// it survives a round trip.
			return head + "\n" + quoteLines(b.Text)
		}
		head += " " + first
		if more {
			head += "\n" + quoteLines(rest)
		}
		return head
	case Code:
		return "```" + cleanLang(b.Lang) + "\n" + b.Text + "\n```"
	case TaskRef:
		return "[[" + b.Ref + "]]"
	case SourceRef:
		s := "[[" + b.Ref + "]]"
		if b.Text != "" {
			s += " " + b.Text
		}
		return s
	default: // Paragraph, and a best effort for unknown types.
		return b.Text
	}
}

// quoteLines prefixes every line of text with "> ", using a bare ">" for
// empty lines so the output never carries trailing whitespace.
func quoteLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

// parser is a line-oriented state machine over the normalised input.
type parser struct {
	lines   []string
	i       int
	sources []string
	blocks  []Block
	gen     IDGen
}

func (p *parser) newBlock(t BlockType) Block {
	b := Block{ID: p.gen.next(), Type: t}
	if len(p.sources) > 0 {
		b.Sources = append([]string(nil), p.sources...)
	}
	return b
}

func (p *parser) emit(b Block) { p.blocks = append(p.blocks, b) }

func (p *parser) parseHeading() {
	line := p.lines[p.i]
	p.i++
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	b := p.newBlock(Heading)
	b.Level = min(n, 3)
	b.Text = strings.TrimSpace(line[n:])
	p.emit(b)
}

func (p *parser) parseCode() {
	line := p.lines[p.i]
	p.i++
	b := p.newBlock(Code)
	if f := strings.Fields(strings.TrimLeft(line[3:], "`")); len(f) > 0 {
		b.Lang = cleanLang(f[0])
	}
	var inner []string
	closed := false
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		p.i++
		if strings.TrimSpace(l) == "```" {
			closed = true
			break
		}
		inner = append(inner, l)
	}
	if !closed {
		// An unterminated fence runs to the end of input; drop the blank
		// lines that trail the actual content.
		for len(inner) > 0 && isBlank(inner[len(inner)-1]) {
			inner = inner[:len(inner)-1]
		}
	}
	b.Text = strings.Join(inner, "\n")
	p.emit(b)
}

func (p *parser) parseTaskRef() {
	m := reTaskRef.FindStringSubmatch(trimRight(p.lines[p.i]))
	p.i++
	b := p.newBlock(TaskRef)
	b.Ref = m[1]
	p.emit(b)
}

func (p *parser) parseSourceRef() {
	m := reSourceRef.FindStringSubmatch(p.lines[p.i])
	p.i++
	b := p.newBlock(SourceRef)
	b.Ref = m[1]
	b.Text = strings.TrimSpace(m[2])
	p.emit(b)
}

func (p *parser) parseQuote() {
	var lines []string
	for p.i < len(p.lines) && isQuote(p.lines[p.i]) {
		lines = append(lines, quoteText(p.lines[p.i]))
		p.i++
	}
	if m := reCallout.FindStringSubmatch(lines[0]); m != nil {
		if kind := strings.ToLower(m[1]); isCalloutKind(kind) {
			b := p.newBlock(Callout)
			b.Kind = kind
			body := lines[1:]
			if first := strings.TrimSpace(m[2]); first != "" {
				body = append([]string{first}, body...)
			}
			b.Text = strings.Join(body, "\n")
			p.emit(b)
			return
		}
	}
	b := p.newBlock(Quote)
	b.Text = strings.Join(lines, "\n")
	p.emit(b)
}

func (p *parser) parseList() {
	m := reListItem.FindStringSubmatch(p.lines[p.i])
	p.i++
	b := p.newBlock(List)
	b.Ordered = isOrderedMarker(m[2])
	b.Items = []string{strings.TrimSpace(m[3])}
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if isBlank(l) {
			break
		}
		if isContinuation(l) {
			b.Items[len(b.Items)-1] += "\n" + strings.TrimSpace(l)
			p.i++
			continue
		}
		m := reListItem.FindStringSubmatch(l)
		if m == nil || isOrderedMarker(m[2]) != b.Ordered {
			break
		}
		b.Items = append(b.Items, strings.TrimSpace(m[3]))
		p.i++
	}
	p.emit(b)
}

func (p *parser) parseParagraph() {
	var lines []string
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if isBlank(l) || (len(lines) > 0 && isBlockStart(l)) {
			break
		}
		lines = append(lines, trimRight(l))
		p.i++
	}
	b := p.newBlock(Paragraph)
	b.Text = strings.Join(lines, "\n")
	p.emit(b)
}

// Line classification. All predicates take the raw (line-ending normalised)
// line; they tolerate trailing whitespace where the syntax allows it.

func isBlank(line string) bool     { return strings.TrimSpace(line) == "" }
func isFence(line string) bool     { return strings.HasPrefix(line, "```") }
func isQuote(line string) bool     { return strings.HasPrefix(line, ">") }
func isListItem(line string) bool  { return reListItem.MatchString(line) }
func isTaskRef(line string) bool   { return reTaskRef.MatchString(trimRight(line)) }
func isSourceRef(line string) bool { return reSourceRef.MatchString(line) }

func isHeading(line string) bool {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n > 0 && n < len(line) && line[n] == ' '
}

// isContinuation reports whether a line inside a list continues the previous
// item: it is indented by at least two spaces or a tab. Any list marker at
// that indentation is a nested item, which is likewise folded into the parent.
func isContinuation(line string) bool {
	return strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")
}

// isBlockStart reports whether line begins a block other than a paragraph and
// therefore terminates a paragraph in progress.
func isBlockStart(line string) bool {
	return isFence(line) || isHeading(line) || isTaskRef(line) ||
		isSourceRef(line) || isQuote(line) || isListItem(line)
}

func isOrderedMarker(marker string) bool { return marker[0] >= '0' && marker[0] <= '9' }

func isCalloutKind(kind string) bool {
	switch kind {
	case "info", "warning", "question", "external":
		return true
	}
	return false
}

// quoteText strips the leading ">" and at most one following space.
func quoteText(line string) string {
	return trimRight(strings.TrimPrefix(strings.TrimPrefix(line, ">"), " "))
}

func trimRight(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

// splitLines normalises line endings and splits the input into lines. A
// single trailing newline does not produce a final empty line.
func splitLines(md string) []string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")
	lines := strings.Split(md, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// cleanLang keeps only the characters a fence info string may carry, so
// that serialising "```" + Lang can never produce a different fence.
func cleanLang(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '+', r == '#', r == '.':
			out.WriteRune(r)
		}
	}
	return out.String()
}
