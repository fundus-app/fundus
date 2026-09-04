package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/config"
)

type httpClient struct {
	base  string
	token string
	http  *http.Client
}

func newClient() (*httpClient, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, err
	}
	base := os.Getenv("FUNDUS_URL")
	if base == "" {
		base = "http://" + cfg.Listen
	}
	tok := os.Getenv("FUNDUS_TOKEN")
	if tok == "" {
		tok = cfg.Token
	}
	return &httpClient{base: strings.TrimRight(base, "/"), token: tok, http: &http.Client{Timeout: 15 * time.Minute}}, nil
}

func (c *httpClient) do(method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Fundus-Client", "cli")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%v (is the daemon running? try: fundus serve)", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("%s: %s", e.Error.Code, e.Error.Message)
		}
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func client(cmd string, args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	switch cmd {
	case "capture", "c":
		return cmdCapture(c, args)
	case "inbox":
		return cmdInbox(c)
	case "open", "relevant", "waiting", "later", "done":
		if cmd == "done" && len(args) > 0 {
			return cmdTaskState(c, args[0], "done")
		}
		if cmd == "later" && len(args) > 0 {
			return cmdTaskState(c, args[0], "later")
		}
		return cmdTasks(c, cmd)
	case "reopen":
		if len(args) == 0 {
			return fmt.Errorf("usage: fundus reopen TASK_ID")
		}
		return cmdTaskState(c, args[0], "open")
	case "ideas", "notes":
		return cmdNotes(c, cmd)
	case "topics":
		return cmdTopics(c)
	case "topic":
		if len(args) == 0 {
			return fmt.Errorf("usage: fundus topic ID")
		}
		return cmdTopic(c, args[0])
	case "show":
		if len(args) == 0 {
			return fmt.Errorf("usage: fundus show ID")
		}
		return cmdShow(c, args[0])
	case "search", "s":
		return cmdSearch(c, strings.Join(args, " "))
	case "changes":
		return cmdChanges(c, args)
	case "undo":
		return cmdUndo(c, args)
	case "retry":
		return cmdRetry(c, args)
	case "dismiss":
		if len(args) == 0 {
			return fmt.Errorf("usage: fundus dismiss CAPTURE_ID")
		}
		return c.do("POST", "/v1/captures/"+args[0]+"/dismiss", nil, nil)
	case "ask", "chat":
		return cmdAsk(c, args)
	case "research":
		return cmdResearch(c, args)
	case "maintain", "maintenance":
		return cmdMaintain(c)
	case "probe":
		return cmdProbe(c, args)
	case "export":
		return cmdExport(c, args)
	case "backup":
		return cmdBackup(c, args)
	case "accept":
		if len(args) == 0 {
			return fmt.Errorf("usage: fundus accept CAPTURE_ID")
		}
		var cap capture
		if err := c.do("POST", "/v1/captures/"+args[0]+"/accept", map[string]any{}, &cap); err != nil {
			return err
		}
		printCapture(cap)
		return nil
	case "stats":
		var v map[string]any
		if err := c.do("GET", "/v1/stats", nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	case "health":
		var v map[string]any
		if err := c.do("GET", "/v1/health", nil, &v); err != nil {
			return err
		}
		return printJSON(v)
	}
	usage()
	return fmt.Errorf("unknown command %q", cmd)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type receipt struct {
	TxnID    string `json:"txn_id"`
	Summary  string `json:"summary"`
	Actor    string `json:"actor"`
	At       time.Time
	UndoneBy string `json:"undone_by"`
}

type capture struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	Result    *struct {
		Classification string  `json:"classification"`
		Confidence     float64 `json:"confidence"`
		Summary        string  `json:"summary"`
		Question       string  `json:"question"`
		Error          string  `json:"error"`
	} `json:"result"`
	Receipts []receipt `json:"receipts"`
}

func cmdCapture(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the receipt")
	source := fs.String("source", "cli", "capture source")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(text) == "" {
		b, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("nothing to capture")
	}
	var cap capture
	path := "/v1/captures"
	if *wait {
		path += "?wait=45000"
	}
	if err := c.do("POST", path, map[string]any{"text": text, "source": *source}, &cap); err != nil {
		return err
	}
	fmt.Printf("captured %s\n", cap.ID)
	if !*wait {
		return nil
	}
	if cap.Status != "pending" && cap.Status != "processing" {
		printCapture(cap)
		return nil
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(700 * time.Millisecond)
		if err := c.do("GET", "/v1/captures/"+cap.ID, nil, &cap); err != nil {
			return err
		}
		switch cap.Status {
		case "pending", "processing":
			continue
		}
		printCapture(cap)
		return nil
	}
	return fmt.Errorf("timed out waiting for %s", cap.ID)
}

func printCapture(cap capture) {
	fmt.Printf("%s  %-12s  %s\n", cap.ID, cap.Status, oneLine(cap.Text, 70))
	if cap.Result != nil {
		if cap.Result.Error != "" {
			fmt.Printf("    error: %s\n", cap.Result.Error)
		}
		if cap.Result.Question != "" {
			fmt.Printf("    question: %s\n", cap.Result.Question)
		}
	}
	for _, r := range cap.Receipts {
		if strings.HasPrefix(r.Actor, "llm:") {
			undone := ""
			if r.UndoneBy != "" {
				undone = " (undone)"
			}
			fmt.Printf("    %s  %s%s\n", r.TxnID, r.Summary, undone)
		}
	}
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func cmdInbox(c *httpClient) error {
	var caps []capture
	if err := c.do("GET", "/v1/inbox", nil, &caps); err != nil {
		return err
	}
	if len(caps) == 0 {
		fmt.Println("inbox is empty")
		return nil
	}
	for _, cap := range caps {
		printCapture(cap)
	}
	return nil
}

type task struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	State      string   `json:"state"`
	Due        string   `json:"due"`
	Score      float64  `json:"score"`
	Reasons    []string `json:"reasons"`
	TopicNames []string `json:"topic_names"`
	Rev        int      `json:"rev"`
}

func cmdTasks(c *httpClient, view string) error {
	path := "/v1/tasks?state=" + view
	if view == "relevant" {
		path = "/v1/relevant?limit=15"
	}
	var tasks []task
	if err := c.do("GET", path, nil, &tasks); err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("nothing here")
		return nil
	}
	for _, t := range tasks {
		extra := ""
		if t.Due != "" {
			extra += " due " + t.Due
		}
		if len(t.TopicNames) > 0 {
			extra += " [" + strings.Join(t.TopicNames, ", ") + "]"
		}
		fmt.Printf("%s  %4.1f  %s%s\n", t.ID, t.Score, t.Text, extra)
		if view == "relevant" && len(t.Reasons) > 0 {
			fmt.Printf("    %s\n", strings.Join(t.Reasons, "; "))
		}
	}
	return nil
}

