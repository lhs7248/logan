//go:build runtimefault

package main

// This opt-in container entrypoint intentionally uses synthetic storage, never OSS.
import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/cloudwego/hertz/pkg/app"
	driver "github.com/go-sql-driver/mysql"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"logan/servergo/internal/weblog"
)

type faultStore struct {
	release chan struct{}
	once    sync.Once
	block   bool
	data    []byte
}

var _ storage.Store = (*faultStore)(nil)

func (s *faultStore) Put(context.Context, string, io.ReadSeeker, int64) error {
	return fmt.Errorf("fixture is read only")
}
func (s *faultStore) Delete(context.Context, string) error { return fmt.Errorf("fixture is read only") }
func (s *faultStore) Sign(context.Context, string) (string, error) {
	return "", fmt.Errorf("fixture has no signed URLs")
}
func (s *faultStore) Get(ctx context.Context, _ string) (io.ReadCloser, error) {
	fmt.Println("PARSER_ENTERED")
	if s.block {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

// faultWebRepository delegates claims and publication to real MySQL; only the
// parser callback is paused at a deterministic barrier after claim.
type faultWebRepository struct {
	weblog.Repository
	release <-chan struct{}
	block   bool
}

func (r *faultWebRepository) Process(ctx context.Context, id int64, parse weblog.ParseTask) error {
	return r.Repository.Process(ctx, id, func(ctx context.Context, task repository.WebTask) ([]repository.WebDetail, error) {
		fmt.Println("PARSER_ENTERED")
		if r.block {
			select {
			case <-r.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return parse(ctx, task)
	})
}

// TestRuntimeFaultContainerEntry is driven by an external Docker controller. The
// test binary must be PID 1: Docker TERM/KILL must target this process itself.
func TestRuntimeFaultContainerEntry(t *testing.T) {
	mode := os.Getenv("LOGAN_RUNTIME_MODE")
	if mode == "" {
		t.Skip("requires isolated Docker controller")
	}
	if os.Getpid() != 1 || os.Getenv("LOGAN_RUNTIME_ALLOW_WRITES") != "1" {
		t.Fatal("requires PID 1 and explicit isolated runtime opt-in")
	}
	if mode != "http-drain" && mode != "native-block" && mode != "native-drain" && mode != "native-recover" && mode != "web-block" && mode != "web-drain" && mode != "web-recover" {
		t.Fatal("unknown runtime mode")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	c, e := config.Load(func(string) string { return "" })
	if e != nil {
		t.Fatal(e)
	}
	c.Address = ":8080"
	c.ExitWait = 10 * time.Second
	a, e := newApplication(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	control := &http.Server{Addr: ":8081", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release" {
			http.NotFound(w, r)
			return
		}
		releaseOnce.Do(func() { close(release) })
		w.WriteHeader(204)
	})}
	go func() { _ = control.ListenAndServe() }()
	defer control.Close()
	a.server.GET("/fault/slow", func(_ context.Context, r *app.RequestContext) {
		fmt.Println("HTTP_ENTERED")
		<-release
		r.String(200, "drained-complete")
	})
	var verify func()
	if mode != "http-drain" {
		dsn := os.Getenv("LOGAN_RUNTIME_MYSQL_DSN")
		parsed, err := driver.ParseDSN(dsn)
		if err != nil || parsed == nil || !strings.HasPrefix(parsed.DBName, "logan_it_") {
			t.Fatal("requires dedicated logan_it_ MySQL database (DSN withheld)")
		}
		dc := c.Database
		dc.Enabled = true
		dc.DSN = dsn
		db, pool, err := repository.Open(context.Background(), dc)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		id, err := strconv.ParseInt(os.Getenv("LOGAN_RUNTIME_TASK_ID"), 10, 64)
		if err != nil || id <= 0 {
			t.Fatal("requires task ID")
		}

		if strings.HasPrefix(mode, "web-") {
			repo := repository.NewWebRepository(db, 1<<20)
			if err = repo.Validate(context.Background()); err != nil {
				t.Fatal("web schema validation failed")
			}
			if err = repo.Ensure(context.Background(), id); err != nil {
				t.Fatal("ensure web task failed")
			}
			wrapped := &faultWebRepository{Repository: repo, release: release, block: mode != "web-recover"}
			svc, err := weblog.NewService(weblog.Config{MaxBytes: 1 << 20, MaxTasks: 100, Workers: 1, Timeout: 90 * time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1<<20), wrapped)
			if err != nil {
				t.Fatal(err)
			}
			a.web = svc
			svc.SetJobObserver(func(_, outcome string, _ time.Duration) { fmt.Println("PARSER_RESULT " + outcome) })
			verify = func() {
				ready, err := repo.Ready(context.Background(), []int64{id})
				if err != nil || !ready {
					t.Fatal("web task not ready")
				}
				var details []repository.WebDetail
				if err := db.Where("task_id = ?", id).Find(&details).Error; err != nil || len(details) != 1 || details[0].Content != "web recovered" {
					t.Fatal("web publication not exactly once")
				}
			}
			if mode == "web-recover" {
				go func() {
					deadline := time.Now().Add(50 * time.Second)
					for time.Now().Before(deadline) {
						ready, err := repo.Ready(context.Background(), []int64{id})
						if err == nil && ready {
							fmt.Println("WEB_RECOVERED")
							cancel()
							return
						}
						time.Sleep(50 * time.Millisecond)
					}
					fmt.Println("RECOVERY_TIMEOUT")
					cancel()
				}()
			}
		} else {
			repo := repository.NewNativeQueries(db, 1<<20)
			if err = repo.Validate(context.Background()); err != nil {
				t.Fatal("native schema validation failed")
			}
			if err = repo.Ensure(context.Background(), id); err != nil {
				t.Fatal("ensure task failed")
			}
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			_, _ = gz.Write([]byte("{\"c\":\"recovered\",\"f\":1,\"l\":1}\n"))
			_ = gz.Close()
			store := &faultStore{release: release, block: mode != "native-recover", data: compressed.Bytes()}
			q, err := native.NewQueryService(native.QueryConfig{MaxCompressed: 1 << 20, MaxPlain: 1 << 20, MaxStaged: 2 << 20, MaxLine: 1 << 20, MaxRecords: 100, MaxIDs: 100, Workers: 1, Timeout: 90 * time.Second, Wait: time.Second, TempDir: t.TempDir()}, store, repo)
			if err != nil {
				t.Fatal(err)
			}
			a.nativeQueries = q
			q.SetJobObserver(func(_, outcome string, _ time.Duration) { fmt.Println("PARSER_RESULT " + outcome) })
			verify = func() {
				ready, err := repo.Ready(context.Background(), id)
				if err != nil || !ready {
					t.Fatal("native task did not publish successfully")
				}
				var details []repository.NativeDetail
				if err := db.Where("task_id = ?", id).Find(&details).Error; err != nil || len(details) != 1 || details[0].Content != "recovered" {
					t.Fatal("native publication not exactly once")
				}
			}
			if mode == "native-recover" {
				go func() {
					deadline := time.Now().Add(50 * time.Second)
					for time.Now().Before(deadline) {
						ready, err := repo.Ready(context.Background(), id)
						if err == nil && ready {
							fmt.Println("NATIVE_RECOVERED")
							cancel()
							return
						}
						time.Sleep(50 * time.Millisecond)
					}
					fmt.Println("RECOVERY_TIMEOUT")
					cancel()
				}()
			}
		}
	}
	fmt.Println("RUNTIME_STARTING " + mode)
	if e = a.RunContext(ctx); e != nil {
		t.Fatal(e)
	}
	if verify != nil {
		verify()
	}
	fmt.Println("RUNTIME_DRAINED")
}
