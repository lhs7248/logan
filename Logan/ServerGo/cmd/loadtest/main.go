// loadtest sends bounded synthetic traffic to an explicitly designated test target.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	Base, Mode, Fixture, Marker string
	Duration, Timeout, Ramp     time.Duration
	QPS, Workers, Queue         int
	AllowTarget, AllowWrites    bool
}
type fixture struct {
	Synthetic        bool `json:"synthetic"`
	TaskID, DetailID int64
	UploadFile       string
	Headers          map[string]string
}
type request struct {
	path, method string
	body         []byte
	headers      map[string]string
}
type report struct {
	ScheduleSeconds float64        `json:"schedule_seconds"`
	Mode            string         `json:"mode"`
	Scope           string         `json:"scope"`
	Attempted       int            `json:"attempted"`
	Sent            int            `json:"sent"`
	Completed       int            `json:"completed"`
	Success         int            `json:"success"`
	Errors          int            `json:"errors"`
	Timeouts        int            `json:"timeouts"`
	Dropped         int            `json:"dropped"`
	Interrupted     bool           `json:"interrupted"`
	Elapsed         float64        `json:"elapsed_seconds"`
	RPS             float64        `json:"achieved_rps"`
	P50             float64        `json:"p50_ms"`
	P95             float64        `json:"p95_ms"`
	P99             float64        `json:"p99_ms"`
	Max             float64        `json:"max_ms"`
	ErrorKinds      map[string]int `json:"error_kinds"`
}

