package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAll20ModeAccepted(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	if err := validateConfig(c); err != nil {
		t.Fatalf("all20 mode rejected: %v", err)
	}
}

func TestAll20RejectsEmptyLatestAndForeignRedirect(t *testing.T) {
	if latestContract([]byte(`[]`)) == "" {
		t.Fatal("empty latest accepted")
	}
	base := "http://logan-verifier-0123456789abcdef0123456789abcdef-go:8080/logan-web"
	for _, target := range []string{"http://example.com:8080/x?Signature=a", "http://fixture.logan-verifier-ffffffffffffffffffffffffffffffff-g-o:8080/x?Signature=a", "http://fixture.logan-verifier-0123456789abcdef0123456789abcdef-g-o:8080/x"} {
		if ownedOSS(base, target) {
			t.Fatal("unsafe redirect accepted")
		}
	}
	if !ownedOSS(base, "http://fixture.logan-verifier-0123456789abcdef0123456789abcdef-g-o:8080/x?Signature=a") {
		t.Fatal("own OSS rejected")
	}
}

func TestAll20FixtureBudgetRequiresOptIn(t *testing.T) {
	f := fixture{NativeBase64: "AQAAABCqqqqqqqqqqqqqqqqqqqqqAA==", NativeContents: make([]string, 17), WebContents: []string{"b"}, WebBody: map[string]any{"fileDate": "2026-10-17"}}
	raw, _ := json.Marshal(f)
	path := t.TempDir() + "/fixture.json"
	os.WriteFile(path, raw, 0600)
	if _, err := loadFixture(path); err == nil {
		t.Fatal("normal fixture limit expanded")
	}
	if _, err := loadFixtureBounded(path, true); err != nil {
		t.Fatal(err)
	}
	f.NativeContents[0] = strings.Repeat("x", 65536)
	raw, _ = json.Marshal(f)
	os.WriteFile(path, raw, 0600)
	if _, err := loadFixtureBounded(path, true); err == nil {
		t.Fatal("native TEXT limit exceeded")
	}
}

