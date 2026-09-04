// Package config loads the daemon configuration (TOML) with sane defaults.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the full daemon configuration.
type Config struct {
	DataDir string `toml:"data_dir"`
	Listen  string `toml:"listen"`
	// Timezone is an IANA zone name for "today", due dates and the model's
	// notion of now (default: the system zone; set it in containers).
	Timezone string `toml:"timezone"`
	// Token, when set, is required as "Authorization: Bearer <token>" on every
	// request that does not come from a loopback address.
	Token string `toml:"token"`
	// RequireTokenOnLoopback forces the bearer token even on 127.0.0.1.
	RequireTokenOnLoopback bool `toml:"require_token_on_loopback"`
	// AllowedHosts lists extra Host header values accepted besides loopback
	// and the listen address (e.g. "fundus.lan", "myhost.tailnet.ts.net").
	AllowedHosts []string `toml:"allowed_hosts"`

	Triage Role `toml:"triage"`
	Chat   Role `toml:"chat"`
	// Dictation names the provider and model that turn recorded speech into
	// capture text. An empty model switches dictation off.
	Dictation Role `toml:"dictation"`
	// Research configures the reader that answers "Research:" tasks from
	// the web (ADR-0010). Provider and model default to the chat role.
	Research Research `toml:"research"`
	// Embedding names the provider and model for semantic search. An empty
	// model keeps search lexical.
	Embedding Role `toml:"embedding"`
	// Maintenance schedules the background curation runs.
	Maintenance Maintenance `toml:"maintenance"`
	Autonomy    Policy      `toml:"autonomy"`

	Providers map[string]Provider `toml:"providers"`

	// Path is where the config was loaded from (not serialized).
	Path string `toml:"-"`

	// file is the configuration as read from disk (defaults + file), before
	// environment variables and command-line flags were applied. Save
	// writes overridden fields from this layer so that a temporary
	// FUNDUS_LISTEN or --fake never ends up in the user's file.
	file       *Config         `toml:"-"`
	overridden map[string]bool `toml:"-"`
}

// Role binds an LLM role (triage, chat, …) to a provider and model.
type Role struct {
	Provider        string   `toml:"provider"`
	Model           string   `toml:"model"`
	MaxAttempts     int      `toml:"max_attempts"`
	Timeout         Duration `toml:"timeout"`
	MaxTokens       int      `toml:"max_tokens"`
	Temperature     *float64 `toml:"temperature"`
	ReasoningEffort string   `toml:"reasoning_effort"`
}

// Research configures web research. Backend selects where searches go:
// "auto" (Brave when a key is set, SearXNG when a URL is set, otherwise the
// research provider's own web search), "brave", "searxng", "provider".
type Research struct {
	Provider       string   `toml:"provider"`
	Model          string   `toml:"model"`
	Backend        string   `toml:"backend"`
	SearchModel    string   `toml:"search_model"` // the provider's search-capable model (OpenAI)
	BraveAPIKeyEnv string   `toml:"brave_api_key_env"`
	BraveAPIKey    string   `toml:"brave_api_key"`
	SearxngURL     string   `toml:"searxng_url"`
	MaxSearches    int      `toml:"max_searches"`
	MaxPages       int      `toml:"max_pages"`
	Timeout        Duration `toml:"timeout"`
	// Manual stops research from starting by itself when triage files a
	// research task; the user then presses "Research this". Off by default:
	// asking for research is the request.
	Manual bool `toml:"manual"`
}

// AutoStart reports whether research tasks start without a click.
func (r Research) AutoStart() bool { return !r.Manual }

// BraveKey resolves the Brave Search key from the file or the environment.
func (r Research) BraveKey() string {
	if r.BraveAPIKey != "" {
		return r.BraveAPIKey
	}
	if r.BraveAPIKeyEnv != "" {
		return os.Getenv(r.BraveAPIKeyEnv)
	}
	return ""
}

