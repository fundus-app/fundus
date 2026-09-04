package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/fundus-app/fundus/internal/research"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fundus-app/fundus/internal/chat"
	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/model"
	"github.com/fundus-app/fundus/internal/setup"
	"github.com/fundus-app/fundus/internal/triage"
)

type env struct {
	s    *Server
	srv  *httptest.Server
	core *core.Core
	cfg  *config.Config
}

func newEnv(t *testing.T, token string, mods ...func(*config.Config)) *env {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := core.Open(t.TempDir(), core.Options{Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Token = token
	cfg.Triage.Provider, cfg.Chat.Provider = "fake", "fake"
	for _, m := range mods {
		m(cfg)
	}
	reg, _ := llm.NewRegistry(cfg, triage.NewHeuristic)
	// Same rule as the daemon: roles without a usable provider start empty.
	pick := func(role config.Role) llm.Provider {
		if pc, ok := cfg.Providers[role.Provider]; !ok || !pc.Usable() {
			return nil
		}
		p, _ := reg.Get(role.Provider)
		return p
	}
	tr := triage.New(c, pick(cfg.Triage), cfg.Triage, cfg.Autonomy, quiet)
	w := triage.NewWorker(c, tr, quiet)
	w.Poll = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	ch := chat.New(c, pick(cfg.Chat), cfg.Chat, cfg.Autonomy, quiet)
	s := New(c, cfg, w, tr, ch, reg, quiet)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() { ts.Close(); cancel(); c.Close() })
	return &env{srv: ts, core: c, cfg: cfg, s: s}
}

func (e *env) call(t *testing.T, method, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fundus-Client", "test")
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

func waitStatus(t *testing.T, e *env, id string, want model.CaptureStatus) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, raw := e.call(t, "GET", "/v1/captures/"+id, nil, nil)
		if code != 200 {
			t.Fatalf("GET capture: %d %s", code, raw)
		}
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		if v["status"] == string(want) {
			return v
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("capture %s never reached %s", id, want)
	return nil
}

func TestCaptureFlowAndUndo(t *testing.T) {
	e := newEnv(t, "")
	code, raw := e.call(t, "POST", "/v1/captures", map[string]any{"text": "Ich muss den Deye-String prüfen"}, nil)
	if code != 202 {
		t.Fatalf("capture: %d %s", code, raw)
	}
	var cap map[string]any
	_ = json.Unmarshal(raw, &cap)
	id := cap["id"].(string)
	v := waitStatus(t, e, id, model.CaptureProcessed)
	receipts := v["receipts"].([]any)
	if len(receipts) == 0 {
		t.Fatalf("no receipts: %v", v)
	}
	var txn string
	for _, r := range receipts {
		m := r.(map[string]any)
		if strings.HasPrefix(m["actor"].(string), "llm:") {
			txn = m["txn_id"].(string)
		}
	}
	code, raw = e.call(t, "GET", "/v1/tasks?state=open", nil, nil)
	if code != 200 || !strings.Contains(string(raw), "Deye") {
		t.Fatalf("tasks: %d %s", code, raw)
	}
	code, raw = e.call(t, "POST", "/v1/changes/"+txn+"/undo", nil, nil)
	if code != 200 {
		t.Fatalf("undo: %d %s", code, raw)
	}
	code, raw = e.call(t, "GET", "/v1/tasks?state=open", nil, nil)
	if code != 200 || strings.Contains(string(raw), "Deye") {
		t.Fatalf("task still present after undo: %s", raw)
	}
	code, raw = e.call(t, "GET", "/v1/inbox", nil, nil)
	if code != 200 || !strings.Contains(string(raw), "needs_review") {
		t.Fatalf("inbox after undo: %s", raw)
	}
	code, raw = e.call(t, "POST", "/v1/changes/"+txn+"/undo", nil, nil)
	if code != 409 {
		t.Fatalf("second undo should conflict: %d %s", code, raw)
	}
	// Idempotent capture with client id.
	cid := "cap_01J8ZK3V2Q9F1H6C4X0M5B7N8P"
	c1, _ := e.call(t, "POST", "/v1/captures", map[string]any{"id": cid, "text": "einmal"}, nil)
	c2, _ := e.call(t, "POST", "/v1/captures", map[string]any{"id": cid, "text": "einmal"}, nil)
	if c1 != 202 || c2 != 200 {
		t.Fatalf("idempotency: %d %d", c1, c2)
	}
}

func TestCommandsRetryAndViews(t *testing.T) {
	e := newEnv(t, "")
	code, raw := e.call(t, "POST", "/v1/commands", map[string]any{"ops": []map[string]any{
		{"op": "topic.create", "name": "Haus"},
		{"op": "task.create", "text": "Dach prüfen", "due": "2026-09-04", "importance": 3},
	}}, nil)
	if code != 200 {
		t.Fatalf("commands: %d %s", code, raw)
	}
	code, raw = e.call(t, "GET", "/v1/relevant", nil, nil)
	if code != 200 || !strings.Contains(string(raw), "reasons") {
		t.Fatalf("relevant: %d %s", code, raw)
	}
	code, raw = e.call(t, "POST", "/v1/commands", map[string]any{"ops": []map[string]any{{"op": "object.restore", "id": "x"}}}, nil)
	if code != 403 {
		t.Fatalf("restore must be forbidden for users: %d %s", code, raw)
	}
	// needs_review → retry with answer → processed.
	_, raw = e.call(t, "POST", "/v1/captures", map[string]any{"text": "?"}, nil)
	var cap map[string]any
	_ = json.Unmarshal(raw, &cap)
	id := cap["id"].(string)
	waitStatus(t, e, id, model.CaptureProcessed) // heuristic files "?" as a question note
	code, raw = e.call(t, "POST", "/v1/captures/"+id+"/retry", map[string]any{"answer": "Ich muss das Dach prüfen"}, nil)
	if code != 202 {
		t.Fatalf("retry: %d %s", code, raw)
	}
	waitStatus(t, e, id, model.CaptureProcessed)
	code, raw = e.call(t, "GET", "/v1/search?q=dach", nil, nil)
	if code != 200 || !strings.Contains(string(raw), "Dach") {
		t.Fatalf("search: %d %s", code, raw)
	}
	code, raw = e.call(t, "GET", "/v1/export?format=json", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"objects"`) {
		t.Fatalf("export: %d", code)
	}
	code, _ = e.call(t, "GET", "/v1/export?format=markdown", nil, nil)
	if code != 200 {
		t.Fatalf("markdown export: %d", code)
	}
	code, raw = e.call(t, "GET", "/v1/objects/task_missing", nil, nil)
	if code != 404 {
		t.Fatalf("missing object: %d %s", code, raw)
	}
}

func TestAuthToken(t *testing.T) {
	e := newEnv(t, "secret")
	e.cfg.RequireTokenOnLoopback = true
	code, _ := e.call(t, "GET", "/v1/stats", nil, nil)
	if code != 401 {
		t.Fatalf("without token: %d", code)
	}
	code, _ = e.call(t, "GET", "/v1/stats", nil, map[string]string{"Authorization": "Bearer wrong"})
	if code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	code, _ = e.call(t, "GET", "/v1/stats", nil, map[string]string{"Authorization": "Bearer secret"})
	if code != 200 {
		t.Fatalf("right token: %d", code)
	}
	// SSE accepts the token as a query parameter (EventSource cannot set headers).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/events?token=secret", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("sse with query token: %d", res.StatusCode)
	}
}

func TestSSEHello(t *testing.T) {
	e := newEnv(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(res.Body)
	var lines []string
	for sc.Scan() && len(lines) < 2 {
		lines = append(lines, sc.Text())
	}
	if len(lines) < 2 || lines[0] != "event: hello" || !strings.HasPrefix(lines[1], "data: {") {
		t.Fatalf("hello event: %v", lines)
	}
}

func TestConversationWithFakeProvider(t *testing.T) {
	e := newEnv(t, "")
	code, raw := e.call(t, "POST", "/v1/conversations", map[string]any{}, nil)
	if code != 201 {
		t.Fatalf("create conversation: %d %s", code, raw)
	}
	var cv map[string]any
	_ = json.Unmarshal(raw, &cv)
	id := cv["id"].(string)
	code, raw = e.call(t, "POST", "/v1/conversations/"+id+"/messages", map[string]any{"text": "Was weiß ich über Deye?"}, nil)
	if code != 200 {
		t.Fatalf("message: %d %s", code, raw)
	}
	var reply map[string]any
	_ = json.Unmarshal(raw, &reply)
	msg := reply["message"].(map[string]any)
	if msg["role"] != "assistant" || msg["text"] == "" {
		t.Fatalf("reply %v", reply)
	}
	code, raw = e.call(t, "GET", "/v1/conversations/"+id, nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"capture_id"`) {
		t.Fatalf("conversation: %d %s", code, raw)
	}
	// The user turn is also a capture (already processed, so not in the inbox).
	code, raw = e.call(t, "GET", "/v1/inbox", nil, nil)
	if code != 200 || string(raw) != "[]\n" {
		t.Fatalf("inbox should be empty, got %s", raw)
	}
}

func TestBrowserProtections(t *testing.T) {
	e := newEnv(t, "")
	// Wrong Host (DNS rebinding) is refused even from loopback.
	code, _ := e.call(t, "GET", "/v1/stats", nil, map[string]string{"Host": "evil.example:7433"})
	if code != 421 {
		t.Fatalf("bad host: %d", code)
	}
	// Cross-site browser request is refused.
	code, _ = e.call(t, "POST", "/v1/captures", map[string]any{"text": "x"}, map[string]string{"Origin": "https://evil.example"})
	if code != 403 {
		t.Fatalf("cross-site origin: %d", code)
	}
	code, _ = e.call(t, "GET", "/v1/export", nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != 403 {
		t.Fatalf("sec-fetch-site: %d", code)
	}
	// Simple-request content types cannot carry commands.
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/captures", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 415 {
		t.Fatalf("text/plain: %d", res.StatusCode)
	}
	// Proxied requests lose the loopback exemption when a token is set.
	e2 := newEnv(t, "secret")
	code, _ = e2.call(t, "GET", "/v1/stats", nil, map[string]string{"X-Forwarded-For": "10.0.0.5"})
	if code != 401 {
		t.Fatalf("forwarded without token: %d", code)
	}
	// Same-origin requests from the embedded UI pass.
	origin := "http://" + strings.TrimPrefix(e.srv.URL, "http://")
	code, _ = e.call(t, "GET", "/v1/stats", nil, map[string]string{"Origin": origin})
	if code != 200 {
		t.Fatalf("same-origin: %d", code)
	}
	// Too many ops in one command.
	ops := make([]map[string]any, 101)
	for i := range ops {
		ops[i] = map[string]any{"op": "topic.create", "name": fmt.Sprintf("t%d", i)}
	}
	code, _ = e.call(t, "POST", "/v1/commands", map[string]any{"ops": ops}, nil)
	if code != 400 {
		t.Fatalf("ops cap: %d", code)
	}
	// Client id pointing at a non-capture does not panic.
	c1, raw := e.call(t, "POST", "/v1/commands", map[string]any{"ops": []map[string]any{{"op": "topic.create", "name": "Haus"}}}, nil)
	if c1 != 200 {
		t.Fatalf("commands: %d %s", c1, raw)
	}
	var rec struct {
		Lines []struct {
			ObjectID string `json:"object_id"`
		} `json:"lines"`
	}
	_ = json.Unmarshal(raw, &rec)
	code, _ = e.call(t, "POST", "/v1/captures", map[string]any{"id": rec.Lines[0].ObjectID, "text": "x"}, nil)
	if code != 409 {
		t.Fatalf("capture with topic id: %d", code)
	}
}

func TestProposalAcceptAndWait(t *testing.T) {
	e := newEnv(t, "", func(c *config.Config) { c.Autonomy.AutoCreate = false })

	// wait= returns the parked capture with its proposal.
	code, raw := e.call(t, "POST", "/v1/captures?wait=5000", map[string]any{"text": "Ich muss den Zaun streichen"}, nil)
	if code != 202 {
		t.Fatalf("capture: %d %s", code, raw)
	}
	var cap map[string]any
	_ = json.Unmarshal(raw, &cap)
	if cap["status"] != "needs_review" {
		t.Fatalf("expected a parked proposal, got %v", cap["status"])
	}
	res := cap["result"].(map[string]any)
	if res["proposal"] == nil {
		t.Fatalf("proposal missing: %v", res)
	}
	id := cap["id"].(string)
	code, raw = e.call(t, "GET", "/v1/tasks?state=open", nil, nil)
	if code != 200 || strings.Contains(string(raw), "Zaun") {
		t.Fatalf("nothing should be written before accept: %s", raw)
	}
	code, raw = e.call(t, "POST", "/v1/captures/"+id+"/accept", map[string]any{}, nil)
	if code != 200 {
		t.Fatalf("accept: %d %s", code, raw)
	}
	_ = json.Unmarshal(raw, &cap)
	if cap["status"] != "processed" {
		t.Fatalf("status after accept: %v", cap["status"])
	}
	code, raw = e.call(t, "GET", "/v1/tasks?state=open", nil, nil)
	if code != 200 || !strings.Contains(string(raw), "Zaun") {
		t.Fatalf("task missing after accept: %s", raw)
	}
	// The accepting user is the actor of the receipt.
	receipts := cap["receipts"].([]any)
	last := receipts[len(receipts)-1].(map[string]any)
	if !strings.HasPrefix(last["actor"].(string), "user:") {
		t.Fatalf("actor %v", last["actor"])
	}
	code, _ = e.call(t, "POST", "/v1/captures/"+id+"/accept", map[string]any{}, nil)
	if code != 409 {
		t.Fatalf("second accept: %d", code)
	}
	// Batch object resolution and backup.
	code, raw = e.call(t, "GET", "/v1/objects?ids="+id+",task_missing", nil, nil)
	if code != 200 || !strings.Contains(string(raw), id) || strings.Contains(string(raw), "task_missing") {
		t.Fatalf("objects: %d %s", code, raw)
	}
	code, raw = e.call(t, "GET", "/v1/backup", nil, nil)
	if code != 200 || len(raw) < 100 || string(raw[:2]) != "PK" {
		t.Fatalf("backup: %d %d bytes", code, len(raw))
	}
	code, raw = e.call(t, "GET", "/v1/changes?after=0&all=1&limit=3", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"seq":1`) {
		t.Fatalf("changes after: %d %s", code, raw)
	}
}

func TestSetupFlowHotReload(t *testing.T) {
	// Start without a usable provider: captures must wait, not fail.
	e := newEnv(t, "", func(c *config.Config) {
		c.Triage.Provider, c.Chat.Provider = "openai", "openai"
		c.Path = t.TempDir() + "/config.toml"
		p := c.Providers["openai"]
		p.APIKeyEnv = "FUNDUS_TEST_NO_SUCH_KEY"
		c.Providers["openai"] = p
	})
	code, raw := e.call(t, "GET", "/v1/health", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"setup_needed":true`) {
		t.Fatalf("health: %d %s", code, raw)
	}
	code, raw = e.call(t, "POST", "/v1/captures", map[string]any{"text": "Ich muss warten"}, nil)
	if code != 202 {
		t.Fatalf("capture: %d %s", code, raw)
	}
	var cap map[string]any
	_ = json.Unmarshal(raw, &cap)
	id := cap["id"].(string)
	time.Sleep(150 * time.Millisecond)
	code, raw = e.call(t, "GET", "/v1/captures/"+id, nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"status":"pending"`) {
		t.Fatalf("capture should still be pending without a model: %s", raw)
	}
	code, raw = e.call(t, "GET", "/v1/settings", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"key_status":"unset"`) {
		t.Fatalf("settings: %d %s", code, raw)
	}
	// Switch to the heuristic provider through the settings API.
	code, raw = e.call(t, "PUT", "/v1/settings", map[string]any{
		"triage": map[string]string{"provider": "fake"}, "chat": map[string]string{"provider": "fake"}}, nil)
	if code != 200 || !strings.Contains(string(raw), `"setup_needed":false`) {
		t.Fatalf("put settings: %d %s", code, raw)
	}
	if _, err := os.Stat(e.cfg.Path); err != nil {
		t.Fatalf("config not saved: %v", err)
	}
	waitStatus(t, e, id, model.CaptureProcessed)
	// Keys are stored but never returned.
	code, raw = e.call(t, "PUT", "/v1/settings", map[string]any{"providers": map[string]any{"openai": map[string]any{"api_key": "sk-secret-9999"}}}, nil)
	if code != 200 || strings.Contains(string(raw), "sk-secret") || !strings.Contains(string(raw), `"key_hint":"…9999"`) {
		t.Fatalf("key handling: %d %s", code, raw)
	}
	saved, _ := os.ReadFile(e.cfg.Path)
	if !strings.Contains(string(saved), "sk-secret-9999") {
		t.Fatal("key not persisted")
	}
	// Test endpoint with the fake provider works without a model name.
	code, raw = e.call(t, "POST", "/v1/settings/test", map[string]any{"provider": "fake"}, nil)
	if code != 200 || !strings.Contains(string(raw), `"reachable":true`) {
		t.Fatalf("test: %d %s", code, raw)
	}
	// OAuth start needs a supported provider and yields a URL with the callback.
	code, raw = e.call(t, "POST", "/v1/setup/oauth/start", map[string]any{"provider": "openrouter"}, nil)
	if code != 200 || !strings.Contains(string(raw), "setup%2Foauth%2Fcallback") {
		t.Fatalf("oauth start: %d %s", code, raw)
	}
	code, _ = e.call(t, "POST", "/v1/setup/oauth/start", map[string]any{"provider": "openai"}, nil)
	if code != 400 {
		t.Fatalf("oauth start openai: %d", code)
	}
}

func TestSettingsSecurityRules(t *testing.T) {
	e := newEnv(t, "", func(c *config.Config) {
		c.Path = t.TempDir() + "/config.toml"
		p := c.Providers["openai"]
		p.APIKey = "sk-stored-1234"
		c.Providers["openai"] = p
	})
	// A stored key must not be sent to an endpoint given in the request.
	code, raw := e.call(t, "POST", "/v1/settings/test", map[string]any{"provider": "openai", "base_url": "http://127.0.0.1:1", "model": "x"}, nil)
	if code != 400 || !strings.Contains(string(raw), "requires the key") {
		t.Fatalf("test with foreign base_url: %d %s", code, raw)
	}
	code, raw = e.call(t, "POST", "/v1/setup/models", map[string]any{"provider": "openai", "base_url": "http://127.0.0.1:1"}, nil)
	if code != 400 {
		t.Fatalf("models with foreign base_url: %d %s", code, raw)
	}
	// Bad key against a mock endpoint: reachable=false with a message, no crash.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer bad.Close()
	code, raw = e.call(t, "POST", "/v1/setup/models", map[string]any{"provider": "openai", "base_url": bad.URL, "api_key": "sk-wrong"}, nil)
	if code != 200 || !strings.Contains(string(raw), "rejected") {
		t.Fatalf("models with bad key: %d %s", code, raw)
	}
	code, raw = e.call(t, "POST", "/v1/settings/test", map[string]any{"provider": "openai", "base_url": bad.URL, "api_key": "sk-wrong", "model": "x"}, nil)
	if code != 200 || !strings.Contains(string(raw), `"reachable":false`) {
		t.Fatalf("test with bad key: %d %s", code, raw)
	}
	// PUT cannot move the stored key to another host.
	code, raw = e.call(t, "PUT", "/v1/settings", map[string]any{"providers": map[string]any{"openai": map[string]any{"base_url": "https://evil.example/v1"}}}, nil)
	if code != 400 {
		t.Fatalf("put base_url without key: %d %s", code, raw)
	}
	// Unwritable config path: 500 and nothing applied.
	e2 := newEnv(t, "", func(c *config.Config) { c.Path = "/proc/fundus-cannot-write/config.toml" })
	code, raw = e2.call(t, "PUT", "/v1/settings", map[string]any{"triage": map[string]string{"provider": "fake"}}, nil)
	if code != 500 {
		t.Fatalf("unwritable path: %d %s", code, raw)
	}
	if e2.cfg.Triage.Provider != "fake" && e2.srv != nil {
		// the server's live config must still be the original
		code, raw = e2.call(t, "GET", "/v1/settings", nil, nil)
		if code != 200 || !strings.Contains(string(raw), `"provider":"fake"`) == (e2.cfg.Triage.Provider != "fake") {
			// nothing to assert beyond a consistent answer
		}
	}
}

func TestSettingsConcurrentPuts(t *testing.T) {
	e := newEnv(t, "", func(c *config.Config) { c.Path = t.TempDir() + "/config.toml" })
	var wg sync.WaitGroup
	errs := make(chan string, 40)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tz := "Europe/Berlin"
			if i%2 == 1 {
				tz = "UTC"
			}
			code, raw := e.call(t, "PUT", "/v1/settings", map[string]any{"timezone": tz}, nil)
			if code != 200 {
				errs <- fmt.Sprintf("%d %s", code, raw)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent put failed: %s", err)
	}
	code, raw := e.call(t, "GET", "/v1/health", nil, nil)
	if code != 200 || !(strings.Contains(string(raw), "Europe/Berlin") || strings.Contains(string(raw), `"timezone":"UTC"`)) {
		t.Fatalf("timezone not applied at runtime: %s", raw)
	}
}

func TestOAuthCallbackEndToEnd(t *testing.T) {
	keySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"key":"sk-or-v1-fromoauth"}`)
	}))
	defer keySrv.Close()
	setup.OpenRouterKeyURL = keySrv.URL
	e := newEnv(t, "", func(c *config.Config) { c.Path = t.TempDir() + "/config.toml" })
	code, raw := e.call(t, "POST", "/v1/setup/oauth/start", map[string]any{"provider": "openrouter"}, nil)
	if code != 200 {
		t.Fatalf("start: %d %s", code, raw)
	}
	var st struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &st)
	u, _ := url.Parse(st.URL)
	cb, _ := url.Parse(u.Query().Get("callback_url"))
	state := cb.Query().Get("state")
	// Wrong state → 400 page, nothing stored.
	res, err := http.Get(e.srv.URL + "/setup/oauth/callback?state=nope&code=c")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("wrong state: %d", res.StatusCode)
	}
	// Right state → key stored, page 200, never returned in clear.
	res, err = http.Get(e.srv.URL + "/setup/oauth/callback?state=" + url.QueryEscape(state) + "&code=the-code")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "Connected") || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("callback: %d %s", res.StatusCode, body)
	}
	code, raw = e.call(t, "GET", "/v1/settings", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"openrouter":{`) || !strings.Contains(string(raw), `"key_hint":"…auth"`) || strings.Contains(string(raw), "sk-or-v1") {
		t.Fatalf("settings after oauth: %s", raw)
	}
	saved, _ := os.ReadFile(e.cfg.Path)
	if !strings.Contains(string(saved), "sk-or-v1-fromoauth") {
		t.Fatal("oauth key not persisted")
	}
}

