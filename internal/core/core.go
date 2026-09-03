// Package core is Fundus's deterministic kernel.
//
// It owns the in-memory state, serializes every write through the event log,
// validates typed operations, produces receipts that reflect what was actually
// written, and implements transaction-level undo. No other package mutates
// objects.
package core

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/doc"
	"github.com/fundus-app/fundus/internal/ids"
	"github.com/fundus-app/fundus/internal/index"
	"github.com/fundus-app/fundus/internal/model"
	"github.com/fundus-app/fundus/internal/store"
)

// Sentinel errors returned by Commit and Undo.
var (
	ErrNotFound  = errors.New("object not found")
	ErrConflict  = errors.New("revision conflict")
	ErrInvalid   = errors.New("invalid operation")
	ErrUndone    = errors.New("transaction already undone")
	ErrPinned    = doc.ErrPinned
	ErrForbidden = errors.New("operation not allowed for this actor")
)

// Options tunes the core.
type Options struct {
	// SnapshotEvery writes a snapshot after this many transactions (0 = only on close).
	SnapshotEvery int
	Logger        *slog.Logger
	// Now is injectable for tests.
	Now func() time.Time
	// Location is the user's time zone for day boundaries (due dates,
	// "today"); nil means time.Local.
	Location *time.Location
}

// Event is published after every committed transaction and on capture changes.
type Event struct {
	Type    string    `json:"type"` // txn.committed | capture.changed | object.changed
	At      time.Time `json:"at"`
	Payload any       `json:"payload,omitempty"`
}

// Core is safe for concurrent use. Reads take a read lock; writes are serialized.
type Core struct {
	mu   sync.RWMutex
	st   *state
	log  *store.Log
	idx  *index.Index
	opts Options
	lg   *slog.Logger

	subMu sync.Mutex
	subs  map[int]chan Event
	nextS int

	sinceSnapshot int
	recovery      *store.Recovery
	closed        bool
	locMu         sync.RWMutex
	instanceID    string
}

type state struct {
	objects map[string]model.Object
	seq     uint64
	// txns holds every transaction without its before-images (those stay
	// on disk and are read back for undo).
	txns     []*model.Txn
	txnByID  map[string]*model.Txn
	undoneBy map[string]string
	byCause  map[string][]*model.Txn
	// topicNames maps normalized names and aliases to topic ids.
	topicNames map[string]string
}

func newState() *state {
	return &state{
		objects:    map[string]model.Object{},
		txnByID:    map[string]*model.Txn{},
		undoneBy:   map[string]string{},
		byCause:    map[string][]*model.Txn{},
		topicNames: map[string]string{},
	}
}

// remember indexes a committed transaction in memory, dropping the
// before-images to keep the footprint proportional to the audit view.
func (s *state) remember(txn *model.Txn) {
	slim := *txn
	slim.Before = nil
	s.txns = append(s.txns, &slim)
	s.txnByID[slim.ID] = &slim
	if slim.UndoOf != "" {
		s.undoneBy[slim.UndoOf] = slim.ID
	}
	if slim.Cause != nil && slim.Cause.ID != "" {
		k := slim.Cause.Kind + ":" + slim.Cause.ID
		s.byCause[k] = append(s.byCause[k], &slim)
	}
}