// Maintenance configures the scheduled curation runs (concept Phase 2):
// integrity checks, topics for untagged notes and tasks, duplicate
// detection, automatic topic summaries and, optionally, help with tasks.
type Maintenance struct {
	Enabled bool `toml:"enabled"`
	// At is the local time of day for the daily run ("03:30"); Every runs
	// on an interval instead when set ("6h"). A run also starts on demand.
	At    string   `toml:"at"`
	Every Duration `toml:"every"`
	// Jobs.
	Integrity  bool `toml:"integrity"`
	Untagged   bool `toml:"untagged"`
	Duplicates bool `toml:"duplicates"`
	Summaries  bool `toml:"summaries"`
	// Assist lets the model work on open tasks: "off", "propose" (drafts
	// and research land in the inbox as proposals), "auto" (drafts are
	// written as notes linked to the task, research starts by itself).
	Assist string `toml:"assist"`
	// UntaggedAfterDays leaves fresh objects alone.
	UntaggedAfterDays int `toml:"untagged_after_days"`
	// KeepRuns limits the run history kept in the data directory.
	KeepRuns int `toml:"keep_runs"`
}

// Policy encodes the autonomy model: what the LLM may do without asking.
type Policy struct {
	// MinConfidence below which a triage result is parked in the inbox
	// instead of being written.
	MinConfidence float64 `toml:"min_confidence" json:"min_confidence"`
	// AutoCreate allows triage to create notes, ideas and tasks without
	// confirmation. When false every capture lands in the inbox as a proposal.
	AutoCreate bool `toml:"auto_create" json:"auto_create"`
	// MaxOpsPerCapture caps how much one capture may change.
	MaxOpsPerCapture int `toml:"max_ops_per_capture" json:"max_ops_per_capture"`
	// MaxNewTopicsPerCapture caps topic sprawl from a single capture.
	MaxNewTopicsPerCapture int `toml:"max_new_topics_per_capture" json:"max_new_topics_per_capture"`
}

// Provider describes one LLM endpoint.
type Provider struct {
	// Type is "openai" (any OpenAI-compatible chat completions API) or "fake".
	Type      string            `toml:"type"`
	BaseURL   string            `toml:"base_url"`
	APIKeyEnv string            `toml:"api_key_env"`
	APIKey    string            `toml:"api_key"`
	Headers   map[string]string `toml:"headers"`
	// Structured selects how JSON output is requested:
	// auto | json_schema | json_object | prompt.
	Structured string `toml:"structured"`
	// WebSearch names the provider's own web search, used by research when
	// no external backend is configured: "chat_completions" (OpenAI's
	// web_search_options with a search model), "openrouter" (the web plugin)
	// or "none". Empty means "none".
	WebSearch string `toml:"web_search"`
	// Transcription selects how speech is transcribed: "audio" uses the
	// /audio/transcriptions endpoint (OpenAI), "chat" sends the recording as
	// an input_audio part of a chat completion (Gemini, OpenRouter), "none"
	// disables dictation for this provider. Empty means "audio".
	Transcription string `toml:"transcription"`
}

// WebSearchMode returns the provider's web search capability.
func (p Provider) WebSearchMode() string {
	switch p.WebSearch {
	case "chat_completions", "openrouter":
		return p.WebSearch
	}
	return "none"
}

// TranscriptionMode returns the effective transcription strategy.
func (p Provider) TranscriptionMode() string {
	if p.Type != "openai" {
		return "none"
	}
	switch p.Transcription {
	case "chat", "none":
		return p.Transcription
	}
	return "audio"
}