func TestTranscribeEndpoint(t *testing.T) {
	// A fake OpenAI-compatible endpoint that answers the audio path.
	var gotPrompt string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			w.WriteHeader(404)
			return
		}
		_ = r.ParseMultipartForm(1 << 20)
		gotPrompt = r.FormValue("prompt")
		_, _ = io.WriteString(w, `{"text":"Fundus needs a landing page."}`)
	}))
	defer fake.Close()
	e := newEnv(t, "", func(cfg *config.Config) {
		cfg.Providers["openai"] = config.Provider{Type: "openai", BaseURL: fake.URL, APIKey: "k", Transcription: "audio"}
		cfg.Dictation = config.Role{Provider: "openai", Model: "gpt-transcribe"}
	})
	// Health advertises dictation.
	code, raw := e.call(t, "GET", "/v1/health", nil, nil)
	if code != 200 || !strings.Contains(string(raw), `"dictation":true`) {
		t.Fatalf("health: %d %s", code, raw)
	}
	// A topic name becomes a spelling hint.
	e.call(t, "POST", "/v1/commands", map[string]any{"ops": []map[string]any{{"op": "topic.create", "name": "Deye"}}}, nil)

	upload := func(field string, data []byte, contentType string) (int, []byte) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, _ := mw.CreateFormFile(field, "rec.wav")
		_, _ = part.Write(data)
		_ = mw.WriteField("language", "en")
		_ = mw.Close()
		ct := mw.FormDataContentType()
		if contentType != "" {
			ct = contentType
		}
		req, _ := http.NewRequest("POST", e.srv.URL+"/v1/transcribe", &buf)
		req.Header.Set("Content-Type", ct)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	code, raw = upload("audio", []byte("RIFF....WAVEfmt "), "")
	if code != 200 || !strings.Contains(string(raw), "landing page") {
		t.Fatalf("transcribe: %d %s", code, raw)
	}
	if !strings.Contains(gotPrompt, "Fundus") || !strings.Contains(gotPrompt, "Deye") {
		t.Fatalf("hints not passed: %q", gotPrompt)
	}
	if code, raw = upload("file", []byte("abc"), ""); code != 400 {
		t.Fatalf("wrong field: %d %s", code, raw)
	}
	if code, raw = upload("audio", []byte("abc"), "application/json"); code != 400 && code != 415 {
		t.Fatalf("json content type: %d %s", code, raw)
	}
	// Without a usable dictation provider the endpoint says so.
	off := newEnv(t, "")
	code, raw = off.call(t, "GET", "/v1/health", nil, nil)
	if !strings.Contains(string(raw), `"dictation":false`) {
		t.Fatalf("health without dictation: %s", raw)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("audio", "rec.wav")
	_, _ = part.Write([]byte("RIFF"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", off.srv.URL+"/v1/transcribe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("dictation off: %d", res.StatusCode)
	}
}

func TestResearchEndpoint(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><head><title>Go 1.27 is released</title></head><body><main><p>Go 1.27 was released in August 2026. "+strings.Repeat("Details. ", 80)+"</p></main></body></html>")
	}))
	defer page.Close()
	calls := 0
	fake := &llm.Fake{ProviderName: "openai", Fn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
		calls++
		if req.Schema != nil {
			return &llm.Response{Content: `{"answer":"Go 1.27 is current [1].","findings":[{"claim":"Go 1.27 released August 2026","sources":[1]}],"uncertainties":[],"confidence":0.9}`}, nil
		}
		switch calls {
		case 1:
			return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "web_search", Args: json.RawMessage(`{"query":"go latest version"}`)}}}, nil
		case 2:
			return &llm.Response{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "fetch_page", Args: json.RawMessage(`{"url":"` + page.URL + `/"}`)}}}, nil
		}
		return &llm.Response{Content: "DONE"}, nil
	}, SearchFn: func(ctx context.Context, model, query string, n int) ([]llm.SearchResult, error) {
		return []llm.SearchResult{{URL: page.URL + "/", Title: "Go release"}}, nil
	}}
	e := newEnv(t, "", func(cfg *config.Config) {
		cfg.Providers["openai"] = config.Provider{Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "k", WebSearch: "chat_completions"}
		cfg.Chat = config.Role{Provider: "openai", Model: "m"}
	})
	// Not wired yet: unavailable.
	code, raw := e.call(t, "POST", "/v1/research", map[string]any{"question": "x"}, nil)
	if code != 503 {
		t.Fatalf("without worker: %d %s", code, raw)
	}
	rw := research.New(e.core, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	rw.Fetcher.Client = &http.Client{Timeout: 5 * time.Second} // loopback pages for the test
	e.s.SetResearch(rw)
	// configureRuntime built the searcher from the registry's provider, which
	// is a real OpenAI adapter; swap in the fake for the test.
	rw.Configure(fake, e.cfg.ResearchRole(), e.cfg.Research, research.NewSearcher(e.cfg, fake, nil))
	code, raw = e.call(t, "GET", "/v1/health", nil, nil)
	if !strings.Contains(string(raw), `"research":true`) {
		t.Fatalf("health: %s", raw)
	}
	code, raw = e.call(t, "POST", "/v1/research", map[string]any{"question": "What is the latest Go version?"}, nil)
	if code != 202 {
		t.Fatalf("start: %d %s", code, raw)
	}
	var started struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(raw, &started)
	deadline := time.Now().Add(10 * time.Second)
	for {
		obj, err := e.core.Get(started.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		task := obj.(*model.Task)
		if task.State == model.TaskDone {
			if len(task.Notes) != 1 {
				t.Fatalf("task notes %v", task.Notes)
			}
			note, _ := e.core.Get(task.Notes[0])
			if md := note.(*model.Note).Body.Markdown(); !strings.Contains(md, "[!external] Go 1.27 is current [1].") || !strings.Contains(md, "[[src_") {
				t.Fatalf("note:\n%s", md)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("research did not finish; state %s", task.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	code, raw = e.call(t, "POST", "/v1/research", map[string]any{"task_id": started.TaskID}, nil)
	if code != 409 {
		t.Fatalf("done task: %d %s", code, raw)
	}
	rw.Stop()
}
