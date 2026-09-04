// Package api exposes the core over a versioned JSON HTTP API with SSE.
//
// Commands mutate state and return receipts, queries read, events stream.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fundus-app/fundus/internal/model"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/chat"
	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/embed"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/maintenance"
	"github.com/fundus-app/fundus/internal/research"
	"github.com/fundus-app/fundus/internal/setup"
	"github.com/fundus-app/fundus/internal/triage"
	"github.com/fundus-app/fundus/internal/webui"
)

// Version is set at build time.
var Version = "dev"

// Server wires handlers to the core.
type Server struct {
	core     *core.Core
	cfg      *config.Config
	worker   *triage.Worker
	triager  *triage.Triager
	chat     *chat.Chat
	reg      *llm.Registry
	research *research.Worker
	maint    *maintenance.Worker
	// Embeddings: index and syncer follow the configuration.
	embedIndex  *embed.Index
	embedder    embed.Embedder
	embedModel  string
	embedCancel context.CancelFunc
	embedKey    string
	lg          *slog.Logger
	mux         *http.ServeMux
	started     time.Time
	DevCORS     bool
	// Warnings are shown in /v1/health (missing API key, proxy hints, …).
	Warnings []string

	chatSlots *chatSlots
	baseCtx   context.Context
	cancel    context.CancelFunc

	cfgMu   sync.RWMutex
	applyMu sync.Mutex // serializes applyConfig (registry build, save, install)
	oauth   *setup.OAuth
}

// New builds the server. worker and chat may be nil (read-only daemon).
func New(c *core.Core, cfg *config.Config, w *triage.Worker, t *triage.Triager, ch *chat.Chat, reg *llm.Registry, lg *slog.Logger) *Server {
	if lg == nil {
		lg = slog.Default()
	}
	s := &Server{core: c, cfg: cfg, worker: w, triager: t, chat: ch, reg: reg, lg: lg, mux: http.NewServeMux(), started: time.Now(),
		chatSlots: newChatSlots(4)}
	s.baseCtx, s.cancel = context.WithCancel(context.Background())
	s.oauth = setup.NewOAuth(nil)
	s.routes()
	return s
}

// config returns the current configuration (it can change at runtime).
func (s *Server) config() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// applyConfig installs a new configuration: providers are rebuilt, triage
// and chat are reconfigured, the worker is kicked so waiting captures get
// filed. The config is saved to disk first; nothing changes if that fails.
func (s *Server) applyConfig(next *config.Config) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	// Build first (pure), then persist, then install: a failure leaves disk
	// and memory in agreement.
	reg, err := llm.NewRegistry(next, triage.NewHeuristic)
	if err != nil {
		return err
	}
	if err := next.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	s.cfgMu.Lock()
	s.cfg = next
	s.reg = reg
	s.cfgMu.Unlock()
	s.configureRuntime()
	if s.worker != nil {
		s.worker.Kick()
	}
	return nil
}

// configureRuntime pushes the current config into triage, chat and the core.
// Roles whose provider is not usable get no provider (the worker waits).
// SetMaintenance wires the maintenance worker.
func (s *Server) SetMaintenance(w *maintenance.Worker) {
	s.maint = w
	s.configureRuntime()
}

// SearchHits runs the configured search: hybrid when embeddings exist.
func (s *Server) SearchHits(ctx context.Context, q string, limit int, types []model.Type, includeAll bool) []core.Hit {
	s.cfgMu.RLock()
	ix, e, m := s.embedIndex, s.embedder, s.embedModel
	s.cfgMu.RUnlock()
	return embed.Search(ctx, s.core, ix, e, m, q, limit, types, includeAll)
}

// SetResearch wires the research worker; configureRuntime keeps it in step
// with the configuration.
func (s *Server) SetResearch(w *research.Worker) {
	s.research = w
	s.configureRuntime()
}

func (s *Server) configureRuntime() {
	s.cfgMu.RLock()
	cfg, reg := s.cfg, s.reg
	s.cfgMu.RUnlock()
	if loc, err := cfg.Location(); err == nil {
		s.core.SetLocation(loc)
	}
	pick := func(role config.Role) llm.Provider {
		pc, ok := cfg.Providers[role.Provider]
		if !ok || !pc.Usable() {
			return nil
		}
		p, err := reg.Get(role.Provider)
		if err != nil {
			return nil
		}
		return p
	}
	if s.triager != nil {
		s.triager.Configure(pick(cfg.Triage), cfg.Triage, cfg.Autonomy)
	}
	if s.chat != nil {
		s.chat.Configure(pick(cfg.Chat), cfg.Chat, cfg.Autonomy)
	}
	if s.research != nil {
		role := cfg.ResearchRole()
		p := pick(role)
		var searcher research.Searcher
		if p != nil {
			searcher = research.NewSearcher(cfg, p, nil)
		}
		s.research.Configure(p, role, cfg.Research, searcher)
	}
	s.configureEmbeddings(cfg)
	if s.triager != nil {
		s.triager.SetSearch(s.SearchHits)
	}
	if s.chat != nil {
		s.chat.SetSearch(s.SearchHits)
	}
	if s.maint != nil {
		s.cfgMu.RLock()
		ix, e, m := s.embedIndex, s.embedder, s.embedModel
		s.cfgMu.RUnlock()
		var r maintenance.Researcher
		if s.research != nil {
			r = s.research
		}
		s.maint.Configure(cfg.Maintenance, cfg.Autonomy, pick(cfg.Chat), cfg.Chat, ix, e, m, r)
	}
}