// Open loads the state from snapshot + event log.
func Open(dataDir string, opts Options) (*Core, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	lg, rec, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}
	c := &Core{st: newState(), log: lg, idx: index.New(), opts: opts, lg: opts.Logger, subs: map[int]chan Event{}, recovery: rec}
	c.instanceID = loadInstanceID(dataDir)
	if rec != nil {
		c.lg.Warn("event log recovered", "file", rec.TruncatedFile, "dropped_bytes", rec.DroppedBytes, "copy", rec.CorruptCopy)
	}

	// The log is canonical; a snapshot is only an accelerator. Any problem
	// with it means a full replay, never a refused start.
	from := uint64(1)
	if snap, err := lg.ReadSnapshot(); err != nil {
		c.lg.Warn("snapshot unusable, replaying full log", "err", err)
	} else if snap != nil {
		objects := make(map[string]model.Object, len(snap.Objects))
		var decodeErr error
		for _, raw := range snap.Objects {
			obj, err := model.Unmarshal(raw)
			if err != nil {
				decodeErr = err
				break
			}
			objects[obj.GetMeta().ID] = obj
		}
		if decodeErr != nil {
			c.lg.Warn("snapshot undecodable, replaying full log", "err", decodeErr)
		} else {
			c.st.objects = objects
			c.st.seq = snap.Seq
			from = snap.Seq + 1
			c.lg.Info("snapshot loaded", "seq", snap.Seq, "objects", len(snap.Objects))
		}
	}
	// Transactions are always replayed from the beginning so the audit view and
	// undo have the full history; ops are only re-applied after the snapshot.
	n := 0
	if err := lg.Replay(1, func(txn *model.Txn) error {
		if txn.Seq >= from {
			ws := newWorkspace(c.st)
			ws.replay = true
			for i := range txn.Ops {
				if err := ws.apply(&txn.Ops[i], txn); err != nil {
					return fmt.Errorf("replay seq %d op %d (%s): %w", txn.Seq, i, txn.Ops[i].Op, err)
				}
			}
			ws.commit(c.st, txn)
			c.st.seq = txn.Seq
			n++
		}
		c.st.remember(txn)
		return nil
	}); err != nil {
		return nil, err
	}
	if c.st.seq != lg.LastSeq() {
		return nil, fmt.Errorf("state seq %d does not match log seq %d", c.st.seq, lg.LastSeq())
	}
	for id, obj := range c.st.objects {
		c.idx.Put(id, obj.SearchFields())
		if t, ok := obj.(*model.Topic); ok {
			c.st.indexTopic(t)
		}
	}
	c.lg.Info("core ready", "objects", len(c.st.objects), "seq", c.st.seq, "replayed", n)
	return c, nil
}

// Recovery reports what the event log had to repair at startup, if anything.
func (c *Core) Recovery() *store.Recovery { return c.recovery }

// Close writes a final snapshot and closes the log.
func (c *Core) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if err := c.snapshotLocked(); err != nil {
		c.lg.Error("snapshot on close failed", "err", err)
	}
	c.subMu.Lock()
	for id, ch := range c.subs {
		close(ch)
		delete(c.subs, id)
	}
	c.subMu.Unlock()
	return c.log.Close()
}

func (c *Core) snapshotLocked() error {
	snap := &store.Snapshot{Seq: c.st.seq, Hash: c.log.LastHash(), At: c.opts.Now()}
	snap.Objects = make([]json.RawMessage, 0, len(c.st.objects))
	for _, obj := range c.st.objects {
		raw, err := model.Marshal(obj)
		if err != nil {
			return err
		}
		snap.Objects = append(snap.Objects, raw)
	}
	if err := c.log.WriteSnapshot(snap); err != nil {
		return err
	}
	c.sinceSnapshot = 0
	return nil
}

// Backup writes a zip of the event log and snapshots. The zip is built into
// a temp file while the write lock is held (so the copy is consistent) and
// streamed to w after the lock is released, so a slow client cannot stall
// the daemon.
func (c *Core) Backup(w io.Writer) error {
	tmp, err := os.CreateTemp("", "fundus-backup-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := c.backupTo(tmp); err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(w, tmp)
	return err
}

func (c *Core) backupTo(w io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.snapshotLocked(); err != nil {
		return err
	}
	files, err := c.log.Files()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		rel := filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path))
		zf, err := zw.Create(rel)
		if err == nil {
			_, err = io.Copy(zf, f)
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// finishCommit indexes a committed transaction and publishes its events.
// Caller holds c.mu.
func (c *Core) finishCommit(txn *model.Txn, ws *workspace, receipt *model.Receipt) {
	ws.commit(c.st, txn)
	c.st.seq = txn.Seq
	c.st.remember(txn)
	for _, id := range ws.touched {
		if ws.removed[id] {
			c.idx.Remove(id)
			continue
		}
		obj := c.st.objects[id]
		c.idx.Put(id, obj.SearchFields())
		if t, ok := obj.(*model.Topic); ok {
			c.st.indexTopic(t)
		}
	}
	receipt.Seq = txn.Seq
	c.sinceSnapshot++
	if c.opts.SnapshotEvery > 0 && c.sinceSnapshot >= c.opts.SnapshotEvery {
		if err := c.snapshotLocked(); err != nil {
			c.lg.Error("periodic snapshot failed", "err", err)
		}
	}
	c.publish(Event{Type: "txn.committed", At: txn.At, Payload: receipt})
	for _, id := range ws.touched {
		obj, exists := c.st.objects[id]
		if cap, ok := obj.(*model.Capture); ok {
			c.publish(Event{Type: "capture.changed", At: txn.At, Payload: cap.Clone()})
			continue
		}
		ch := map[string]any{"id": id, "type": ids.Prefix(id), "removed": !exists}
		if exists {
			ch["type"] = string(obj.GetMeta().Type)
			ch["rev"] = obj.GetMeta().Rev
		}
		c.publish(Event{Type: "object.changed", At: txn.At, Payload: ch})
	}
}

// Snapshot forces a snapshot write.
func (c *Core) Snapshot() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// Seq returns the last committed sequence number.
func (c *Core) Seq() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st.seq
}

