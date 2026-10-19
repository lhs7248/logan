package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	if percentile([]float64{3, 1, 2}, .95) != 3 {
		t.Fatal("nearest rank")
	}
}
func TestSemantic(t *testing.T) {
	if classify([]string{"a"}, []string{"a", "b"}, "native") != "native_content_incomplete" {
		t.Fatal("loss hidden")
	}
	if classify([]string{"x"}, []string{"a", "b"}, "native") != "content_mismatch" {
		t.Fatal("wrong oracle classified known")
	}
}
func TestBounds(t *testing.T) {
	c := defaults()
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	c.Duration = time.Hour
	c.QPS = 100
	if validateConfig(c) == nil {
		t.Fatal("unbounded offered")
	}
}
func TestCancellation(t *testing.T) {

	c := defaults()
	c.Base = "http://test"
	c.Timeout = 10 * time.Millisecond
	a := newStats()
	e := &engine{c: c, stats: a, client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	_, k := e.call(context.Background(), "native-search", "GET", "/logan/task/search.json", nil, nil, nil)
	if k != "timeout" {
		t.Fatal(k)
	}
	if a.HTTP.Errors != 1 || a.HTTP.Sent != 1 {
		t.Fatal(a.HTTP)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBoundedScheduleAndWindows(t *testing.T) {
	c := defaults()
	c.Duration = 40 * time.Millisecond
	c.QPS = 10000
	c.Queue = 0
	c.Workers = 1
	c.Timeout = time.Second
	a := newStats()
	e := &engine{c: c, stats: a, client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		time.Sleep(15 * time.Millisecond)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":500}`)), Header: http.Header{}}, nil
	})}}
	e.run(context.Background())
	if a.Attempted != 400 || a.Dropped != a.Attempted-a.Workflows.Sent || a.Dropped == 0 {
		t.Fatal(a.Attempted, a.Dropped, a.Workflows.Sent)
	}
	if a.Windows[0] == nil || a.HTTP.Errors != a.HTTP.Sent {
		t.Fatal("windows/error counters")
	}
}
func TestWorkflowKnownLossStrict(t *testing.T) {
	c := defaults()
	a := newStats()
	e := &engine{c: c, stats: a, runID: "test", f: fixture{NativeBase64: "AQ==", NativeContents: []string{"a", "b"}}}
	e.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		data := `"/logan/downing?name=x"`
		switch {
		case strings.Contains(r.URL.Path, "search"):
			data = `[{"taskId":1,"deviceId":"phase19-test-0"}]`
		case strings.Contains(r.URL.Path, "brief"):
			data = `[[{"id":2}]]`
		case strings.Contains(r.URL.Path, "details"):
			data = `[{"id":2,"content":"a"}]`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":` + data + `}`))}, nil
	})}
	l := e.flow(context.Background(), 0, "native")
	if l.Classification != "native_content_incomplete" || a.HTTP.Errors != 1 || a.Workflows.Errors != 1 || a.Workflows.Success != 0 {
		t.Fatal(l, a.HTTP, a.Workflows)
	}
}
func TestMinuteBoundary(t *testing.T) {
	s := newStats()
	s.Start = time.Now().Add(-61 * time.Second)
	s.record("web-index", 2, "", false)
	if s.Windows[1] == nil || s.Windows[1].HTTP.Sent != 1 {
		t.Fatal("minute bucket")
	}
}
func TestParentCancelled(t *testing.T) {
	c := defaults()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &engine{c: c, stats: newStats()}
	e.run(ctx)
	if !e.stats.Interrupted || e.stats.HTTP.Sent != 0 {
		t.Fatal("cancellation ignored")
	}
}
func TestTargetRestriction(t *testing.T) {
	c := defaults()
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	for _, host := range []string{"example.com", "192.168.1.1", "10.0.0.1", "logan-verifier-other-go"} {
		c.Base = "http://" + host + ":8080"
		if validateConfig(c) == nil {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"127.0.0.1", "[::1]", "localhost", "logan-verifier-0123456789abcdef0123456789abcdef-go"} {
		c.Base = "http://" + host + ":8080"
		if err := validateConfig(c); err != nil {
			t.Fatal(host, err)
		}
	}
}
func TestReportWriteFailurePersists(t *testing.T) {
	c := defaults()
	c.Output = t.TempDir() + "/missing/report.json"
	e := &engine{c: c, stats: newStats()}
	if e.write(false) == nil {
		t.Fatal("expected failed periodic write")
	}
	e.c.Output = t.TempDir() + "/report.json"
	if err := e.write(true); err != nil {
		t.Fatal(err)
	}
	if e.stats.ReportWriteErrors != 1 || e.snapshot(true)["report_write_errors"] != 1 {
		t.Fatal("failed periodic write forgotten")
	}
}
func TestFinalTickReconciliation(t *testing.T) {
	c := defaults()
	c.Duration = time.Nanosecond
	c.QPS = 1
	e := &engine{c: c, stats: newStats()}
	e.run(context.Background())
	if e.stats.Attempted != 1 || e.stats.ReconciledArrivals != 1 || e.stats.Dropped != 1 {
		t.Fatal(e.stats.Attempted, e.stats.ReconciledArrivals, e.stats.Dropped)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e = &engine{c: c, stats: newStats()}
	e.run(ctx)
	if e.stats.Attempted != 0 || e.stats.ReconciledArrivals != 0 {
		t.Fatal("cancelled ideal arrivals invented")
	}
}
func TestActualWebDetailIDShape(t *testing.T) {
	got := ids(json.RawMessage(`[[{"detailId":1},{"detailId":2}]]`))
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatal(got)
	}
	got = ids(json.RawMessage(`{"rows":[{"detailId":3},{"id":4},{"id":0},{"detailId":2.5},{"id":2147483648}]}`))
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatal(got)
	}
}
func TestWebAsyncWorkflow(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			c := defaults()
			c.ParseTimeout = 600 * time.Millisecond
			a := newStats()
			e := &engine{c: c, stats: a, runID: "webtest", f: fixture{WebContents: []string{"a", "b"}, WebBody: map[string]any{"deviceId": "original-sdk-device", "fileDate": "2026-10-17"}}}
			indexes := 0
			e.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				data := `null`
				if strings.Contains(r.URL.Path, "upload") {
					var sent map[string]any
					if json.NewDecoder(r.Body).Decode(&sent) != nil || sent["deviceId"] != "phase19-webtest-0" || sent["fileDate"] != "2026-10-17" {
						t.Error("SDK device/date payload not correctly preserved/overridden")
					}
				}
				switch {
				case strings.Contains(r.URL.Path, "search"):
					data = `[{"taskId":5,"deviceId":"phase19-webtest-0"}]`
				case strings.Contains(r.URL.Path, "detailIndex"):
					indexes++
					data = `[]`
					if indexes > 1 && !empty {
						data = `[[{"detailId":1},{"detailId":2}]]`
					}
				case strings.Contains(r.URL.Path, "details"):
					data = `[{"id":1,"content":"a"},{"id":2,"content":"b"}]`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":` + data + `}`))}, nil
			})}
			l := e.flow(context.Background(), 0, "web")
			if empty {
				if l.Classification != "parse_timeout" {
					t.Fatal(l)
				}
			} else if l.Classification != "" || len(l.DetailIDs) != 2 || indexes != 2 {
				t.Fatal(l, indexes)
			}
		})
	}
}
func TestQueuedCyclesDoNotStartAfterSchedule(t *testing.T) {
	c := defaults()
	c.Duration = 30 * time.Millisecond
	c.Drain = time.Second
	c.QPS = 100
	c.Queue = 10
	c.Workers = 1
	e := &engine{c: c, stats: newStats()}
	e.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		time.Sleep(80 * time.Millisecond)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":500}`))}, nil
	})}
	e.run(context.Background())
	if e.stats.Workflows.Sent != 1 || e.stats.Attempted != 3 || e.stats.Dropped != 2 {
		t.Fatal(e.stats.Workflows.Sent, e.stats.Attempted, e.stats.Dropped)
	}
}
func TestFixtureDateVariation(t *testing.T) {
	for _, date := range []string{"2026-10-15", "2026-10-17", "not-date", "2026-02-30"} {
		f := fixture{NativeBase64: "AQAAABCqqqqqqqqqqqqqqqqqqqqqAA==", NativeContents: []string{"a"}, WebContents: []string{"b"}, WebBody: map[string]any{"fileDate": date, "deviceId": "sdk-original-device"}}
		raw, _ := json.Marshal(f)
		path := t.TempDir() + "/fixture.json"
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := loadFixture(path)
		valid := date == "2026-10-15" || date == "2026-10-17"
		if (err == nil) != valid {
			t.Fatal(date, err)
		}
	}
}

