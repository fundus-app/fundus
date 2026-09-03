package core

import (
	"archive/zip"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

// Export is the full JSON export: every object plus the audit trail summary.
type Export struct {
	Version    int              `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	Seq        uint64           `json:"seq"`
	Objects    []model.Object   `json:"objects"`
	Changes    []*model.Receipt `json:"changes"`
}

// ExportJSON returns everything in the canonical JSON model.
func (c *Core) ExportJSON() *Export {
	ex := &Export{Version: 1, ExportedAt: c.opts.Now().UTC(), Seq: c.Seq()}
	c.Each(nil, func(o model.Object) bool { ex.Objects = append(ex.Objects, o); return true })
	sort.Slice(ex.Objects, func(i, j int) bool {
		a, b := ex.Objects[i].GetMeta(), ex.Objects[j].GetMeta()
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	ex.Changes = c.Changes(ChangesQuery{IncludeQuiet: true})
	if ex.Objects == nil {
		ex.Objects = []model.Object{}
	}
	if ex.Changes == nil {
		ex.Changes = []*model.Receipt{}
	}
	return ex
}

// ExportMarkdownZip writes a zip of Markdown files: one per note and topic,
// a tasks file, and the raw captures. Front matter carries ids and links so
// the export stays traceable.
func (c *Core) ExportMarkdownZip(w io.Writer) error {
	zw := zip.NewWriter(w)
	add := func(name, content string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		return err
	}
	name := func(o model.Object) string {
		t := strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
				return r
			case r == 'ä':
				return 'a'
			case r == 'ö':
				return 'o'
			case r == 'ü':
				return 'u'
			}
			return -1
		}, o.Title())
		t = strings.TrimSpace(t)
		if len(t) > 60 {
			t = t[:60]
		}
		if t == "" {
			t = o.GetMeta().ID
		}
		return strings.ReplaceAll(t, " ", "-") + "-" + shortID(o.GetMeta().ID) + ".md"
	}
	topicName := func(id string) string {
		if o, err := c.Get(id); err == nil {
			return o.Title()
		}
		return id
	}

	for _, n := range c.Notes("", true) {
		var sb strings.Builder
		fmt.Fprintf(&sb, "---\nid: %s\ntype: note\nkind: %s\ncreated: %s\nupdated: %s\n", n.ID, n.Kind, n.CreatedAt.Format(time.RFC3339), n.UpdatedAt.Format(time.RFC3339))
		if len(n.Topics) > 0 {
			sb.WriteString("topics:\n")
			for _, t := range n.Topics {
				fmt.Fprintf(&sb, "  - %s  # %s\n", t, topicName(t))
			}
		}
		if len(n.Origins) > 0 {
			fmt.Fprintf(&sb, "origins: [%s]\n", strings.Join(n.Origins, ", "))
		}
		if n.Archived {
			sb.WriteString("archived: true\n")
		}
		fmt.Fprintf(&sb, "---\n\n# %s\n\n%s\n", n.NoteTitle, n.Body.Markdown())
		dir := "notes/"
		if n.Kind == model.NoteKindIdea {
			dir = "ideas/"
		}
		if err := add(dir+name(n.Note), sb.String()); err != nil {
			return err
		}
	}
	for _, tv := range c.Topics(true) {
		page, err := c.Topic(tv.ID)
		if err != nil {
			continue
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "---\nid: %s\ntype: topic\nkind: %s\n", tv.ID, tv.Kind)
		if len(tv.Aliases) > 0 {
			fmt.Fprintf(&sb, "aliases: [%s]\n", strings.Join(tv.Aliases, ", "))
		}
		fmt.Fprintf(&sb, "---\n\n# %s\n\n", tv.Name)
		if md := tv.Summary.Markdown(); md != "" {
			sb.WriteString(md + "\n\n")
		}
		if len(page.Notes) > 0 {
			sb.WriteString("## Notes\n\n")
			for _, n := range page.Notes {
				fmt.Fprintf(&sb, "- [[%s]] %s\n", n.ID, n.NoteTitle)
			}
			sb.WriteString("\n")
		}
		if len(page.Tasks) > 0 {
			sb.WriteString("## Tasks\n\n")
			for _, t := range page.Tasks {
				box := " "
				if t.State == model.TaskDone {
					box = "x"
				}
				fmt.Fprintf(&sb, "- [%s] %s  <!-- %s, %s -->\n", box, t.Text, t.ID, t.State)
			}
		}
		if err := add("topics/"+name(tv.Topic), sb.String()); err != nil {
			return err
		}
	}
	{
		var sb strings.Builder
		sb.WriteString("# Tasks\n\n")
		for _, state := range []model.TaskState{model.TaskOpen, model.TaskWaiting, model.TaskLater, model.TaskDone} {
			tasks := c.Tasks([]model.TaskState{state}, true)
			if len(tasks) == 0 {
				continue
			}
			fmt.Fprintf(&sb, "## %s\n\n", strings.ToUpper(string(state[:1]))+string(state[1:]))
			for _, t := range tasks {
				box := " "
				if t.State == model.TaskDone {
					box = "x"
				}
				fmt.Fprintf(&sb, "- [%s] %s", box, t.Text)
				if t.Due != "" {
					fmt.Fprintf(&sb, " (due %s)", t.Due)
				}
				if len(t.TopicNames) > 0 {
					fmt.Fprintf(&sb, " [%s]", strings.Join(t.TopicNames, ", "))
				}
				fmt.Fprintf(&sb, "  <!-- %s -->\n", t.ID)
			}
			sb.WriteString("\n")
		}
		if err := add("tasks.md", sb.String()); err != nil {
			return err
		}
	}
	{
		var sb strings.Builder
		sb.WriteString("# Captures\n\nRaw input, newest first. Never edited by Fundus.\n\n")
		for _, cap := range c.Captures("", 0) {
			fmt.Fprintf(&sb, "## %s  <!-- %s, %s, %s -->\n\n%s\n\n", cap.CreatedAt.Local().Format("2006-01-02 15:04"), cap.ID, cap.Source, cap.Status, cap.Text)
		}
		if err := add("captures.md", sb.String()); err != nil {
			return err
		}
	}
	return zw.Close()
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '_'); i >= 0 && len(id) > i+9 {
		return strings.ToLower(id[len(id)-8:])
	}
	return id
}
