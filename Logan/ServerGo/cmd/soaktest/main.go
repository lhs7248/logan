// soaktest exercises bounded synthetic upload/parse/read workflows.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	Base, Mode, Fixture, Marker, Output    string
	Duration, Timeout, ParseTimeout, Drain time.Duration
	QPS                                    float64
	Workers, Queue                         int
	ArrivalBurst                           int
	AllowTarget, AllowWrites               bool
	LargeFixture                           bool
}

func defaults() config {
	return config{Base: "http://127.0.0.1:8080", Mode: "mixed", Duration: 10 * time.Second, Timeout: 5 * time.Second, ParseTimeout: 15 * time.Second, Drain: 20 * time.Second, QPS: 1, Workers: 2, Queue: 2, ArrivalBurst: 1}
}
func validateConfig(c config) error {
	u, e := url.Parse(c.Base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/logan-web") {
		return errors.New("invalid test origin")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !(ip != nil && ip.IsLoopback()) && host != "localhost" && !regexp.MustCompile(`^logan-verifier-[a-f0-9]{32}-(java|go)$`).MatchString(host) {
		return errors.New("target must be loopback or controller-owned verifier hostname")
	}
	if !c.AllowTarget || !c.AllowWrites || c.Marker != "logan-load-test" {
		return errors.New("explicit test target and writes marker required")
	}
	timeoutMax, parseMax, drainMax := 5*time.Second, 15*time.Second, 30*time.Second
	if c.LargeFixture {
		timeoutMax, parseMax, drainMax = 30*time.Second, 60*time.Second, 60*time.Second
	}
	if c.Duration <= 0 || c.Duration > 61*time.Minute || c.Timeout <= 0 || c.Timeout > timeoutMax || c.ParseTimeout <= 0 || c.ParseTimeout > parseMax || c.Drain <= 0 || c.Drain > drainMax || math.IsNaN(c.QPS) || math.IsInf(c.QPS, 0) || c.QPS <= 0 || math.Ceil(c.QPS*c.Duration.Seconds()) > 100000 || c.Workers < 1 || c.Workers > 128 || c.Queue < 0 || c.Queue > 1024 {
		return errors.New("invalid bounded workload settings")
	}
	if c.ArrivalBurst < 1 || c.ArrivalBurst > c.Workers || (c.ArrivalBurst > 1 && c.Mode != "all20") {
		return errors.New("invalid arrival burst")
	}
	switch c.Mode {
	case "mixed", "native", "web", "read", "all20":
	default:
		return errors.New("invalid mode")
	}
	return nil
}

type fixture struct {
	NativePlainBase64    string         `json:"nativePlainBase64,omitempty"`
	NativeDownloadBase64 string         `json:"nativeDownloadBase64,omitempty"`
	WebRawLogs           []string       `json:"webRawLogs,omitempty"`
	NativeBase64         string         `json:"nativeBase64"`
	NativeContents       []string       `json:"nativeContents"`
	WebContents          []string       `json:"webContents"`
	WebBody              map[string]any `json:"webBody"`
}

func loadFixture(path string) (fixture, error) { return loadFixtureBounded(path, false) }
func loadFixtureBounded(path string, large bool) (fixture, error) {
	var f fixture
	file, e := os.Open(path)
	if e != nil {
		return f, errors.New("fixture unavailable")
	}
	defer file.Close()
	fixtureLimit, wireLimit, recordLimit := 2<<20, 1<<20, 16
	if large {
		fixtureLimit, wireLimit, recordLimit = 256<<20, 32<<20, 16384
	}
	b, e := io.ReadAll(io.LimitReader(file, int64(fixtureLimit)+1))
	if e != nil || len(b) > fixtureLimit {
		return f, errors.New("fixture unavailable or oversized")
	}
	if json.Unmarshal(b, &f) != nil {
		return f, errors.New("fixture JSON")
	}
	n, e := base64.StdEncoding.DecodeString(f.NativeBase64)
	if e != nil || !validFrame(n) || len(n) > wireLimit || len(f.NativeContents) < 1 || len(f.NativeContents) > recordLimit || len(f.WebContents) < 1 || len(f.WebContents) > recordLimit {
		return f, errors.New("fixture bounds")
	}
	date, ok := f.WebBody["fileDate"].(string)
	parsed, err := time.Parse("2006-01-02", date)
	if !ok || err != nil || parsed.Year() < 2020 || parsed.Year() > 2100 {
		return f, errors.New("fixture date must be valid ISO date year2020..2100")
	}
	for _, ss := range [][]string{f.NativeContents, f.WebContents} {
		for _, s := range ss {
			if len(s) > 65536 || (large && len(s) > 65535) {
				return f, errors.New("oracle size")
			}
		}
	}
	for k := range f.WebBody {
		switch k {
		case "logPageNo", "webSource", "environment", "logArray", "fileDate", "client", "customInfo", "deviceId":
		default:
			return f, errors.New("unapproved web fixture field")
		}
	}
	if err := validateExtraFixture(f, large); err != nil {
		return f, err
	}
	return f, nil
}

func validFrame(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for len(b) > 0 {
		if len(b) < 7 || b[0] != 1 {
			return false
		}
		n := int64(binary.BigEndian.Uint32(b[1:5]))
		if n <= 0 || n%16 != 0 || n > int64(len(b)-6) || b[5+n] != 0 {
			return false
		}
		b = b[6+n:]
	}
	return true
}

type summary struct {
	Sent, Completed, Success, Errors                  int
	ErrorKinds                                        map[string]int
	All, OK                                           []float64 `json:"-"`
	P50, P95, P99, SuccessP50, SuccessP95, SuccessP99 float64
	QPS                                               float64
}

func (s *summary) add(ms float64, k string) {
	s.Sent++
	s.Completed++
	s.All = append(s.All, ms)
	if k == "" {
		s.Success++
		s.OK = append(s.OK, ms)
	} else {
		s.Errors++
		if s.ErrorKinds == nil {
			s.ErrorKinds = map[string]int{}
		}
		s.ErrorKinds[k]++
	}
}
func percentile(a []float64, p float64) float64 {
	if len(a) == 0 {
		return 0
	}
	b := append([]float64(nil), a...)
	sort.Float64s(b)
	return b[int(math.Ceil(p*float64(len(b))))-1]
}
func (s summary) finish(seconds float64) summary {
	s.P50 = percentile(s.All, .5)
	s.P95 = percentile(s.All, .95)
	s.P99 = percentile(s.All, .99)
	s.SuccessP50 = percentile(s.OK, .5)
	s.SuccessP95 = percentile(s.OK, .95)
	s.SuccessP99 = percentile(s.OK, .99)
	if seconds > 0 {
		s.QPS = float64(s.Sent) / seconds
	}
	return s
}

type ledger struct {
	Cycle          int
	Flow           string
	TaskID         int64
	DetailIDs      []int64
	Classification string
}
type window struct {
	Minute    int
	HTTP      summary
	Routes    map[string]*summary
	Workflows summary
}
type stats struct {
	E2EActive                               map[string]int
	E2E                                     map[string][]e2eSample
	ActiveWorkflows, MaxConcurrentWorkflows int
	mu                                      sync.Mutex
	HTTP                                    summary
	Routes                                  map[string]*summary
	Workflows                               summary
	Windows                                 map[int]*window
	Ledger                                  []ledger
	Attempted, Dropped                      int
	ReportWriteErrors                       int
	ReconciledArrivals                      int
	SeedFailure                             string
	Start                                   time.Time
	Interrupted                             bool
	Seeds                                   []ledger
}

func newStats() *stats {
	return &stats{Routes: map[string]*summary{}, Windows: map[int]*window{}, Start: time.Now()}
}
func (s *stats) record(route string, ms float64, k string, workflow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := int(time.Since(s.Start) / time.Minute)
	w := s.Windows[minute]
	if w == nil {
		w = &window{Minute: minute, Routes: map[string]*summary{}}
		s.Windows[minute] = w
	}
	if workflow {
		s.Workflows.add(ms, k)
		w.Workflows.add(ms, k)
		return
	}
	s.HTTP.add(ms, k)
	w.HTTP.add(ms, k)
	if s.Routes[route] == nil {
		s.Routes[route] = &summary{}
	}
	s.Routes[route].add(ms, k)
	if w.Routes[route] == nil {
		w.Routes[route] = &summary{}
	}
	w.Routes[route].add(ms, k)
}

type engine struct {
	c        config
	f        fixture
	stats    *stats
	client   *http.Client
	budgetMu sync.Mutex
	requests int
	runID    string
}

func (e *engine) call(ctx context.Context, route, method, path string, body []byte, headers map[string]string, check func(json.RawMessage) string) (json.RawMessage, string) {
	e.budgetMu.Lock()
	if e.requests >= 1000000 {
		e.budgetMu.Unlock()
		return nil, "request_limit"
	}
	e.requests++
	e.budgetMu.Unlock()
	one, cancel := context.WithTimeout(ctx, e.c.Timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(one, method, e.c.Base+path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := e.client.Do(req)
	kind := ""
	var data json.RawMessage
	if err != nil {
		kind = "transport"
	} else {
		responseLimit := 1 << 20
		if e.c.Mode == "all20" {
			responseLimit = 64 << 20
		}
		b, er := io.ReadAll(io.LimitReader(resp.Body, int64(responseLimit)+1))
		resp.Body.Close()
		var env struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		switch {
		case er != nil:
			kind = "body_read"
		case len(b) > responseLimit:
			kind = "body_limit"
		case resp.StatusCode != 200:
			kind = "http_status"
		case json.Unmarshal(b, &env) != nil:
			kind = "invalid_json"
		case env.Code != 200:
			kind = fmt.Sprintf("business_code_%d", env.Code)
		default:
			data = env.Data
			if check != nil {
				kind = check(data)
			}
		}
	}
	if one.Err() != nil {
		if errors.Is(one.Err(), context.DeadlineExceeded) {
			kind = "timeout"
		} else {
			kind = "cancelled"
		}
	}
	e.stats.record(route, float64(time.Since(start))/float64(time.Millisecond), kind, false)
	return data, kind
}
func classify(actual, expected []string, flow string) string {
	a := append([]string(nil), actual...)
	b := append([]string(nil), expected...)
	if flow == "native" {
		sort.Strings(a)
		sort.Strings(b)
	}
	if len(a) == len(b) {
		equal := true
		for i := range a {
			if a[i] != b[i] {
				equal = false
			}
		}
		if equal {
			return ""
		}
	}
	if flow == "native" && len(expected) == 2 && len(actual) == 1 && actual[0] == expected[0] {
		return "native_content_incomplete"
	}
	return "content_mismatch"
}
func ids(raw json.RawMessage) []int64 {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	out := []int64{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, y := range x {
				walk(y)
			}
		case map[string]any:
			for _, key := range []string{"id", "detailId"} {
				if n, ok := x[key].(float64); ok && n > 0 && n == math.Trunc(n) && n <= 2147483647 {
					out = append(out, int64(n))
					break
				}
			}
			// Map keys are sorted so nested collection ordering is deterministic.
			keys := []string{}
			for key := range x {
				if key != "id" && key != "detailId" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(x[key])
			}
		}
	}
	walk(v)
	return out
}

// validateDetails binds the oracle to exactly the requested database rows.
func validateDetails(d json.RawMessage, requested []int64, expected []string, flow string) string {
	var rows []struct {
		ID      int64  `json:"id"`
		Content string `json:"content"`
	}
	if json.Unmarshal(d, &rows) != nil {
		return "detail_contract"
	}
	if len(rows) != len(requested) {
		return "detail_identity"
	}
	want := map[int64]bool{}
	for _, id := range requested {
		if id <= 0 || want[id] {
			return "detail_identity"
		}
		want[id] = true
	}
	actual := []string{}
	seen := map[int64]bool{}
	for _, r := range rows {
		if !want[r.ID] || seen[r.ID] {
			return "detail_identity"
		}
		seen[r.ID] = true
		actual = append(actual, r.Content)
	}
	return classify(actual, expected, flow)
}

func (e *engine) flow(ctx context.Context, index int, flow string) (l ledger) {
	if e.c.Mode == "all20" {
		return e.all20Flow(ctx, index, flow)
	}
	l = ledger{Cycle: index, Flow: flow}
	start := time.Now()
	defer func() {
		e.stats.record("workflow", float64(time.Since(start))/float64(time.Millisecond), l.Classification, true)
	}()
	device := fmt.Sprintf("phase19-%s-%d", e.runID, index)
	native := flow == "native"
	prefix := "/logan/web"
	upload := "/upload.json"
	expected := e.f.WebContents
	var body []byte
	headers := map[string]string{"Content-Type": "application/json"}
	if native {
		prefix = "/logan/task"
		expected = e.f.NativeContents
		body, _ = base64.StdEncoding.DecodeString(e.f.NativeBase64)
		upload = "/logan/upload.json"
		headers = map[string]string{"Content-Type": "application/octet-stream", "appId": "logan-load-test", "deviceId": device, "unionId": "phase19-synthetic", "platform": "1", "fileDate": "2026-10-15", "buildVersion": "1", "appVersion": "1"}
	} else {
		web := map[string]any{}
		for k, v := range e.f.WebBody {
			web[k] = v
		}
		web["deviceId"] = device
		body, _ = json.Marshal(web)
		upload = prefix + upload
	}
	uploadStart := e.stats.beginE2E(flow)
	var queryableElapsed time.Duration
	defer func() { e.stats.recordE2E(flow, l, uploadStart, queryableElapsed) }()
	_, k := e.call(ctx, flow+"-upload", "POST", upload, body, headers, func(d json.RawMessage) string {
		if len(d) == 0 {
			return "upload_contract"
		}
		return ""
	})
	if k != "" {
		l.Classification = k
		return
	}
	search := prefix + "/search.json?deviceId=" + device + "&beginTime=1&endTime=4102444800000"
	if native {
		search += "&platform=1"
	}
	_, k = e.call(ctx, flow+"-search", "GET", search, nil, nil, func(d json.RawMessage) string {
		var rows []struct {
			TaskID   int64  `json:"taskId"`
			DeviceID string `json:"deviceId"`
		}
		if json.Unmarshal(d, &rows) != nil || len(rows) != 1 || rows[0].TaskID <= 0 || rows[0].TaskID > 2147483647 || rows[0].DeviceID != device {
			return "task_discovery"
		}
		l.TaskID = rows[0].TaskID
		return ""
	})
	if k != "" {
		l.Classification = k
		return
	}
	task := strconv.FormatInt(l.TaskID, 10)
	path := prefix + "/detailIndex.json?tasks=" + task + "&logTypes=&keyword=&beginTime=00:00&endTime=23:59"
	if native {
		path = prefix + "/" + task + "/brief.json?logTypes=&keyword="
	}
	parseCtx, cancel := context.WithTimeout(ctx, e.c.ParseTimeout)
	defer cancel()
	for attempt := 0; attempt < 30; attempt++ {
		var raw json.RawMessage
		raw, k = e.call(parseCtx, flow+"-index", "GET", path, nil, nil, nil)
		if k != "" {
			l.Classification = k
			return
		}
		l.DetailIDs = ids(raw)
		if len(l.DetailIDs) > 0 {
			break
		}
		select {
		case <-parseCtx.Done():
			l.Classification = "parse_timeout"
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	if len(l.DetailIDs) == 0 {
		l.Classification = "parse_timeout"
		return
	}
	if len(l.DetailIDs) > 16 {
		l.Classification = "index_contract"
		return
	}
	parts := []string{}
	for _, id := range l.DetailIDs {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	path = prefix + "/details.json?detailIds=" + strings.Join(parts, ",")
	if native {
		path = prefix + "/query/details.json?detailIds=" + strings.Join(parts, ",")
	}
	_, k = e.call(ctx, flow+"-details", "GET", path, nil, nil, func(d json.RawMessage) string {
		return validateDetails(d, l.DetailIDs, expected, flow)
	})
	l.Classification = k
	if k == "" {
		queryableElapsed = time.Since(uploadStart)
	}
	return
}
func (e *engine) read(ctx context.Context, index int) ledger {
	seed := e.stats.Seeds[index%len(e.stats.Seeds)]
	prefix := "/logan/web/details.json?detailIds="
	expected := e.f.WebContents
	if seed.Flow == "native" {
		prefix = "/logan/task/query/details.json?detailIds="
		expected = e.f.NativeContents
	}
	parts := []string{}
	for _, id := range seed.DetailIDs {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	start := time.Now()
	_, k := e.call(ctx, seed.Flow+"-details", "GET", prefix+strings.Join(parts, ","), nil, nil, func(d json.RawMessage) string {
		return validateDetails(d, seed.DetailIDs, expected, seed.Flow)
	})
	e.stats.record("workflow", float64(time.Since(start))/float64(time.Millisecond), k, true)
	return ledger{Cycle: index, Flow: seed.Flow, TaskID: seed.TaskID, DetailIDs: seed.DetailIDs, Classification: k}
}
func (e *engine) snapshot(final bool) map[string]any {
	s := e.stats
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Since(s.Start).Seconds()
	routes := map[string]summary{}
	for k, v := range s.Routes {
		routes[k] = v.finish(elapsed)
	}
	windows := []window{}
	for i := 0; i <= int(elapsed/60); i++ {
		w := s.Windows[i]
		if w == nil {
			continue
		}
		secs := math.Min(60, elapsed-float64(i)*60)
		out := window{Minute: i, HTTP: w.HTTP.finish(secs), Workflows: w.Workflows.finish(secs), Routes: map[string]*summary{}}
		for k, v := range w.Routes {
			x := v.finish(secs)
			out.Routes[k] = &x
		}
		windows = append(windows, out)
	}
	return map[string]any{"upload_to_all_details": s.e2eReports(final), "max_concurrent_workflows": s.MaxConcurrentWorkflows, "final": final, "seed_failure": s.SeedFailure, "mode": e.c.Mode, "run_prefix": "phase19-" + e.runID, "schedule_seconds": e.c.Duration.Seconds(), "elapsed_seconds": elapsed, "start_utc": s.Start.UTC().Format(time.RFC3339Nano), "snapshot_utc": time.Now().UTC().Format(time.RFC3339Nano), "attempted": s.Attempted, "report_write_errors": s.ReportWriteErrors, "reconciled_arrivals": s.ReconciledArrivals, "arrival_accounting": "attempted is ideal scheduled arrivals; missed final ticks are dropped; interrupted runs retain actual offered arrivals", "dropped": s.Dropped, "interrupted": s.Interrupted, "http": s.HTTP.finish(elapsed), "workflows": s.Workflows.finish(elapsed), "routes": routes, "minutes": windows, "ledger": append([]ledger(nil), s.Ledger...), "seeds": s.Seeds}
}
func (e *engine) write(final bool) (err error) {
	defer func() {
		if err != nil {
			e.stats.mu.Lock()
			e.stats.ReportWriteErrors++
			e.stats.mu.Unlock()
		}
	}()
	b, err := json.Marshal(e.snapshot(final))
	if err != nil {
		return err
	}
	if e.c.Output != "" {
		tmp := e.c.Output + ".tmp"
		if err = os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
			return err
		}
		if err = os.Rename(tmp, e.c.Output); err != nil {
			return err
		}
	}
	if final {
		fmt.Println(string(b))
	}
	return nil
}

// Nextafter removes a multiplication rounding artifact at exact integral arrivals.
func scheduledArrivals(duration time.Duration, qps float64) int {
	return int(math.Ceil(math.Nextafter(duration.Seconds()*qps, 0)))
}
func (e *engine) run(parent context.Context) {
	s := e.stats
	start := time.Now()
	s.Start = start
	ctx, cancel := context.WithDeadline(parent, start.Add(e.c.Duration+e.c.Drain))
	defer cancel()
	jobs := make(chan int, e.c.Queue)
	var wg sync.WaitGroup
	for i := 0; i < e.c.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil || !time.Now().Before(start.Add(e.c.Duration)) {
					continue
				}
				flow := "native"
				if e.c.Mode == "web" || ((e.c.Mode == "mixed" || e.c.Mode == "all20") && index%2 == 1) {
					flow = "web"
				}
				s.mu.Lock()
				s.ActiveWorkflows++
				if s.ActiveWorkflows > s.MaxConcurrentWorkflows {
					s.MaxConcurrentWorkflows = s.ActiveWorkflows
				}
				s.mu.Unlock()
				var l ledger
				if e.c.Mode == "read" {
					l = e.read(ctx, index)
				} else {
					l = e.flow(ctx, index, flow)
				}
				s.mu.Lock()
				s.ActiveWorkflows--
				s.Ledger = append(s.Ledger, l)
				s.mu.Unlock()
			}
		}()
	}
	tick := time.NewTicker(time.Millisecond * 10)
	defer tick.Stop()
	progress := time.NewTicker(time.Minute)
	defer progress.Stop()
	offered := 0
loop:
	for {
		elapsed := time.Since(start)
		if elapsed >= e.c.Duration || parent.Err() != nil {
			break
		}
		target := int(math.Ceil(elapsed.Seconds() * e.c.QPS))
		if e.c.ArrivalBurst > 1 {
			target = int(math.Ceil(elapsed.Seconds()*e.c.QPS/float64(e.c.ArrivalBurst))) * e.c.ArrivalBurst
			ideal := scheduledArrivals(e.c.Duration, e.c.QPS)
			if target > ideal {
				target = ideal
			}
		}
		for offered < target {
			s.mu.Lock()
			s.Attempted++
			s.mu.Unlock()
			select {
			case jobs <- offered:
			default:
			}
			offered++
		}
		select {
		case <-parent.Done():
			break loop
		case <-progress.C:
			_ = e.write(false)
		case <-tick.C:
		}
	}
	s.mu.Lock()
	if parent.Err() == nil {
		s.ReconciledArrivals = scheduledArrivals(e.c.Duration, e.c.QPS) - s.Attempted
		s.Attempted += s.ReconciledArrivals
	}
	s.Interrupted = parent.Err() != nil
	s.mu.Unlock()
	close(jobs)
	wg.Wait()
	s.mu.Lock()
	s.Dropped = s.Attempted - s.Workflows.Sent
	s.mu.Unlock()
}
func main() {
	validateOnly := false
	c := defaults()
	flag.BoolVar(&c.LargeFixture, "large-fixture", false, "explicit bounded large synthetic fixture budget")
	flag.BoolVar(&validateOnly, "validate-only", false, "validate config and fixture without HTTP")
	flag.StringVar(&c.Base, "base-url", c.Base, "test origin optionally /logan-web")
	flag.StringVar(&c.Mode, "mode", c.Mode, "mixed, native, web, read or all20")
	flag.StringVar(&c.Fixture, "fixture", "", "validated synthetic fixture JSON")
	flag.StringVar(&c.Output, "output", "", "atomic report file")
	flag.StringVar(&c.Marker, "test-target-marker", "", "logan-load-test")
	flag.BoolVar(&c.AllowTarget, "allow-test-target", false, "")
	flag.BoolVar(&c.AllowWrites, "allow-writes", false, "")
	flag.DurationVar(&c.Duration, "duration", c.Duration, "")
	flag.DurationVar(&c.Timeout, "timeout", c.Timeout, "")
	flag.DurationVar(&c.ParseTimeout, "parse-timeout", c.ParseTimeout, "")
	flag.DurationVar(&c.Drain, "drain", c.Drain, "")
	flag.Float64Var(&c.QPS, "qps", c.QPS, "offered cycles/sec")
	flag.IntVar(&c.Workers, "concurrency", c.Workers, "")
	flag.IntVar(&c.ArrivalBurst, "arrival-burst", c.ArrivalBurst, "all20 simultaneous arrivals per burst, preserving average QPS")
	flag.IntVar(&c.Queue, "queue", c.Queue, "")
	flag.Parse()
	if err := validateConfig(c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	f, err := loadFixtureBounded(c.Fixture, c.LargeFixture)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if c.Mode == "all20" && (f.NativePlainBase64 == "" || f.NativeDownloadBase64 == "" || len(f.WebRawLogs) != len(f.WebContents)) {
		fmt.Fprintln(os.Stderr, "all20 download oracles required")
		os.Exit(2)
	}
	if validateOnly {
		fmt.Printf("{\"validated\":true,\"native_records\":%d,\"web_records\":%d}\n", len(f.NativeContents), len(f.WebContents))
		return
	}
	tr := &http.Transport{MaxConnsPerHost: c.Workers, MaxIdleConnsPerHost: c.Workers, MaxIdleConns: c.Workers}
	defer tr.CloseIdleConnections()
	e := &engine{c: c, f: f, stats: newStats(), runID: strconv.FormatInt(time.Now().UnixNano(), 36), client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if c.Mode == "read" {
		for i, flow := range []string{"native", "web"} {
			seed := e.flow(ctx, -i-1, flow)
			e.stats.Seeds = append(e.stats.Seeds, seed)
			if seed.Classification != "" {
				e.stats.SeedFailure = seed.Classification
				e.stats.Interrupted = ctx.Err() != nil
				if err := e.write(true); err != nil {
					fmt.Fprintln(os.Stderr, "seed diagnostic report write failed")
					os.Exit(2)
				}
				fmt.Fprintln(os.Stderr, "seed failed: "+seed.Classification)
				os.Exit(1)
			}
		}
		seeds := e.stats.Seeds
		e.stats = newStats()
		e.stats.Seeds = seeds
	}
	e.run(ctx)
	if err = e.write(true); err != nil {
		fmt.Fprintln(os.Stderr, "report write failed")
		os.Exit(2)
	}
	if e.stats.ReportWriteErrors > 0 {
		os.Exit(2)
	}
	if e.stats.Interrupted {
		os.Exit(130)
	}
	if e.stats.Workflows.Errors > 0 || e.stats.Workflows.Success == 0 || e.stats.Dropped > 0 {
		os.Exit(1)
	}
}