func TestFixtureUnknownFieldRejected(t *testing.T) {
	f := fixture{NativeBase64: "AQAAABCqqqqqqqqqqqqqqqqqqqqqAA==", NativeContents: []string{"a"}, WebContents: []string{"b"}, WebBody: map[string]any{"fileDate": "2026-10-17", "credential": "forbidden"}}
	raw, _ := json.Marshal(f)
	path := t.TempDir() + "/fixture.json"
	_ = os.WriteFile(path, raw, 0600)
	if _, err := loadFixture(path); err == nil {
		t.Fatal("unknown field accepted")
	}
}
func TestValidateOnlyCLI(t *testing.T) {
	if os.Getenv("LOGAN_SOAK_VALIDATE_HELPER") == "1" {
		for i, s := range os.Args {
			if s == "--" {
				os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
				break
			}
		}
		flag.CommandLine = flag.NewFlagSet("validate", flag.ExitOnError)
		main()
		os.Exit(0)
	}
	f := fixture{NativeBase64: "AQAAABCqqqqqqqqqqqqqqqqqqqqqAA==", NativeContents: []string{"a"}, WebContents: []string{"b"}, WebBody: map[string]any{"fileDate": "2026-10-17", "deviceId": "original"}}
	raw, _ := json.Marshal(f)
	path := t.TempDir() + "/fixture.json"
	_ = os.WriteFile(path, raw, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestValidateOnlyCLI$", "--", "--base-url", "http://127.0.0.1:1", "--fixture", path, "--allow-test-target", "--allow-writes", "--test-target-marker", "logan-load-test", "--validate-only")
	cmd.Env = append(os.Environ(), "LOGAN_SOAK_VALIDATE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"validated":true`) {
		t.Fatal(string(out), err)
	}
}
func TestDetailIdentityStrict(t *testing.T) {
	for _, flow := range []string{"native", "web"} {
		for _, data := range []string{`[{"id":99,"content":"same"}]`, `[{"id":1,"content":"same"},{"id":1,"content":"same"}]`, `[]`} {
			if k := validateDetails(json.RawMessage(data), []int64{1}, []string{"same"}, flow); k != "detail_identity" {
				t.Fatal(flow, data, k)
			}
		}
	}
	if k := validateDetails(json.RawMessage(`[{"id":1,"content":"a"}]`), []int64{1}, []string{"a", "b"}, "native"); k != "native_content_incomplete" {
		t.Fatal(k)
	}
}
func TestReadFixedSeedIdentity(t *testing.T) {
	for _, flow := range []string{"native", "web"} {
		e := &engine{c: defaults(), stats: newStats(), f: fixture{NativeContents: []string{"same"}, WebContents: []string{"same"}}}
		e.stats.Seeds = []ledger{{Flow: flow, TaskID: 1, DetailIDs: []int64{2}}}
		e.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"id":3,"content":"same"}]}`))}, nil
		})}
		if l := e.read(context.Background(), 0); l.Classification != "detail_identity" {
			t.Fatal(flow, l)
		}
	}
}

func TestClockMarginBound(t *testing.T) {
	c := defaults()
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	c.Duration = 61 * time.Minute
	if err := validateConfig(c); err != nil {
		t.Fatal(err)
	}
	c.Duration += time.Second
	if validateConfig(c) == nil {
		t.Fatal("clock margin unbounded")
	}
}

func TestBusinessErrorKeepsNumericCodeWithoutBody(t *testing.T) {
	e := &engine{c: defaults(), stats: newStats(), client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":500,"msg":"secret-SQL secret-device"}`))}, nil
	})}}
	_, kind := e.call(context.Background(), "web-upload", "POST", "/logan/web/upload.json", nil, nil, nil)
	if kind != "business_code_500" || e.stats.HTTP.ErrorKinds["business_code_500"] != 1 {
		t.Fatalf("numeric code lost: %s", kind)
	}
	b, _ := json.Marshal(e.snapshot(true))
	if strings.Contains(string(b), "secret-") {
		t.Fatal("body leaked into measurement")
	}
}
