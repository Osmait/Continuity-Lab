package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed dist
var assets embed.FS

func Handler() http.Handler {
	root, rootErr := fs.Sub(assets, "dist")
	var files http.Handler
	if rootErr == nil {
		files = http.FileServer(http.FS(root))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rootErr != nil {
			http.Error(w, "web interface unavailable", http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/sw.js" {
			if r.URL.Path == "/sw.js" {
				w.Header().Set("Cache-Control", "no-store, max-age=0")
				w.Header().Set("Service-Worker-Allowed", "/")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		index, readErr := fs.ReadFile(root, "index.html")
		if readErr != nil {
			http.Error(w, "web interface unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