// configureEmbeddings opens the vector index for the configured model and
// (re)starts the syncer; a change of provider or model swaps both.
func (s *Server) configureEmbeddings(cfg *config.Config) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if !cfg.EmbeddingAvailable() {
		if s.embedCancel != nil {
			s.embedCancel()
			s.embedCancel = nil
		}
		s.embedIndex, s.embedder, s.embedModel = nil, nil, ""
		return
	}
	pc := cfg.Providers[cfg.Embedding.Provider]
	key := cfg.Embedding.Provider + "|" + cfg.Embedding.Model + "|" + pc.BaseURL
	if s.embedIndex != nil && s.embedModel == cfg.Embedding.Model && s.embedKey == key {
		return
	}
	if s.embedCancel != nil {
		s.embedCancel()
		s.embedCancel = nil
	}
	ix, err := embed.Open(cfg.DataDir, cfg.Embedding.Model)
	if err != nil {
		s.lg.Warn("embeddings index", "err", err)
		return
	}
	client := &embed.Client{Name: cfg.Embedding.Provider, BaseURL: pc.BaseURL, APIKey: pc.ResolveAPIKey(), Headers: pc.Headers}
	s.embedIndex, s.embedder, s.embedModel, s.embedKey = ix, client, cfg.Embedding.Model, key
	ctx, cancel := context.WithCancel(context.Background())
	s.embedCancel = cancel
	go embed.NewSyncer(s.core, ix, client, cfg.Embedding.Model, s.lg).Run(ctx)
}

// Close stops background helpers the server started.
func (s *Server) Close() {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.embedCancel != nil {
		s.embedCancel()
		s.embedCancel = nil
	}
}

// Handler returns the root handler with middleware applied.
func (s *Server) Handler() http.Handler {
	return s.logging(s.cors(s.auth(s.mux)))
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /v1/health", s.handleHealth)
	m.HandleFunc("GET /v1/stats", s.handleStats)

	m.HandleFunc("POST /v1/captures", s.handleCapture)
	m.HandleFunc("GET /v1/captures", s.handleCaptures)
	m.HandleFunc("GET /v1/captures/{id}", s.handleCaptureGet)
	m.HandleFunc("POST /v1/captures/{id}/retry", s.handleCaptureRetry)
	m.HandleFunc("POST /v1/captures/{id}/dismiss", s.handleCaptureDismiss)
	m.HandleFunc("POST /v1/captures/{id}/accept", s.handleCaptureAccept)
	m.HandleFunc("GET /v1/inbox", s.handleInbox)

	m.HandleFunc("GET /v1/objects", s.handleObjects)
	m.HandleFunc("GET /v1/objects/{id}", s.handleObject)
	m.HandleFunc("GET /v1/notes", s.handleNotes)
	m.HandleFunc("GET /v1/tasks", s.handleTasks)
	m.HandleFunc("GET /v1/relevant", s.handleRelevant)
	m.HandleFunc("GET /v1/topics", s.handleTopics)
	m.HandleFunc("GET /v1/topics/{id}", s.handleTopic)
	m.HandleFunc("GET /v1/search", s.handleSearch)

	m.HandleFunc("GET /v1/changes", s.handleChanges)
	m.HandleFunc("GET /v1/changes/{id}", s.handleChange)
	m.HandleFunc("POST /v1/changes/{id}/undo", s.handleUndo)
	m.HandleFunc("POST /v1/commands", s.handleCommands)

	m.HandleFunc("GET /v1/conversations", s.handleConversations)
	m.HandleFunc("POST /v1/conversations", s.handleConversationCreate)
	m.HandleFunc("GET /v1/conversations/{id}", s.handleConversation)
	m.HandleFunc("POST /v1/conversations/{id}/messages", s.handleConversationMessage)

	m.HandleFunc("GET /v1/export", s.handleExport)
	m.HandleFunc("GET /v1/backup", s.handleBackup)
	m.HandleFunc("GET /v1/settings", s.handleSettingsGet)
	m.HandleFunc("PUT /v1/settings", s.handleSettingsPut)
	m.HandleFunc("POST /v1/settings/test", s.handleSettingsTest)
	m.HandleFunc("POST /v1/transcribe", s.handleTranscribe)
	m.HandleFunc("GET /v1/maintenance", s.handleMaintenanceStatus)
	m.HandleFunc("POST /v1/maintenance/run", s.handleMaintenanceRun)
	m.HandleFunc("GET /v1/research", s.handleResearchList)
	m.HandleFunc("POST /v1/research", s.handleResearchStart)
	m.HandleFunc("GET /v1/setup/models", s.handleSetupModels)
	m.HandleFunc("POST /v1/setup/models", s.handleSetupModels)
	m.HandleFunc("POST /v1/setup/oauth/start", s.handleOAuthStart)
	m.HandleFunc("GET /setup/oauth/callback", s.handleOAuthCallback)
	m.HandleFunc("GET /v1/llm/probe", s.handleProbe)
	m.HandleFunc("GET /v1/events", s.handleEvents)

	m.Handle("/", webui.Handler())
}

