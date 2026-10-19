package main

import (
	"context"
	"fmt"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cloudwego/hertz/pkg/app"
	"io"
	"logan/servergo/internal/config"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/tempfiles"
	"logan/servergo/internal/weblog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runtimeConfig(t *testing.T) config.Config {
	t.Helper()
	c, e := config.Load(func(string) string { return "" })
	if e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	c.Address = l.Addr().String()
	l.Close()
	c.ExitWait = 100 * time.Millisecond
	return c
}
func startRuntime(t *testing.T, a *application, c config.Config) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.RunContext(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, e := net.DialTimeout("tcp", c.Address, 20*time.Millisecond)
		if e == nil {
			conn.Close()
			return cancel, done
		}
		select {
		case e := <-done:
			cancel()
			t.Fatalf("startup: %v", e)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	t.Fatal("listener did not start")
	return nil, nil
}
func awaitRuntime(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not stop")
	}
}
func TestRunContextDrainsHTTPBeforeSessionCleanup(t *testing.T) {
	c := runtimeConfig(t)
	c.ExitWait = time.Second
	a, e := newApplication(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	s, e := tempfiles.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a.session = s
	entered, release := make(chan struct{}), make(chan struct{})
	a.server.GET("/slow", func(_ context.Context, r *app.RequestContext) {
		close(entered)
		<-release
		if _, e := os.Stat(s.Path()); e != nil {
			t.Error("session removed during request")
		}
		r.String(200, "complete")
	})
	cancel, done := startRuntime(t, a, c)
	client := &http.Client{Timeout: 2 * time.Second}
	response := make(chan string, 1)
	go func() {
		r, e := client.Get("http://" + c.Address + "/slow")
		if e != nil {
			response <- e.Error()
			return
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if !r.Close {
			t.Error("draining response kept connection alive")
		}
		response <- string(b)
	}()
	<-entered
	cancel()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if got := <-response; got != "complete" {
		t.Fatal(got)
	}
	awaitRuntime(t, done)
	if _, e = os.Stat(s.Path()); !os.IsNotExist(e) {
		t.Fatal("session not cleaned", e)
	}
}
func TestRunContextForcedStreamWaitsForHandler(t *testing.T) {
	c := runtimeConfig(t)
	c.Native.Enabled = true
	a, e := newApplication(context.Background(), func() config.Config { v := c; v.Native.Enabled = false; return v }())
	if e != nil {
		t.Fatal(e)
	}
	s, e := tempfiles.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a.session = s // enable streaming without constructing native dependencies
	a.server = newServerRuntime(c, nil, nil, a.connections, a.draining, nil)
	entered, finished := make(chan struct{}), make(chan struct{})
	a.server.POST("/stream", func(ctx context.Context, r *app.RequestContext) {
		close(entered)
		_, _ = io.Copy(io.Discard, r.RequestBodyStream())
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond)
		if _, e := os.Stat(s.Path()); e != nil {
			t.Error("session removed before canceled handler finished")
		}
		close(finished)
	})
	cancel, done := startRuntime(t, a, c)
	conn, e := net.Dial("tcp", c.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /stream HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100000\r\n\r\n%s", strings.Repeat("a", 65536))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stream handler not entered")
	}
	cancel()
	awaitRuntime(t, done)
	select {
	case <-finished:
	default:
		t.Fatal("returned before handler finished")
	}
}
func TestRunContextOccupiedBindCleansSession(t *testing.T) {
	c := runtimeConfig(t)
	listener, e := net.Listen("tcp", c.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	a, e := newApplication(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	s, e := tempfiles.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a.session = s
	worker := attachRuntimeWorker(t, a)
	pool, mock, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	mock.ExpectClose()
	a.pool = pool
	done := make(chan error, 1)
	go func() { done <- a.RunContext(context.Background()) }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("bind failure missing")
		}
	case <-time.After(time.Second):
		t.Fatal("bind failure hung")
	}
	if _, e = os.Stat(s.Path()); !os.IsNotExist(e) {
		t.Fatal("session not cleaned", e)
	}
	if worker.scans.Load() != 0 {
		t.Fatal("worker claimed before listener bind succeeded")
	}
	if e = mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestRunContextAlreadyCanceledClosesListener(t *testing.T) {
	for i := 0; i < 5; i++ {
		c := runtimeConfig(t)
		a, e := newApplication(context.Background(), c)
		if e != nil {
			t.Fatal(e)
		}
		worker := attachRuntimeWorker(t, a)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan error, 1)
		go func() { done <- a.RunContext(ctx) }()
		awaitRuntime(t, done)
		if worker.scans.Load() != 0 {
			t.Fatal("already canceled startup began claims")
		}
		l, e := net.Listen("tcp", c.Address)
		if e != nil {
			t.Fatal("listener left behind", e)
		}
		l.Close()
	}
}
func TestMetricsOptionalAndHealthCompatibility(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c := runtimeConfig(t)
		c.MetricsEnabled = enabled
		a, e := newApplication(context.Background(), c)
		if e != nil {
			t.Fatal(e)
		}
		cancel, done := startRuntime(t, a, c)
		client := &http.Client{Timeout: time.Second}
		for path, status := range map[string]int{"/ping": 200, "/livez": 200, "/readyz": 503, "/logan-web/logan/latest.json": 501, "/metrics": func() int {
			if enabled {
				return 200
			}
			return 404
		}()} {
			r, e := client.Get("http://" + c.Address + path)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode != status {
				t.Fatalf("%s %d: %s", path, r.StatusCode, b)
			}
			if path == "/metrics" && enabled && !strings.Contains(string(b), "logan_http_requests_total") {
				t.Fatal("metrics missing")
			}
		}
		cancel()
		awaitRuntime(t, done)
	}
}

type runtimeWebRepo struct {
	weblog.Repository
	scans atomic.Int64
}

func (r *runtimeWebRepo) Pending(context.Context, int) ([]int64, error) {
	r.scans.Add(1)
	return nil, nil
}
func attachRuntimeWorker(t *testing.T, a *application) *runtimeWebRepo {
	t.Helper()
	repo := &runtimeWebRepo{}
	s, e := weblog.NewService(weblog.Config{MaxBytes: 1024, MaxTasks: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "UTC"}, protocol.NewWebDecoder(nil, 1024), repo)
	if e != nil {
		t.Fatal(e)
	}
	a.web = s
	return repo
}
