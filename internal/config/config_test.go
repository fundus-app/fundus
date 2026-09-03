package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveKeepsFileValuesForOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("listen = \"127.0.0.1:7433\"\ntoken = \"file-token\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUNDUS_LISTEN", "127.0.0.1:7499")
	t.Setenv("FUNDUS_TOKEN", "env-token")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7499" || cfg.Token != "env-token" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	cfg.DataDir = "/tmp/x"
	cfg.Override("data_dir")
	cfg.Triage.Provider, cfg.Triage.Model = "fake", "heuristic"
	cfg.Override("providers")
	key := "test-key-1"
	p := cfg.Providers["openai"]
	p.APIKey = key
	cfg.Providers["openai"] = p
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	for _, want := range []string{`listen = "127.0.0.1:7433"`, `token = "file-token"`, `api_key = "test-key-1"`, `provider = "openai"`} {
		if !strings.Contains(text, want) {
			t.Errorf("saved file lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"7499", "env-token", "/tmp/x", `provider = "fake"`} {
		if strings.Contains(text, bad) {
			t.Errorf("saved file leaked override %q", bad)
		}
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	// Reload without env: the file values win, the key persists.
	os.Unsetenv("FUNDUS_LISTEN")
	os.Unsetenv("FUNDUS_TOKEN")
	again, err := Load(path)
	if err != nil || again.Listen != "127.0.0.1:7433" || again.Token != "file-token" || again.Providers["openai"].APIKey != "test-key-1" {
		t.Fatalf("reload: %v %+v", err, again)
	}
}

func TestIsLoopbackListen(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:7433": true, "localhost:1": true, "[::1]:7433": true, ":7433": false, "0.0.0.0:7433": false, "192.168.1.5:7433": false, "nonsense": false} {
		if got := IsLoopbackListen(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}