func cmdTaskState(c *httpClient, id, state string) error {
	var obj struct {
		Object task `json:"object"`
	}
	if err := c.do("GET", "/v1/objects/"+id, nil, &obj); err != nil {
		return err
	}
	var rec receipt
	ops := []map[string]any{{"op": "task.update", "id": id, "expected_rev": obj.Object.Rev, "state": state}}
	if err := c.do("POST", "/v1/commands", map[string]any{"ops": ops}, &rec); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", rec.TxnID, rec.Summary)
	return nil
}

func cmdNotes(c *httpClient, kind string) error {
	k := "note"
	if kind == "ideas" {
		k = "idea"
	}
	var notes []struct {
		ID         string    `json:"id"`
		Title      string    `json:"title"`
		Preview    string    `json:"preview"`
		TopicNames []string  `json:"topic_names"`
		UpdatedAt  time.Time `json:"updated_at"`
	}
	if err := c.do("GET", "/v1/notes?kind="+k, nil, &notes); err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Println("nothing here")
		return nil
	}
	for _, n := range notes {
		topics := ""
		if len(n.TopicNames) > 0 {
			topics = " [" + strings.Join(n.TopicNames, ", ") + "]"
		}
		fmt.Printf("%s  %s  %s%s\n", n.ID, n.UpdatedAt.Local().Format("2006-01-02"), n.Title, topics)
		if n.Preview != "" {
			fmt.Printf("    %s\n", oneLine(n.Preview, 100))
		}
	}
	return nil
}

func cmdTopics(c *httpClient) error {
	var topics []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		NoteCount     int    `json:"note_count"`
		OpenTaskCount int    `json:"open_task_count"`
	}
	if err := c.do("GET", "/v1/topics", nil, &topics); err != nil {
		return err
	}
	if len(topics) == 0 {
		fmt.Println("no topics yet")
		return nil
	}
	for _, t := range topics {
		fmt.Printf("%s  %-8s  %s  (%d notes, %d open tasks)\n", t.ID, t.Kind, t.Name, t.NoteCount, t.OpenTaskCount)
	}
	return nil
}

