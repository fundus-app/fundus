package research

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/ids"
	"github.com/fundus-app/fundus/internal/model"
)

// Store writes the findings: one source object per cited page, one note whose
// answer sits in an "external" callout with [n] citations, and the task
// completed with a link to the note. Everything lands in one transaction, so
// one undo removes all of it. Only this function writes; the reader never
// does (ADR-0010).
func Store(ctx context.Context, c *core.Core, task *model.Task, f *Findings, actor string) (*model.Receipt, string, error) {
	if f == nil || len(f.Sources) == 0 {
		return nil, "", fmt.Errorf("nothing to store")
	}
	var ops []model.Op
	srcIDs := map[int]string{}
	for _, s := range f.Sources {
		id := ids.New(ids.PrefixSource)
		srcIDs[s.Index] = id
		title := s.Title
		ops = append(ops, model.Op{Op: "source.create", ID: id, URL: s.URL, Title: &title, Text: s.Excerpt, Query: s.Query})
	}
	// Renumber citations to the order in which sources appear in the note.
	renum := map[int]int{}
	for i, s := range f.Sources {
		renum[s.Index] = i + 1
	}
	answer := renumber(f.Answer, renum)
	var md strings.Builder
	md.WriteString("> [!external] ")
	first := true
	for _, line := range strings.Split(answer, "\n") {
		if !first {
			md.WriteString("\n> ")
		}
		md.WriteString(line)
		first = false
	}
	md.WriteString("\n")
	if len(f.Findings) > 0 {
		md.WriteString("\n## Findings\n\n")
		for _, fd := range f.Findings {
			var cites []string
			for _, n := range fd.Sources {
				if m, ok := renum[n]; ok {
					cites = append(cites, fmt.Sprintf("[%d]", m))
				}
			}
			fmt.Fprintf(&md, "- %s %s\n", strings.TrimSpace(fd.Claim), strings.Join(cites, ""))
		}
	}
	md.WriteString("\n## Sources\n\n")
	for i, s := range f.Sources {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = hostOf(s.URL)
		}
		fmt.Fprintf(&md, "[[%s]] [%d] %s — %s (retrieved %s)\n", srcIDs[s.Index], i+1, title, s.URL, s.FetchedAt.Format("2 Jan 2006"))
	}
	if len(f.Uncertainties) > 0 {
		md.WriteString("\n## Open questions\n\n")
		for _, u := range f.Uncertainties {
			fmt.Fprintf(&md, "- %s\n", strings.TrimSpace(u))
		}
	}
	fmt.Fprintf(&md, "\nResearched on %s with %s (search: %s), %d searches, %d pages read.\n",
		f.Finished.Format("2 Jan 2006"), f.Model, f.Backend, f.Searches, f.Pages)

	noteID := ids.New(ids.PrefixNote)
	// The note is titled with the question itself, in the user's language.
	title := trimSentence(model.Shorten(Question(task), 120))
	ops = append(ops, model.Op{Op: "note.create", ID: noteID, Kind: string(model.NoteKindNote), Title: &title, Markdown: md.String(),
		Topics: task.Topics, Origins: task.Origins})
	ops = append(ops, model.Op{Op: "task.update", ID: task.ID, ExpectedRev: task.Rev, State: string(model.TaskDone), AddNotes: []string{noteID}})
	rec, err := c.Commit(ctx, actor, &model.Cause{Kind: "research", ID: task.ID}, ops)
	if err != nil {
		return nil, "", err
	}
	return rec, noteID, nil
}

// renumber rewrites [n] citations in text through the map, dropping unknown
// ones.
func renumber(text string, m map[int]int) string {
	var sb strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '[' {
			j := i + 1
			n := 0
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				n = n*10 + int(text[j]-'0')
				j++
			}
			if j > i+1 && j < len(text) && text[j] == ']' {
				if k, ok := m[n]; ok {
					fmt.Fprintf(&sb, "[%d]", k)
				}
				i = j
				continue
			}
		}
		sb.WriteByte(text[i])
	}
	return sb.String()
}

func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "..") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	}
	return s
}

// Actor names the research worker as a model actor.
func Actor(provider, modelName string) string {
	return "llm:research/" + provider + "/" + modelName
}

var _ = time.Now
