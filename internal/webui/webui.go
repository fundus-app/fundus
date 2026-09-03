// Package webui serves the embedded Flutter web build.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:web
var files embed.FS

// Handler serves the UI with an SPA fallback to index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err)
	}
	fsrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := sub.Open(p); err == nil {
			f.Close()
			if strings.HasSuffix(p, ".html") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fsrv.ServeHTTP(w, r)
			return
		}
		// SPA fallback (or placeholder when no UI has been built in).
		if f, err := sub.Open("index.html"); err == nil {
			f.Close()
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			fsrv.ServeHTTP(w, r2)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(placeholder))
	})
}

// Built reports whether a UI build is embedded.
func Built() bool {
	f, err := files.Open("web/index.html")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

const placeholder = `<!doctype html>
<meta charset="utf-8">
<title>Fundus</title>
<style>body{font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;line-height:1.5;color:#222}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>Fundus daemon is running</h1>
<p>The web UI is not built into this binary. Build it with <code>make ui</code> and rebuild, or use the desktop app or the CLI.</p>
<p>API: <a href="/v1/health">/v1/health</a></p>
`
