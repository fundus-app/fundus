// Package maintenance runs the background curation the concept calls Phase
// 2: integrity checks, topics for untagged notes and tasks, duplicate
// detection, automatic topic summaries and, when switched on, help with
// open tasks. Every change is an ordinary transaction with a receipt and
// undo; anything that is not information-preserving is filed as a proposal
// in the inbox instead of being applied.
package maintenance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/embed"
	"github.com/fundus-app/fundus/internal/llm"
)

// Researcher is the research worker as maintenance sees it.
type Researcher interface {
	Available() bool
	Start(taskID string) error
}

// Run is the report of one maintenance run.
type Run struct {
	ID       string      `json:"id"`
	Trigger  string      `json:"trigger"` // schedule | manual
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished"`
	Jobs     []JobReport `json:"jobs"`
	Error    string      `json:"error,omitempty"`
}

// JobReport is what one job did.
type JobReport struct {
	Name     string   `json:"name"`
	Checked  int      `json:"checked"`
	Changed  int      `json:"changed"`
	Proposed int      `json:"proposed"`
	Notes    []string `json:"notes,omitempty"`
	TxnIDs   []string `json:"txn_ids,omitempty"`
	Error    string   `json:"error,omitempty"`
	Skipped  string   `json:"skipped,omitempty"`
}

// Progress is the payload of maintenance.progress events.
type Progress struct {
	RunID   string `json:"run_id"`
	Job     string `json:"job,omitempty"`
	Summary string `json:"summary"`
	Done    bool   `json:"done,omitempty"`
}

// ErrRunning is returned when a run is already in progress.
var ErrRunning = errors.New("a maintenance run is already in progress")

// Status is what the API reports.
type Status struct {
	Enabled bool      `json:"enabled"`
	Running bool      `json:"running"`
	RunID   string    `json:"run_id,omitempty"`
	Next    time.Time `json:"next,omitempty"`
	Last    *Run      `json:"last,omitempty"`
}

// Worker owns the schedule, the run history and the job configuration.
type Worker struct {
	core *core.Core
	lg   *slog.Logger
	dir  string
	Now  func() time.Time

	mu       sync.Mutex
	cfg      config.Maintenance
	policy   config.Policy
	provider llm.Provider
	role     config.Role
	index    *embed.Index
	embedder embed.Embedder
	embModel string
	research Researcher
	runs     []*Run
	running  *Run
	state    *state
	wake     chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup
}

// New builds a worker that keeps its history under dataDir/maintenance.
func New(c *core.Core, dataDir string, lg *slog.Logger) *Worker {
	if lg == nil {
		lg = slog.Default()
	}
	w := &Worker{core: c, lg: lg, dir: filepath.Join(dataDir, "maintenance"), Now: time.Now, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	_ = os.MkdirAll(w.dir, 0o700)
	w.runs = loadRuns(filepath.Join(w.dir, "runs.jsonl"))
	w.state = loadState(filepath.Join(w.dir, "state.json"))
	return w
}

// Configure installs the schedule, the model and the optional helpers.
func (w *Worker) Configure(cfg config.Maintenance, policy config.Policy, provider llm.Provider, role config.Role,
	ix *embed.Index, e embed.Embedder, embModel string, r Researcher) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cfg, w.policy, w.provider, w.role = cfg, policy, provider, role
	w.index, w.embedder, w.embModel, w.research = ix, e, embModel, r
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Kick asks the scheduler to re-evaluate (after a configuration change).
func (w *Worker) Kick() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Status reports schedule and history.
func (w *Worker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := Status{Enabled: w.cfg.Enabled, Running: w.running != nil}
	if w.running != nil {
		st.RunID = w.running.ID
	}
	if len(w.runs) > 0 {
		st.Last = w.runs[len(w.runs)-1]
	}
	if w.cfg.Enabled {
		st.Next = w.nextLocked(w.Now())
	}
	return st
}

// Runs returns the history, newest first.
func (w *Worker) Runs(limit int) []*Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Run, 0, len(w.runs))
	for i := len(w.runs) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, w.runs[i])
	}
	return out
}