// Duration is a TOML-friendly time.Duration ("30s", "2m").
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.Duration.String()), nil }

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		DataDir: defaultDataDir(),
		Listen:  "127.0.0.1:7433",
		Triage: Role{
			Provider:    "openai",
			Model:       "gpt-5.6-luna",
			MaxAttempts: 3,
			Timeout:     Duration{90 * time.Second},
			MaxTokens:   4000,
		},
		Chat: Role{
			Provider:    "openai",
			Model:       "gpt-5.6-terra",
			MaxAttempts: 2,
			Timeout:     Duration{180 * time.Second},
			MaxTokens:   8000,
		},
		Dictation: Role{
			Provider: "openai",
			Model:    "gpt-4o-mini-transcribe",
			Timeout:  Duration{120 * time.Second},
		},
		Embedding: Role{
			Provider: "openai",
			Model:    "text-embedding-3-small",
			Timeout:  Duration{2 * time.Minute},
		},
		Maintenance: Maintenance{
			Enabled: true, At: "03:30", Integrity: true, Untagged: true, Duplicates: true, Summaries: true,
			Assist: "off", UntaggedAfterDays: 1, KeepRuns: 30,
		},
		Research: Research{
			Backend:        "auto",
			SearchModel:    "gpt-5-search-api",
			BraveAPIKeyEnv: "BRAVE_API_KEY",
			MaxSearches:    3,
			MaxPages:       4,
			Timeout:        Duration{4 * time.Minute},
		},
		Autonomy: Policy{
			MinConfidence:          0.6,
			AutoCreate:             true,
			MaxOpsPerCapture:       12,
			MaxNewTopicsPerCapture: 2,
		},
		Providers: map[string]Provider{
			"openai": {
				Type:          "openai",
				BaseURL:       "https://api.openai.com/v1",
				APIKeyEnv:     "OPENAI_API_KEY",
				Transcription: "audio",
				WebSearch:     "chat_completions",
			},
			"gemini": {
				Type:          "openai",
				BaseURL:       "https://generativelanguage.googleapis.com/v1beta/openai",
				APIKeyEnv:     "GEMINI_API_KEY",
				Transcription: "chat",
			},
			"openrouter": {
				Type:          "openai",
				BaseURL:       "https://openrouter.ai/api/v1",
				APIKeyEnv:     "OPENROUTER_API_KEY",
				Transcription: "chat",
				WebSearch:     "openrouter",
			},
			"anthropic": {
				Type:          "openai",
				BaseURL:       "https://api.anthropic.com/v1",
				APIKeyEnv:     "ANTHROPIC_API_KEY",
				Transcription: "none",
			},
			"ollama": {
				Type:          "openai",
				BaseURL:       "http://127.0.0.1:11434/v1",
				Transcription: "none",
			},
			"fake": {Type: "fake"},
		},
	}
}

// DefaultPath returns the default config file location.
func DefaultPath() string {
	if p := os.Getenv("FUNDUS_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "fundus", "config.toml")
}

func defaultDataDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "fundus")
}

