package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"logan/servergo/biz/handler"
	"logan/servergo/internal/config"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/weblog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type webMemory struct {
	mu         sync.Mutex
	tasks      map[int64]repository.WebTask
	details    []repository.WebDetail
	pending    map[int64]bool
	failed     bool
	saveError  bool
	parseCalls int
}

func (m *webMemory) Validate(context.Context) error { return nil }
func (m *webMemory) Save(_ context.Context, t *repository.WebTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveError {
		return fmt.Errorf("storage failure")
	}
	for id, old := range m.tasks {
		if old.DeviceID == t.DeviceID && old.LogDate == t.LogDate && old.PageNum == t.PageNum {
			old.Content = t.Content
			m.tasks[id] = old
			return nil
		}
	}
	t.ID = int64(len(m.tasks) + 1)
	m.tasks[t.ID] = *t
	return nil
}
func (m *webMemory) Tasks(_ context.Context, ids []int64) ([]repository.WebTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.WebTask{}
	for id := int64(1); id <= int64(len(m.tasks)); id++ {
		for _, want := range ids {
			if id == want {
				out = append(out, m.tasks[id])
				break
			}
		}
	}
	return out, nil
}
func (m *webMemory) Search(ctx context.Context, b, e int64, device string) ([]repository.WebTask, error) {
	tasks, _ := m.Latest(ctx)
	out := []repository.WebTask{}
	for _, t := range tasks {
		if t.AddTime >= b && t.AddTime <= e && t.DeviceID == device {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *webMemory) Latest(ctx context.Context) ([]repository.WebTask, error) {
	return m.Tasks(ctx, []int64{1, 2, 3})
}
func (m *webMemory) Details(_ context.Context, ids []int64) ([]repository.WebDetail, error) {
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
func (m *webMemory) Match(_ context.Context, id int64, b, e int, types []int) ([]repository.WebDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.WebDetail{}
	for _, d := range m.details {
		if d.TaskID != id || d.MinuteOffset < b || d.MinuteOffset > e {
			continue
		}
		match := len(types) == 0
		for _, typ := range types {
			if typ == d.LogType {
				match = true
			}
		}
		if match {
			out = append(out, d)
		}
	}
	return out, nil
}
func (m *webMemory) Ensure(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks[id].Status == 0 {
		m.pending[id] = true
	}
	return nil
}
func (m *webMemory) Pending(context.Context, int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []int64{}
	for id := range m.pending {
		out = append(out, id)
	}
	return out, nil
}
func (m *webMemory) Process(ctx context.Context, id int64, parse func(context.Context, repository.WebTask) ([]repository.WebDetail, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pending[id] {
		return nil
	}
	t := m.tasks[id]
	details, err := parse(ctx, t)
	if err != nil {
		m.failed = true
		return err
	}
	for _, d := range details {
		d.ID = int64(len(m.details) + 7)
		m.details = append(m.details, d)
	}
	t.Status = 1
	m.tasks[id] = t
	delete(m.pending, id)
	m.parseCalls++
	return nil
}
func (m *webMemory) Ready(_ context.Context, ids []int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return false, fmt.Errorf("parse failure")
	}
	for _, id := range ids {
		if m.tasks[id].Status != 1 {
			return false, nil
		}
	}
	return true, nil
}
func webFixtureRepo() *webMemory {
	rows := map[int64]repository.WebTask{}
	for _, t := range []repository.WebTask{{ID: 1, PageNum: 2, Content: `["A"]`}, {ID: 2, PageNum: 1, Content: `["B"]`}} {
		t.DeviceID = "d"
		t.LogDate = 1609430400000
		t.AddTime = 2000
		t.Status = 1
		t.UpdateTime = time.UnixMilli(3000)
		rows[t.ID] = t
	}
	zero := 0
	return &webMemory{tasks: rows, pending: map[int64]bool{}, details: []repository.WebDetail{{ID: 7, TaskID: 2, LogType: 1, Content: "hello", LogTime: 1609459200000, MinuteOffset: 480, AddTime: 4000, LogLevel: &zero}, {ID: 8, TaskID: 1, LogType: 2, Content: "world", LogTime: 1609459260000, MinuteOffset: 481, AddTime: 4000, LogLevel: &zero}}}
}
func newWebTestService(t *testing.T, repo *webMemory) *weblog.Service {
	t.Helper()
	s, err := weblog.NewService(weblog.Config{MaxBytes: 1 << 20, MaxTasks: 200, Workers: 2, Timeout: time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1<<20), repo)
	if err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	t.Cleanup(s.Close)
	return s
}
func TestWebHTTPMatchesOriginalJava(t *testing.T) {
	raw, err := os.ReadFile("tests/contract/golden/java-web-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Method, Path string
		RequestBody      *string
		Status           int
		Headers          map[string]string
		Body             string
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("bad fixture")
	}
	for _, v := range cases {
		t.Run(v.ID, func(t *testing.T) {
			repo := webFixtureRepo()
			repo.saveError = v.ID == "upload-save-error"
			svc := newWebTestService(t, repo)
			cfg, _ := config.Load(func(string) string { return "" })
			cfg.ContextPath = ""
			h := newServerWithWeb(cfg, nil, handler.NewWeb(svc, 1<<20, time.Second))
			var body *ut.Body
			if v.RequestBody != nil {
				body = &ut.Body{Body: strings.NewReader(*v.RequestBody), Len: len(*v.RequestBody)}
			}
			headers := []ut.Header{{Key: "Origin", Value: "http://localhost:3000"}, {Key: "Content-Type", Value: "application/json"}}
			if v.Method == "OPTIONS" {
				headers = append(headers, ut.Header{Key: "Access-Control-Request-Method", Value: "POST"})
			}
			w := ut.PerformRequest(h.Engine, v.Method, v.Path, body, headers...)
			if w.Code != v.Status {
				t.Fatalf("status %d want %d body=%s", w.Code, v.Status, w.Body)
			}
			for k, value := range v.Headers {
				if actual := string(w.Header().Peek(k)); actual != value {
					t.Errorf("header %s=%q want %q", k, actual, value)
				}
			}
			expected, _ := base64.StdEncoding.DecodeString(v.Body)
			if json.Valid(expected) {
				var a, b any
				_ = json.Unmarshal(expected, &a)
				_ = json.Unmarshal(w.Body.Bytes(), &b)
				aa, _ := json.Marshal(a)
				bb, _ := json.Marshal(b)
				if !bytes.Equal(aa, bb) {
					t.Errorf("body %s want %s", w.Body, expected)
				}
			} else if !bytes.Equal(expected, w.Body.Bytes()) {
				t.Errorf("body %s want %s", w.Body, expected)
			}
		})
	}
}

func (m *webMemory) Contents(ctx context.Context, ids []int64, max int64) ([]byte, error) {
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

func (m *webMemory) MatchIndexes(ctx context.Context, id int64, b, e int, types []int, keyword string, limit int) ([]repository.WebDetail, error) {
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

func (m *webMemory) BoundedDetails(ctx context.Context, ids []int64, max int64) ([]repository.WebDetail, error) {
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