func main() { os.Exit(cli()) }
func cli() int {
	c := config{}
	output := ""
	flag.StringVar(&c.Base, "base-url", "http://127.0.0.1:8080", "test server origin, optionally /logan-web prefix")
	flag.StringVar(&c.Mode, "mode", "health", "health, read, mixed, upload")
	flag.StringVar(&c.Fixture, "fixture", "", "synthetic fixture JSON")
	flag.StringVar(&c.Marker, "test-target-marker", "", "must equal logan-load-test for business modes")
	flag.BoolVar(&c.AllowTarget, "allow-test-target", false, "explicitly designate target as a test environment")
	flag.BoolVar(&c.AllowWrites, "allow-writes", false, "allow uploads and lazy parsing in business modes")
	flag.DurationVar(&c.Duration, "duration", 10*time.Second, "fixed scheduling duration (max 1h)")
	flag.DurationVar(&c.Timeout, "timeout", 2*time.Second, "per request timeout (max 1m)")
	flag.DurationVar(&c.Ramp, "ramp", 0, "linear QPS ramp duration")
	flag.IntVar(&c.QPS, "qps", 5, "maximum offered QPS")
	flag.IntVar(&c.Workers, "concurrency", 2, "fixed worker count (max 1024)")
	flag.IntVar(&c.Queue, "queue", 2, "bounded pending request queue (max 65536)")
	flag.StringVar(&output, "output", "", "optional private JSON report file")
	flag.Parse()
	reqs, err := prepare(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r := run(ctx, c, reqs, nil)
	raw, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(raw))
	if output != "" {
		if err := os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write report")
			return 2
		}
	}
	if r.Interrupted {
		return 130
	}
	if r.Errors > 0 || r.Success == 0 || r.Dropped > 0 {
		return 1
	}
	return 0
}
func prepare(c config) ([]request, error) {
	bad := func(s string) ([]request, error) { return nil, errors.New(s) }
	u, e := url.Parse(c.Base)
	if e != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/logan-web" {
		return bad("base-url must be an origin, optionally /logan-web, without credentials/query/fragment")
	}
	if !c.AllowTarget {
		return bad("--allow-test-target is required; use only an isolated test environment")
	}
	if c.Duration <= 0 || c.Duration > time.Hour || c.Timeout <= 0 || c.Timeout > time.Minute || c.QPS < 1 || c.QPS > 100000 || c.Workers < 1 || c.Workers > 1024 || c.Queue < 0 || c.Queue > 65536 || c.Ramp < 0 || c.Ramp > c.Duration || float64(c.QPS)*c.Duration.Seconds() > 1000000 {
		return bad("invalid bounds: duration <=1h, timeout <=1m, QPS <=100000, total offered <=1000000")
	}
	if c.Mode == "health" {
		return []request{{path: "/ping", method: "GET"}}, nil
	}
	if c.Mode != "read" && c.Mode != "mixed" && c.Mode != "upload" {
		return bad("unknown mode")
	}
	if !c.AllowWrites || c.Marker != "logan-load-test" {
		return bad("business traffic requires --allow-writes --test-target-marker logan-load-test (queries can trigger parsing)")
	}
	raw, e := readBounded(c.Fixture, 65536)
	if e != nil {
		return bad("cannot read fixture")
	}
	var f fixture
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&f) != nil || !f.Synthetic {
		return bad("fixture must contain synthetic:true and known fields only")
	}
	if f.TaskID <= 0 || f.TaskID > 2147483647 || f.DetailID <= 0 || f.DetailID > 2147483647 {
		return bad("positive fixture TaskID and DetailID <= int32 required")
	}
	reqs := []request{{path: fmt.Sprintf("/logan/task/%d/brief.json", f.TaskID), method: "GET"}, {path: fmt.Sprintf("/logan/task/%d/detail.json", f.DetailID), method: "GET"}}
	if c.Mode == "read" {
		return reqs, nil
	}
	body, e := readBounded(f.UploadFile, 1<<20)
	if e != nil || !validFrame(body) {
		return bad("upload requires a nonempty bounded native SDK frame file")
	}
	allowed := map[string]bool{"appId": true, "deviceId": true, "unionId": true, "platform": true, "buildVersion": true, "appVersion": true, "fileDate": true}
	safe := regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,80}$`)
	for k, v := range f.Headers {
		if !allowed[k] || !safe.MatchString(v) {
			return bad("upload metadata contains unapproved header/value")
		}
	}
	if f.Headers["appId"] != "logan-load-test" || f.Headers["deviceId"] != "synthetic-load-test" || f.Headers["fileDate"] != "2020-01-01" || f.Headers["platform"] != "1" {
		return bad("upload metadata must use documented synthetic identity/date/platform")
	}
	up := request{path: "/logan/upload.json", method: "POST", body: body, headers: f.Headers}
	if c.Mode == "upload" {
		return []request{up}, nil
	}
	return append(reqs, up), nil
}
func readBounded(path string, max int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, errors.New("size limit")
	}
	return b, e
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
func validate(resp *http.Response, mode string, paths ...string) string {
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil {
		return "body_read"
	}
	if len(b) > 1<<20 {
		return "body_limit"
	}
	if resp.StatusCode != 200 {
		return "http_status"
	}
	var v struct {
		Code    *int            `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &v) != nil {
		return "invalid_json"
	}
	if mode == "health" {
		if v.Message != "pong" {
			return "health_contract"
		}
		return ""
	}
	if v.Code == nil || *v.Code != 200 {
		return "business_code"
	}
	if len(v.Data) == 0 || string(v.Data) == "null" {
		return "missing_fixture_data"
	}
	path := ""
	if len(paths) > 0 {
		path = paths[0]
	}
	switch {
	case strings.HasSuffix(path, "/brief.json"):
		var rows [][]struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(v.Data, &rows) != nil || len(rows) == 0 {
			return "missing_fixture_data"
		}
		for _, page := range rows {
			if len(page) == 0 {
				return "missing_fixture_data"
			}
			for _, row := range page {
				if row.ID <= 0 {
					return "fixture_contract"
				}
			}
		}
	case strings.HasSuffix(path, "/detail.json"):
		var id int64
		fmt.Sscanf(path, "/logan/task/%d/detail.json", &id)
		var row struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(v.Data, &row) != nil || id <= 0 || row.ID != id {
			return "fixture_contract"
		}
	case path == "/logan/upload.json":
		var location string
		if json.Unmarshal(v.Data, &location) != nil {
			return "upload_contract"
		}
		u, e := url.Parse(location)
		if e != nil || u.Scheme != "" || u.Host != "" || u.Path != "/logan/downing" || u.Query().Get("name") == "" {
			return "upload_contract"
		}
	}
	return ""
}
func offeredTarget(c config, elapsed time.Duration) int {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > c.Duration {
		elapsed = c.Duration
	}
	seconds := elapsed.Seconds()
	area := seconds * float64(c.QPS)
	if c.Ramp > 0 {
		ramp := c.Ramp.Seconds()
		if seconds < ramp {
			area = float64(c.QPS) * seconds * seconds / (2 * ramp)
		} else {
			area = float64(c.QPS) * (seconds - ramp/2)
		}
	}
	return int(math.Ceil(area))
}
func run(parent context.Context, c config, reqs []request, client *http.Client) report {
	r := report{ScheduleSeconds: c.Duration.Seconds(), Mode: c.Mode, Scope: "synthetic business workload; not a capacity certification", ErrorKinds: map[string]int{}}
	if c.Mode == "health" {
		r.Scope = "HTTP health smoke only; not a business benchmark"
	}
	if client == nil {
		tr := &http.Transport{MaxConnsPerHost: c.Workers, MaxIdleConnsPerHost: c.Workers, MaxIdleConns: c.Workers}
		defer tr.CloseIdleConnections()
		client = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, c.Duration)
	defer cancel()
	jobs := make(chan int, c.Queue)
	var wg sync.WaitGroup
	var mu sync.Mutex
	lat := make([]float64, 0)
	for i := 0; i < c.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case idx, ok := <-jobs:
					if !ok {
						return
					}
					if ctx.Err() != nil {
						return
					}
					one, cancel := context.WithTimeout(parent, c.Timeout)
					q := reqs[idx%len(reqs)]
					req, _ := http.NewRequestWithContext(one, q.method, c.Base+q.path, bytes.NewReader(q.body))
					for k, v := range q.headers {
						req.Header.Set(k, v)
					}
					if q.method == "POST" {
						req.Header.Set("Content-Type", "application/octet-stream")
					}
					t := time.Now()
					mu.Lock()
					r.Sent++
					mu.Unlock()
					resp, err := client.Do(req)
					kind := ""
					timeout := false
					if err != nil {
						kind = "transport"
						timeout = errors.Is(one.Err(), context.DeadlineExceeded)
					} else {
						kind = validate(resp, c.Mode, q.path)
						if kind != "" {
							timeout = errors.Is(one.Err(), context.DeadlineExceeded)
						}
					}
					cancel()
					ms := float64(time.Since(t)) / float64(time.Millisecond)
					mu.Lock()
					r.Completed++
					lat = append(lat, ms)
					if kind == "" {
						r.Success++
					} else {
						r.Errors++
						r.ErrorKinds[kind]++
						if timeout {
							r.Timeouts++
						}
					}
					mu.Unlock()
				}
			}
		}()
	}
	// Offer index zero immediately; later offers follow the ceiling of the rate integral.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	scheduled := 0
	offer := func(target int) {
		for scheduled < target {
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			r.Attempted++
			mu.Unlock()
			select {
			case jobs <- scheduled:
			default:
			}
			scheduled++
		}
	}
	if parent.Err() == nil {
		offer(1)
	}
schedule:
	for {
		select {
		case <-ctx.Done():
			break schedule
		case <-ticker.C:
			offer(offeredTarget(c, time.Since(start)))
		}
	}
	// Reconcile ticks never observed by the scheduler without starting work after the deadline.
	window := time.Since(start)
	if parent.Err() == nil {
		window = c.Duration
	}
	target := offeredTarget(c, window)
	mu.Lock()
	if target > r.Attempted {
		r.Attempted = target
	}
	mu.Unlock()
	close(jobs)
	wg.Wait()
	r.Dropped = r.Attempted - r.Sent
	r.Interrupted = parent.Err() != nil
	r.Elapsed = time.Since(start).Seconds()
	r.RPS = float64(r.Success) / r.Elapsed
	sort.Float64s(lat)
	if len(lat) > 0 {
		pct := func(p float64) float64 { i := int(float64(len(lat)-1) * p); return lat[i] }
		r.P50 = pct(.5)
		r.P95 = pct(.95)
		r.P99 = pct(.99)
		r.Max = lat[len(lat)-1]
	}
	return r
}
