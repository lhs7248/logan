// Package workbenchui serves the independently embedded investigation client.
package workbenchui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var assets embed.FS

// Handler serves the client at either / or /workbench/.
func Handler() http.Handler {
	root, _ := fs.Sub(assets, "static")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		if strings.HasPrefix(u.Path, "/workbench/") {
			clone.URL.Path = strings.TrimPrefix(u.Path, "/workbench")
		}
		if u.Path == "/workbench" {
			http.Redirect(w, r, "/workbench/", http.StatusTemporaryRedirect)
			return
		}
		files.ServeHTTP(w, clone)
	})
}