// Load reads the config at path (or the default path when empty). A missing
// file is not an error: defaults apply. Environment variables override:
// FUNDUS_DATA_DIR, FUNDUS_LISTEN, FUNDUS_TOKEN.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	cfg := Default()
	cfg.Path = path
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(os.Stderr, "warning: %s is readable by other users (mode %o); run: chmod 600 %s\n", path, st.Mode().Perm(), path)
		}
		// Decode over defaults so unspecified keys keep their default values.
		// Provider tables in the file replace the defaults with the same name.
		if _, err := toml.Decode(string(data), cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg.file = cfg.Clone()
	cfg.overridden = map[string]bool{}
	applyEnv(cfg)
	cfg.DataDir = expandHome(cfg.DataDir)
	// Files written before 0.3.5 know no transcription mode; give the
	// well-known providers theirs so Ollama and Anthropic never receive
	// recordings.
	for name, p := range cfg.Providers {
		if p.Transcription == "" {
			p.Transcription = PresetTranscription(name)
		}
		if p.WebSearch == "" {
			p.WebSearch = PresetWebSearch(name)
		}
		cfg.Providers[name] = p
	}
	if cfg.Research.Backend == "" {
		cfg.Research.Backend = "auto"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// PresetWebSearch returns the web search mode of a well-known provider name.
func PresetWebSearch(name string) string {
	if p, ok := Default().Providers[name]; ok {
		return p.WebSearch
	}
	return ""
}

// ResearchRole returns the role research runs with: its own provider and
// model, or the chat role when they are not set.
func (c *Config) ResearchRole() Role {
	r := c.Chat
	if c.Research.Provider != "" {
		r.Provider = c.Research.Provider
		r.Model = c.Research.Model
	} else if c.Research.Model != "" {
		r.Model = c.Research.Model
	}
	if c.Research.Timeout.Duration > 0 {
		r.Timeout = c.Research.Timeout
	}
	return r
}

// ResearchBackend resolves "auto" to the backend that can actually run:
// brave with a key, searxng with a URL, provider when the research provider
// has a web search of its own; "" when nothing can search.
func (c *Config) ResearchBackend() string {
	b := c.Research.Backend
	prov, ok := c.Providers[c.ResearchRole().Provider]
	providerOK := ok && prov.Usable() && prov.WebSearchMode() != "none"
	switch b {
	case "brave":
		if c.Research.BraveKey() != "" {
			return b
		}
		return ""
	case "searxng":
		if c.Research.SearxngURL != "" {
			return b
		}
		return ""
	case "provider":
		if providerOK {
			return b
		}
		return ""
	}
	switch {
	case c.Research.BraveKey() != "":
		return "brave"
	case c.Research.SearxngURL != "":
		return "searxng"
	case providerOK:
		return "provider"
	}
	return ""
}

// EmbeddingAvailable reports whether semantic search can run.
func (c *Config) EmbeddingAvailable() bool {
	p, ok := c.Providers[c.Embedding.Provider]
	return ok && p.Usable() && p.Type == "openai" && c.Embedding.Model != ""
}

// ResearchAvailable reports whether research can run: a usable model and a
// search backend.
func (c *Config) ResearchAvailable() bool {
	role := c.ResearchRole()
	p, ok := c.Providers[role.Provider]
	return ok && p.Usable() && p.Type != "fake" && role.Model != "" && c.ResearchBackend() != ""
}

// PresetTranscription returns the transcription mode of a well-known provider
// name, and "" (meaning "audio") for names it does not know.
func PresetTranscription(name string) string {
	if p, ok := Default().Providers[name]; ok {
		return p.Transcription
	}
	return ""
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("FUNDUS_DATA_DIR"); v != "" {
		cfg.DataDir = v
		cfg.overridden["data_dir"] = true
	}
	if v := os.Getenv("FUNDUS_LISTEN"); v != "" {
		cfg.Listen = v
		cfg.overridden["listen"] = true
	}
	if v := os.Getenv("FUNDUS_TOKEN"); v != "" {
		cfg.Token = v
		cfg.overridden["token"] = true
	}
}

// Override records that a field was set by a flag or the environment for
// this process only; Save keeps the file's own value for it. Keys:
// data_dir, listen, token, providers (for --fake).
func (c *Config) Override(key string) {
	if c.overridden == nil {
		c.overridden = map[string]bool{}
	}
	c.overridden[key] = true
}

// Overridden reports whether key is overridden for this process.
func (c *Config) Overridden(key string) bool { return c.overridden[key] }

// IsLoopbackListen reports whether addr can only be reached from this machine.
func IsLoopbackListen(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Validate checks referential integrity between roles and providers.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return errors.New("config: data_dir is empty")
	}
	if c.Dictation.Provider != "" {
		if _, ok := c.Providers[c.Dictation.Provider]; !ok {
			return fmt.Errorf("config: dictation.provider %q is not defined under [providers]", c.Dictation.Provider)
		}
	}
	if c.Research.Provider != "" {
		if _, ok := c.Providers[c.Research.Provider]; !ok {
			return fmt.Errorf("config: research.provider %q is not defined under [providers]", c.Research.Provider)
		}
	}
	switch c.Research.Backend {
	case "", "auto", "brave", "searxng", "provider":
	default:
		return fmt.Errorf("config: research.backend %q (auto|brave|searxng|provider)", c.Research.Backend)
	}
	if c.Embedding.Provider != "" {
		if _, ok := c.Providers[c.Embedding.Provider]; !ok {
			return fmt.Errorf("config: embedding.provider %q is not defined under [providers]", c.Embedding.Provider)
		}
	}
	switch c.Maintenance.Assist {
	case "", "off", "propose", "auto":
	default:
		return fmt.Errorf("config: maintenance.assist %q (off|propose|auto)", c.Maintenance.Assist)
	}
	if c.Maintenance.At != "" {
		if _, err := time.Parse("15:04", c.Maintenance.At); err != nil {
			return fmt.Errorf("config: maintenance.at %q is not HH:MM", c.Maintenance.At)
		}
	}
	for name, r := range map[string]Role{"triage": c.Triage, "chat": c.Chat} {
		if r.Provider == "" {
			return fmt.Errorf("config: %s.provider is empty", name)
		}
		p, ok := c.Providers[r.Provider]
		if !ok {
			return fmt.Errorf("config: %s.provider %q is not defined under [providers]", name, r.Provider)
		}
		if p.Type != "fake" && r.Model == "" {
			return fmt.Errorf("config: %s.model is empty", name)
		}
	}
	for name, p := range c.Providers {
		switch p.Type {
		case "openai":
			if p.BaseURL == "" {
				return fmt.Errorf("config: providers.%s.base_url is empty", name)
			}
		case "fake":
		default:
			return fmt.Errorf("config: providers.%s.type %q unknown (openai|fake)", name, p.Type)
		}
	}
	if c.Autonomy.MinConfidence < 0 || c.Autonomy.MinConfidence > 1 {
		return errors.New("config: autonomy.min_confidence must be within 0..1")
	}
	return nil
}

// ResolveAPIKey returns the API key for a provider from its config or env.
func (p Provider) ResolveAPIKey() string {
	if p.APIKey != "" {
		return p.APIKey
	}
	if p.APIKeyEnv != "" {
		return os.Getenv(p.APIKeyEnv)
	}
	return ""
}

// WriteDefault writes a commented default config to path if it does not exist.
func WriteDefault(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultTOML), 0o600)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

