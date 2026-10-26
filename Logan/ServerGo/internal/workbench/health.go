package workbench

import "net/http"

// HealthHTTP reports only process liveness and instance store readiness.
func (s *Server) HealthHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	ready := !s.closed && s.unhealthy == nil
	s.mu.Unlock()
	if r.URL.Path == "/readyz" && !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unavailable\n"))
		return
	}
	w.Write([]byte("ok\n"))
}