func TestAll20HTTPContractsAndBatches(t *testing.T) {
	for _, flow := range []string{"native", "web"} {
		for _, bad := range []string{"", "identity", "download", "latest", "stale-latest", "wrong-device-latest", "upload", "single", "task", "object", "redirect"} {
			t.Run(flow+bad, func(t *testing.T) {
				if flow == "web" && (bad == "object" || bad == "redirect") {
					t.Skip("native object contract")
				}
				count := 201
				contents := make([]string, count)
				for i := range contents {
					contents[i] = fmt.Sprintf("content-%d", i)
				}
				var packed bytes.Buffer
				z := gzip.NewWriter(&packed)
				z.Write([]byte("exact plaintext\n"))
				z.Close()
				device := "phase19-test-0"
				downloadURL := "/logan/downing?name=object"
				batches := 0
				objectGET := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/object" {
						if bad == "object" {
							w.Write([]byte("wrong"))
							return
						}
						objectGET++
						w.Write(packed.Bytes())
						return
					}
					if r.URL.Path == "/logan/downing" {
						w.Header().Set("Location", "http://fixture.logan-verifier-0123456789abcdef0123456789abcdef-g-o:8080/object?Signature=synthetic")
						if bad == "redirect" {
							w.Header().Set("Location", "http://external.invalid:8080/?Signature=x")
						}
						w.WriteHeader(302)
						return
					}
					if r.URL.Path == "/logan/web/download.json" {
						w.Header().Set("Content-Type", "application/octet-stream")
						w.WriteHeader(201)
						logs := []string{"raw"}
						if bad == "download" {
							logs = []string{"wrong"}
						}
						json.NewEncoder(w).Encode(logs)
						return
					}
					var data any = "ok"
					p := r.URL.Path
					switch {
					case strings.Contains(p, "upload"):
						if bad == "" {
							time.Sleep(10 * time.Millisecond)
						}
						data = true
						if flow == "native" {
							data = downloadURL
						}
						if bad == "upload" {
							data = false
						}
					case strings.Contains(p, "search") || strings.Contains(p, "latest"):
						data = []any{map[string]any{"taskId": 1, "deviceId": device, "logDate": 1, "addTime": 1}}
						if strings.Contains(p, "latest") && bad == "stale-latest" {
							data = []any{map[string]any{"taskId": 99, "deviceId": device, "logDate": 1, "addTime": 1}}
						}
						if strings.Contains(p, "latest") && bad == "wrong-device-latest" {
							data = []any{map[string]any{"taskId": 1, "deviceId": "phase19-stale-0", "logDate": 1, "addTime": 1}}
						}
						if strings.Contains(p, "latest") && bad == "latest" {
							data = []any{}
						}
					case strings.Contains(p, "logtypes"):
						data = []any{map[string]any{"first": 1, "second": "日志类型1"}, map[string]any{"first": 2, "second": "日志类型2"}, map[string]any{"first": 3, "second": "日志类型3"}}
					case strings.Contains(p, "brief") || strings.Contains(p, "detailIndex"):
						rows := []any{}
						for i := 0; i < count; i++ {
							rows = append(rows, map[string]any{"detailId": i + 2})
						}
						data = []any{rows}
					case strings.Contains(p, "info") || strings.Contains(p, "taskDetail"):
						data = map[string]any{"taskId": 1, "deviceId": device}
						if bad == "task" {
							data = map[string]any{"taskId": 2, "deviceId": device}
						}
					case strings.Contains(p, "details"):
						if bad == "" {
							time.Sleep(10 * time.Millisecond)
						}
						batches++
						parts := strings.Split(r.URL.Query().Get("detailIds"), ",")
						if len(parts) > 200 {
							t.Error("detail batch exceeds API limit")
						}
						rows := []any{}
						for _, v := range parts {
							id, _ := strconv.Atoi(v)
							rowID := id
							if bad == "identity" {
								rowID = 99999
							}
							rows = append(rows, map[string]any{"id": rowID, "content": contents[id-2]})
						}
						data = rows
					case strings.Contains(p, "/detail.json") || strings.Contains(p, "logDetail"):
						if bad == "" {
							time.Sleep(30 * time.Millisecond)
						}
						data = map[string]any{"id": 2, "content": contents[0]}
						if bad == "single" {
							data = map[string]any{"id": 2, "content": "wrong"}
						}
					case strings.Contains(p, "exception"):
						data = "exception"
					case strings.Contains(p, "download") || strings.Contains(p, "getDownLoad"):
						data = downloadURL
						if flow == "web" {
							data = "/logan/web/download.json?tasks=1"
						}
						if bad == "download" && flow == "native" {
							data = "/logan/downing?name=wrong"
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data})
				}))
				defer server.Close()
				tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
				}}
				defer tr.CloseIdleConnections()
				c := defaults()
				c.Mode = "all20"
				c.Base = "http://logan-verifier-0123456789abcdef0123456789abcdef-go:8080"
				e := &engine{c: c, stats: newStats(), runID: "test", client: &http.Client{Transport: tr}, f: fixture{NativeContents: contents, WebContents: contents, NativeDownloadBase64: base64.StdEncoding.EncodeToString(packed.Bytes()), NativePlainBase64: base64.StdEncoding.EncodeToString([]byte("exact plaintext\n")), WebRawLogs: []string{"raw"}, WebBody: map[string]any{"fileDate": "2026-10-17"}}}
				l := e.flow(context.Background(), 0, flow)
				rawReport, _ := json.Marshal(e.snapshot(true)["upload_to_all_details"])
				var report map[string]struct {
					Samples []struct {
						Queryable bool
						ElapsedMS float64
						Failure   string
					}
					Censored     int
					P95MS, P99MS *float64
				}
				errReport := json.Unmarshal(rawReport, &report)
				ok := errReport == nil
				if !ok || len(report[flow].Samples) != 1 {
					t.Fatal("missing per-flow end-to-end sample")
				}
				sample := report[flow].Samples[0]
				// Later route failures must not erase the already proven detail milestone.
				reached := bad == "" || bad == "single" || bad == "download" || bad == "object" || bad == "redirect"
				if sample.Queryable != reached || sample.ElapsedMS <= 0 || sample.Failure != l.Classification {
					t.Fatalf("incorrect milestone sample: %+v, workflow=%s", sample, l.Classification)
				}
				if reached && (report[flow].Censored != 0 || report[flow].P95MS == nil || report[flow].P99MS == nil) {
					t.Fatalf("later failure erased completed milestone: %+v", report[flow])
				}
				if !reached && (report[flow].Censored != 1 || report[flow].P95MS != nil || report[flow].P99MS != nil) {
					t.Fatalf("failed workflow disappeared from percentiles: %+v", report[flow])
				}

				if bad == "" {
					if sample.ElapsedMS < 30 || e.stats.Workflows.All[0]-sample.ElapsedMS < 30 {
						t.Fatalf("milestone must include upload and both detail batches, excluding later routes: %+v, workflow=%v", sample, e.stats.Workflows.All)
					}
					if l.Classification != "" || batches != 2 || len(l.DetailIDs) != count {
						t.Fatal(l.Classification, batches, len(l.DetailIDs))
					}
					if flow == "native" && objectGET != 1 {
						t.Fatal("object not read")
					}
					if len(e.stats.Routes) != 10 {
						t.Fatal("missing route", e.stats.Routes)
					}
				} else if l.Classification == "" {
					t.Fatal("false success", bad)
				}
			})
		}
	}
}

func TestAll20LargeTimeoutsAreExplicit(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	c.Timeout = 30 * time.Second
	c.ParseTimeout = 60 * time.Second
	c.Drain = 60 * time.Second
	if validateConfig(c) == nil {
		t.Fatal("normal time bounds expanded")
	}
	c.LargeFixture = true
	if err := validateConfig(c); err != nil {
		t.Fatal("large profile bounded timeouts rejected", err)
	}
	c.Timeout += time.Second
	if validateConfig(c) == nil {
		t.Fatal("large timeout unbounded")
	}
}
func TestAll20WebWireBudget(t *testing.T) {
	f := fixture{WebBody: map[string]any{"logArray": strings.Repeat("x", (32<<20)+1)}}
	if validateExtraFixture(f, true) == nil {
		t.Fatal("oversized web HTTP body accepted")
	}
}
func TestAll20GzipMembersAndPadding(t *testing.T) {
	var b bytes.Buffer
	for _, v := range []string{"one", "two"} {
		z := gzip.NewWriter(&b)
		z.Write([]byte(v))
		z.Close()
		b.Write([]byte{2, 2})
	}
	got, err := nativePlain(b.Bytes())
	if err != nil || string(got) != "onetwo" {
		t.Fatal(string(got), err)
	}
	for _, bad := range [][]byte{[]byte("invalid"), append(b.Bytes(), make([]byte, 17)...)} {
		if _, err := nativePlain(bad); err == nil {
			t.Fatal("invalid gzip accepted")
		}
	}
}

func TestAll20RawLogAndInjectedDeviceBudget(t *testing.T) {
	for _, f := range []fixture{
		{WebRawLogs: []string{strings.Repeat("x", (4<<20)+1)}},
		{WebBody: map[string]any{"logArray": strings.Repeat("x", (32<<20)-30)}},
	} {
		if validateExtraFixture(f, true) == nil {
			t.Fatal("large fixture overflow accepted")
		}
	}
}
