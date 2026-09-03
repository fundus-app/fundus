package triage

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/model"
)

// Worker processes pending captures one at a time. It wakes on capture
// events and polls as a fallback, so nothing stays pending after a crash.
type Worker struct {
	core    *core.Core
	triager *Triager
	lg      *slog.Logger
	wake    chan struct{}
	stop    chan struct{}
	Poll    time.Duration
}

// NewWorker builds a worker.
func NewWorker(c *core.Core, t *Triager, lg *slog.Logger) *Worker {
	if lg == nil {
		lg = slog.Default()
	}
	return &Worker{core: c, triager: t, lg: lg, wake: make(chan struct{}, 1), stop: make(chan struct{}), Poll: 30 * time.Second}
}

// Stop asks the worker to return after the capture in flight, without
// cancelling that capture's model call.
func (w *Worker) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
}

// Kick asks the worker to look for pending captures now.
func (w *Worker) Kick() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is done.
func (w *Worker) Run(ctx context.Context) {
	w.resetStale(ctx)
	events, cancel := w.core.Subscribe()
	defer cancel()
	ticker := time.NewTicker(w.Poll)
	defer ticker.Stop()
	w.drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == "capture.changed" {
				if cap, ok := ev.Payload.(*model.Capture); ok && cap.Status == model.CapturePending {
					w.drain(ctx)
				}
			}
		case <-w.wake:
			w.drain(ctx)
		case <-ticker.C:
			w.drain(ctx)
		}
	}
}

// drain processes every pending capture once, then retries transient
// failures whose backoff has elapsed. A capture is touched at most once per
// drain so a persistent error (full disk, closed core) cannot spin the loop.
func (w *Worker) drain(ctx context.Context) {
	if !w.triager.Ready() {
		return // no model yet: captures wait in the inbox, nothing is lost
	}
	tried := map[string]bool{}
	for {
		if ctx.Err() != nil || w.stopping() {
			return
		}
		var next *model.Capture
		for _, cap := range w.core.PendingCaptures() {
			if !tried[cap.ID] {
				next = cap
				break
			}
		}
		if next == nil {
			for _, cap := range w.retryable() {
				if !tried[cap.ID] {
					next = cap
					break
				}
			}
		}
		if next == nil {
			return
		}
		tried[next.ID] = true
		w.process(ctx, next)
	}
}

func (w *Worker) process(ctx context.Context, cap *model.Capture) {
	if cap.Status == model.CaptureFailed {
		if _, err := w.core.Commit(ctx, "system", &model.Cause{Kind: "capture", ID: cap.ID},
			[]model.Op{{Op: "capture.set_status", ID: cap.ID, ExpectedRev: cap.Rev, Status: string(model.CapturePending)}}); err != nil {
			w.lg.Warn("re-queue failed capture", "capture", cap.ID, "err", err)
			return
		}
	}
	rec, err := w.triager.Process(ctx, cap.ID)
	switch {
	case err != nil:
		w.lg.Warn("triage failed", "capture", cap.ID, "err", err)
	case rec != nil:
		w.lg.Info("triage done", "capture", cap.ID, "txn", rec.TxnID, "summary", rec.Summary)
	default:
		w.lg.Info("triage parked or dismissed", "capture", cap.ID)
	}
}

// MaxRetries bounds automatic retries of transient failures (about a day
// with exponential backoff starting at one minute).
const MaxRetries = 10

// retryable lists failed captures with a transient error whose backoff has
// elapsed, oldest first.
func (w *Worker) retryable() []*model.Capture {
	now := time.Now()
	var out []*model.Capture
	for _, cap := range w.core.Captures(model.CaptureFailed, 0) {
		if cap.Result == nil || !cap.Result.Retryable || cap.Attempts >= MaxRetries {
			continue
		}
		backoff := time.Minute << uint(min(cap.Attempts, 8)) // 1m … ~4h
		if now.Sub(cap.Result.ProcessedAt) >= backoff {
			out = append(out, cap)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// resetStale returns captures stuck in "processing" (daemon crashed mid-run)
// to the queue.
func (w *Worker) resetStale(ctx context.Context) {
	for _, cap := range w.core.Captures(model.CaptureProcessing, 0) {
		if _, err := w.core.Commit(ctx, "system", &model.Cause{Kind: "system", ID: "restart"},
			[]model.Op{{Op: "capture.set_status", ID: cap.ID, Status: string(model.CapturePending)}}); err != nil {
			w.lg.Error("reset stale capture", "capture", cap.ID, "err", err)
		}
	}
}

func (w *Worker) stopping() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}