// ---------------------------------------------------------------------------
// Commit / Undo

// Commit validates and applies ops atomically, appends the transaction to the
// log and returns a receipt describing the actual effects.
func (c *Core) Commit(ctx context.Context, actor string, cause *model.Cause, ops []model.Op) (*model.Receipt, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: no operations", ErrInvalid)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("core closed")
	}
	txn := &model.Txn{ID: ids.New(ids.PrefixTxn), At: c.opts.Now().UTC(), Actor: actor, Cause: cause, Ops: ops}
	ws := newWorkspace(c.st)
	for i := range ops {
		if strings.HasPrefix(ops[i].Op, "object.restore") || ops[i].Op == "object.remove" {
			if cause == nil || cause.Kind != "undo" {
				return nil, fmt.Errorf("%w: %s is reserved for undo", ErrForbidden, ops[i].Op)
			}
		}
		if err := ws.apply(&ops[i], txn); err != nil {
			return nil, fmt.Errorf("op %d (%s): %w", i, ops[i].Op, err)
		}
	}
	txn.Before = ws.before
	txn.Touched = ws.touched
	receipt := buildReceipt(txn, ws)
	txn.Summary = receipt.Summary
	txn.Lines = receipt.Lines
	if err := c.log.Append(txn); err != nil {
		return nil, fmt.Errorf("append event log: %w", err)
	}
	c.finishCommit(txn, ws, receipt)
	return receipt, nil
}

// UndoConflict describes objects that changed after the transaction to undo.
type UndoConflict struct {
	TxnID   string   `json:"txn_id"`
	Objects []string `json:"objects"`
}

func (u *UndoConflict) Error() string {
	return fmt.Sprintf("%s: %d object(s) were modified after %s", ErrConflict.Error(), len(u.Objects), u.TxnID)
}

func (u *UndoConflict) Unwrap() error { return ErrConflict }