func cmdTopic(c *httpClient, id string) error {
	var page struct {
		Topic struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Aliases []string `json:"aliases"`
		} `json:"topic"`
		Notes []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Kind  string `json:"kind"`
		} `json:"notes"`
		Tasks []task `json:"tasks"`
	}
	if err := c.do("GET", "/v1/topics/"+id, nil, &page); err != nil {
		return err
	}
	fmt.Printf("# %s (%s)\n", page.Topic.Name, page.Topic.ID)
	if len(page.Topic.Aliases) > 0 {
		fmt.Printf("aliases: %s\n", strings.Join(page.Topic.Aliases, ", "))
	}
	if len(page.Notes) > 0 {
		fmt.Println("\nnotes:")
		for _, n := range page.Notes {
			fmt.Printf("  %s  %-5s %s\n", n.ID, n.Kind, n.Title)
		}
	}
	if len(page.Tasks) > 0 {
		fmt.Println("\ntasks:")
		for _, t := range page.Tasks {
			fmt.Printf("  %s  %-7s %s\n", t.ID, t.State, t.Text)
		}
	}
	return nil
}

func cmdShow(c *httpClient, id string) error {
	var resp struct {
		Object    json.RawMessage `json:"object"`
		Markdown  string          `json:"markdown"`
		Receipts  []receipt       `json:"receipts"`
		Backlinks []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"backlinks"`
		Topics []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"topics"`
	}
	if err := c.do("GET", "/v1/objects/"+id, nil, &resp); err != nil {
		return err
	}
	var obj map[string]any
	_ = json.Unmarshal(resp.Object, &obj)
	if resp.Markdown != "" {
		delete(obj, "body")
		delete(obj, "summary")
	}
	if err := printJSON(obj); err != nil {
		return err
	}
	if resp.Markdown != "" {
		fmt.Println("---")
		fmt.Println(resp.Markdown)
	}
	if len(resp.Topics) > 0 {
		fmt.Println("---\ntopics:")
		for _, t := range resp.Topics {
			fmt.Printf("  %s  %s\n", t.ID, t.Title)
		}
	}
	if len(resp.Backlinks) > 0 {
		fmt.Println("---\nbacklinks:")
		for _, b := range resp.Backlinks {
			fmt.Printf("  %s  %-6s %s\n", b.ID, b.Type, b.Title)
		}
	}
	if len(resp.Receipts) > 0 {
		fmt.Println("---\nhistory:")
		for _, r := range resp.Receipts {
			undone := ""
			if r.UndoneBy != "" {
				undone = " (undone)"
			}
			fmt.Printf("  %s  %-24s %s%s\n", r.TxnID, r.Actor, r.Summary, undone)
		}
	}
	return nil
}

func cmdSearch(c *httpClient, q string) error {
	if strings.TrimSpace(q) == "" {
		return fmt.Errorf("usage: fundus search QUERY")
	}
	var hits []struct {
		ID      string  `json:"id"`
		Type    string  `json:"type"`
		Title   string  `json:"title"`
		Score   float64 `json:"score"`
		Preview string  `json:"preview"`
	}
	if err := c.do("GET", "/v1/search?q="+urlQuery(q), nil, &hits); err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Println("no results")
		return nil
	}
	for _, h := range hits {
		fmt.Printf("%s  %-6s %5.2f  %s\n", h.ID, h.Type, h.Score, h.Title)
		if h.Preview != "" {
			fmt.Printf("    %s\n", oneLine(h.Preview, 100))
		}
	}
	return nil
}

func urlQuery(s string) string { return url.QueryEscape(s) }

// parseFlags parses flags that may appear anywhere in args, so
// "fundus capture Buy milk --wait" and "fundus undo TXN --force" both work.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return fs.Parse(append(flags, positional...))
}

func cmdChanges(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	limit := fs.Int("limit", 30, "how many")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	var recs []struct {
		receipt
		At time.Time `json:"at"`
	}
	if err := c.do("GET", fmt.Sprintf("/v1/changes?limit=%d", *limit), nil, &recs); err != nil {
		return err
	}
	for _, r := range recs {
		undone := ""
		if r.UndoneBy != "" {
			undone = " (undone)"
		}
		fmt.Printf("%s  %s  %-28s %s%s\n", r.At.Local().Format("01-02 15:04"), r.TxnID, r.Actor, oneLine(r.Summary, 90), undone)
	}
	return nil
}

func cmdUndo(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	force := fs.Bool("force", false, "undo even if objects changed since")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: fundus undo TXN_ID [--force]")
	}
	var rec receipt
	if err := c.do("POST", "/v1/changes/"+fs.Arg(0)+"/undo", map[string]any{"force": *force}, &rec); err != nil {
		return err
	}
	fmt.Printf("%s  %s\n", rec.TxnID, rec.Summary)
	return nil
}

func cmdRetry(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	answer := fs.String("answer", "", "answer to the clarification question")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: fundus retry CAPTURE_ID [--answer TEXT]")
	}
	var cap capture
	if err := c.do("POST", "/v1/captures/"+fs.Arg(0)+"/retry", map[string]any{"answer": *answer}, &cap); err != nil {
		return err
	}
	fmt.Printf("re-queued %s\n", cap.ID)
	return nil
}

