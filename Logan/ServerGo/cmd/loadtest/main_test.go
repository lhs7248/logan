package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testConfig() config {
	return config{Base: "http://127.0.0.1:8080", Mode: "health", AllowTarget: true, Duration: 60 * time.Millisecond, Timeout: 10 * time.Millisecond, QPS: 1000, Workers: 1, Queue: 1}
}
func TestResponseContracts(t *testing.T) {
	for _, tc := range []struct {
		status           int
		body, mode, want string
	}{{200, `{"message":"pong"}`, "health", ""}, {501, `{}`, "read", "http_status"}, {503, `{}`, "read", "http_status"}, {200, `{"code":501}`, "read", "business_code"}, {200, `{"code":200,"data":null}`, "read", "missing_fixture_data"}, {200, `{"code":200,"data":[]}`, "read", ""}, {200, strings.Repeat("x", (1<<20)+1), "health", "body_limit"}} {
		r := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}
		if got := validate(r, tc.mode); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}
func TestHangBoundedAndDropped(t *testing.T) {
	c := testConfig()
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	start := time.Now()
	r := run(context.Background(), c, []request{{path: "/ping", method: "GET"}}, client)
	if time.Since(start) > time.Second || r.Errors == 0 || r.Timeouts == 0 || r.Dropped == 0 || r.Attempted != r.Sent+r.Dropped || r.Completed != r.Sent || r.Attempted != offeredTarget(c, c.Duration) {
		t.Fatalf("bad accounting %+v", r)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := run(ctx, testConfig(), []request{{path: "/ping", method: "GET"}}, nil)
	if !r.Interrupted || r.Success != 0 {
		t.Fatal(r)
	}
}
func TestFixtureAndTargetGuards(t *testing.T) {
	c := testConfig()
	c.AllowTarget = false
	if _, e := prepare(c); e == nil {
		t.Fatal("target gate")
	}
	c.AllowTarget = true
	c.Base += "?secret=x"
	if _, e := prepare(c); e == nil {
		t.Fatal("query accepted")
	}
	c = testConfig()
	c.Mode = "read"
	if _, e := prepare(c); e == nil {
		t.Fatal("write gate")
	}
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	c.Fixture = filepath.Join(t.TempDir(), "fixture.json")
	os.WriteFile(c.Fixture, []byte(`{"synthetic":true,"TaskID":0,"DetailID":1}`), 0600)
	if _, e := prepare(c); e == nil {
		t.Fatal("invalid ID")
	}
	os.WriteFile(c.Fixture, []byte(`{"synthetic":true,"TaskID":1,"DetailID":2}`), 0600)
	if _, e := prepare(c); e != nil {
		t.Fatal(e)
	}
}
func TestFrameAndFileLimits(t *testing.T) {
	if validFrame([]byte{}) {
		t.Fatal("empty frame")
	}
	if validFrame([]byte{1, 0, 0, 0, 16}) {
		t.Fatal("truncated frame")
	}
	p := filepath.Join(t.TempDir(), "body")
	os.WriteFile(p, []byte("1234"), 0600)
	if _, e := readBounded(p, 3); e == nil {
		t.Fatal("limit")
	}
}

func TestHTTPServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Error("unexpected route")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":"pong"}`)
	}))
	defer server.Close()
	c := testConfig()
	c.Base = server.URL
	c.QPS = 100
	c.Duration = 100 * time.Millisecond
	c.Timeout = 50 * time.Millisecond
	reqs, e := prepare(c)
	if e != nil {
		t.Fatal(e)
	}
	r := run(context.Background(), c, reqs, nil)
	if r.Success == 0 || r.Errors != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestInflightDrainsAfterSchedule(t *testing.T) {
	c := testConfig()
	c.QPS = 20
	c.Duration = 60 * time.Millisecond
	c.Timeout = 200 * time.Millisecond
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":"pong"}`))}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	r := run(context.Background(), c, []request{{path: "/ping", method: "GET"}}, client)
	if r.Success != 2 || r.Errors != 0 || r.Elapsed < c.Duration.Seconds() {
		t.Fatalf("active request canceled at schedule boundary: %+v", r)
	}
}

func TestEndpointFixtureValidation(t *testing.T) {
	for _, tc := range []struct{ path, body, want string }{
		{"/logan/task/1/brief.json", `{"code":200,"data":[]}`, "missing_fixture_data"},
		{"/logan/task/1/brief.json", `{"code":200,"data":[[{"id":2}]]}`, ""},
		{"/logan/task/2/detail.json", `{"code":200,"data":{"id":3}}`, "fixture_contract"},
		{"/logan/task/2/detail.json", `{"code":200,"data":{"id":2}}`, ""},
		{"/logan/upload.json", `{"code":200,"data":"anything"}`, "upload_contract"},
		{"/logan/upload.json", `{"code":200,"data":"/logan/downing?name=synthetic.log"}`, ""},
	} {
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}
		if got := validate(resp, "read", tc.path); got != tc.want {
			t.Fatalf("%s got %s want %s", tc.path, got, tc.want)
		}
	}
}
func TestOfferedIntegral(t *testing.T) {
	c := testConfig()
	c.Duration = 10 * time.Second
	c.QPS = 200
	if offeredTarget(c, c.Duration) != 2000 {
		t.Fatal("lost final offers")
	}
	c.Ramp = 2 * time.Second
	if offeredTarget(c, time.Second) != 50 || offeredTarget(c, c.Duration) != 1800 {
		t.Fatal("ramp integral")
	}
	c.Ramp = 0
	c.QPS = 1
	c.Duration = time.Second
	if offeredTarget(c, time.Nanosecond) != 1 || offeredTarget(c, c.Duration) != 1 {
		t.Fatal("low QPS boundary")
	}
}
func TestOneQPSOneSecond(t *testing.T) {
	c := testConfig()
	c.Duration = time.Second
	c.QPS = 1
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":"pong"}`))}, nil
	})}
	r := run(context.Background(), c, []request{{path: "/ping", method: "GET"}}, client)
	if r.Attempted != 1 || r.Success != 1 || r.Dropped != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestFlushedBodyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := testConfig()
	c.Base = server.URL
	c.QPS = 1
	c.Timeout = 20 * time.Millisecond
	r := run(context.Background(), c, []request{{path: "/ping", method: "GET"}}, nil)
	if r.Timeouts != 1 || r.ErrorKinds["body_read"] != 1 || r.Attempted != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestDeadlineBeforeFirstTickAccountsMissingOffer(t *testing.T) {
	c := testConfig()
	c.Duration = time.Nanosecond
	c.QPS = 1
	r := run(context.Background(), c, []request{{path: "/ping", method: "GET"}}, nil)
	if r.Attempted != 1 || r.Sent != 0 || r.Dropped != 1 {
		t.Fatalf("unobserved final tick was lost: %+v", r)
	}
}