// Undo reverts a transaction by restoring the before-images of every object it
// touched. If any of those objects was modified afterwards the call fails with
// *UndoConflict unless force is set. Undo never re-triggers automatic
// processing: captures whose prior state was pending/processing are parked in
// the inbox for review instead. Captures themselves are never removed.
func (c *Core) Undo(ctx context.Context, actor, txnID string, force bool) (*model.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("core closed")
	}
	head, ok := c.st.txnByID[txnID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, txnID)
	}
	if by, done := c.st.undoneBy[txnID]; done {
		return nil, fmt.Errorf("%w: by %s", ErrUndone, by)
	}
	for _, op := range head.Ops {
		if op.Op == "capture.create" {
			return nil, fmt.Errorf("%w: captures are the raw log and cannot be undone; dismiss %s instead", ErrInvalid, op.ID)
		}
	}
	if len(head.Lines) == 0 {
		return nil, fmt.Errorf("%w: %s is a bookkeeping transaction with no visible effect", ErrInvalid, txnID)
	}
	txn, err := c.log.ReadTxn(head.Seq)
	if err != nil {
		return nil, fmt.Errorf("read transaction: %w", err)
	}
	var conflicts []string
	var ops []model.Op
	for _, id := range txn.Touched {
		before := txn.Before[id]
		cur, exists := c.st.objects[id]
		// Expected revision after the transaction.
		expected := 1
		created := len(before) == 0 || string(before) == "null"
		if !created {
			var m struct {
				Rev int `json:"rev"`
			}
			_ = json.Unmarshal(before, &m)
			expected = m.Rev + 1
		}
		if exists && cur.GetMeta().Rev != expected {
			conflicts = append(conflicts, id)
		}
		if !exists && !created {
			// Removed later (an undo of its creation): restoring would
			// resurrect it behind the user's back.
			conflicts = append(conflicts, id)
		}
		switch {
		case created:
			ops = append(ops, model.Op{Op: "object.remove", ID: id})
		case ids.Prefix(id) == ids.PrefixCapture:
			var cap model.Capture
			if json.Unmarshal(before, &cap) == nil && (cap.Status == model.CapturePending || cap.Status == model.CaptureProcessing) {
				ops = append(ops, model.Op{Op: "capture.set_status", ID: id, Status: string(model.CaptureNeedsReview),
					Result: &model.CaptureResult{Summary: "Processing was undone: " + txn.Summary, Reason: "undone", ProcessedAt: c.opts.Now().UTC()}})
				continue
			}
			ops = append(ops, model.Op{Op: "object.restore", ID: id, Object: before})
		default:
			ops = append(ops, model.Op{Op: "object.restore", ID: id, Object: before})
		}
	}
	if len(conflicts) > 0 && !force {
		return nil, &UndoConflict{TxnID: txnID, Objects: conflicts}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: nothing to undo", ErrInvalid)
	}
	undo := &model.Txn{ID: ids.New(ids.PrefixTxn), At: c.opts.Now().UTC(), Actor: actor,
		Cause: &model.Cause{Kind: "undo", ID: txnID}, Ops: ops, UndoOf: txnID}
	ws := newWorkspace(c.st)
	for i := range ops {
		if err := ws.apply(&ops[i], undo); err != nil {
			return nil, fmt.Errorf("undo op %d (%s): %w", i, ops[i].Op, err)
		}
	}
	undo.Before = ws.before
	undo.Touched = ws.touched
	receipt := buildReceipt(undo, ws)
	receipt.Summary = "Undid: " + head.Summary
	undo.Summary = receipt.Summary
	undo.Lines = receipt.Lines
	if err := c.log.Append(undo); err != nil {
		return nil, fmt.Errorf("append event log: %w", err)
	}
	c.finishCommit(undo, ws, receipt)
	return receipt, nil
}

// ---------------------------------------------------------------------------
// Reads

// Get returns a clone of the object, or ErrNotFound.
func (c *Core) Get(id string) (model.Object, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	obj, ok := c.st.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return obj.Clone(), nil
}

// Each calls fn with a clone of every object of the given types (all when
// empty). The clones are taken under the lock and fn runs outside it, so fn
// may call back into the core. Returning false stops iteration.
func (c *Core) Each(types []model.Type, fn func(model.Object) bool) {
	want := map[model.Type]bool{}
	for _, t := range types {
		want[t] = true
	}
	c.mu.RLock()
	objs := make([]model.Object, 0, len(c.st.objects))
	for _, obj := range c.st.objects {
		if len(want) > 0 && !want[obj.GetMeta().Type] {
			continue
		}
		objs = append(objs, obj.Clone())
	}
	c.mu.RUnlock()
	for _, o := range objs {
		if !fn(o) {
			return
		}
	}
}

// List returns clones of all objects of a type, sorted newest first.
func (c *Core) List(t model.Type) []model.Object {
	var out []model.Object
	c.Each([]model.Type{t}, func(o model.Object) bool { out = append(out, o); return true })
	sort.Slice(out, func(i, j int) bool { return out[i].GetMeta().CreatedAt.After(out[j].GetMeta().CreatedAt) })
	return out
}

