package workbench

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthReadiness(t *testing.T) {
	s, err := Open(Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, state := range []string{"healthy", "unhealthy", "closed"} {
		s.mu.Lock()
		s.unhealthy = nil
		s.closed = false
		if state == "unhealthy" {
			s.unhealthy = errors.New("private-token-secret")
		}
		if state == "closed" {
			s.closed = true
		}
		s.mu.Unlock()
		for _, path := range []string{"/healthz", "/readyz"} {
			w := httptest.NewRecorder()
			s.HealthHTTP(w, httptest.NewRequest("GET", path, nil))
			want := 200
			if path == "/readyz" && state != "healthy" {
				want = 503
			}
			if w.Code != want || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("%s %s: %d %s", state, path, w.Code, w.Body.String())
			}
		}
	}
	s.closed = false
}
