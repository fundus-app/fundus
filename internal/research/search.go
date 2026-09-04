// Package research answers "Research:" tasks from the web (concept §9,
// ADR-0010). A reader without write tools searches and reads pages and
// returns marked findings; the curator stores them as sources and one note
// whose external claims stay recognisable as external.
package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fundus-app/fundus/internal/config"
	"github.com/fundus-app/fundus/internal/llm"
)

// Result is one web search hit.
type Result = llm.SearchResult

// Searcher finds pages for a query.
type Searcher interface {
	Search(ctx context.Context, query string, n int) ([]Result, error)
	Name() string
}

// NewSearcher builds the backend the configuration resolves to, or nil when
// none can search. provider is the research provider (for its own search).
func NewSearcher(cfg *config.Config, provider llm.Provider, client *http.Client) Searcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	switch cfg.ResearchBackend() {
	case "brave":
		return &brave{key: cfg.Research.BraveKey(), client: client}
	case "searxng":
		return &searxng{base: strings.TrimRight(cfg.Research.SearxngURL, "/"), client: client}
	case "provider":
		ws, ok := provider.(llm.WebSearcher)
		if !ok {
			return nil
		}
		model := cfg.ResearchRole().Model
		if pc := cfg.Providers[cfg.ResearchRole().Provider]; pc.WebSearchMode() == "chat_completions" && cfg.Research.SearchModel != "" {
			model = cfg.Research.SearchModel
		}
		return &providerSearch{ws: ws, model: model, name: provider.Name()}
	}
	return nil
}

type brave struct {
	key    string
	client *http.Client
}

func (b *brave) Name() string { return "brave" }

func (b *brave) Search(ctx context.Context, query string, n int) ([]Result, error) {
	q := url.Values{"q": {query}, "count": {fmt.Sprint(clamp(n, 1, 20))}, "text_decorations": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.search.brave.com/res/v1/web/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.key)
	var body struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := getJSON(b.client, req, &body); err != nil {
		return nil, fmt.Errorf("brave: %w", err)
	}
	out := make([]Result, 0, len(body.Web.Results))
	for _, r := range body.Web.Results {
		out = append(out, Result{URL: r.URL, Title: r.Title, Snippet: r.Description})
	}
	return out, nil
}

type searxng struct {
	base   string
	client *http.Client
}

func (s *searxng) Name() string { return "searxng" }

func (s *searxng) Search(ctx context.Context, query string, n int) ([]Result, error) {
	q := url.Values{"q": {query}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	var body struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := getJSON(s.client, req, &body); err != nil {
		return nil, fmt.Errorf("searxng: %w", err)
	}
	out := make([]Result, 0, n)
	for _, r := range body.Results {
		out = append(out, Result{URL: r.URL, Title: r.Title, Snippet: r.Content})
		if len(out) >= n {
			break
		}
	}
	return out, nil
}

type providerSearch struct {
	ws    llm.WebSearcher
	model string
	name  string
}

func (p *providerSearch) Name() string { return p.name }

func (p *providerSearch) Search(ctx context.Context, query string, n int) ([]Result, error) {
	return p.ws.SearchWeb(ctx, p.model, query, n)
}

func getJSON(client *http.Client, req *http.Request, v any) error {
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return errors.New("the search key was rejected")
		}
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, msg)
	}
	return json.Unmarshal(raw, v)
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