// ---------------------------------------------------------------------------
// Middleware

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The static UI never needs a token: it contains no data. The OAuth
		// callback is reached by a browser redirect and carries its own state.
		if !strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/setup/oauth/callback" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/setup/oauth/callback" {
			if !s.hostAllowed(r) {
				writeError(w, http.StatusMisdirectedRequest, "bad_host", "request Host is not this daemon")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// Browsers on this machine can reach the daemon: refuse requests that
		// arrive from another site (CSRF) or via DNS rebinding (wrong Host).
		// These checks apply with or without a token.
		if !s.hostAllowed(r) {
			writeError(w, http.StatusMisdirectedRequest, "bad_host", "request Host is not this daemon")
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "cross_site", "cross-site requests are not allowed")
			return
		}
		if (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) && !jsonBody(r) &&
			!(r.URL.Path == "/v1/transcribe" && multipartBody(r)) {
			writeError(w, http.StatusUnsupportedMediaType, "content_type", "Content-Type must be application/json")
			return
		}
		cfg := s.config()
		if cfg.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Requests relayed by a proxy are not local, whatever the socket says.
		local := isLoopback(r.RemoteAddr) && r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("Forwarded") == "" && r.Header.Get("X-Real-IP") == ""
		if !cfg.RequireTokenOnLoopback && local {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearer(r)
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(cfg.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// SSE via EventSource cannot set headers; allow a query token there.
	if r.URL.Path == "/v1/events" {
		return r.URL.Query().Get("token")
	}
	return ""
}

// hostAllowed accepts loopback hosts, the configured listen host and any
// extra allowed_hosts. Everything else is a DNS-rebinding attempt or a
// misconfigured proxy.
func (s *Server) hostAllowed(r *http.Request) bool {
	// A daemon bound to every interface is deliberately exposed; the token
	// is its protection and any Host is accepted.
	cfg := s.config()
	if lh, _, err := net.SplitHostPort(cfg.Listen); err == nil {
		if lh == "" {
			return true
		}
		if ip := net.ParseIP(strings.Trim(lh, "[]")); ip != nil && ip.IsUnspecified() {
			return true
		}
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return true
		}
		lh := cfg.Listen
		if h, _, err := net.SplitHostPort(lh); err == nil {
			lh = h
		}
		if lip := net.ParseIP(strings.Trim(lh, "[]")); lip != nil && lip.Equal(ip) {
			return true
		}
	}
	for _, a := range cfg.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(a), host) {
			return true
		}
	}
	return false
}

// originAllowed rejects cross-site browser requests. Non-browser clients send
// no Origin and no Sec-Fetch-Site and pass through.
func (s *Server) originAllowed(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs == "cross-site" {
		return s.DevCORS && devOrigin(r.Header.Get("Origin"))
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return origin == ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return s.DevCORS && devOrigin(origin)
}

// devOrigin is true for local development origins such as flutter run -d chrome.
func devOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func jsonBody(r *http.Request) bool {
	if r.ContentLength == 0 && r.Body == http.NoBody {
		return true
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return r.ContentLength == 0
	}
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.DevCORS {
			origin := r.Header.Get("Origin")
			if origin != "" && devOrigin(origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Fundus-Client")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/v1/events" {
			s.lg.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------------------------------------------------------------------------
// Helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

// multipartBody reports whether the request carries a multipart form (the
// recording upload). The Origin and Host checks above still apply to it.
func multipartBody(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "multipart/form-data"
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

func writeErrorDetails(w http.ResponseWriter, status int, code, msg string, details any) {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	e.Error.Details = details
	writeJSON(w, status, e)
}

// writeCoreError maps core errors onto HTTP statuses.
func writeCoreError(w http.ResponseWriter, err error) {
	var uc *core.UndoConflict
	switch {
	case errors.As(err, &uc):
		writeErrorDetails(w, http.StatusConflict, "undo_conflict", err.Error(), uc)
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, core.ErrUndone):
		writeError(w, http.StatusConflict, "already_undone", err.Error())
	case errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrPinned):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, core.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// decode reads a JSON body of at most 4 MiB. An empty body leaves v untouched.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	err := dec.Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func actorFor(r *http.Request) string {
	c := clientName(r.Header.Get("X-Fundus-Client"))
	if c == "" {
		c = "api"
	}
	return "user:" + c
}

// ListenAndServe runs the HTTP server until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	// No WriteTimeout: SSE streams stay open. ReadTimeout bounds request
	// bodies, IdleTimeout reaps idle keep-alive connections.
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		s.cancel() // abort detached chat turns
		shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errCh:
		s.cancel()
		return err
	}
}
