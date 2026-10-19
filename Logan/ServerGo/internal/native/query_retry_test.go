package native

import (
	"context"
	"io"
	"logan/servergo/internal/repository"
	"os"
	"sync"
	"testing"
	"time"
)

type blockingRetryStore struct {
	*queryStore
	entered, release chan struct{}
	mu               sync.Mutex
	calls            int
}

func (s *blockingRetryStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	body, e := s.queryStore.Get(ctx, key)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	s.calls++
	retry := s.calls > 1
	s.mu.Unlock()
	if !retry {
		return body, nil
	}
	return &retryBody{ReadCloser: body, ctx: ctx, entered: s.entered, release: s.release}, nil
}

type retryBody struct {
	io.ReadCloser
	ctx     context.Context
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *retryBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return b.ReadCloser.Read(p)
	}
}

// An already failed parse can be retried immediately by the in-memory fixture.
// Its current download file is active work, and disappears after that attempt drains.
func TestNativeQueryRetryTemporaryFilesExistOnlyWhileAttemptActive(t *testing.T) {
	name := "test.log"
	task := repository.NativeTask{ID: 1, LogFileName: &name}
	repo := &queryMemory{tasks: map[int64]repository.NativeTask{1: task}, pending: map[int64]bool{1: true}}
	store := &blockingRetryStore{queryStore: &queryStore{data: gzipQuery("{broken}\n")}, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := QueryConfig{MaxCompressed: 1 << 20, MaxPlain: 1 << 20, MaxStaged: 2 << 20, MaxLine: 1 << 20, MaxRecords: 1000, MaxIDs: 100, Workers: 1, Timeout: time.Second, Wait: time.Second, TempDir: t.TempDir()}
	q, e := NewQueryService(cfg, store, repo)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if _, e = q.parse(context.Background(), task); e == nil {
		t.Fatal("expected first parse failure")
	}
	files, e := os.ReadDir(cfg.TempDir)
	if e != nil || len(files) != 0 {
		t.Fatalf("completed failure leaked files: %v %v", files, e)
	}
	q.Start(context.Background())
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("retry did not enter body read")
	}
	files, e = os.ReadDir(cfg.TempDir)
	if e != nil || len(files) == 0 {
		t.Fatalf("expected active retry download file: %v %v", files, e)
	}
	q.StopClaims()
	close(store.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = q.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	files, e = os.ReadDir(cfg.TempDir)
	if e != nil || len(files) != 0 {
		t.Fatalf("drained retry leaked files: %v %v", files, e)
	}
}
