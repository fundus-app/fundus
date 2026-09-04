package research

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

// Page is a fetched, reduced web page.
type Page struct {
	URL       string
	FinalURL  string
	Title     string
	Text      string
	FetchedAt time.Time
	Truncated bool
}

// Fetcher downloads pages with the hardening ADR-0010 asks for: only
// http(s), no private or loopback destinations (checked per hop), bounded
// size and time, and a readability-style reduction to text.
type Fetcher struct {
	Client    *http.Client
	MaxBytes  int64
	MaxChars  int
	UserAgent string
	now       func() time.Time
}

// ErrBlocked marks destinations the fetcher refuses.
var ErrBlocked = errors.New("blocked destination")

// NewFetcher returns a fetcher with the default limits.
func NewFetcher(userAgent string) *Fetcher {
	f := &Fetcher{MaxBytes: 2 << 20, MaxChars: 12000, UserAgent: userAgent, now: time.Now}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: nil, // never through a proxy: the address check must see the real host
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				if !publicIP(ip.IP) {
					last = fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip.IP)
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("%w: %s has no address", ErrBlocked, host)
			}
			return nil, last
		},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	f.Client = &http.Client{
		Timeout:   25 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect to %s", ErrBlocked, req.URL.Scheme)
			}
			return nil
		},
	}
	return f
}

// publicIP reports whether ip is a routable public address.
func publicIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
		// Carrier-grade NAT (100.64.0.0/10) and the benchmarking range.
		if ip4[0] == 100 && ip4[1]&0xc0 == 64 || ip4[0] == 198 && ip4[1]&0xfe == 18 {
			return false
		}
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast())
}

// Fetch downloads one page. Only http(s) URLs with a host are accepted.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: only http(s) URLs can be read", ErrBlocked)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in URLs are not allowed", ErrBlocked)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,application/json;q=0.5,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en, de;q=0.8, *;q=0.5")
	res, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	raw, err := io.ReadAll(io.LimitReader(res.Body, f.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	truncated := int64(len(raw)) > f.MaxBytes
	if truncated {
		raw = raw[:f.MaxBytes]
	}
	page := &Page{URL: rawURL, FinalURL: res.Request.URL.String(), FetchedAt: f.now(), Truncated: truncated}
	switch {
	case ct == "text/html" || ct == "application/xhtml+xml" || (ct == "" && looksLikeHTML(raw)):
		page.Title, page.Text = Extract(string(raw))
	case strings.HasPrefix(ct, "text/") || ct == "application/json" || ct == "application/xml":
		page.Text = squeeze(string(raw))
	default:
		return nil, fmt.Errorf("unsupported content type %q", ct)
	}
	if n := len([]rune(page.Text)); n > f.MaxChars {
		page.Text = string([]rune(page.Text)[:f.MaxChars])
		page.Truncated = true
	}
	if strings.TrimSpace(page.Text) == "" {
		return nil, errors.New("the page has no readable text")
	}
	return page, nil
}

func looksLikeHTML(b []byte) bool {
	head := strings.ToLower(string(b[:min(len(b), 512)]))
	return strings.Contains(head, "<html") || strings.Contains(head, "<!doctype html")
}

// Extract reduces HTML to its title and main text: scripts, styles,
// navigation and boilerplate elements are dropped, block elements become
// lines, and an <article> or <main> subtree wins when it carries the bulk.
func Extract(src string) (title, text string) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", squeeze(src)
	}
	var article, main *html.Node
	var walkFind func(*html.Node)
	walkFind = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if title == "" && n.FirstChild != nil {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "article":
				if article == nil {
					article = n
				}
			case "main":
				if main == nil {
					main = n
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkFind(c)
		}
	}
	walkFind(doc)
	body := doc
	for _, cand := range []*html.Node{article, main} {
		if cand != nil && len(collect(cand)) >= 500 {
			body = cand
			break
		}
	}
	return title, squeeze(collect(body))
}

var skipTags = map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "nav": true, "header": true,
	"footer": true, "aside": true, "form": true, "iframe": true, "template": true, "button": true}
var blockTags = map[string]bool{"p": true, "div": true, "section": true, "article": true, "main": true, "li": true, "ul": true,
	"ol": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "tr": true, "td": true, "th": true,
	"br": true, "blockquote": true, "pre": true, "table": true, "dd": true, "dt": true, "figcaption": true}

func collect(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			sb.WriteString(n.Data)
		case html.ElementNode:
			if skipTags[n.Data] {
				return
			}
			if blockTags[n.Data] {
				sb.WriteByte('\n')
			}
		case html.CommentNode:
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] {
			sb.WriteByte('\n')
		}
	}
	walk(n)
	return sb.String()
}

// squeeze collapses whitespace: runs of blanks become one space, more than
// one blank line becomes one.
func squeeze(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.FieldsFunc(line, unicode.IsSpace), " ")
		if line == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
