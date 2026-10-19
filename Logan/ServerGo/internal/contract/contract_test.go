package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComparePreservesNumbersNullAndOrder(t *testing.T) {
	good := []struct{ a, b string }{{`{"id":9007199254740993,"v":null}`, `{"v":null,"id":9007199254740993}`}}
	for _, v := range good {
		if err := CompareJSON([]byte(v.a), []byte(v.b)); err != nil {
			t.Fatal(err)
		}
	}
	bad := [][2]string{
		{`{"data":null}`, `{"data":[]}`},
		{`{"data":null}`, `{}`},
		{`[1,2]`, `[2,1]`},
		{`9007199254740993`, `9007199254740992`},
		{`{} {}`, `{}`},
	}
	for _, v := range bad {
		if CompareJSON([]byte(v[0]), []byte(v[1])) == nil {
			t.Fatalf("missed mismatch: %v", v)
		}
	}
}
func TestExpandRejectsMissingVariables(t *testing.T) {
	if _, err := Expand("/task/${TASK_ID}", nil); err == nil {
		t.Fatal("missing fixture accepted")
	}
	s, err := Expand("/task/${TASK_ID}", map[string]string{"TASK_ID": "123"})
	if err != nil || s != "/task/123" {
		t.Fatalf("%q %v", s, err)
	}
}
func TestCaptureDoesNotFollowRedirectsAndRequiresWriteOptIn(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Location", "/target")
		w.WriteHeader(302)
	}))
	defer srv.Close()
	c := Case{ID: "download", Method: "GET", Path: "/redirect", Comparison: "bytes"}
	got, err := Capture(context.Background(), srv.URL, c, nil, false)
	if err != nil || got.Status != 302 || hits != 1 {
		t.Fatalf("redirect capture: %+v %v hits=%d", got, err, hits)
	}
	c.SideEffects = true
	if _, err := Capture(context.Background(), srv.URL, c, nil, false); err == nil {
		t.Fatal("side effects not gated")
	}
	if hits != 1 {
		t.Fatal("unsafe request sent")
	}
}
func TestCaseManifestCoversJavaRoutes(t *testing.T) {
	m, err := LoadManifest("../../tests/contract/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := "../../../Server/src/main/java/com/meituan/logan/web/controller"
	routes, err := ScanJavaRoutes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 20 {
		t.Fatalf("Java routes=%d", len(routes))
	}
	seen := map[string]bool{}
	for _, r := range m.Routes {
		seen[r.Method+" "+r.Path] = true
	}
	for _, r := range routes {
		if !seen[r.Method+" "+r.Path] {
			t.Errorf("missing route %s %s", r.Method, r.Path)
		}
	}
	for _, r := range m.Routes {
		count := 0
		for _, c := range m.Cases {
			if c.Route == r.ID {
				count++
			}
		}
		if count == 0 {
			t.Errorf("route %s has no cases", r.ID)
		}
	}
}
func TestSnapshotRoundTripAndMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.json")
	a := Snapshot{Version: 1, CaseID: "one", Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"code":200,"data":[]}`)}
	if err := SaveSnapshot(path, a); err != nil {
		t.Fatal(err)
	}
	b, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareSnapshots(a, b, "json"); err != nil {
		t.Fatal(err)
	}
	b.Status = 500
	if CompareSnapshots(a, b, "json") == nil {
		t.Fatal("status ignored")
	}
	b = a
	b.Headers = map[string]string{"Content-Type": "text/plain"}
	if CompareSnapshots(a, b, "json") == nil {
		t.Fatal("headers ignored")
	}
	data, _ := os.ReadFile(path)
	if !json.Valid(data) {
		t.Fatal("invalid snapshot")
	}
}
func TestCaptureRejectsUnsafePaths(t *testing.T) {
	for _, p := range []string{"https://elsewhere.test/x", "//elsewhere.test/x", "/a/../b"} {
		_, err := Capture(context.Background(), "http://localhost:1", Case{Path: p, Method: "GET"}, nil, false)
		if err == nil || strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("unsafe path: %s: %v", p, err)
		}
	}
}
