package weblog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryRepo struct {
	mu      sync.Mutex
	tasks   map[int64]repository.WebTask
	details []repository.WebDetail
	pending map[int64]bool
	parses  int
}

func (m *memoryRepo) Save(_ context.Context, t *repository.WebTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, v := range m.tasks {
		if v.DeviceID == t.DeviceID && v.LogDate == t.LogDate && v.PageNum == t.PageNum {
			v.Content = t.Content
			m.tasks[id] = v
			return nil
		}
	}
	t.ID = int64(len(m.tasks) + 1)
	m.tasks[t.ID] = *t
	return nil
}
func (m *memoryRepo) Tasks(_ context.Context, ids []int64) ([]repository.WebTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := []repository.WebTask{}
	for _, id := range ids {
		if t, ok := m.tasks[id]; ok {
			v = append(v, t)
		}
	}
	return v, nil
}
func (m *memoryRepo) Search(ctx context.Context, b, e int64, device string) ([]repository.WebTask, error) {
	all, _ := m.Tasks(ctx, []int64{1, 2, 3})
	out := []repository.WebTask{}
	for _, t := range all {
		if t.AddTime >= b && t.AddTime <= e && t.DeviceID == device {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memoryRepo) Latest(ctx context.Context) ([]repository.WebTask, error) {
	return m.Tasks(ctx, []int64{1, 2, 3})
}
func (m *memoryRepo) Details(_ context.Context, ids []int64) ([]repository.WebDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.WebDetail{}
	for _, d := range m.details {
		for _, id := range ids {
			if d.ID == id {
				out = append(out, d)
				break
			}
		}
	}
	return out, nil
}
func (m *memoryRepo) Match(_ context.Context, id int64, b, e int, types []int) ([]repository.WebDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.WebDetail{}
	for _, d := range m.details {
		if d.TaskID == id && d.MinuteOffset >= b && d.MinuteOffset <= e {
			out = append(out, d)
		}
	}
	return out, nil
}
func (m *memoryRepo) Ensure(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks[id].Status == 0 {
		m.pending[id] = true
	}
	return nil
}
func (m *memoryRepo) Pending(context.Context, int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []int64{}
	for id := range m.pending {
		ids = append(ids, id)
	}
	return ids, nil
}
func (m *memoryRepo) Process(ctx context.Context, id int64, parse ParseTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pending[id] {
		return nil
	}
	t := m.tasks[id]
	details, err := parse(ctx, t)
	if err != nil {
		return err
	}
	for _, d := range details {
		d.ID = int64(len(m.details) + 1)
		m.details = append(m.details, d)
	}
	t.Status = 1
	m.tasks[id] = t
	delete(m.pending, id)
	m.parses++
	return nil
}
func (m *memoryRepo) Ready(_ context.Context, ids []int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if m.tasks[id].Status != 1 {
			return false, nil
		}
	}
	return true, nil
}
func (m *memoryRepo) Validate(context.Context) error { return nil }
func newTestService(t *testing.T) (*Service, *memoryRepo) {
	t.Helper()
	r := &memoryRepo{tasks: map[int64]repository.WebTask{}, pending: map[int64]bool{}}
	s, err := NewService(Config{MaxBytes: 1 << 20, MaxTasks: 100, Workers: 2, Timeout: time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1<<20), r)
	if err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	t.Cleanup(s.Close)
	return s, r
}
func TestWebUploadParseIndexAndOriginalDownload(t *testing.T) {
	s, r := newTestService(t)
	line := `{"t":"1","c":"hello%2Bworld","d":"1609459200000"}`
	raw := url.QueryEscape(fmt.Sprintf(`{"l":%q}`, base64.StdEncoding.EncodeToString([]byte(line))))
	req := Upload{DeviceID: "d", LogPageNo: 1, FileDate: "2021-01-01", LogArray: raw}
	if err := s.Upload(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pages, err := s.Index(context.Background(), []int64{1}, nil, "hello+", 0, 1439)
			if err != nil || len(pages) != 1 || len(pages[0]) != 1 {
				t.Errorf("index: %+v %v", pages, err)
			}
		}()
	}
	wg.Wait()
	r.mu.Lock()
	if r.parses != 1 || len(r.details) != 1 || r.details[0].Content != "hello+world" {
		t.Fatalf("parse not atomic: %+v", r.details)
	}
	r.mu.Unlock()
	b, err := s.Download(context.Background(), []int64{1})
	if err != nil || string(b) != `["{\"t\":\"1\",\"c\":\"hello%2Bworld\",\"d\":\"1609459200000\"}"]` {
		t.Fatalf("download %s %v", b, err)
	}
}
func TestWebDetailsPreserveRequestedHolesAndDuplicates(t *testing.T) {
	s, r := newTestService(t)
	r.details = []repository.WebDetail{{ID: 7, Content: "x"}}
	out, err := s.Details(context.Background(), []int64{7, 99, 7})
	if err != nil || len(out) != 3 || out[0] == nil || out[1] != nil || out[2] != nil {
		t.Fatalf("Java OrderUtil mismatch %+v %v", out, err)
	}
}
func TestWebOverwriteAnalyzedPageKeepsLegacyPublishedDetails(t *testing.T) {
	s, r := newTestService(t)
	r.tasks[1] = repository.WebTask{ID: 1, DeviceID: "d", PageNum: 1, LogDate: 1609430400000, Status: 1, Content: `["old"]`}
	r.details = []repository.WebDetail{{ID: 7, TaskID: 1, Content: "old"}}
	raw := url.QueryEscape(`{"l":"bmV3"}`)
	if err := s.Upload(context.Background(), Upload{DeviceID: "d", LogPageNo: 1, FileDate: "2021-01-01", LogArray: raw}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tasks[1].Content != `["new"]` || r.tasks[1].Status != 1 || r.details[0].ID != 7 {
		t.Fatal("changed legacy overwrite semantics")
	}
}

func (m *memoryRepo) Contents(ctx context.Context, ids []int64, max int64) ([]byte, error) {
	rows, err := m.Tasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].PageNum < rows[j].PageNum })
	var out []byte
	for _, r := range rows {
		if int64(len(out)+len(r.Content)) > max {
			return nil, fmt.Errorf("limit")
		}
		out = append(out, r.Content...)
	}
	return out, nil
}