// Hit is a search result.
type Hit struct {
	ID     string       `json:"id"`
	Type   model.Type   `json:"type"`
	Title  string       `json:"title"`
	Score  float64      `json:"score"`
	Object model.Object `json:"-"`
}

// Search runs the lexical index over all objects. Archived objects and
// captures are excluded unless includeAll is set.
func (c *Core) Search(q string, limit int, types []model.Type, includeAll bool) []Hit {
	c.mu.RLock()
	defer c.mu.RUnlock()
	want := map[model.Type]bool{}
	for _, t := range types {
		want[t] = true
	}
	hits := c.idx.Search(q, limit, func(id string) bool {
		obj, ok := c.st.objects[id]
		if !ok {
			return false
		}
		m := obj.GetMeta()
		if len(want) > 0 && !want[m.Type] {
			return false
		}
		if !includeAll && (m.Archived || m.Type == model.TypeCapture) {
			return false
		}
		return true
	})
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		obj := c.st.objects[h.ID]
		out = append(out, Hit{ID: h.ID, Type: obj.GetMeta().Type, Title: obj.Title(), Score: h.Score, Object: obj.Clone()})
	}
	return out
}

// FindTopic resolves a topic by exact id, or by normalized name/alias.
func (c *Core) FindTopic(nameOrID string) (*model.Topic, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if obj, ok := c.st.objects[nameOrID]; ok {
		if t, ok := obj.(*model.Topic); ok {
			return t.Clone().(*model.Topic), true
		}
		return nil, false
	}
	if id, ok := c.st.topicNames[NormalizeName(nameOrID)]; ok {
		if t, ok := c.st.objects[id].(*model.Topic); ok && !t.Archived {
			return t.Clone().(*model.Topic), true
		}
	}
	return nil, false
}

