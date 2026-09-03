package main

// End-to-end tests: build the real binary, run `fundus serve --fake` on a free
// port with temporary config and data, and drive it through the CLI and HTTP
// exactly as a user would.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type e2e struct {
	t      *testing.T
	bin    string
	url    string
	env    []string
	daemon *exec.Cmd
	home   string
}

func startE2E(t *testing.T, extra ...string) *e2e {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e skipped in -short mode")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "fundus")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	home := filepath.Join(root, "home")
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"FUNDUS_LISTEN="+addr,
		"FUNDUS_OPEN=0",
		"FUNDUS_URL=http://"+addr,
		"OPENAI_API_KEY=", "ANTHROPIC_API_KEY=", "OPENROUTER_API_KEY=",
	)
	args := append([]string{"serve", "--fake"}, extra...)
	d := exec.Command(bin, args...)
	d.Env = env
	var logBuf bytes.Buffer
	d.Stdout, d.Stderr = &logBuf, &logBuf
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	e := &e2e{t: t, bin: bin, url: "http://" + addr, env: env, daemon: d, home: home}
	t.Cleanup(func() {
		_ = d.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { d.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = d.Process.Kill()
		}
		if t.Failed() {
			t.Logf("daemon log:\n%s", logBuf.String())
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(e.url + "/v1/health"); err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return e
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon did not come up:\n%s", logBuf.String())
	return nil
}

func (e *e2e) run(args ...string) (string, error) {
	cmd := exec.Command(e.bin, args...)
	cmd.Env = e.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *e2e) mustRun(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("fundus %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (e *e2e) api(method, path string, body any) (int, []byte) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

func TestE2ECaptureLifecycle(t *testing.T) {
	e := startE2E(t)

	out := e.mustRun("capture", "--wait", "I must call the dentist tomorrow")
	if !strings.Contains(out, "processed") || !strings.Contains(out, "Created task") {
		t.Fatalf("capture --wait: %s", out)
	}
	if !strings.Contains(e.mustRun("open"), "dentist") {
		t.Fatal("task not listed in open")
	}
	// Flags anywhere: `undo TXN --force` style parsing.
	changes := e.mustRun("changes", "--limit", "5")
	var txn string
	for _, line := range strings.Split(changes, "\n") {
		if strings.Contains(line, "Created task") {
			for _, f := range strings.Fields(line) {
				if strings.HasPrefix(f, "txn_") {
					txn = f
				}
			}
		}
	}
	if txn == "" {
		t.Fatalf("no txn in changes:\n%s", changes)
	}
	if out := e.mustRun("undo", txn); !strings.Contains(out, "Undid") {
		t.Fatalf("undo: %s", out)
	}
	if strings.Contains(e.mustRun("open"), "dentist") {
		t.Fatal("task still open after undo")
	}
	if inbox := e.mustRun("inbox"); !strings.Contains(inbox, "needs_review") {
		t.Fatalf("capture should be back in the inbox after undo:\n%s", inbox)
	}
	// Second undo of the same transaction is refused.
	if out, err := e.run("undo", txn); err == nil || !strings.Contains(out, "already") {
		t.Fatalf("second undo should fail: %v %s", err, out)
	}
	// Ideas, notes, topics, search, show, export, backup, verify.
	e.mustRun("capture", "--wait", "Maybe a small e-ink display for the office would be nice")
	if !strings.Contains(e.mustRun("ideas"), "e-ink") {
		t.Fatal("idea missing")
	}
	if !strings.Contains(e.mustRun("search", "office"), "note_") {
		t.Fatal("search found nothing")
	}
	exportPath := filepath.Join(t.TempDir(), "kb.zip")
	e.mustRun("export", "--format", "markdown", "--out", exportPath)
	if st, err := os.Stat(exportPath); err != nil || st.Size() < 200 {
		t.Fatalf("export: %v", err)
	}
	backupPath := filepath.Join(t.TempDir(), "b.zip")
	e.mustRun("backup", "--out", backupPath)
	if st, err := os.Stat(backupPath); err != nil || st.Size() < 200 {
		t.Fatalf("backup: %v", err)
	}
	if out := e.mustRun("stats"); !strings.Contains(out, `"captures": 2`) {
		t.Fatalf("stats: %s", out)
	}
}

func TestE2ESetupAndRestart(t *testing.T) {
	// Start WITHOUT --fake: no model → setup needed, captures wait.
	e := startE2E(t)
	_ = e.daemon.Process.Signal(os.Interrupt)
	e.daemon.Wait()
	d := exec.Command(e.bin, "serve")
	d.Env = e.env
	var logBuf bytes.Buffer
	d.Stdout, d.Stderr = &logBuf, &logBuf
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Process.Signal(os.Interrupt); d.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(e.url + "/v1/health"); err == nil {
			res.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	code, raw := e.api("GET", "/v1/health", nil)
	if code != 200 || !strings.Contains(string(raw), `"setup_needed":true`) {
		t.Fatalf("health: %d %s", code, raw)
	}
	out := e.mustRun("capture", "Waits for a model")
	if !strings.Contains(out, "captured cap_") {
		t.Fatalf("capture: %s", out)
	}
	time.Sleep(300 * time.Millisecond)
	if !strings.Contains(e.mustRun("inbox"), "pending") {
		t.Fatal("capture should wait as pending without a model")
	}
	// Switch to the rules provider through the settings API; the waiting capture gets filed.
	code, raw = e.api("PUT", "/v1/settings", map[string]any{"triage": map[string]string{"provider": "fake"}, "chat": map[string]string{"provider": "fake"}})
	if code != 200 || !strings.Contains(string(raw), `"setup_needed":false`) {
		t.Fatalf("settings: %d %s", code, raw)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(e.mustRun("notes"), "Waits for a model") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(e.mustRun("notes"), "Waits for a model") {
		t.Fatal("waiting capture was not filed after setup")
	}
	cfg, err := os.ReadFile(filepath.Join(e.home, ".config", "fundus", "config.toml"))
	if err != nil || !strings.Contains(string(cfg), `provider = "fake"`) {
		t.Fatalf("config not saved: %v %s", err, cfg)
	}
	if strings.Contains(string(cfg), e.url[len("http://"):]) {
		t.Fatal("FUNDUS_LISTEN override must not be persisted")
	}
	// Restart: state survives, verify is consistent, log file exists (no tty).
	_ = d.Process.Signal(os.Interrupt)
	d.Wait()
	if out := e.mustRun("verify"); !strings.Contains(out, "consistent") {
		t.Fatalf("verify: %s", out)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".local", "state", "fundus", "fundus.log")); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	d2 := exec.Command(e.bin, "serve")
	d2.Env = e.env
	if err := d2.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d2.Process.Signal(os.Interrupt); d2.Wait() }()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := http.Get(e.url + "/v1/health"); err == nil {
			res.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out := e.mustRun("notes"); !strings.Contains(out, "Waits for a model") {
		t.Fatalf("state lost after restart: %s", out)
	}
	// The no-argument start detects the running daemon instead of binding again.
	if out := e.mustRun(); !strings.Contains(out, "already running") {
		t.Fatalf("second start: %s", out)
	}
	_ = fmt.Sprint
}
