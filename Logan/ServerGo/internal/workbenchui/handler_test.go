package workbenchui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedRoutes(t *testing.T) {
	for _, path := range []string{"/", "/workbench/", "/workbench/app.js", "/workbench/api.mjs", "/workbench/upload.mjs", "/workbench/drafts.mjs", "/workbench/style.css"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing content security policy")
		}
		if strings.Contains(path, "app.js") && !strings.Contains(w.Body.String(), "createAPI") {
			t.Fatal("module missing")
		}
	}
}
func TestMissingAssetDoesNotReturnHTML(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/workbench/no-such-file.js", nil))
	if w.Code != 404 {
		t.Fatalf("missing script: %d", w.Code)
	}
}
