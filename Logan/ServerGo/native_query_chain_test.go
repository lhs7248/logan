package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/model"
	"logan/servergo/biz/router"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeMemory struct {
	mu      sync.Mutex
	tasks   map[int64]repository.NativeTask
	details []repository.NativeDetail
	pending map[int64]bool
	failed  bool
	parses  int
}

func (m *nativeMemory) Validate(context.Context) error { return nil }
func (m *nativeMemory) Create(_ context.Context, t *repository.NativeTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = int64(len(m.tasks) + 1)
	m.tasks[t.ID] = *t
	return nil
}
func (m *nativeMemory) Find(_ context.Context, id int64) (*repository.NativeTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		return &t, nil
	}
	return nil, nil
}
func (m *nativeMemory) Latest(context.Context) ([]repository.NativeTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeTask{}
	for _, t := range m.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}
func (m *nativeMemory) Search(_ context.Context, device string, b, e int64, p int) ([]repository.NativeTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeTask{}
	for _, t := range m.tasks {
		if t.DeviceID != nil && *t.DeviceID == device && t.AddTime >= b && t.AddTime <= e && (!(p == 1 || p == 2) || t.Platform == p) {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *nativeMemory) BoundedDetails(_ context.Context, ids []int64, max int64) ([]repository.NativeDetail, error) {
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
func (m *nativeMemory) MatchIndexes(_ context.Context, id int64, types []int, keyword *string, limit int) ([]repository.NativeDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []repository.NativeDetail{}
	for _, d := range m.details {
		if d.TaskID != id {
			continue
		}
		match := len(types) == 0
		for _, typ := range types {
			if d.LogType == typ {
				match = true
			}
		}
		if keyword != nil && !strings.Contains(d.Content, *keyword) {
			match = false
		}
		if match {
			out = append(out, repository.NativeDetail{ID: d.ID, LogType: d.LogType, LogTime: d.LogTime})
		}
	}
	if len(out) > limit {
		return nil, repository.ErrWebLimit
	}
	return out, nil
}
func (m *nativeMemory) Ensure(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks[id].Status == 0 {
		m.pending[id] = true
	}
	return nil
}
func (m *nativeMemory) Pending(context.Context, int) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []int64{}
	for id := range m.pending {
		out = append(out, id)
	}
	return out, nil
}
func (m *nativeMemory) Ready(_ context.Context, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return false, fmt.Errorf("failed")
	}
	return m.tasks[id].Status == 1, nil
}
func (m *nativeMemory) Process(ctx context.Context, id int64, parse func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pending[id] {
		return nil
	}
	task := m.tasks[id]
	stage, err := parse(ctx, task)
	if err != nil {
		m.failed = true
		return err
	}
	defer stage.Close()
	decoder := json.NewDecoder(stage)
	rows := []repository.NativeDetail{}
	for {
		var d repository.NativeDetail
		err := decoder.Decode(&d)
		if err == io.EOF {
			break
		}
		if err != nil {
			m.failed = true
			return err
		}
		d.ID = int64(7 + len(m.details) + len(rows))
		d.TaskID = id
		rows = append(rows, d)
	}
	m.details = append(m.details, rows...)
	task.Status = 1
	m.tasks[id] = task
	delete(m.pending, id)
	m.parses++
	return nil
}
func nativeFixtureMemory() *nativeMemory {
	m := &nativeMemory{tasks: map[int64]repository.NativeTask{}, pending: map[int64]bool{}}
	device := "d"
	for _, v := range []struct {
		id, date int64
		p        int
		add      int64
	}{{1, 100, 1, 1000}, {2, 200, 2, 2000}, {3, 100, 1, 3000}, {9007199254740993, 300, 1, 4000}} {
		date := v.date
		name := fmt.Sprintf("test-%d.log", v.id)
		m.tasks[v.id] = repository.NativeTask{ID: v.id, LogDate: &date, Platform: v.p, AddTime: v.add, DeviceID: &device, Status: 1, LogFileName: &name}
	}
	for i := 0; i < 25; i++ {
		m.details = append(m.details, repository.NativeDetail{ID: int64(7 + i), TaskID: 1, LogType: i % 4, Content: fmt.Sprintf("line-%d <tag> & \n中文", i), LogTime: int64(100 - i)})
	}
	return m
}
func nativeQueryTestService(t *testing.T, store native.ObjectReader, repo native.QueryRepository) *native.QueryService {
	t.Helper()
	q, err := native.NewQueryService(native.QueryConfig{MaxCompressed: 1 << 20, MaxPlain: 2 << 20, MaxStaged: 4 << 20, MaxLine: 1 << 20, MaxRecords: 1000, MaxIDs: 200, Workers: 2, Timeout: time.Second, Wait: time.Second, TempDir: t.TempDir()}, store, repo)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	t.Cleanup(q.Close)
	return q
}

type unusedNativeStore struct{}

func (unusedNativeStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("unexpected OSS access")
}
func TestNativeQueriesMatchOriginalJavaControllerAndServices(t *testing.T) {
	raw, err := os.ReadFile("tests/contract/golden/java-native-query-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Method, Path string
		Status           int
		Headers          map[string]string
		Body, Unhandled  *string
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("invalid Java HTTP fixture")
	}
	for _, v := range cases {
		t.Run(v.ID, func(t *testing.T) {
			q := nativeQueryTestService(t, unusedNativeStore{}, nativeFixtureMemory())
			cfg, _ := config.Load(func(string) string { return "" })
			cfg.ContextPath = ""
			h := newServerWithWeb(cfg, nil, nil, handler.NewNativeQueries(q, time.Second))
			headers := []ut.Header{{Key: "Origin", Value: "http://localhost:3000"}}
			if v.Method == "OPTIONS" {
				headers = append(headers, ut.Header{Key: "Access-Control-Request-Method", Value: "GET"})
			}
			response := ut.PerformRequest(h.Engine, v.Method, v.Path, nil, headers...)
			if response.Code != v.Status {
				t.Fatalf("status %d want %d body=%s", response.Code, v.Status, response.Body)
			}
			if v.Unhandled != nil {
				return
			}
			for k, want := range v.Headers {
				if got := string(response.Header().Peek(k)); got != want {
					t.Errorf("header%s %q want %q", k, got, want)
				}
			}
			body, _ := base64.StdEncoding.DecodeString(*v.Body)
			if json.Valid(body) {
				aDecoder := json.NewDecoder(bytes.NewReader(body))
				bDecoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
				aDecoder.UseNumber()
				bDecoder.UseNumber()
				var a, b any
				_ = aDecoder.Decode(&a)
				_ = bDecoder.Decode(&b)
				aa, _ := json.Marshal(a)
				bb, _ := json.Marshal(b)
				if !bytes.Equal(aa, bb) {
					t.Errorf("body %s want %s", response.Body, body)
				}
				if !bytes.Equal(body, response.Body.Bytes()) {
					t.Errorf("Java wire JSON differs: %s want %s", response.Body, body)
				}
			} else if !bytes.Equal(body, response.Body.Bytes()) {
				t.Errorf("body %s want %s", response.Body, body)
			}
		})
	}
}

func TestNativeUploadOSSParseAndAllQueries(t *testing.T) {
	repo := &nativeMemory{tasks: map[int64]repository.NativeTask{}, pending: map[int64]bool{}}
	var mu sync.Mutex
	objects := map[string][]byte{}
	gets, puts := 0, 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			objects[r.URL.Path] = body
			puts++
			mu.Unlock()
			w.Header().Set("ETag", `"test"`)
		case "GET":
			mu.Lock()
			body, ok := objects[r.URL.Path]
			gets++
			mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected OSS operation %s", r.Method)
			w.WriteHeader(405)
		}
	}))
	defer cloud.Close()
	store, err := storage.NewOSS(config.OSS{Endpoint: cloud.URL, Region: "test", Bucket: "test", Prefix: "logan-logs/", AccessKeyID: "test", AccessKeySecret: "test", Timeout: time.Second, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg, _ := config.Load(func(string) string { return "" })
	cfg.ContextPath = ""
	cfg.Native = config.Native{Enabled: true, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: t.TempDir(), MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 2, Timeout: time.Second}
	upload, err := native.NewService(cfg.Native, cfg.Timezone, store, repo)
	if err != nil {
		t.Fatal(err)
	}
	parseTemp := t.TempDir()
	queries, err := native.NewQueryService(native.QueryConfig{MaxCompressed: 1 << 20, MaxPlain: 2 << 20, MaxStaged: 4 << 20, MaxLine: 1 << 20, MaxRecords: 1000, MaxIDs: 200, Workers: 2, Timeout: time.Second, Wait: time.Second, TempDir: parseTemp}, store, repo)
	if err != nil {
		t.Fatal(err)
	}
	queries.Start(context.Background())
	defer queries.Close()
	h := newServerWithWeb(cfg, handler.NewNative(upload), nil, handler.NewNativeQueries(queries, time.Second))
	var plaintext bytes.Buffer
	for i := 0; i < 25; i++ {
		record := map[string]any{"c": fmt.Sprintf("payload-%d", i), "f": i % 4, "l": 100 - i}
		if i == 0 {
			record["n"] = strings.Repeat("thread", 12000)
		}
		_ = json.NewEncoder(&plaintext).Encode(record)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write(plaintext.Bytes())
	_ = gz.Close()
	padded := append([]byte(nil), compressed.Bytes()...)
	padding := 16 - len(padded)%16
	padded = append(padded, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, _ := aes.NewCipher(cfg.Native.Key)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, cfg.Native.IV).CryptBlocks(ciphertext, padded)
	wire := make([]byte, 5, len(ciphertext)+5)
	wire[0] = 1
	binary.BigEndian.PutUint32(wire[1:5], uint32(len(ciphertext)))
	wire = append(wire, ciphertext...)
	response := ut.PerformRequest(h.Engine, "POST", "/logan/upload.json", &ut.Body{Body: bytes.NewReader(wire), Len: len(wire)}, ut.Header{Key: "deviceId", Value: "d"}, ut.Header{Key: "platform", Value: "1"}, ut.Header{Key: "fileDate", Value: "2026-10-16"})
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"code":200`)) {
		t.Fatalf("upload %d %s", response.Code, response.Body)
	}
	for _, path := range []string{"/logan/latest.json", "/logan/task/search.json?deviceId=d&beginTime=0&endTime=9999999999999", "/logan/task/1/info.json", "/logan/meta/logtypes.json"} {
		r := ut.PerformRequest(h.Engine, "GET", path, nil)
		if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(`"code":200`)) {
			t.Fatalf("query %s=%d %s", path, r.Code, r.Body)
		}
	}
	brief := ut.PerformRequest(h.Engine, "GET", "/logan/task/1/brief.json", nil)
	var pages struct {
		Code int
		Data [][]model.NativeSimple
	}
	if json.Unmarshal(brief.Body.Bytes(), &pages) != nil || pages.Code != 200 || len(pages.Data) != 2 || len(pages.Data[0]) != 20 || len(pages.Data[1]) != 5 || pages.Data[0][0].ID != 7 {
		t.Fatalf("brief %d %s", brief.Code, brief.Body)
	}
	again := ut.PerformRequest(h.Engine, "GET", "/logan/task/1/brief.json", nil)
	if !bytes.Equal(again.Body.Bytes(), brief.Body.Bytes()) {
		t.Fatal("retry changed published IDs")
	}
	details := ut.PerformRequest(h.Engine, "GET", "/logan/task/query/details.json?detailIds=8,404,7,8", nil)
	var detailRows struct{ Data []*model.NativeDetail }
	if json.Unmarshal(details.Body.Bytes(), &detailRows) != nil || len(detailRows.Data) != 4 || detailRows.Data[1] != nil || detailRows.Data[3] != nil || *detailRows.Data[0].SimpleContent != "payload-1" {
		t.Fatalf("details %s", details.Body)
	}
	one := ut.PerformRequest(h.Engine, "GET", "/logan/task/7/detail.json", nil)
	if !bytes.Contains(one.Body.Bytes(), []byte(`"formatContent":"payload-0"`)) {
		t.Fatalf("detail %s", one.Body)
	}
	info := ut.PerformRequest(h.Engine, "GET", "/logan/task/1/info.json", nil)
	if !bytes.Contains(info.Body.Bytes(), []byte(`"status":1`)) {
		t.Fatal("task did not publish analyzed status")
	}
	mu.Lock()
	if gets != 1 || puts != 1 {
		t.Errorf("OSS calls puts=%d gets=%d", puts, gets)
	}
	mu.Unlock()
	for _, dir := range []string{cfg.Native.TempDir, parseTemp} {
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("native chain temporary files leaked")
		}
	}
	ready := ut.PerformRequest(h.Engine, "GET", "/readyz", nil)
	if ready.Code != 503 {
		t.Fatal("unvalidated service became production-ready")
	}
}

func TestAllTwentyLegacyMappingsHaveImplementations(t *testing.T) {
	cfg, _ := config.Load(func(string) string { return "" })
	cfg.Native = config.Native{Enabled: true, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: t.TempDir(), MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 1, Timeout: time.Second}
	repo := nativeFixtureMemory()
	upload, err := native.NewService(cfg.Native, cfg.Timezone, &streamStore{}, &streamTasks{})
	if err != nil {
		t.Fatal(err)
	}
	q := nativeQueryTestService(t, unusedNativeStore{}, repo)
	web := newWebTestService(t, webFixtureRepo())
	h := newServerWithWeb(cfg, handler.NewNative(upload), handler.NewWeb(web, 1<<20, time.Second), handler.NewNativeQueries(q, time.Second))
	for _, route := range router.LegacyRoutes() {
		path := strings.ReplaceAll(strings.ReplaceAll(route.Path, ":taskId", "1"), ":detailId", "7")
		response := ut.PerformRequest(h.Engine, route.Method, cfg.ContextPath+path, nil)
		if response.Code == 501 {
			t.Errorf("unimplemented legacy route %s %s", route.Method, path)
		}
	}
}

func TestNativeQueriesRealTCPAndConnectionReuse(t *testing.T) {
	q := nativeQueryTestService(t, unusedNativeStore{}, nativeFixtureMemory())
	addr, _, _ := startNativeTCP(t, time.Second, handler.NewNativeQueries(q, time.Second))
	var connections atomic.Int32
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		connections.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	for _, v := range []struct {
		path   string
		status int
		body   string
	}{{"/logan/latest.json", 200, `"taskId":9007199254740993`}, {"/logan/task/search.json?deviceId=d&endTime=0", 200, `"code":200`}, {"/logan/task/9007199254740993/info.json", 200, `"taskId":9007199254740993`}, {"/logan/meta/logtypes.json", 200, `"first":1`}, {"/logan/task/1/brief.json?logTypes=1&logTypes=3", 200, `"id":10`}, {"/logan/task/query/details.json?detailIds=8&detailIds=7", 200, `"simpleContent":"line-1 <tag> & \n中文"`}, {"/logan/task/7/detail.json", 200, `"formatContent":"line-0 <tag> & \n中文"`}, {"/logan/task/2147483648/brief.json", 400, ""}} {
		request, _ := http.NewRequest("GET", "http://"+addr+v.path, nil)
		request.Header.Set("Origin", "http://localhost:3000")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != v.status || !bytes.Contains(body, []byte(v.body)) {
			t.Errorf("%s=%d %s", v.path, response.StatusCode, body)
		}
		if response.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Errorf("CORS missing on %s", v.path)
		}
		if response.Close {
			t.Error("query closed reusable connection")
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("query TCP connection not reused: %d", connections.Load())
	}
}
