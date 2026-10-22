package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyScopedReadPreservesDistinctReferences(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Error("write")
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("credential")
		}
		var data any
		switch r.URL.Path {
		case "/logan/task/123/info.json":
			data = map[string]any{"taskId": 123, "appId": "mobile", "platform": 1, "deviceId": "device-a", "appVersion": "1.0", "buildVersion": "10"}
		case "/logan/web/taskDetail.json":
			data = map[string]any{"taskId": 123, "deviceId": "device-a", "webSource": "https://app.example.invalid"}
		case "/logan/task/456/detail.json", "/logan/web/logDetail.json":
			data = map[string]any{"id": 456, "taskId": 123, "content": "observed", "logTime": 123}
		default:
			t.Errorf("path %s", r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data})
	}))
	defer srv.Close()
	cfg := LegacyConfig{BaseURL: srv.URL, Token: "secret", Mappings: []LegacyMapping{{ProjectID: "p", Kind: "native", TaskID: 123, AppID: "mobile", Platform: "1", DeviceID: "device-a"}, {ProjectID: "p", Kind: "web", TaskID: 123, AppID: "browser", Platform: "web", DeviceID: "device-a", WebSource: "https://app.example.invalid"}}}
	l, err := NewLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := l.List(context.Background(), "p")
	if err != nil || len(tasks) != 2 || tasks[0].Ref.Kind == tasks[1].Ref.Kind || tasks[0].Ref.TaskID != "123" {
		t.Fatalf("tasks %+v %v", tasks, err)
	}
	for _, kind := range []string{"native", "web"} {
		detail, err := l.Detail(context.Background(), "p", kind, 123, 456)
		if err != nil || detail.Ref.DetailID != "456" || detail.Content != "observed" || detail.Ref.SourceRevision == "" {
			t.Fatalf("detail %+v %v", detail, err)
		}
	}
	before := calls.Load()
	if _, err := l.List(context.Background(), "other"); !errors.Is(err, ErrUnconfigured) {
		t.Fatal(err)
	}
	if _, err := l.Detail(context.Background(), "p", "native", 999, 456); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("unscoped outbound call")
	}
}
func TestLegacyRejectsScopeMismatchAndUnsafeDetails(t *testing.T) {
	mode := "scope"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		if strings.Contains(r.URL.Path, "info.json") {
			app := "mobile"
			if mode == "scope" {
				app = "other"
			}
			data = map[string]any{"taskId": 123, "appId": app, "platform": 1, "deviceId": "device-a"}
		} else {
			data = map[string]any{"id": 456, "taskId": 999, "content": "other-tenant-secret"}
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data})
	}))
	defer srv.Close()
	l, _ := NewLegacy(LegacyConfig{BaseURL: srv.URL, Mappings: []LegacyMapping{{ProjectID: "p", Kind: "native", TaskID: 123, AppID: "mobile", Platform: "1", DeviceID: "device-a"}}})
	if tasks, err := l.List(context.Background(), "p"); !errors.Is(err, ErrUnavailable) || len(tasks) != 0 {
		t.Fatalf("leak %+v %v", tasks, err)
	}
	mode = "detail"
	if detail, err := l.Detail(context.Background(), "p", "native", 123, 456); !errors.Is(err, ErrUnavailable) || detail.Content != "" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("leak %+v %v", detail, err)
	}
	l, _ = NewLegacy(LegacyConfig{})
	if _, err := l.List(context.Background(), "p"); !errors.Is(err, ErrUnconfigured) {
		t.Fatal(err)
	}
	if _, err := NewLegacy(LegacyConfig{BaseURL: "https://user:secret@example.invalid", Mappings: []LegacyMapping{}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := NewLegacy(LegacyConfig{BaseURL: srv.URL, Mappings: []LegacyMapping{{ProjectID: "p", Kind: "web", TaskID: 123, AppID: "browser", Platform: "web", DeviceID: "device-a"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestLegacyBoundsCancellationAndSanitizedFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		limit   int64
		want    error
	}{
		{"status", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret-diagnostic", 500) }, 0, ErrUnavailable},
		{"invalid", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret-diagnostic")) }, 0, ErrInvalidResponse},
		{"limit", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 1000))) }, 32, ErrLimitExceeded},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://outside.example.invalid", http.StatusTemporaryRedirect)
		}, 0, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			l, _ := NewLegacy(LegacyConfig{BaseURL: srv.URL, MaxResponseBytes: tc.limit, Mappings: []LegacyMapping{{ProjectID: "p", Kind: "native", TaskID: 123, AppID: "mobile", Platform: "1", DeviceID: "device-a"}}})
			if tasks, err := l.List(context.Background(), "p"); !errors.Is(err, tc.want) || len(tasks) != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("failure %+v %v", tasks, err)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	l, _ := NewLegacy(LegacyConfig{BaseURL: srv.URL, Timeout: 10 * time.Millisecond, Mappings: []LegacyMapping{{ProjectID: "p", Kind: "native", TaskID: 123, AppID: "mobile", Platform: "1", DeviceID: "device-a"}}})
	if _, err := l.List(context.Background(), "p"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.List(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
