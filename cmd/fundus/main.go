// Command fundus is the Fundus daemon and its command-line client in one
// static binary. `fundus serve` runs the daemon; every other subcommand
// talks to a running daemon over HTTP.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fundus-app/fundus/internal/api"
	"github.com/fundus-app/fundus/internal/chat"
	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/core"
	"github.com/fundus-app/fundus/internal/llm"
	"github.com/fundus-app/fundus/internal/maintenance"
	"github.com/fundus-app/fundus/internal/research"
	"github.com/fundus-app/fundus/internal/triage"
)

func main() {
	// No arguments: start the daemon (or find the running one) and open the UI.
	if len(os.Args) < 2 {
		if err := run(nil); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run", "start":
		err = run(args)
	case "ui":
		err = openUI()
	case "serve":
		err = serve(args)
	case "config":
		err = configCmd(args)
	case "token":
		err = tokenCmd(args)
	case "verify":
		err = verify(args)
	case "version":
		fmt.Println("fundus", api.Version)
	case "help", "-h", "--help":
		usage()
	default:
		err = client(cmd, args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`fundus — capture anything, let your AI maintain the rest.

Just run it:
  fundus                                start the daemon and open the UI (first run: setup in the browser)
  fundus ui                             open the UI of the running daemon

Daemon:
  fundus serve [--config FILE] [--data DIR] [--listen ADDR] [--fake] [--dev-cors] [--log-level LEVEL]
  fundus config init [--path FILE]      write a default config file
  fundus token new                      print a random bearer token
  fundus verify [--data DIR]            offline: replay the whole event log and compare with the snapshot

Client (talks to the running daemon; FUNDUS_URL / FUNDUS_TOKEN override config):
  fundus capture [TEXT...] [--wait]     capture a thought (reads stdin when no text)
  fundus inbox                          captures that still need attention
  fundus open | relevant | waiting | later | done
  fundus ideas | notes | topics
  fundus topic ID
  fundus show ID
  fundus search QUERY...
  fundus done TASK_ID | later TASK_ID | reopen TASK_ID
  fundus changes [--limit N]
  fundus undo TXN_ID [--force]
  fundus retry CAPTURE_ID [--answer TEXT]
  fundus dismiss CAPTURE_ID
  fundus ask [--conversation ID] TEXT...
  fundus research TASK_ID | QUESTION... research on the web and wait for the note
  fundus maintain                       run maintenance now and print the report
  fundus probe [--role triage|chat]
  fundus export [--format json|markdown] [--out FILE]
  fundus backup [--out FILE]            consistent zip of the event log and snapshot
  fundus accept CAPTURE_ID              apply the model's parked proposal
  fundus stats | health
`)
}

// run is the one-command start: if a daemon already answers on the
// configured address, open the UI; otherwise serve in the foreground and
// open the UI once listening.
func run(args []string) error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	listen := cfg.Listen
	// Honour --listen given to run/serve when deciding what to probe and open.
	for i, a := range args {
		if a == "--listen" && i+1 < len(args) {
			listen = args[i+1]
		} else if strings.HasPrefix(a, "--listen=") {
			listen = strings.TrimPrefix(a, "--listen=")
		}
	}
	if up, _ := daemonUp(listen); up {
		if !sameInstance(listen, cfg.DataDir) {
			return fmt.Errorf("something else answers at http://%s (not this Fundus data directory); choose another port with FUNDUS_LISTEN", listen)
		}
		fmt.Printf("Fundus is already running at http://%s\n", listen)
		return openBrowser("http://" + listen + "/")
	}
	if err := portFree(listen); err != nil {
		return err
	}
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if up, _ := daemonUp(listen); up {
				fmt.Printf("\nFundus is running at http://%s  (Ctrl+C stops it)\n", listen)
				if err := openBrowser("http://" + listen + "/"); err != nil {
					fmt.Printf("Open the address above in your browser.\n")
				}
				return
			}
		}
	}()
	return serve(args)
}

// daemonUp reports whether an HTTP server answers on listen. A 401 counts:
// it means a Fundus daemon with require_token_on_loopback is there.
func daemonUp(listen string) (bool, int) {
	c := &http.Client{Timeout: 700 * time.Millisecond}
	res, err := c.Get("http://" + listen + "/v1/health")
	if err != nil {
		return false, 0
	}
	res.Body.Close()
	return res.StatusCode == 200 || res.StatusCode == 401, res.StatusCode
}

