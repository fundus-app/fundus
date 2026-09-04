package research

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
)

// Progress is the payload of research.progress events.
type Progress struct {
	TaskID  string `json:"task_id"`
	Step    string `json:"step"` // search | fetch | read | store | done | error
	Summary string `json:"summary,omitempty"`
	NoteID  string `json:"note_id,omitempty"`
	Sources int    `json:"sources,omitempty"`
}

// Errors reported to callers.
var (
	ErrUnavailable = errors.New("research is not available: no model or no search backend")
	ErrRunning     = errors.New("research is already running for this task")
	ErrNotOpen     = errors.New("only open tasks can be researched")
)

// Worker runs research jobs, one goroutine per task, and reports progress
// as core events.
type Worker struct {
	core *core.Core
	lg   *slog.Logger
	// Fetcher can be replaced in tests.
	Fetcher *Fetcher
	// Now can be replaced in tests.
	Now func() time.Time

	mu       sync.Mutex
	provider llm.Provider
	role     config.Role
	research config.Research
	searcher Searcher
	running  map[string]context.CancelFunc
	wg       sync.WaitGroup
	stopped  bool
}

// New builds a worker; Configure must follow.
func New(c *core.Core, lg *slog.Logger, version string) *Worker {
	if lg == nil {
		lg = slog.Default()
	}
	return &Worker{core: c, lg: lg, running: map[string]context.CancelFunc{},
		Fetcher: NewFetcher("Fundus/" + version + " (+https://github.com/fundus-app/fundus)"), Now: time.Now}
}

// Configure installs the model, limits and search backend (nil disables).
func (w *Worker) Configure(provider llm.Provider, role config.Role, rc config.Research, searcher Searcher) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.provider, w.role, w.research, w.searcher = provider, role, rc, searcher
}

// Available reports whether research can run right now.
func (w *Worker) Available() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.provider != nil && w.searcher != nil && w.role.Model != ""
}

// Running lists task ids under research.
func (w *Worker) Running() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.running))
	for id := range w.running {
		out = append(out, id)
	}
	return out
}

// Start researches an open task in the background.
func (w *Worker) Start(taskID string) error {
	obj, err := w.core.Get(taskID)
	if err != nil {
		return err
	}
	task, ok := obj.(*model.Task)
	if !ok {
		return fmt.Errorf("%s is not a task", taskID)
	}
	if task.State == model.TaskDone || task.Archived {
		return ErrNotOpen
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return errors.New("shutting down")
	}
	if w.provider == nil || w.searcher == nil || w.role.Model == "" {
		w.mu.Unlock()
		return ErrUnavailable
	}
	if _, busy := w.running[taskID]; busy {
		w.mu.Unlock()
		return ErrRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.running[taskID] = cancel
	reader := &Reader{Provider: w.provider, Role: w.role, Searcher: w.searcher, Fetcher: w.Fetcher,
		MaxSearches: w.research.MaxSearches, MaxPages: w.research.MaxPages, Now: w.Now, Log: w.lg}
	w.wg.Add(1)
	w.mu.Unlock()
	go w.run(ctx, task, reader)
	return nil
}

// StartQuestion files a research task for question on behalf of actor and
// starts it. It returns the task id.
func (w *Worker) StartQuestion(ctx context.Context, question, actor string, topics []string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("empty question")
	}
	if !w.Available() {
		return "", ErrUnavailable
	}
	rec, err := w.core.Commit(ctx, actor, nil, []model.Op{{Op: "task.create", Text: question, Kind: string(model.TaskKindResearch), Topics: topics}})
	if err != nil {
		return "", err
	}
	taskID := rec.Lines[0].ObjectID
	return taskID, w.Start(taskID)
}

// AutoKick starts research for every open "Research:" task that has no note
// yet, when the configuration allows it.
func (w *Worker) AutoKick() {
	w.mu.Lock()
	auto := w.research.AutoStart()
	w.mu.Unlock()
	if !auto || !w.Available() {
		return
	}
	for _, tv := range w.core.Tasks([]model.TaskState{model.TaskOpen}, false) {
		if IsResearchTask(tv.Task) && len(tv.Notes) == 0 {
			if err := w.Start(tv.ID); err != nil && !errors.Is(err, ErrRunning) {
				w.lg.Warn("auto research", "task", tv.ID, "err", err)
			}
		}
	}
}

// Stop cancels running jobs and waits for them.
func (w *Worker) Stop() {
	w.mu.Lock()
	w.stopped = true
	for _, cancel := range w.running {
		cancel()
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *Worker) run(ctx context.Context, task *model.Task, reader *Reader) {
	defer w.wg.Done()
	defer func() {
		w.mu.Lock()
		delete(w.running, task.ID)
		w.mu.Unlock()
	}()
	publish := func(p Progress) {
		p.TaskID = task.ID
		w.core.Publish(core.Event{Type: "research.progress", At: w.Now(), Payload: p})
	}
	question := Question(task)
	findings, err := reader.Read(ctx, question, func(s Step) {
		publish(Progress{Step: s.Kind, Summary: s.Summary})
	})
	if err != nil {
		if ctx.Err() != nil {
			publish(Progress{Step: "error", Summary: "research was cancelled"})
			return
		}
		w.lg.Warn("research failed", "task", task.ID, "err", err)
		publish(Progress{Step: "error", Summary: err.Error()})
		return
	}
	publish(Progress{Step: "store", Summary: fmt.Sprintf("writing the note with %d sources", len(findings.Sources))})
	// Re-read the task: its revision may have moved while we were reading.
	obj, err := w.core.Get(task.ID)
	if err != nil {
		publish(Progress{Step: "error", Summary: "the task disappeared"})
		return
	}
	current := obj.(*model.Task)
	actor := Actor(reader.Provider.Name(), reader.Role.Model)
	_, noteID, err := Store(ctx, w.core, current, findings, actor)
	if err != nil {
		w.lg.Warn("research store failed", "task", task.ID, "err", err)
		publish(Progress{Step: "error", Summary: "could not store the findings: " + err.Error()})
		return
	}
	publish(Progress{Step: "done", Summary: findings.Answer, NoteID: noteID, Sources: len(findings.Sources)})
}
