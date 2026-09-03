// Package setup implements first-run configuration: provider credentials,
// model discovery and OAuth flows, driven by the UI without restarts.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fundus-app/fundus/internal/config"
)

// ProviderView is what the UI sees about a provider: never the key itself.
type ProviderView struct {
	Type      string `json:"type"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	KeyStatus string `json:"key_status"` // set | env | unset | none
	KeyHint   string `json:"key_hint,omitempty"`
	Local     bool   `json:"local"`
	OAuth     bool   `json:"oauth"`
	Usable    bool   `json:"usable"`
	// Transcription is audio | chat | none: whether dictation works here.
	Transcription string `json:"transcription"`
}

// View is the settings document returned to clients.
type View struct {
	Path        string                  `json:"path"`
	Listen      string                  `json:"listen"`
	Timezone    string                  `json:"timezone"`
	TokenSet    bool                    `json:"token_set"`
	SetupNeeded bool                    `json:"setup_needed"`
	Triage      RoleView                `json:"triage"`
	Chat        RoleView                `json:"chat"`
	Dictation   RoleView                `json:"dictation"`
	Autonomy    config.Policy           `json:"autonomy"`
	Providers   map[string]ProviderView `json:"providers"`
}

// RoleView names provider and model of a role.
type RoleView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Patch is a partial settings update.
type Patch struct {
	Triage    *RoleView                `json:"triage,omitempty"`
	Chat      *RoleView                `json:"chat,omitempty"`
	Dictation *RoleView                `json:"dictation,omitempty"`
	Timezone  *string                  `json:"timezone,omitempty"`
	Token     *string                  `json:"token,omitempty"`
	Autonomy  *PolicyPatch             `json:"autonomy,omitempty"`
	Providers map[string]ProviderPatch `json:"providers,omitempty"`
}

// PolicyPatch updates autonomy fields.
type PolicyPatch struct {
	MinConfidence          *float64 `json:"min_confidence,omitempty"`
	AutoCreate             *bool    `json:"auto_create,omitempty"`
	MaxOpsPerCapture       *int     `json:"max_ops_per_capture,omitempty"`
	MaxNewTopicsPerCapture *int     `json:"max_new_topics_per_capture,omitempty"`
}

// ProviderPatch updates a provider; an empty api_key clears the stored key.
type ProviderPatch struct {
	APIKey  *string `json:"api_key,omitempty"`
	BaseURL *string `json:"base_url,omitempty"`
	Type    string  `json:"type,omitempty"`
}

// OAuthProviders lists providers with a supported connect flow.
var OAuthProviders = map[string]bool{"openrouter": true}

// BuildView renders the config for clients.
func BuildView(cfg *config.Config) View {
	v := View{Path: cfg.Path, Listen: cfg.Listen, Timezone: cfg.Timezone, TokenSet: cfg.Token != "", SetupNeeded: cfg.SetupNeeded(),
		Triage: RoleView{cfg.Triage.Provider, cfg.Triage.Model}, Chat: RoleView{cfg.Chat.Provider, cfg.Chat.Model},
		Dictation: RoleView{cfg.Dictation.Provider, cfg.Dictation.Model},
		Autonomy:  cfg.Autonomy, Providers: map[string]ProviderView{}}
	if v.Timezone == "" {
		v.Timezone = time.Local.String()
	}
	for name, p := range cfg.Providers {
		pv := ProviderView{Type: p.Type, BaseURL: p.BaseURL, APIKeyEnv: p.APIKeyEnv, Local: p.Local(), OAuth: OAuthProviders[name], Usable: p.Usable(),
			Transcription: p.TranscriptionMode()}
		switch {
		case p.Type == "fake":
			pv.KeyStatus = "none"
		case p.APIKey != "":
			pv.KeyStatus, pv.KeyHint = "set", hint(p.APIKey)
		case p.ResolveAPIKey() != "":
			pv.KeyStatus, pv.KeyHint = "env", hint(p.ResolveAPIKey())
		default:
			pv.KeyStatus = "unset"
		}
		v.Providers[name] = pv
	}
	return v
}

func hint(key string) string {
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}

// Apply merges a patch into a copy of cfg and validates it.
func Apply(cfg *config.Config, p Patch) (*config.Config, error) {
	next := cfg.Clone()
	if p.Timezone != nil {
		if *p.Timezone != "" {
			if _, err := time.LoadLocation(*p.Timezone); err != nil {
				return nil, fmt.Errorf("timezone: %w", err)
			}
		}
		next.Timezone = *p.Timezone
	}
	if p.Token != nil {
		next.Token = strings.TrimSpace(*p.Token)
		if next.Token == "" && !config.IsLoopbackListen(next.Listen) {
			return nil, errors.New("the token cannot be removed while Fundus listens on a network address")
		}
	}
	if p.Autonomy != nil {
		if p.Autonomy.MinConfidence != nil {
			next.Autonomy.MinConfidence = *p.Autonomy.MinConfidence
		}
		if p.Autonomy.AutoCreate != nil {
			next.Autonomy.AutoCreate = *p.Autonomy.AutoCreate
		}
		if p.Autonomy.MaxOpsPerCapture != nil {
			next.Autonomy.MaxOpsPerCapture = *p.Autonomy.MaxOpsPerCapture
		}
		if p.Autonomy.MaxNewTopicsPerCapture != nil {
			next.Autonomy.MaxNewTopicsPerCapture = *p.Autonomy.MaxNewTopicsPerCapture
		}
	}
	for name, pp := range p.Providers {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		prov, ok := next.Providers[name]
		if !ok {
			if pp.Type == "" {
				pp.Type = "openai"
			}
			prov = config.Provider{Type: pp.Type, Transcription: config.PresetTranscription(name)}
		}
		if pp.BaseURL != nil {
			u := strings.TrimSpace(*pp.BaseURL)
			if u != "" {
				parsed, err := url.Parse(u)
				if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
					return nil, fmt.Errorf("providers.%s.base_url must be an http(s) URL", name)
				}
			}
			u = strings.TrimRight(u, "/")
			if !sameHost(prov.BaseURL, u) {
				// A key must never follow the endpoint to a new host: the
				// caller has to supply the key for the new endpoint. A
				// provider without any key (Ollama on another machine) has
				// nothing to protect.
				if pp.APIKey == nil && (prov.ResolveAPIKey() != "" || prov.APIKeyEnv != "") {
					return nil, fmt.Errorf("providers.%s: changing base_url requires api_key in the same request", name)
				}
				prov.APIKeyEnv = ""
			}
			prov.BaseURL = u
		}
		if pp.APIKey != nil {
			prov.APIKey = strings.TrimSpace(*pp.APIKey)
		}
		if prov.APIKey != "" && strings.HasPrefix(strings.ToLower(prov.BaseURL), "http://") && !prov.Local() {
			return nil, fmt.Errorf("providers.%s: refusing to send a key over plain http to a remote host", name)
		}
		next.Providers[name] = prov
	}
	if p.Triage != nil {
		if p.Triage.Provider != "" && p.Triage.Provider != next.Triage.Provider {
			// Dictation follows the filing provider unless set on its own.
			if p.Dictation == nil && next.Dictation.Provider == next.Triage.Provider {
				next.Dictation.Provider = strings.ToLower(strings.TrimSpace(p.Triage.Provider))
				next.Dictation.Model = ""
			}
			next.Triage.Provider = strings.ToLower(strings.TrimSpace(p.Triage.Provider))
			next.Triage.Model = "" // a model belongs to a provider; require a new one
		}
		if p.Triage.Model != "" {
			next.Triage.Model = p.Triage.Model
		}
	}
	if p.Dictation != nil {
		if p.Dictation.Provider != "" && p.Dictation.Provider != next.Dictation.Provider {
			next.Dictation.Provider = strings.ToLower(strings.TrimSpace(p.Dictation.Provider))
		}
		// The model is taken as given: an empty one switches dictation off.
		next.Dictation.Model = p.Dictation.Model
	}
	if p.Chat != nil {
		if p.Chat.Provider != "" && p.Chat.Provider != next.Chat.Provider {
			next.Chat.Provider = strings.ToLower(strings.TrimSpace(p.Chat.Provider))
			next.Chat.Model = ""
		}
		if p.Chat.Model != "" {
			next.Chat.Model = p.Chat.Model
		}
	}
	// The heuristic has no model name; keep the audit label honest.
	if pc, ok := next.Providers[next.Triage.Provider]; ok && pc.Type == "fake" {
		next.Triage.Model = "heuristic"
	}
	if pc, ok := next.Providers[next.Chat.Provider]; ok && pc.Type == "fake" {
		next.Chat.Model = "heuristic"
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	return next, nil
}

// ---------------------------------------------------------------------------
// Model discovery

// Suggestion names default models for the roles; an empty Transcribe means
// the provider cannot hear.
type Suggestion struct {
	Triage     string `json:"triage"`
	Chat       string `json:"chat"`
	Transcribe string `json:"transcribe"`
}

// ModelList is the result of listing a provider's models.
type ModelList struct {
	Models    []string   `json:"models"`
	Suggested Suggestion `json:"suggested"`
	Error     string     `json:"error,omitempty"`
}

// ListModels queries an OpenAI-compatible /models endpoint.
func ListModels(ctx context.Context, name string, p config.Provider, client *http.Client) ModelList {
	out := ModelList{Models: []string{}}
	if p.Type != "openai" {
		return out
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.BaseURL, "/")+"/models", nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if key := p.ResolveAPIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		out.Error = describe(err, p)
		return out
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 400 {
		out.Error = fmt.Sprintf("HTTP %d from %s", res.StatusCode, p.BaseURL)
		if res.StatusCode == 401 || res.StatusCode == 403 {
			out.Error = "the key was rejected"
		}
		return out
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		out.Error = "unexpected response from " + p.BaseURL
		return out
	}
	for _, m := range body.Data {
		if m.ID != "" {
			out.Models = append(out.Models, m.ID)
		}
	}
	sort.Strings(out.Models)
	out.Suggested = Suggest(name, out.Models)
	return out
}

func describe(err error, p config.Provider) string {
	if p.Local() {
		return "nothing is listening at " + p.BaseURL + " (is Ollama running?)"
	}
	return "could not reach " + p.BaseURL + ": " + err.Error()
}

// Suggest picks default triage (fast) and chat (capable) models from a list.
func Suggest(provider string, models []string) Suggestion {
	has := func(id string) bool {
		for _, m := range models {
			if m == id {
				return true
			}
		}
		return false
	}
	first := func(cands ...string) string {
		for _, c := range cands {
			if has(c) {
				return c
			}
		}
		return ""
	}
	prefix := func(prefixes ...string) string {
		for _, pre := range prefixes {
			for _, m := range models {
				if strings.HasPrefix(m, pre) {
					return m
				}
			}
		}
		return ""
	}
	var s Suggestion
	switch provider {
	case "openai":
		// Explicit favourites first, then the newest generation by version
		// number so a future gpt-5.7 is picked up without a release.
		s.Triage = first("gpt-5.6-luna", "gpt-5.4-mini", "gpt-5-mini", "gpt-4.1-mini")
		if s.Triage == "" {
			s.Triage = newestGPT(models, "luna", "mini", "nano")
		}
		s.Chat = first("gpt-5.6-terra", "gpt-5.5", "gpt-5.4", "gpt-5.1", "gpt-5", "gpt-4.1")
		if s.Chat == "" {
			s.Chat = newestGPT(models, "terra", "sol", "")
		}
		s.Transcribe = first("gpt-transcribe", "gpt-4o-mini-transcribe", "gpt-4o-transcribe", "whisper-1")
	case "gemini":
		s.Triage = first("gemini-3.5-flash-lite", "gemini-3.1-flash-lite", "gemini-2.5-flash-lite", "gemini-3.8-flash", "gemini-2.5-flash")
		s.Chat = first("gemini-3.8-flash", "gemini-3.7-flash", "gemini-3.6-flash", "gemini-3.5-flash", "gemini-3.1-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash")
		if s.Triage == "" {
			s.Triage = prefix("gemini-3", "gemini-2.5-flash")
		}
		if s.Chat == "" {
			s.Chat = s.Triage
		}
		s.Transcribe = s.Chat
	case "anthropic":
		s.Triage = first("claude-haiku-4-5", "claude-sonnet-5", "claude-sonnet-4-6")
		s.Chat = first("claude-opus-5", "claude-sonnet-5", "claude-opus-4-8")
	case "openrouter":
		s.Triage = first("openai/gpt-5.6-luna", "openai/gpt-5.4-mini", "openai/gpt-5-mini", "anthropic/claude-haiku-4.5", "google/gemini-2.5-flash")
		s.Chat = first("openai/gpt-5.6-terra", "anthropic/claude-sonnet-5", "openai/gpt-5.5", "anthropic/claude-opus-5", "openai/gpt-5.1")
		s.Transcribe = first("google/gemini-3.8-flash", "google/gemini-2.5-flash", "google/gemini-2.5-flash-lite")
	case "ollama":
		s.Triage = prefix("qwen3:", "llama3.3", "llama3.1", "mistral", "gemma3")
		s.Chat = prefix("qwen3:", "llama3.3", "llama3.1", "gemma3", "mistral")
	}
	if s.Triage == "" && len(models) > 0 {
		s.Triage = models[0]
	}
	if s.Chat == "" {
		s.Chat = s.Triage
	}
	return s
}

// newestGPT returns the model with the highest "gpt-<major>.<minor>" version
// among plain model names (no dates, no codex/pro/chat-latest variants) whose
// suffix is one of variants ("" = the bare name). Ties keep variant order.
func newestGPT(models []string, variants ...string) string {
	best, bestMajor, bestMinor, bestRank := "", -1, -1, len(variants)
	for _, m := range models {
		rest, ok := strings.CutPrefix(m, "gpt-")
		if !ok {
			continue
		}
		ver, suffix, _ := strings.Cut(rest, "-")
		majS, minS, _ := strings.Cut(ver, ".")
		major, err1 := strconv.Atoi(majS)
		minor, err2 := strconv.Atoi(minS)
		if err1 != nil || (minS != "" && err2 != nil) {
			continue
		}
		rank := -1
		for i, v := range variants {
			if suffix == v {
				rank = i
				break
			}
		}
		if rank < 0 {
			continue
		}
		if major > bestMajor || (major == bestMajor && minor > bestMinor) || (major == bestMajor && minor == bestMinor && rank < bestRank) {
			best, bestMajor, bestMinor, bestRank = m, major, minor, rank
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// OAuth (PKCE)

// OpenRouter endpoints; variables so tests can point them at a mock.
var (
	OpenRouterAuthURL = "https://openrouter.ai/auth"
	OpenRouterKeyURL  = "https://openrouter.ai/api/v1/auth/keys"
)

func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	if ea != nil || eb != nil {
		return false
	}
	return strings.EqualFold(ua.Host, ub.Host) && ua.Scheme == ub.Scheme
}

// maxPendingFlows bounds the OAuth state table.
const maxPendingFlows = 16

// OAuth runs PKCE flows that end with a provider key stored in the config.
type OAuth struct {
	mu      sync.Mutex
	pending map[string]pendingFlow
	client  *http.Client
}

type pendingFlow struct {
	provider string
	verifier string
	expires  time.Time
}

// NewOAuth builds the flow store.
func NewOAuth(client *http.Client) *OAuth {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OAuth{pending: map[string]pendingFlow{}, client: client}
}

// Start returns the URL the user must open. callback is the daemon URL that
// receives the code (http://127.0.0.1:7433/setup/oauth/callback).
func (o *OAuth) Start(provider, callback string) (string, error) {
	if !OAuthProviders[provider] {
		return "", fmt.Errorf("%s has no connect flow; paste an API key instead", provider)
	}
	verifier := randomString(64)
	state := randomString(24)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	o.mu.Lock()
	for k, f := range o.pending {
		if time.Now().After(f.expires) {
			delete(o.pending, k)
		}
	}
	if len(o.pending) >= maxPendingFlows {
		o.mu.Unlock()
		return "", errors.New("too many connection attempts in progress; try again in a few minutes")
	}
	o.pending[state] = pendingFlow{provider: provider, verifier: verifier, expires: time.Now().Add(10 * time.Minute)}
	o.mu.Unlock()
	cb := callback + "?state=" + url.QueryEscape(state)
	q := url.Values{}
	q.Set("callback_url", cb)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return OpenRouterAuthURL + "?" + q.Encode(), nil
}

// ErrUnknownState means the callback did not match a pending flow.
var ErrUnknownState = errors.New("unknown or expired oauth state")

// Finish exchanges the code for a key. It returns the provider name and key.
func (o *OAuth) Finish(ctx context.Context, state, code string) (string, string, error) {
	o.mu.Lock()
	f, ok := o.pending[state]
	if ok {
		delete(o.pending, state)
	}
	o.mu.Unlock()
	if !ok || time.Now().After(f.expires) {
		return "", "", ErrUnknownState
	}
	if code == "" {
		return "", "", errors.New("no code in callback")
	}
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": f.verifier, "code_challenge_method": "S256"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OpenRouterKeyURL, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("key exchange: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		return "", "", fmt.Errorf("key exchange failed: HTTP %d", res.StatusCode)
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Key == "" {
		return "", "", errors.New("key exchange returned no key")
	}
	return f.provider, out.Key, nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}