// nextLocked computes the next scheduled start after now.
func (w *Worker) nextLocked(now time.Time) time.Time {
	if w.cfg.Every.Duration > 0 {
		last := time.Time{}
		if len(w.runs) > 0 {
			last = w.runs[len(w.runs)-1].Started
		}
		if last.IsZero() {
			return now.Add(time.Minute)
		}
		return last.Add(w.cfg.Every.Duration)
	}
	at := w.cfg.At
	if at == "" {
		at = "03:30"
	}
	hm, err := time.Parse("15:04", at)
	if err != nil {
		hm, _ = time.Parse("15:04", "03:30")
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	// Skip a slot that already ran today (a manual run counts).
	if len(w.runs) > 0 && w.runs[len(w.runs)-1].Started.After(next.Add(-24*time.Hour)) && w.runs[len(w.runs)-1].Trigger == "schedule" {
		return next
	}
	return next
}

// Run drives the schedule until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	for {
		w.mu.Lock()
		enabled := w.cfg.Enabled
		next := w.nextLocked(w.Now())
		w.mu.Unlock()
		wait := time.Hour
		if enabled {
			wait = time.Until(next)
			if wait < time.Second {
				wait = time.Second
			}
			if wait > time.Hour {
				wait = time.Hour
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.stop:
			timer.Stop()
			return
		case <-w.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		w.mu.Lock()
		due := w.cfg.Enabled && !w.Now().Before(w.nextLocked(w.Now()).Add(-time.Second))
		w.mu.Unlock()
		if due {
			if _, err := w.RunNow(ctx, "schedule"); err != nil && !errors.Is(err, ErrRunning) {
				w.lg.Warn("maintenance run", "err", err)
			}
		}
	}
}

// Start begins a run in the background and returns its id.
func (w *Worker) Start(trigger string) (string, error) {
	w.mu.Lock()
	if w.running != nil {
		w.mu.Unlock()
		return "", ErrRunning
	}
	run := &Run{ID: "run_" + w.Now().UTC().Format("20060102T150405"), Trigger: trigger, Started: w.Now()}
	w.running = run
	w.wg.Add(1)
	w.mu.Unlock()
	go func() {
		defer w.wg.Done()
		w.execute(context.Background(), run)
	}()
	return run.ID, nil
}

// RunNow executes a run synchronously.
func (w *Worker) RunNow(ctx context.Context, trigger string) (*Run, error) {
	w.mu.Lock()
	if w.running != nil {
		w.mu.Unlock()
		return nil, ErrRunning
	}
	run := &Run{ID: "run_" + w.Now().UTC().Format("20060102T150405"), Trigger: trigger, Started: w.Now()}
	w.running = run
	w.mu.Unlock()
	w.execute(ctx, run)
	return run, nil
}

// Stop ends the scheduler and waits for a background run to finish.
func (w *Worker) Stop() {
	w.mu.Lock()
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *Worker) publish(p Progress) {
	w.core.Publish(core.Event{Type: "maintenance.progress", At: w.Now(), Payload: p})
}

func (w *Worker) execute(ctx context.Context, run *Run) {
	w.mu.Lock()
	cfg, policy, provider, role := w.cfg, w.policy, w.provider, w.role
	ix, emb, embModel, research := w.index, w.embedder, w.embModel, w.research
	w.mu.Unlock()
	env := &jobEnv{w: w, run: run, cfg: cfg, policy: policy, provider: provider, role: role, index: ix, embedder: emb, embModel: embModel, research: research}
	jobs := []struct {
		name string
		on   bool
		fn   func(context.Context, *jobEnv) JobReport
	}{
		{"integrity", cfg.Integrity, integrity},
		{"untagged", cfg.Untagged, untagged},
		{"duplicates", cfg.Duplicates, duplicates},
		{"summaries", cfg.Summaries, summaries},
		{"assist", cfg.Assist == "propose" || cfg.Assist == "auto", assist},
	}
	for _, j := range jobs {
		if !j.on {
			continue
		}
		if ctx.Err() != nil {
			run.Error = "cancelled"
			break
		}
		w.publish(Progress{RunID: run.ID, Job: j.name, Summary: "running " + j.name})
		rep := j.fn(ctx, env)
		rep.Name = j.name
		run.Jobs = append(run.Jobs, rep)
		w.publish(Progress{RunID: run.ID, Job: j.name, Summary: fmt.Sprintf("%s: %d checked, %d changed, %d proposed", j.name, rep.Checked, rep.Changed, rep.Proposed)})
	}
	run.Finished = w.Now()
	w.mu.Lock()
	w.runs = append(w.runs, run)
	keep := cfg.KeepRuns
	if keep <= 0 {
		keep = 30
	}
	if len(w.runs) > keep {
		w.runs = w.runs[len(w.runs)-keep:]
	}
	w.running = nil
	saveRuns(filepath.Join(w.dir, "runs.jsonl"), w.runs)
	saveState(filepath.Join(w.dir, "state.json"), w.state)
	w.mu.Unlock()
	w.publish(Progress{RunID: run.ID, Summary: summarizeRun(run), Done: true})
}

func summarizeRun(r *Run) string {
	changed, proposed := 0, 0
	for _, j := range r.Jobs {
		changed += j.Changed
		proposed += j.Proposed
	}
	return fmt.Sprintf("Maintenance finished: %d changes, %d proposals in the inbox.", changed, proposed)
}

// state remembers what maintenance already did so it does not nag.
type state struct {
	// Proposed maps a pair key (sorted ids) to when it was proposed.
	Proposed map[string]time.Time `json:"proposed"`
	// Summaries maps topic id to the block written and when.
	Summaries map[string]summaryState `json:"summaries"`
	// Assisted maps task id to when help was offered.
	Assisted map[string]time.Time `json:"assisted"`
}

type summaryState struct {
	Block string    `json:"block"`
	At    time.Time `json:"at"`
	Hash  string    `json:"hash"`
}

func loadState(path string) *state {
	st := &state{Proposed: map[string]time.Time{}, Summaries: map[string]summaryState{}, Assisted: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(raw, st)
	}
	if st.Proposed == nil {
		st.Proposed = map[string]time.Time{}
	}
	if st.Summaries == nil {
		st.Summaries = map[string]summaryState{}
	}
	if st.Assisted == nil {
		st.Assisted = map[string]time.Time{}
	}
	return st
}

func saveState(path string, st *state) {
	raw, _ := json.MarshalIndent(st, "", " ")
	_ = os.WriteFile(path+".tmp", raw, 0o600)
	_ = os.Rename(path+".tmp", path)
}

func loadRuns(path string) []*Run {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var runs []*Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var r Run
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			runs = append(runs, &r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Started.Before(runs[j].Started) })
	return runs
}

func saveRuns(path string, runs []*Run) {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	for _, r := range runs {
		raw, _ := json.Marshal(r)
		_, _ = f.Write(append(raw, '\n'))
	}
	f.Close()
	_ = os.Rename(path+".tmp", path)
}

// pairKey orders two ids so a pair is proposed once.
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}