const defaultTOML = `# Fundus daemon configuration.
# Every key is optional; the values below are the defaults.

# data_dir = "~/.local/share/fundus"
listen = "127.0.0.1:7433"
# IANA time zone for due dates and "today" (default: system zone; set in containers).
# timezone = "Europe/Berlin"

# Bearer token required for non-loopback clients (set one before exposing the
# daemon on a network). Generate with: fundus token new
# token = ""
# Require the token on 127.0.0.1 too (multi-user machines).
# require_token_on_loopback = false
# Extra Host header values to accept when reached through a name or proxy.
# allowed_hosts = ["fundus.lan"]

[triage]
provider = "openai"
model = "gpt-5.6-luna"
max_attempts = 3
timeout = "90s"

[chat]
provider = "openai"
model = "gpt-5.6-terra"
timeout = "180s"

# Speech to text for the microphone button. An empty model switches it off.
[dictation]
provider = "openai"
model = "gpt-4o-mini-transcribe"

# Semantic search: vectors from an OpenAI-compatible /embeddings endpoint,
# cached under <data_dir>/embeddings, never in the event log. An empty model
# keeps search lexical. Ollama: "nomic-embed-text"; Gemini: "gemini-embedding-001".
[embedding]
provider = "openai"
model = "text-embedding-3-small"

# Background curation (concept Phase 2). Runs daily at "at" (local time) or
# every "every"; "fundus maintain" and POST /v1/maintenance/run start one now.
# assist: off | propose | auto — let the model help with open tasks (drafts
# from your own notes, research). "propose" puts its work in the inbox for
# you to accept; "auto" writes notes linked to the task and starts research.
[maintenance]
enabled = true
at = "03:30"
integrity = true
untagged = true
duplicates = true
summaries = true
assist = "off"
untagged_after_days = 1
keep_runs = 30

# Web research for "Research:" tasks (ADR-0010: the reader has no write
# tools; findings are stored marked as external, with sources). provider and
# model default to [chat]. backend: auto | brave | searxng | provider.
# "auto" takes Brave when a key is set, SearXNG when a URL is set, otherwise
# the provider's own search (OpenAI: search_model; OpenRouter: web plugin).
[research]
backend = "auto"
search_model = "gpt-5-search-api"
brave_api_key_env = "BRAVE_API_KEY"
# searxng_url = "http://127.0.0.1:8080"
max_searches = 3
max_pages = 4
timeout = "4m"
# Research starts by itself when a capture asks for it. Set manual = true to
# start it with "Research this" instead.
manual = false

[autonomy]
# Below this confidence a capture is parked in the inbox instead of written.
min_confidence = 0.6
# Let triage create notes, ideas and tasks without confirmation (receipt + undo).
# With false, every capture becomes a proposal in the inbox to accept or dismiss.
auto_create = true
max_ops_per_capture = 12
max_new_topics_per_capture = 2

# transcription: how dictation reaches the provider. "audio" = the
# /audio/transcriptions endpoint, "chat" = the recording goes into a chat
# completion as input_audio, "none" = no dictation with this provider.
[providers.openai]
type = "openai"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
transcription = "audio"
web_search = "chat_completions"

[providers.gemini]
type = "openai"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai"
api_key_env = "GEMINI_API_KEY"
transcription = "chat"

[providers.openrouter]
type = "openai"
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"
transcription = "chat"
web_search = "openrouter"

[providers.anthropic]
type = "openai"
base_url = "https://api.anthropic.com/v1"
api_key_env = "ANTHROPIC_API_KEY"
transcription = "none"

# Local or remote Ollama: point base_url at the machine that runs it.
[providers.ollama]
type = "openai"
base_url = "http://127.0.0.1:11434/v1"
transcription = "none"

# A model-free provider using simple heuristics. Useful for trying Fundus
# without any API key: set triage.provider = "fake".
[providers.fake]
type = "fake"
`