func cmdAsk(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	conv := fs.String("conversation", "", "conversation id (default: new)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	text := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("usage: fundus ask TEXT")
	}
	id := *conv
	if id == "" {
		var cv struct {
			ID string `json:"id"`
		}
		if err := c.do("POST", "/v1/conversations", map[string]any{}, &cv); err != nil {
			return err
		}
		id = cv.ID
	}
	var reply struct {
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
		Receipts []receipt `json:"receipts"`
		Steps    []struct {
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
		} `json:"steps"`
	}
	if err := c.do("POST", "/v1/conversations/"+id+"/messages", map[string]any{"text": text}, &reply); err != nil {
		return err
	}
	for _, st := range reply.Steps {
		if st.Kind == "tool_call" || st.Kind == "receipt" {
			fmt.Fprintf(os.Stderr, "  · %s\n", st.Summary)
		}
	}
	fmt.Println(reply.Message.Text)
	fmt.Fprintf(os.Stderr, "\n(conversation %s)\n", id)
	return nil
}

func cmdProbe(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	role := fs.String("role", "triage", "triage|chat")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	var v map[string]any
	if err := c.do("GET", "/v1/llm/probe?role="+*role, nil, &v); err != nil {
		return err
	}
	return printJSON(v)
}

func cmdExport(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "json", "json|markdown")
	out := fs.String("out", "", "output file (default stdout)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	req, err := http.NewRequest("GET", c.base+"/v1/export?format="+*format, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, string(raw))
	}
	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	_, err = io.Copy(w, res.Body)
	return err
}

func cmdBackup(c *httpClient, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fs.String("out", "fundus-backup.zip", "output file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	req, err := http.NewRequest("GET", c.base+"/v1/backup", nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, string(raw))
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		return err
	}
	fmt.Println("wrote", *out)
	return nil
}

// cmdResearch starts research for a task id or a question and waits for the
// note, printing the daemon's progress on the way.
func cmdResearch(c *httpClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: fundus research TASK_ID | QUESTION...")
	}
	body := map[string]any{}
	if len(args) == 1 && strings.HasPrefix(args[0], "task_") {
		body["task_id"] = args[0]
	} else {
		body["question"] = strings.Join(args, " ")
	}
	var started struct {
		TaskID string `json:"task_id"`
	}
	if err := c.do("POST", "/v1/research", body, &started); err != nil {
		return err
	}
	fmt.Println("researching", started.TaskID)
	deadline := time.Now().Add(6 * time.Minute)
	lastState := ""
	for time.Now().Before(deadline) {
		var task struct {
			State string   `json:"state"`
			Notes []string `json:"notes"`
		}
		if err := c.do("GET", "/v1/objects/"+started.TaskID, nil, &task); err != nil {
			return err
		}
		if task.State != lastState {
			lastState = task.State
		}
		if task.State == "done" && len(task.Notes) > 0 {
			fmt.Println()
			return cmdShow(c, task.Notes[len(task.Notes)-1])
		}
		var status struct {
			Running []string `json:"running"`
		}
		_ = c.do("GET", "/v1/research", nil, &status)
		running := false
		for _, id := range status.Running {
			if id == started.TaskID {
				running = true
			}
		}
		if !running && task.State != "done" {
			return fmt.Errorf("research stopped without a result; see the daemon log")
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for the research note")
}

// cmdMaintain starts a maintenance run and prints its report.
func cmdMaintain(c *httpClient) error {
	var started struct {
		RunID string `json:"run_id"`
	}
	if err := c.do("POST", "/v1/maintenance/run", map[string]any{}, &started); err != nil {
		return err
	}
	fmt.Println("maintenance run", started.RunID)
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		var st struct {
			Running bool `json:"running"`
			Last    *struct {
				ID   string `json:"id"`
				Jobs []struct {
					Name     string   `json:"name"`
					Checked  int      `json:"checked"`
					Changed  int      `json:"changed"`
					Proposed int      `json:"proposed"`
					Notes    []string `json:"notes"`
					Error    string   `json:"error"`
					Skipped  string   `json:"skipped"`
				} `json:"jobs"`
			} `json:"last"`
		}
		if err := c.do("GET", "/v1/maintenance", nil, &st); err != nil {
			return err
		}
		if !st.Running && st.Last != nil && st.Last.ID == started.RunID {
			for _, j := range st.Last.Jobs {
				line := fmt.Sprintf("%-11s checked %-4d changed %-4d proposed %d", j.Name, j.Checked, j.Changed, j.Proposed)
				if j.Skipped != "" {
					line += "  (skipped: " + j.Skipped + ")"
				}
				if j.Error != "" {
					line += "  error: " + j.Error
				}
				fmt.Println(line)
				for _, n := range j.Notes {
					fmt.Println("   -", n)
				}
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for the run")
}