// sameInstance checks that the daemon on listen serves this data directory
// (its /v1/health instance id matches <data_dir>/instance). Unknown when the
// health endpoint needs a token; then it is trusted.
func sameInstance(listen, dataDir string) bool {
	want, err := os.ReadFile(filepath.Join(dataDir, "instance"))
	if err != nil {
		return true // no local instance file yet (first run elsewhere)
	}
	c := &http.Client{Timeout: 700 * time.Millisecond}
	res, err := c.Get("http://" + listen + "/v1/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return true
	}
	var h struct {
		Instance string `json:"instance"`
	}
	if json.NewDecoder(res.Body).Decode(&h) != nil || h.Instance == "" {
		return false
	}
	return h.Instance == strings.TrimSpace(string(want))
}

// portFree explains a port conflict in plain words instead of a bind error.
func portFree(listen string) error {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("port %s is used by another program; set another address with FUNDUS_LISTEN or listen in the config", listen)
	}
	return l.Close()
}

func openUI() error {
	cfg, err := config.Load("")
	if err != nil {
		return err
	}
	if up, _ := daemonUp(cfg.Listen); !up {
		return fmt.Errorf("Fundus is not running at http://%s; start it with: fundus", cfg.Listen)
	}
	return openBrowser("http://" + cfg.Listen + "/")
}

