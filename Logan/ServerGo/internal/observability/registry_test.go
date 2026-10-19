package observability

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsBounded(t *testing.T) {
	r := New()
	r.Maintenance("secret-key", "secret-key")
	r.ObserveJob("native", "success", time.Second)
	r.SetMaintenanceSnapshot(1, 2, 3, 4)
	r.SetDBStats(sql.DBStats{OpenConnections: 7, InUse: 3, WaitCount: 5})
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	s := w.Body.String()
	for _, want := range []string{"logan_jobs_duration_seconds_count", "logan_db_connections{state=\"open\"} 7", "logan_maintenance_tasks{state=\"pending\"} 1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "secret-key") {
		t.Fatal("unbounded label")
	}
}

func TestHTTPTemplateAndSanitizedLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	r := New()
	h := server.New()
	h.Use(r.HTTPMiddleware())
	h.GET("/items/:id", func(_ context.Context, c *app.RequestContext) { c.Status(201) })
	h.GET("/metrics", func(_ context.Context, c *app.RequestContext) { c.Status(200) })
	for _, path := range []string{"/items/secret-id?signature=secret-token", "/items/second", "/unknown-secret", "/metrics"} {
		ut.PerformRequest(h.Engine, "GET", path, nil)
	}
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	s := w.Body.String()
	if !strings.Contains(s, `route="/items/:id",status="201"} 2`) {
		t.Fatal(s)
	}
	if !strings.Contains(s, `route="__unmatched__"`) {
		t.Fatal(s)
	}
	if strings.Contains(s, `route="/metrics"`) {
		t.Fatal("self observation")
	}
	for _, bad := range []string{"secret-id", "secret-token", "unknown-secret"} {
		if strings.Contains(s+logs.String(), bad) {
			t.Fatal("secret leaked", bad)
		}
	}
}
func TestPanicBalancesInflight(t *testing.T) {
	r := New()
	h := server.New()
	h.Use(r.HTTPMiddleware())
	h.GET("/panic", func(_ context.Context, c *app.RequestContext) { panic("private") })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		ut.PerformRequest(h.Engine, "GET", "/panic", nil)
	}()
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	s := w.Body.String()
	if !strings.Contains(s, "logan_http_inflight 0") || !strings.Contains(s, `status="500"} 1`) {
		t.Fatal(s)
	}
}

func TestJobStateMetricsBoundedAndReset(t *testing.T) {
	r := New()
	r.SetJobStates("native", 5, 4, 3)
	r.SetJobStates("secret-kind", 1, 2, 3)
	r.SetJobStates("native", 0, 0, 0)
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	s := w.Body.String()
	for _, want := range []string{`logan_processing_jobs{kind="native",state="pending"} 0`, `logan_processing_jobs{kind="native",state="running"} 0`, `logan_processing_jobs{kind="other",state="failed"} 3`} {
		if !strings.Contains(s, want) {
			t.Fatal(s)
		}
	}
	if strings.Contains(s, "secret-kind") {
		t.Fatal("kind cardinality unbounded")
	}
}