// TopicsMentionedIn returns topics whose name or alias occurs in text.
func (c *Core) TopicsMentionedIn(text string) []*model.Topic {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := map[string]bool{}
	var out []*model.Topic
	for name, id := range c.st.topicNames {
		if seen[id] {
			continue
		}
		if index.ContainsPhrase(text, name) {
			if t, ok := c.st.objects[id].(*model.Topic); ok && !t.Archived {
				seen[id] = true
				out = append(out, t.Clone().(*model.Topic))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Txn returns a transaction by id.
func (c *Core) Txn(id string) (*model.Txn, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.st.txnByID[id]
	return t, ok
}

// ChangesQuery selects transactions for the audit view.
type ChangesQuery struct {
	Limit  int
	Before uint64 // only seq < Before
	// After selects seq > After and switches to oldest-first order (for
	// resuming an event stream); Ascending forces that order even for 0.
	After        uint64
	Ascending    bool
	IncludeQuiet bool
}

// Changes returns receipts newest first, or oldest first when After or
// Ascending is set so a client can replay in order.
func (c *Core) Changes(q ChangesQuery) []*model.Receipt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*model.Receipt
	take := func(t *model.Txn) bool {
		if q.Before > 0 && t.Seq >= q.Before {
			return true
		}
		if q.After > 0 && t.Seq <= q.After {
			return true
		}
		r := c.receiptFor(t)
		if r.Quiet && !q.IncludeQuiet {
			return true
		}
		out = append(out, r)
		return q.Limit <= 0 || len(out) < q.Limit
	}
	if q.After > 0 || q.Ascending {
		for _, t := range c.st.txns {
			if !take(t) {
				break
			}
		}
		return out
	}
	for i := len(c.st.txns) - 1; i >= 0; i-- {
		if !take(c.st.txns[i]) {
			break
		}
	}
	return out
}

// ReceiptFor re-renders the receipt of a stored transaction.
func (c *Core) ReceiptFor(txnID string) (*model.Receipt, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.st.txnByID[txnID]
	if !ok {
		return nil, false
	}
	return c.receiptFor(t), true
}

// receiptFor rebuilds a receipt from the stored lines and summary, so the
// audit view shows what was said at the time.
func (c *Core) receiptFor(t *model.Txn) *model.Receipt {
	r := &model.Receipt{TxnID: t.ID, Seq: t.Seq, At: t.At, Actor: t.Actor, Cause: t.Cause, UndoOf: t.UndoOf,
		Lines: t.Lines, Summary: t.Summary, Touched: t.Touched}
	if r.Lines == nil {
		r.Lines = []model.ReceiptLine{}
	}
	r.Quiet = len(t.Lines) == 0
	r.UndoneBy = c.st.undoneBy[t.ID]
	r.Undoable = r.UndoneBy == "" && !r.Quiet
	for _, op := range t.Ops {
		if op.Op == "capture.create" {
			r.Undoable = false
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// Events

// MaxSubscribers bounds concurrent event subscriptions.
const MaxSubscribers = 32

// ErrTooManySubscribers is returned by Subscribe when the cap is reached.
var ErrTooManySubscribers = errors.New("too many event subscribers")

// Subscribe returns a channel of events and a cancel func. Slow subscribers
// drop events rather than block the core. It returns a nil channel and a
// no-op cancel when MaxSubscribers is reached.
func (c *Core) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	c.subMu.Lock()
	if len(c.subs) >= MaxSubscribers {
		c.subMu.Unlock()
		return nil, func() {}
	}
	id := c.nextS
	c.nextS++
	c.subs[id] = ch
	c.subMu.Unlock()
	return ch, func() {
		c.subMu.Lock()
		if s, ok := c.subs[id]; ok {
			delete(c.subs, id)
			close(s)
		}
		c.subMu.Unlock()
	}
}

func (c *Core) publish(ev Event) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// NormalizeName canonicalizes topic names for matching.
func NormalizeName(s string) string {
	return strings.Join(index.Tokenize(s), " ")
}

func (s *state) indexTopic(t *model.Topic) {
	// Remove stale entries pointing at this topic, then re-add.
	for k, id := range s.topicNames {
		if id == t.ID {
			delete(s.topicNames, k)
		}
	}
	if t.Archived {
		return
	}
	if n := NormalizeName(t.Name); n != "" {
		s.topicNames[n] = t.ID
	}
	for _, a := range t.Aliases {
		n := NormalizeName(a)
		if n == "" {
			continue
		}
		if holder, taken := s.topicNames[n]; taken && holder != t.ID {
			// Keep the entry only if its holder is still a live topic.
			if o, ok := s.objects[holder].(*model.Topic); ok && !o.Archived {
				continue
			}
		}
		s.topicNames[n] = t.ID
	}
}

// Publish emits an event that is not tied to a transaction (e.g. chat progress).
func (c *Core) Publish(ev Event) { c.publish(ev) }

// ReceiptsForCause returns receipts of transactions caused by (kind, id), oldest first.
func (c *Core) ReceiptsForCause(kind, id string) []*model.Receipt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*model.Receipt
	for _, t := range c.st.byCause[kind+":"+id] {
		out = append(out, c.receiptFor(t))
	}
	return out
}

// ReceiptsForObject returns the most recent receipts that touched id, newest first.
func (c *Core) ReceiptsForObject(id string, limit int) []*model.Receipt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*model.Receipt
	for i := len(c.st.txns) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		t := c.st.txns[i]
		for _, tid := range t.Touched {
			if tid == id {
				out = append(out, c.receiptFor(t))
				break
			}
		}
	}
	return out
}

// Location returns the user's time zone.
func (c *Core) Location() *time.Location {
	c.locMu.RLock()
	defer c.locMu.RUnlock()
	return c.opts.Location
}

// SetLocation changes the time zone at runtime (settings change).
func (c *Core) SetLocation(loc *time.Location) {
	if loc == nil {
		return
	}
	c.locMu.Lock()
	c.opts.Location = loc
	c.locMu.Unlock()
}

// Now returns the current time in the user's zone.
func (c *Core) Now() time.Time { return c.opts.Now().In(c.Location()) }

// InstanceID identifies this data directory. Clients compare it with the
// id in <data_dir>/instance to make sure they talk to their own daemon.
func (c *Core) InstanceID() string { return c.instanceID }

// loadInstanceID reads or creates <dir>/instance.
func loadInstanceID(dir string) string {
	path := filepath.Join(dir, "instance")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b))
	}
	id := ids.New("inst")
	_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	return id
}