// Location resolves the configured time zone (falls back to the system zone).
func (c *Config) Location() (*time.Location, error) {
	if c.Timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(c.Timezone)
}

// Save writes the configuration to c.Path (or the default path) with mode
// 0600, creating the directory. Secrets stay in this file; there is no
// separate secret store, which keeps "one file, one process" true.
func (c *Config) Save() error {
	path := c.Path
	if path == "" {
		path = DefaultPath()
		c.Path = path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Restore fields that are overridden for this process only.
	out := c.Clone()
	if c.file != nil {
		if c.overridden["data_dir"] {
			out.DataDir = c.file.DataDir
		}
		if c.overridden["listen"] {
			out.Listen = c.file.Listen
		}
		if c.overridden["token"] {
			out.Token = c.file.Token
		}
		if c.overridden["providers"] {
			out.Triage.Provider, out.Triage.Model = c.file.Triage.Provider, c.file.Triage.Model
			out.Chat.Provider, out.Chat.Model = c.file.Chat.Provider, c.file.Chat.Model
		}
	}
	var buf strings.Builder
	buf.WriteString("# Fundus configuration. Edited by Fundus when settings change in the UI.\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(out); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.WriteString(buf.String()); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Usable reports whether a provider can be called: fake providers always,
// local endpoints without a key, remote endpoints only with a key.
func (p Provider) Usable() bool {
	switch p.Type {
	case "fake":
		return true
	case "openai":
		if p.ResolveAPIKey() != "" {
			return true
		}
		// A provider that names no key source needs none (Ollama, also on
		// another machine); one that does is unusable until the key is there.
		return p.Local() || p.APIKeyEnv == ""
	}
	return false
}

// Local reports whether the provider's endpoint is on this machine.
func (p Provider) Local() bool {
	u := strings.ToLower(p.BaseURL)
	return strings.Contains(u, "://127.0.0.1") || strings.Contains(u, "://localhost") || strings.Contains(u, "://[::1]")
}

// DictationAvailable reports whether recorded speech can be transcribed with
// the current configuration.
func (c *Config) DictationAvailable() bool {
	if c.Dictation.Provider == "" || c.Dictation.Model == "" {
		return false
	}
	p, ok := c.Providers[c.Dictation.Provider]
	return ok && p.Usable() && p.TranscriptionMode() != "none"
}

// SetupNeeded is true when the triage provider cannot be used yet.
func (c *Config) SetupNeeded() bool {
	p, ok := c.Providers[c.Triage.Provider]
	return !ok || !p.Usable()
}

// Clone returns a deep copy (the file layer and override set are shared).
func (c *Config) Clone() *Config {
	cp := *c
	cp.Providers = make(map[string]Provider, len(c.Providers))
	for k, v := range c.Providers {
		if v.Headers != nil {
			h := make(map[string]string, len(v.Headers))
			for hk, hv := range v.Headers {
				h[hk] = hv
			}
			v.Headers = h
		}
		cp.Providers[k] = v
	}
	cp.AllowedHosts = append([]string(nil), c.AllowedHosts...)
	if c.Triage.Temperature != nil {
		t := *c.Triage.Temperature
		cp.Triage.Temperature = &t
	}
	if c.Chat.Temperature != nil {
		t := *c.Chat.Temperature
		cp.Chat.Temperature = &t
	}
	return &cp
}
