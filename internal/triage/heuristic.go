package triage

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/fundus-app/fundus/internal/index"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// Heuristic is a model-free provider. It files captures with simple rules so
// Fundus can be tried (and tested) without any API key. Conversation requests
// get a fixed reply.
type Heuristic struct{ name string }

// NewHeuristic returns the fake provider.
func NewHeuristic(name string) llm.Provider {
	if name == "" {
		name = "fake"
	}
	return &Heuristic{name: name}
}

func (h *Heuristic) Name() string { return h.name }

var (
	captureRe = regexp.MustCompile(`(?s)<capture[^>]*>\n(.*?)\n</capture>`)
	contextRe = regexp.MustCompile(`(?s)<context>\n(.*?)</context>`)
	taskRe    = regexp.MustCompile(`(?i)\b(ich muss|muss ich|ich sollte|sollte ich|nicht vergessen|erinner|todo|to-do|to do|i must|i need to|i should|i have to|remind me|don't forget|check whether|prüfen|fix|reparieren|kaufen|buy|anrufen|call)\b`)
	ideaRe    = regexp.MustCompile(`(?i)\b(idee|idea|vielleicht|maybe|könnte man|could|wäre (es )?(cool|sinnvoll|schön)|would be (nice|cool)|irgendwann|someday)\b`)
	doneRe    = regexp.MustCompile(`(?i)\b(erledigt|habe ich (gemacht|erledigt)|done|finished|fertig)\b`)
)

func (h *Heuristic) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req.Schema == nil || req.Schema.Name != SchemaName {
		if len(req.Tools) > 0 || req.Schema == nil {
			return &llm.Response{Model: "heuristic", Content: "The rules provider files captures but cannot hold a conversation. Choose a model in Settings to ask questions."}, nil
		}
		return &llm.Response{Model: "heuristic", Content: "{}"}, nil
	}
	var last string
	for _, m := range req.Messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	text := ""
	if m := captureRe.FindStringSubmatch(last); m != nil {
		text = strings.TrimSpace(m[1])
	}
	var tctx struct {
		Mentioned []TopicCtx `json:"mentioned_topics"`
		Tasks     []TaskCtx  `json:"open_tasks"`
	}
	if m := contextRe.FindStringSubmatch(last); m != nil {
		_ = json.Unmarshal([]byte(m[1]), &tctx)
	}
	var topics []string
	for _, t := range tctx.Mentioned {
		topics = append(topics, t.ID)
	}
	res := Result{Confidence: 0.75, Summary: "Filed by heuristic."}
	title := model.Shorten(strings.SplitN(text, "\n", 2)[0], 70)
	switch {
	case text == "":
		res.Classification = "unclear"
		res.Confidence = 0.2
		res.Question = "The capture was empty."
	case doneRe.MatchString(text) && matchingTask(text, tctx.Tasks) != "":
		res.Classification = "info"
		res.Operations = []Operation{{Op: "task.complete", TaskID: matchingTask(text, tctx.Tasks)}}
		res.Summary = "Marked the matching task as done."
	case ideaRe.MatchString(text):
		res.Classification = "idea"
		res.Operations = []Operation{{Op: "note.create", Kind: "idea", Title: title, Markdown: text, Topics: topics}}
		res.Summary = "Saved as an idea."
	case taskRe.MatchString(text):
		res.Classification = "task"
		res.Operations = []Operation{{Op: "task.create", Text: title, Topics: topics}}
		res.Summary = "Created a task."
	case strings.HasSuffix(strings.TrimSpace(text), "?"):
		res.Classification = "question"
		res.Operations = []Operation{{Op: "note.create", Kind: "note", Title: title, Markdown: text, Topics: topics}}
		res.Summary = "Saved the question as a note."
	default:
		res.Classification = "note"
		res.Operations = []Operation{{Op: "note.create", Kind: "note", Title: title, Markdown: text, Topics: topics}}
		res.Summary = "Saved as a note."
	}
	out, _ := json.Marshal(res)
	return &llm.Response{Model: "heuristic", Content: string(out)}, nil
}

// matchingTask returns the open task sharing at least two content tokens
// with the text, or "".
func matchingTask(text string, tasks []TaskCtx) string {
	words := map[string]bool{}
	for _, w := range index.Tokenize(text) {
		words[w] = true
	}
	best, bestN := "", 0
	for _, t := range tasks {
		n := 0
		for _, w := range index.Tokenize(t.Text) {
			if words[w] {
				n++
			}
		}
		if n > bestN {
			best, bestN = t.ID, n
		}
	}
	if bestN >= 2 {
		return best
	}
	return ""
}
