package core

import (
	"fmt"
	"sort"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

// TaskView is a task with its computed attention score and resolved names.
type TaskView struct {
	*model.Task
	Score      float64  `json:"score"`
	Reasons    []string `json:"reasons,omitempty"`
	TopicNames []string `json:"topic_names,omitempty"`
}

// NoteView is a note with resolved topic names.
type NoteView struct {
	*model.Note
	TopicNames []string `json:"topic_names,omitempty"`
	Preview    string   `json:"preview"`
}

// TopicView adds counts to a topic.
type TopicView struct {
	*model.Topic
	NoteCount     int       `json:"note_count"`
	OpenTaskCount int       `json:"open_task_count"`
	LastActivity  time.Time `json:"last_activity"`
}

// TopicPage bundles everything linked to a topic.
type TopicPage struct {
	Topic *model.Topic `json:"topic"`
	Notes []NoteView   `json:"notes"`
	Tasks []TaskView   `json:"tasks"`
}

// Stats summarizes the knowledge base.
type Stats struct {
	Captures      int    `json:"captures"`
	Inbox         int    `json:"inbox"`
	Notes         int    `json:"notes"`
	Ideas         int    `json:"ideas"`
	OpenTasks     int    `json:"open_tasks"`
	Topics        int    `json:"topics"`
	Conversations int    `json:"conversations"`
	Seq           uint64 `json:"seq"`
}

// Inbox lists captures that still need attention, newest first.
func (c *Core) Inbox() []*model.Capture {
	var out []*model.Capture
	c.Each([]model.Type{model.TypeCapture}, func(o model.Object) bool {
		cap := o.(*model.Capture)
		switch cap.Status {
		case model.CapturePending, model.CaptureProcessing, model.CaptureNeedsReview, model.CaptureFailed:
			out = append(out, cap)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Captures lists captures newest first, optionally filtered by status.
func (c *Core) Captures(status model.CaptureStatus, limit int) []*model.Capture {
	var out []*model.Capture
	c.Each([]model.Type{model.TypeCapture}, func(o model.Object) bool {
		cap := o.(*model.Capture)
		if status == "" || cap.Status == status {
			out = append(out, cap)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// PendingCaptures returns captures waiting for a worker, oldest first.
func (c *Core) PendingCaptures() []*model.Capture {
	var out []*model.Capture
	c.Each([]model.Type{model.TypeCapture}, func(o model.Object) bool {
		cap := o.(*model.Capture)
		if cap.Status == model.CapturePending {
			out = append(out, cap)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (c *Core) topicActivity() map[string]time.Time {
	act := map[string]time.Time{}
	bump := func(topics []string, at time.Time) {
		for _, t := range topics {
			if at.After(act[t]) {
				act[t] = at
			}
		}
	}
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		switch v := o.(type) {
		case *model.Note:
			bump(v.Topics, v.UpdatedAt)
		case *model.Task:
			bump(v.Topics, v.UpdatedAt)
		}
		return true
	})
	return act
}

func (c *Core) topicNames(ids []string) []string {
	var names []string
	for _, id := range ids {
		if o, err := c.Get(id); err == nil {
			names = append(names, o.Title())
		}
	}
	return names
}

// Tasks returns tasks in the given states (all when empty), scored and sorted
// by attention score, then newest first. Archived tasks are excluded unless
// includeArchived is set.
func (c *Core) Tasks(states []model.TaskState, includeArchived bool) []TaskView {
	want := map[model.TaskState]bool{}
	for _, s := range states {
		want[s] = true
	}
	now := c.Now()
	act := c.topicActivity()
	var out []TaskView
	c.Each([]model.Type{model.TypeTask}, func(o model.Object) bool {
		t := o.(*model.Task)
		if t.Archived && !includeArchived {
			return true
		}
		if len(want) > 0 && !want[t.State] {
			return true
		}
		s, why := Score(t, now, act)
		out = append(out, TaskView{Task: t, Score: s, Reasons: why, TopicNames: c.topicNames(t.Topics)})
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Relevant returns the open tasks most worth attention right now.
func (c *Core) Relevant(limit int) []TaskView {
	all := c.Tasks([]model.TaskState{model.TaskOpen}, false)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}

// Notes lists notes of a kind (all when empty), newest first.
func (c *Core) Notes(kind model.NoteKind, includeArchived bool) []NoteView {
	var out []NoteView
	c.Each([]model.Type{model.TypeNote}, func(o model.Object) bool {
		n := o.(*model.Note)
		if n.Archived && !includeArchived {
			return true
		}
		if kind != "" && n.Kind != kind {
			return true
		}
		out = append(out, c.noteView(n))
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (c *Core) noteView(n *model.Note) NoteView {
	return NoteView{Note: n, TopicNames: c.topicNames(n.Topics), Preview: model.Shorten(n.Body.PlainText(), 240)}
}

// Topics lists topics with link counts, most recently active first.
func (c *Core) Topics(includeArchived bool) []TopicView {
	views := map[string]*TopicView{}
	c.Each([]model.Type{model.TypeTopic}, func(o model.Object) bool {
		t := o.(*model.Topic)
		if t.Archived && !includeArchived {
			return true
		}
		views[t.ID] = &TopicView{Topic: t, LastActivity: t.UpdatedAt}
		return true
	})
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		switch v := o.(type) {
		case *model.Note:
			if v.Archived {
				return true
			}
			for _, id := range v.Topics {
				if tv, ok := views[id]; ok {
					tv.NoteCount++
					if v.UpdatedAt.After(tv.LastActivity) {
						tv.LastActivity = v.UpdatedAt
					}
				}
			}
		case *model.Task:
			if v.Archived {
				return true
			}
			for _, id := range v.Topics {
				if tv, ok := views[id]; ok {
					if v.State == model.TaskOpen {
						tv.OpenTaskCount++
					}
					if v.UpdatedAt.After(tv.LastActivity) {
						tv.LastActivity = v.UpdatedAt
					}
				}
			}
		}
		return true
	})
	out := make([]TopicView, 0, len(views))
	for _, v := range views {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastActivity.Equal(out[j].LastActivity) {
			return out[i].LastActivity.After(out[j].LastActivity)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Topic returns a topic page with everything linked to it.
func (c *Core) Topic(id string) (*TopicPage, error) {
	o, err := c.Get(id)
	if err != nil {
		return nil, err
	}
	t, ok := o.(*model.Topic)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a topic", ErrInvalid, id)
	}
	page := &TopicPage{Topic: t}
	now := c.Now()
	act := c.topicActivity()
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		switch v := o.(type) {
		case *model.Note:
			if !v.Archived && contains(v.Topics, id) {
				page.Notes = append(page.Notes, c.noteView(v))
			}
		case *model.Task:
			if !v.Archived && contains(v.Topics, id) {
				s, why := Score(v, now, act)
				page.Tasks = append(page.Tasks, TaskView{Task: v, Score: s, Reasons: why, TopicNames: c.topicNames(v.Topics)})
			}
		}
		return true
	})
	sort.Slice(page.Notes, func(i, j int) bool { return page.Notes[i].UpdatedAt.After(page.Notes[j].UpdatedAt) })
	sort.Slice(page.Tasks, func(i, j int) bool {
		if page.Tasks[i].State != page.Tasks[j].State {
			return page.Tasks[i].State == model.TaskOpen
		}
		return page.Tasks[i].Score > page.Tasks[j].Score
	})
	return page, nil
}

// Backlinks returns objects that reference id (notes via topics/related, tasks
// via topics/notes).
func (c *Core) Backlinks(id string) []model.Object {
	var out []model.Object
	c.Each([]model.Type{model.TypeNote, model.TypeTask}, func(o model.Object) bool {
		switch v := o.(type) {
		case *model.Note:
			if contains(v.Topics, id) || contains(v.Related, id) || contains(v.Origins, id) {
				out = append(out, v)
			}
		case *model.Task:
			if contains(v.Topics, id) || contains(v.Notes, id) || contains(v.Origins, id) {
				out = append(out, v)
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].GetMeta().UpdatedAt.After(out[j].GetMeta().UpdatedAt) })
	return out
}

// Conversations lists conversations, most recently active first.
func (c *Core) Conversations(limit int) []*model.Conversation {
	var out []*model.Conversation
	c.Each([]model.Type{model.TypeConversation}, func(o model.Object) bool {
		cv := o.(*model.Conversation)
		if !cv.Archived {
			out = append(out, cv)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].LastMessageAt, out[j].LastMessageAt
		if a.IsZero() {
			a = out[i].CreatedAt
		}
		if b.IsZero() {
			b = out[j].CreatedAt
		}
		return a.After(b)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Messages returns the messages of a conversation in order.
func (c *Core) Messages(convID string) []*model.Message {
	var out []*model.Message
	c.Each([]model.Type{model.TypeMessage}, func(o model.Object) bool {
		m := o.(*model.Message)
		if m.ConversationID == convID {
			out = append(out, m)
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// ConversationView is a conversation with its messages, the shape clients read.
type ConversationView struct {
	*model.Conversation
	Messages []*model.Message `json:"messages"`
}

// Conversation returns a conversation with its messages.
func (c *Core) Conversation(id string) (*ConversationView, error) {
	o, err := c.Get(id)
	if err != nil {
		return nil, err
	}
	cv, ok := o.(*model.Conversation)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a conversation", ErrInvalid, id)
	}
	msgs := c.Messages(id)
	if msgs == nil {
		msgs = []*model.Message{}
	}
	return &ConversationView{Conversation: cv, Messages: msgs}, nil
}

// Stats counts objects.
func (c *Core) Stats() Stats {
	var s Stats
	c.Each(nil, func(o model.Object) bool {
		switch v := o.(type) {
		case *model.Capture:
			s.Captures++
			switch v.Status {
			case model.CapturePending, model.CaptureProcessing, model.CaptureNeedsReview, model.CaptureFailed:
				s.Inbox++
			}
		case *model.Note:
			if !v.Archived {
				if v.Kind == model.NoteKindIdea {
					s.Ideas++
				} else {
					s.Notes++
				}
			}
		case *model.Task:
			if !v.Archived && v.State == model.TaskOpen {
				s.OpenTasks++
			}
		case *model.Topic:
			if !v.Archived {
				s.Topics++
			}
		case *model.Conversation:
			s.Conversations++
		}
		return true
	})
	s.Seq = c.Seq()
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