type blockingRepo struct {
	*memoryRepo
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingRepo) Process(ctx context.Context, id int64, parse ParseTask) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
	}
	return b.memoryRepo.Process(ctx, id, parse)
}
func TestWebWaiterCancellationDoesNotLosePersistedTask(t *testing.T) {
	r := &blockingRepo{memoryRepo: &memoryRepo{tasks: map[int64]repository.WebTask{1: {ID: 1, Content: `["{\"t\":1,\"c\":\"x\",\"d\":1}"]`}}, pending: map[int64]bool{}}, started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewService(Config{MaxBytes: 1024, MaxTasks: 20, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1024), r)
	if err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Index(ctx, []int64{1}, nil, "", 0, 1439); done <- err }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled waiter succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not cancel")
	}
	close(r.release)
	deadline := time.After(time.Second)
	for {
		ready, _ := r.Ready(context.Background(), []int64{1})
		if ready {
			break
		}
		select {
		case <-deadline:
			t.Fatal("detached job was lost")
		case <-time.After(time.Millisecond):
		}
	}
	first, _ := s.Index(context.Background(), []int64{1}, nil, "", 0, 1439)
	second, _ := s.Index(context.Background(), []int64{1}, nil, "", 0, 1439)
	if len(first) != 1 || first[0][0].ID != second[0][0].ID || r.parses != 1 {
		t.Fatal("published IDs not stable")
	}
}
func TestWebParseFailureDoesNotReturnSuccessfulEmptyIndex(t *testing.T) {
	s, r := newTestService(t)
	r.mu.Lock()
	r.tasks[1] = repository.WebTask{ID: 1, Content: `["broken"]`}
	r.mu.Unlock()
	pages, err := s.Index(context.Background(), []int64{1}, nil, "", 0, 1439)
	if err == nil || pages != nil {
		t.Fatalf("failed parse reported success %+v %v", pages, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tasks[1].Status != 0 || len(r.details) != 0 {
		t.Fatal("failed job published results")
	}
}

func (m *memoryRepo) MatchIndexes(ctx context.Context, id int64, b, e int, types []int, keyword string, limit int) ([]repository.WebDetail, error) {
	rows, err := m.Match(ctx, id, b, e, types)
	if err != nil {
		return nil, err
	}
	out := []repository.WebDetail{}
	for _, d := range rows {
		if keyword == "" || strings.Contains(d.Content, keyword) {
			d.Content = ""
			out = append(out, d)
		}
	}
	if len(out) > limit {
		return nil, fmt.Errorf("index limit")
	}
	return out, nil
}
func TestWebUploadBindingMatchesJacksonCoercions(t *testing.T) {
	for _, raw := range []string{`{"deviceId":123,"logPageNo":"1"}`, `{"deviceId":"d","logPageNo":1.5}`} {
		var u Upload
		if err := json.Unmarshal([]byte(raw), &u); err != nil || u.LogPageNo != 1 || u.DeviceID == "" {
			t.Fatalf("Jackson coercion %s: %+v %v", raw, u, err)
		}
	}
	for _, raw := range []string{`{"logPageNo":"bad"}`, `{"logPageNo":2147483648}`, `{"deviceId":{}}`} {
		var u Upload
		if json.Unmarshal([]byte(raw), &u) == nil {
			t.Fatalf("invalid binding accepted %s", raw)
		}
	}
}

func (m *memoryRepo) BoundedDetails(ctx context.Context, ids []int64, max int64) ([]repository.WebDetail, error) {
	rows, err := m.Details(ctx, ids)
	if err != nil {
		return nil, err
	}
	var size int64
	for _, r := range rows {
		size += int64(len(r.Content))
		if size > max {
			return nil, fmt.Errorf("detail limit")
		}
	}
	return rows, nil
}

type aggregationRepo struct {
	*memoryRepo
	rows []repository.WebTask
}

func (r *aggregationRepo) Search(context.Context, int64, int64, string) ([]repository.WebTask, error) {
	return r.rows, nil
}
func TestWebSearchOrderingMatchesOriginalJava(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/protocol/java-web-behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Search struct{ Data []struct{ TaskID int64 } }
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	repo := &aggregationRepo{memoryRepo: &memoryRepo{tasks: map[int64]repository.WebTask{}, pending: map[int64]bool{}}}
	for i := 0; i < 5; i++ {
		repo.rows = append(repo.rows, repository.WebTask{ID: int64(i + 1), DeviceID: "d", PageNum: 1, LogDate: 1609430400000 + int64(i)*86400000, AddTime: 2000})
	}
	s, err := NewService(Config{MaxBytes: 1 << 20, MaxTasks: 100, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1<<20), repo)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.Search(context.Background(), 1, 2, "d")
	if err != nil || len(actual) != len(fixture.Search.Data) {
		t.Fatal(actual, err)
	}
	for i, v := range actual {
		if v.TaskID != fixture.Search.Data[i].TaskID {
			t.Fatalf("search order[%d]=%d Java=%d", i, v.TaskID, fixture.Search.Data[i].TaskID)
		}
	}
}
