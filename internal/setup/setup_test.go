package setup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fundus-app/fundus/internal/config"
)

func TestApplyAndView(t *testing.T) {
	cfg := config.Default()
	if !cfg.SetupNeeded() {
		t.Fatal("default config without keys must need setup")
	}
	key := "sk-test-1234"
	next, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"openai": {APIKey: &key}},
		Triage: &RoleView{Provider: "openai", Model: "gpt-5.4-mini"}, Chat: &RoleView{Provider: "openai", Model: "gpt-5.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.SetupNeeded() || cfg.Providers["openai"].APIKey != "" {
		t.Fatal("apply must not mutate the original and must make setup unnecessary")
	}
	v := BuildView(next)
	if v.Providers["openai"].KeyStatus != "set" || v.Providers["openai"].KeyHint != "…1234" || v.SetupNeeded {
		t.Fatalf("view %+v", v.Providers["openai"])
	}
	if strings.Contains(mustJSON(v), key) {
		t.Fatal("view leaks the key")
	}
	bad := "ftp://x"
	if _, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"ollama": {BaseURL: &bad}}}); err == nil {
		t.Fatal("bad base url accepted")
	}
	if _, err := Apply(cfg, Patch{Triage: &RoleView{Provider: "nope"}}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	tz := "Europe/Berlin"
	if n, err := Apply(cfg, Patch{Timezone: &tz}); err != nil || n.Timezone != tz {
		t.Fatalf("timezone: %v", err)
	}
}

func TestSuggestAndListModels(t *testing.T) {
	s := Suggest("openai", []string{"gpt-4.1", "gpt-5.4-mini", "gpt-5.5", "whisper-1"})
	if s.Triage != "gpt-5.4-mini" || s.Chat != "gpt-5.5" {
		t.Fatalf("suggest %+v", s)
	}
	s = Suggest("ollama", []string{"llama3.1:8b", "qwen3:14b"})
	if s.Triage != "qwen3:14b" {
		t.Fatalf("ollama suggest %+v", s)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
	}))
	defer srv.Close()
	ml := ListModels(context.Background(), "openai", config.Provider{Type: "openai", BaseURL: srv.URL, APIKey: "k"}, nil)
	if ml.Error != "" || len(ml.Models) != 2 || ml.Models[0] != "a" {
		t.Fatalf("list %+v", ml)
	}
	ml = ListModels(context.Background(), "openai", config.Provider{Type: "openai", BaseURL: srv.URL}, nil)
	if ml.Error != "the key was rejected" {
		t.Fatalf("unauthorized: %+v", ml)
	}
}

func TestOAuthPKCE(t *testing.T) {
	var gotVerifier, gotCode string
	keySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotVerifier, gotCode = body["code_verifier"], body["code"]
		_, _ = io.WriteString(w, `{"key":"sk-or-v1-abc"}`)
	}))
	defer keySrv.Close()
	OpenRouterKeyURL = keySrv.URL
	o := NewOAuth(nil)
	u, err := o.Start("openrouter", "http://127.0.0.1:7433/setup/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	q := parsed.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("url %s", u)
	}
	cb, _ := url.Parse(q.Get("callback_url"))
	state := cb.Query().Get("state")
	if state == "" {
		t.Fatal("state missing from callback url")
	}
	if _, _, err := o.Finish(context.Background(), "wrong", "c"); err == nil {
		t.Fatal("unknown state accepted")
	}
	provider, key, err := o.Finish(context.Background(), state, "the-code")
	if err != nil || provider != "openrouter" || key != "sk-or-v1-abc" || gotCode != "the-code" || gotVerifier == "" {
		t.Fatalf("finish: %v %s %s", err, provider, key)
	}
	if _, _, err := o.Finish(context.Background(), state, "again"); err == nil {
		t.Fatal("state must be single use")
	}
	if _, err := o.Start("openai", "http://x"); err == nil {
		t.Fatal("openai has no oauth flow")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestApplyRules(t *testing.T) {
	cfg := config.Default()
	key := "sk-1"
	cfg.Providers["openai"] = config.Provider{Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: key, APIKeyEnv: "OPENAI_API_KEY"}
	// Changing the endpoint host without a key is refused; with a key the env fallback is dropped.
	other := "https://attacker.example/v1"
	if _, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"openai": {BaseURL: &other}}}); err == nil {
		t.Fatal("base_url change without key accepted")
	}
	k2 := "sk-2"
	next, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"openai": {BaseURL: &other, APIKey: &k2}}})
	if err != nil || next.Providers["openai"].APIKeyEnv != "" || next.Providers["openai"].APIKey != "sk-2" {
		t.Fatalf("base_url change with key: %v %+v", err, next.Providers["openai"])
	}
	// Same host, different path: fine without a key.
	same := "https://api.openai.com/v2"
	if _, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"openai": {BaseURL: &same}}}); err != nil {
		t.Fatalf("same host: %v", err)
	}
	// Plain http to a remote host with a key is refused; loopback is fine.
	plain := "http://lan-box:8080/v1"
	if _, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"openai": {BaseURL: &plain, APIKey: &k2}}}); err == nil {
		t.Fatal("key over plain http accepted")
	}
	local := "http://127.0.0.1:11434/v1"
	if _, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"ollama": {BaseURL: &local}}}); err != nil {
		t.Fatalf("local http: %v", err)
	}
	// Token cannot be emptied on a network listen.
	cfg2 := config.Default()
	cfg2.Listen, cfg2.Token = "0.0.0.0:7433", "t"
	empty := ""
	if _, err := Apply(cfg2, Patch{Token: &empty}); err == nil {
		t.Fatal("token removal on network listen accepted")
	}
	if _, err := Apply(cfg, Patch{Token: &empty}); err != nil {
		t.Fatalf("token removal on loopback: %v", err)
	}
	// Provider switch without a model is rejected (model reset), with model accepted.
	cfg3 := config.Default()
	cfg3.Triage.Provider, cfg3.Triage.Model = "fake", "heuristic"
	if _, err := Apply(cfg3, Patch{Triage: &RoleView{Provider: "openai"}}); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("provider switch kept stale model: %v", err)
	}
	if n, err := Apply(cfg3, Patch{Triage: &RoleView{Provider: "openai", Model: "gpt-5.4-mini"}}); err != nil || n.Triage.Model != "gpt-5.4-mini" {
		t.Fatalf("provider switch with model: %v", err)
	}
}

