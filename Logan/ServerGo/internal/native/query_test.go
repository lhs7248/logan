package native

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"logan/servergo/internal/repository"
	"os"
	"sync"
	"testing"
	"time"
)

type queryMemory struct {
	mu      sync.Mutex
	tasks   map[int64]repository.NativeTask
	details []repository.NativeDetail
	pending map[int64]bool
	failed  bool
	parses  int
}

func (m *queryMemory) Validate(context.Context) error { return nil }
func (m *queryMemory) Find(_ context.Context, id int64) (*repository.NativeTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		return &t, nil
	}
	return nil, nil
}
func (m *queryMemory) Latest(context.Context) ([]repository.NativeTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeTask{}
	for _, t := range m.tasks {
		out = append(out, t)
	}
	return out, nil
}
func (m *queryMemory) Search(ctx context.Context, device string, b, e int64, platform int) ([]repository.NativeTask, error) {
	all, _ := m.Latest(ctx)
	out := []repository.NativeTask{}
	for _, t := range all {
		if t.DeviceID != nil && *t.DeviceID == device && t.AddTime >= b && t.AddTime <= e && (platform != 1 && platform != 2 || t.Platform == platform) {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *queryMemory) BoundedDetails(_ context.Context, ids []int64, max int64) ([]repository.NativeDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeDetail{}
	var used int64
	for _, d := range m.details {
		for _, id := range ids {
			if d.ID == id {
				used += int64(len(d.Content))
				if used > max {
					return nil, repository.ErrWebLimit
				}
				out = append(out, d)
				break
			}
		}
	}
	return out, nil
}
func (m *queryMemory) MatchIndexes(_ context.Context, id int64, types []int, keyword *string, limit int) ([]repository.NativeDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeDetail{}
	for _, d := range m.details {
		if d.TaskID == id {
			ok := len(types) == 0
			for _, n := range types {
				if n == d.LogType {
					ok = true
				}
			}
			if ok {
				out = append(out, d)
			}
		}
	}
	if len(out) > limit {
		return nil, repository.ErrWebLimit
	}
	return out, nil
}
func (m *queryMemory) Ensure(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks[id].Status == 0 {
		m.pending[id] = true
	}
	return nil
}
func (m *queryMemory) Pending(context.Context, int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []int64{}
	for id := range m.pending {
		ids = append(ids, id)
	}
	return ids, nil
}
func (m *queryMemory) Ready(_ context.Context, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return false, errors.New("failed")
	}
	return m.tasks[id].Status == 1, nil
}
func (m *queryMemory) Process(ctx context.Context, id int64, parse func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pending[id] {
		return nil
	}
	t := m.tasks[id]
	staged, err := parse(ctx, t)
	if err != nil {
		m.failed = true
		return err
	}
	defer staged.Close()
	decoded := json.NewDecoder(staged)
	out := []repository.NativeDetail{}
	for {
		var d repository.NativeDetail
		err := decoded.Decode(&d)
		if err == io.EOF {
			break
		}
		if err != nil {
			m.failed = true
			return err
		}
		d.ID = int64(len(m.details) + len(out) + 1)
		d.TaskID = id
		out = append(out, d)
	}
	m.details = append(m.details, out...)
	t.Status = 1
	m.tasks[id] = t
	delete(m.pending, id)
	m.parses++
	return nil
}

type queryStore struct {
	data         []byte
	err          error
	mu           sync.Mutex
	gets, closes int
}

func (s *queryStore) Get(context.Context, string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.err != nil {
		return nil, s.err
	}
	return &queryReadCloser{Reader: bytes.NewReader(s.data), onClose: func() { s.mu.Lock(); s.closes++; s.mu.Unlock() }}, nil
}

type queryReadCloser struct {
	io.Reader
	onClose func()
}

func (r *queryReadCloser) Close() error { r.onClose(); return nil }
func gzipQuery(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = io.WriteString(w, s)
	_ = w.Close()
	return b.Bytes()
}
func queryFixture(t *testing.T, plain string) (*QueryService, *queryMemory, *queryStore, QueryConfig) {
	t.Helper()
	name := "test.log"
	r := &queryMemory{tasks: map[int64]repository.NativeTask{1: {ID: 1, LogFileName: &name}}, pending: map[int64]bool{}}
	store := &queryStore{data: gzipQuery(plain)}
	cfg := QueryConfig{MaxCompressed: 1 << 20, MaxPlain: 1 << 20, MaxStaged: 2 << 20, MaxLine: 1 << 20, MaxRecords: 1000, MaxIDs: 100, Workers: 2, Timeout: time.Second, Wait: time.Second, TempDir: t.TempDir()}
	q, err := NewQueryService(cfg, store, r)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	t.Cleanup(q.Close)
	return q, r, store, cfg
}
func TestNativeQueryOSSParseConcurrentBriefAndStableDetails(t *testing.T) {
	q, r, store, cfg := queryFixture(t, "{\"c\":\"hello\",\"f\":1,\"l\":3}\r\n{\"c\":\"world\",\"f\":99,\"l\":2}\n")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := q.Brief(context.Background(), 1, nil, nil)
			if err != nil || len(v) != 1 || len(v[0]) != 2 {
				t.Errorf("brief %+v %v", v, err)
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	if r.parses != 1 || r.details[1].LogType != 0 || r.details[0].ID != 1 {
		t.Fatalf("bad publication %+v", r.details)
	}
	r.mu.Unlock()
	details, err := q.Details(context.Background(), []int64{2, 404, 1, 2})
	if err != nil || len(details) != 4 || details[0] == nil || details[1] != nil || details[3] != nil || *details[0].SimpleContent != "world" || details[0].FormatContent != nil {
		t.Fatalf("detail ordering/format %+v %v", details, err)
	}
	one, err := q.Detail(context.Background(), 1)
	if err != nil || one == nil || *one.FormatContent != "hello" || one.SimpleContent != nil {
		t.Fatal(one, err)
	}
	store.mu.Lock()
	if store.gets != 1 || store.closes != 1 {
		t.Fatalf("OSS bodies: %+v", store)
	}
	store.mu.Unlock()
	files, _ := os.ReadDir(cfg.TempDir)
	if len(files) != 0 {
		t.Fatal("parse temp files leaked")
	}
}
func TestNativeQueryMalformedPlaintextPublishesNothing(t *testing.T) {
	q, r, _, cfg := queryFixture(t, "{\"c\":\"ok\",\"f\":1,\"l\":3}\n{broken}\n")
	out, err := q.Brief(context.Background(), 1, nil, nil)
	if err == nil || out != nil {
		t.Fatal("failed parse returned successful empty result")
	}
	// The fixture keeps failures pending, so another worker may already be
	// retrying when Brief reports failure. Drain attempts before checking cleanup.
	q.StopClaims()
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	if r.tasks[1].Status != 0 || len(r.details) != 0 {
		t.Fatal("partial parse published")
	}
	r.mu.Unlock()
	files, _ := os.ReadDir(cfg.TempDir)
	if len(files) != 0 {
		t.Fatal("failed parse temp files leaked")
	}
}
func TestNativeQueryTaskOrderAndNullDate(t *testing.T) {
	q, r, _, _ := queryFixture(t, "")
	d1, d2 := int64(100), int64(200)
	r.mu.Lock()
	r.tasks = map[int64]repository.NativeTask{1: {ID: 1, LogDate: &d1}, 2: {ID: 2, LogDate: &d2}, 3: {ID: 3, LogDate: &d1}, 4: {ID: 4}}
	r.mu.Unlock()
	rows, err := q.Latest(context.Background())
	if err != nil || len(rows) != 4 || rows[0].TaskID != 2 || rows[1].TaskID != 3 || rows[3].LogDate != 0 {
		t.Fatal(rows, err)
	}
	v, err := q.Info(context.Background(), 99)
	if err != nil || v != nil {
		t.Fatal(v, err)
	}
}

var _ = fmt.Sprintf

type blockedQueryRepo struct {
	*queryMemory
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockedQueryRepo) Process(ctx context.Context, id int64, parse func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error {
	r.once.Do(func() { close(r.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
	}
	return r.queryMemory.Process(ctx, id, parse)
}
func TestNativeWaiterCancellationKeepsBackgroundJob(t *testing.T) {
	name := "test.log"
	r := &blockedQueryRepo{queryMemory: &queryMemory{tasks: map[int64]repository.NativeTask{1: {ID: 1, LogFileName: &name}}, pending: map[int64]bool{}}, started: make(chan struct{}), release: make(chan struct{})}
	store := &queryStore{data: gzipQuery("{\"c\":\"x\",\"f\":1,\"l\":1}\n")}
	q, err := NewQueryService(QueryConfig{MaxCompressed: 1024, MaxPlain: 1024, MaxStaged: 2048, MaxLine: 1024, MaxRecords: 10, MaxIDs: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, TempDir: t.TempDir()}, store, r)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	defer q.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := q.Brief(ctx, 1, nil, nil); done <- err }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled caller reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter failed to cancel")
	}
	close(r.release)
	deadline := time.After(time.Second)
	for {
		ready, _ := r.Ready(context.Background(), 1)
		if ready {
			break
		}
		select {
		case <-deadline:
			t.Fatal("persisted job lost after cancellation")
		case <-time.After(time.Millisecond):
		}
	}
	a, err := q.Brief(context.Background(), 1, nil, nil)
	if err != nil || len(a) != 1 || len(a[0]) != 1 {
		t.Fatal(a, err)
	}
	b, err := q.Brief(context.Background(), 1, nil, nil)
	if err != nil || b[0][0].ID != a[0][0].ID || r.parses != 1 {
		t.Fatal("retry recreated published details")
	}
}
func TestNativeParseBudgetsAndNullContentCleanAllFiles(t *testing.T) {
	for _, mode := range []string{"compressed", "plain", "staged", "line", "records", "null-content"} {
		t.Run(mode, func(t *testing.T) {
			plain := "{\"c\":\"hello world\",\"f\":1,\"l\":1}\n{\"c\":\"second\",\"f\":2,\"l\":2}\n"
			if mode == "null-content" {
				plain = "{\"c\":null}"
			}
			q, r, store, cfg := queryFixture(t, plain)
			switch mode {
			case "compressed":
				q.cfg.MaxCompressed = 5
			case "plain":
				q.cfg.MaxPlain = 5
			case "staged":
				q.cfg.MaxStaged = 5
			case "line":
				q.cfg.MaxLine = 5
			case "records":
				q.cfg.MaxRecords = 1
			}
			task, _ := r.Find(context.Background(), 1)
			staged, err := q.parse(context.Background(), *task)
			if staged != nil {
				_ = staged.Close()
			}
			if err == nil {
				t.Fatal("invalid/oversize source accepted")
			}
			files, _ := os.ReadDir(cfg.TempDir)
			if len(files) != 0 {
				t.Fatal("temp files leaked")
			}
			store.mu.Lock()
			if store.closes != 1 {
				t.Error("OSS response body not closed")
			}
			store.mu.Unlock()
		})
	}
}
func TestNativeGzipSalvagePreservesJavaCompletionBoundary(t *testing.T) {
	q, r, store, cfg := queryFixture(t, "")
	store.data = []byte("not gzip")
	pages, err := q.Brief(context.Background(), 1, nil, nil)
	if err != nil || len(pages) != 0 {
		t.Fatal(pages, err)
	}
	r.mu.Lock()
	if r.tasks[1].Status != 1 || r.parses != 1 {
		t.Fatal("changed Java empty decompression semantics")
	}
	r.mu.Unlock()
	files, _ := os.ReadDir(cfg.TempDir)
	if len(files) != 0 {
		t.Fatal("salvage temp files leaked")
	}
}

type blockingQueryStore struct {
	reader  *io.PipeReader
	started chan struct{}
}

func (s blockingQueryStore) Get(context.Context, string) (io.ReadCloser, error) {
	close(s.started)
	return s.reader, nil
}
func TestNativeSlowOSSDeadlineClosesBodyAndRemovesFiles(t *testing.T) {
	q, r, _, cfg := queryFixture(t, "")
	reader, writer := io.Pipe()
	defer writer.Close()
	started := make(chan struct{})
	q.store = blockingQueryStore{reader, started}
	task, _ := r.Find(context.Background(), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		body, err := q.parse(ctx, *task)
		if body != nil {
			_ = body.Close()
		}
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("slow OSS accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked OSS body not closed on deadline")
	}
	files, _ := os.ReadDir(cfg.TempDir)
	if len(files) != 0 {
		t.Fatal("deadline temp files leaked")
	}
	if _, err := writer.Write([]byte("x")); err == nil {
		t.Fatal("OSS body not closed")
	}
}

func TestNativeQueryRejectsOverflowingCompressedBudget(t *testing.T) {
	cfg := QueryConfig{MaxCompressed: 1<<63 - 1, MaxPlain: 1024, MaxStaged: 2048, MaxLine: 1024, MaxRecords: 10, MaxIDs: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, TempDir: t.TempDir()}
	if _, err := NewQueryService(cfg, &queryStore{}, &queryMemory{}); err == nil {
		t.Fatal("maxCompressed+1 would overflow LimitedReader")
	}
}