// openBrowser opens url with the platform opener. It is a no-op in headless
// environments (containers, SSH sessions) and when FUNDUS_OPEN=0.
func openBrowser(url string) error {
	if os.Getenv("FUNDUS_OPEN") == "0" {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Start()
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file (default ~/.config/fundus/config.toml)")
	dataDir := fs.String("data", "", "data directory override")
	listen := fs.String("listen", "", "listen address override")
	fake := fs.Bool("fake", false, "use the model-free heuristic provider for triage and chat")
	devCORS := fs.Bool("dev-cors", false, "allow cross-origin requests (flutter run -d chrome)")
	insecure := fs.Bool("insecure", false, "allow non-loopback listen without a token")
	logLevel := fs.String("log-level", "info", "debug|info|warn|error")
	logFile := fs.String("log-file", os.Getenv("FUNDUS_LOG_FILE"), "also write the log to this file (default when started without a terminal: $XDG_STATE_HOME/fundus/fundus.log)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lvl := slog.LevelInfo
	_ = lvl.UnmarshalText([]byte(strings.ToUpper(*logLevel)))
	logOut, logPath, closeLog := logDestination(*logFile)
	defer closeLog()
	lg := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(lg)
	if logPath != "" {
		lg.Info("logging to file", "path", logPath)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
		cfg.Override("data_dir")
	}
	if *listen != "" {
		cfg.Listen = *listen
		cfg.Override("listen")
	}
	if *fake {
		cfg.Triage.Provider, cfg.Triage.Model = "fake", "heuristic"
		cfg.Chat.Provider, cfg.Chat.Model = "fake", "heuristic"
		cfg.Override("providers")
	}
	if cfg.Token == "" && !isLoopbackAddr(cfg.Listen) && !*insecure {
		return fmt.Errorf("listen address %s is not loopback but no token is configured; set token in config or pass --insecure", cfg.Listen)
	}
	if *devCORS && !isLoopbackAddr(cfg.Listen) {
		return fmt.Errorf("--dev-cors is only allowed with a loopback listen address")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}

	loc, err := cfg.Location()
	if err != nil {
		return fmt.Errorf("timezone: %w", err)
	}
	c, err := core.Open(cfg.DataDir, core.Options{SnapshotEvery: 200, Logger: lg, Location: loc})
	if err != nil {
		return err
	}
	defer c.Close()
	var warnings []string

	reg, err := llm.NewRegistry(cfg, triage.NewHeuristic)
	if err != nil {
		return err
	}
	// Roles whose provider is not usable yet start without a provider; the
	// worker waits and the UI offers the setup wizard.
	pick := func(role config.Role) llm.Provider {
		if pc, ok := cfg.Providers[role.Provider]; !ok || !pc.Usable() {
			return nil
		}
		p, err := reg.Get(role.Provider)
		if err != nil {
			return nil
		}
		return p
	}
	if cfg.SetupNeeded() {
		lg.Info("no model connected yet; open the UI to set one up (captures are kept meanwhile)")
	}
	if cfg.Token != "" && !cfg.RequireTokenOnLoopback {
		lg.Info("loopback clients are exempt from the token; set require_token_on_loopback = true if a reverse proxy runs on this host")
	}
	tr := triage.New(c, pick(cfg.Triage), cfg.Triage, cfg.Autonomy, lg)
	worker := triage.NewWorker(c, tr, lg)
	ch := chat.New(c, pick(cfg.Chat), cfg.Chat, cfg.Autonomy, lg)
	rw := research.New(c, lg, api.Version)
	ch.SetResearcher(rw)
	worker.AfterProcess = rw.AutoKick
	mw := maintenance.New(c, cfg.DataDir, lg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch.MarkInterrupted(ctx)
	// The worker outlives the signal: it finishes the capture in flight
	// after the HTTP server has stopped, then is cancelled explicitly.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.Run(workerCtx)
	}()

	srv := api.New(c, cfg, worker, tr, ch, reg, lg)
	srv.SetResearch(rw)
	srv.SetMaintenance(mw)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mw.Run(workerCtx)
	}()
	srv.DevCORS = *devCORS
	srv.Warnings = warnings
	lg.Info("fundus listening", "addr", cfg.Listen, "data", cfg.DataDir, "triage", cfg.Triage.Provider+"/"+cfg.Triage.Model, "chat", cfg.Chat.Provider+"/"+cfg.Chat.Model, "timezone", loc.String(), "version", api.Version)
	serveErr := srv.ListenAndServe(ctx, cfg.Listen)
	// Stop background work before the core closes: the worker gets a grace
	// period to finish the capture in flight, then is cancelled; chat turns
	// were cancelled by the server shutdown.
	worker.Stop()
	rw.Stop()
	mw.Stop()
	srv.Close()
	select {
	case <-waitGroupDone(&wg):
	case <-time.After(20 * time.Second):
		lg.Warn("worker did not finish in time; cancelling")
		stopWorker()
		wg.Wait()
	}
	stopWorker()
	if serveErr != nil && ctx.Err() == nil {
		return serveErr
	}
	lg.Info("shutting down")
	return nil
}

// isLoopbackAddr is true only for listen addresses that cannot be reached
// from other machines. ":7433" binds every interface and is not loopback.
func isLoopbackAddr(addr string) bool { return config.IsLoopbackListen(addr) }

// logDestination returns the writer for logs. Interactive runs log to
// stderr only; runs without a terminal (desktop app, systemd, detached)
// also write to a file so problems stay visible.
func logDestination(explicit string) (io.Writer, string, func()) {
	path := explicit
	if path == "" {
		if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			base := os.Getenv("XDG_STATE_HOME")
			if base == "" {
				home, _ := os.UserHomeDir()
				base = filepath.Join(home, ".local", "state")
			}
			path = filepath.Join(base, "fundus", "fundus.log")
		}
	}
	if path == "" {
		return os.Stderr, "", func() {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return os.Stderr, "", func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return os.Stderr, "", func() {}
	}
	return io.MultiWriter(os.Stderr, f), path, func() { f.Close() }
}

func configCmd(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return fmt.Errorf("usage: fundus config init [--path FILE]")
	}
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	path := fs.String("path", "", "config path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	p := *path
	if p == "" {
		p = config.DefaultPath()
	}
	if err := config.WriteDefault(p); err != nil {
		return err
	}
	fmt.Println("wrote", p)
	return nil
}

func tokenCmd(args []string) error {
	if len(args) == 0 || args[0] != "new" {
		return fmt.Errorf("usage: fundus token new")
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(b))
	return nil
}

// verify opens a data directory offline, replays the full event log without
// the snapshot and reports what it finds. It is the tool to run after a
// crash, a restore from backup, or before an upgrade.
func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dataDir := fs.String("data", "", "data directory (default from config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := *dataDir
	if dir == "" {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		dir = cfg.DataDir
	}
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// Pass 1: as the daemon would start (snapshot + tail).
	c, err := core.Open(dir, core.Options{Logger: quiet})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	fromSnapshot := c.Stats()
	if rec := c.Recovery(); rec != nil {
		fmt.Printf("recovered: cut %d damaged bytes from %s (copy: %s)\n", rec.DroppedBytes, rec.TruncatedFile, rec.CorruptCopy)
	}
	if err := c.Close(); err != nil {
		return err
	}
	// Pass 2: full replay from seq 1, snapshot ignored, in a scratch copy.
	tmp, err := os.MkdirTemp("", "fundus-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(filepath.Join(dir, "events"), filepath.Join(tmp, "events")); err != nil {
		return err
	}
	c2, err := core.Open(tmp, core.Options{Logger: quiet})
	if err != nil {
		return fmt.Errorf("full replay failed: %w", err)
	}
	replayed := c2.Stats()
	c2.Close()
	fmt.Printf("event log:   seq %d\n", replayed.Seq)
	fmt.Printf("objects:     %d captures, %d notes, %d ideas, %d open tasks, %d topics, %d conversations\n",
		replayed.Captures, replayed.Notes, replayed.Ideas, replayed.OpenTasks, replayed.Topics, replayed.Conversations)
	if fromSnapshot != replayed {
		return fmt.Errorf("snapshot state differs from full replay: %+v vs %+v", fromSnapshot, replayed)
	}
	fmt.Println("snapshot:    consistent with full replay")
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func waitGroupDone(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	return ch
}