func TestSuggestCurrentFamilies(t *testing.T) {
	models := []string{"gpt-4.1", "gpt-5.4-mini", "gpt-5.5", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-4o-mini-transcribe", "gpt-transcribe", "whisper-1"}
	s := Suggest("openai", models)
	if s.Triage != "gpt-5.6-luna" || s.Chat != "gpt-5.6-terra" || s.Transcribe != "gpt-transcribe" {
		t.Fatalf("openai %+v", s)
	}
	// A future generation is found by version without a code change.
	s = Suggest("openai", []string{"gpt-5.7-luna", "gpt-5.7-terra", "gpt-5.7-sol", "gpt-5.7-2027-01-01", "gpt-5.7-codex", "gpt-4o-mini-transcribe"})
	if s.Triage != "gpt-5.7-luna" || s.Chat != "gpt-5.7-terra" || s.Transcribe != "gpt-4o-mini-transcribe" {
		t.Fatalf("future openai %+v", s)
	}
	if got := newestGPT([]string{"gpt-6", "gpt-5.9-terra", "gpt-6-terra"}, "terra", ""); got != "gpt-6-terra" {
		t.Fatalf("newestGPT %q", got)
	}
	s = Suggest("gemini", []string{"gemini-2.5-flash", "gemini-3.5-flash-lite", "gemini-3.8-flash", "gemini-3.1-pro-preview"})
	if s.Triage != "gemini-3.5-flash-lite" || s.Chat != "gemini-3.8-flash" || s.Transcribe != "gemini-3.8-flash" {
		t.Fatalf("gemini %+v", s)
	}
	if s := Suggest("anthropic", []string{"claude-sonnet-5"}); s.Transcribe != "" {
		t.Fatalf("anthropic cannot hear: %+v", s)
	}
}

func TestApplyDictationAndOllamaAddress(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["openai"] = config.Provider{Type: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-1", Transcription: "audio"}
	if !cfg.DictationAvailable() {
		t.Fatal("default openai setup should allow dictation")
	}
	// Ollama on another machine: no key anywhere, so the address may change freely.
	remote := "http://lan-box:11434/v1"
	next, err := Apply(cfg, Patch{Providers: map[string]ProviderPatch{"ollama": {BaseURL: &remote}}})
	if err != nil || next.Providers["ollama"].BaseURL != remote {
		t.Fatalf("remote ollama: %v %+v", err, next.Providers["ollama"])
	}
	// Switching the filing provider takes dictation along and clears its model.
	next, err = Apply(cfg, Patch{Triage: &RoleView{Provider: "ollama", Model: "qwen3:8b"}, Chat: &RoleView{Provider: "ollama", Model: "qwen3:8b"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Dictation.Provider != "ollama" || next.Dictation.Model != "" || next.DictationAvailable() {
		t.Fatalf("dictation after provider switch: %+v", next.Dictation)
	}
	v := BuildView(next)
	if v.Dictation.Provider != "ollama" || v.Providers["ollama"].Transcription != "none" || v.Providers["openai"].Transcription != "audio" || v.Providers["gemini"].Transcription != "chat" {
		t.Fatalf("view %+v %+v", v.Dictation, v.Providers["ollama"])
	}
	// An explicit dictation role keeps working on its own provider.
	next, err = Apply(next, Patch{Dictation: &RoleView{Provider: "openai", Model: "gpt-transcribe"}})
	if err != nil || !next.DictationAvailable() || next.Dictation.Model != "gpt-transcribe" {
		t.Fatalf("explicit dictation: %v %+v", err, next.Dictation)
	}
	empty := ""
	_ = empty
	next, err = Apply(next, Patch{Dictation: &RoleView{Provider: "openai", Model: ""}})
	if err != nil || next.DictationAvailable() {
		t.Fatalf("switching dictation off: %v %+v", err, next.Dictation)
	}
}
