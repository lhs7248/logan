package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestActualCLoganUploadParseAndQueryRecovery(t *testing.T) {
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
	cfg.Native = config.Native{Enabled: true, Key: []byte("0123456789012345"), IV: []byte("0123456789012345"), TempDir: t.TempDir(), MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 2, Timeout: time.Second}
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

	wire, err := os.ReadFile("testdata/protocol/clogan-regression/native.bin")
	if err != nil {
		t.Fatal(err)
	}
	response := ut.PerformRequest(h.Engine, "POST", "/logan/upload.json", &ut.Body{Body: bytes.NewReader(wire), Len: len(wire)}, ut.Header{Key: "deviceId", Value: "sdk-regression"}, ut.Header{Key: "platform", Value: "1"}, ut.Header{Key: "fileDate", Value: "2026-10-16"})
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"code":200`)) {
		t.Fatalf("upload: %d %s", response.Code, response.Body)
	}
	for attempt := 0; attempt < 2; attempt++ {
		brief := ut.PerformRequest(h.Engine, "GET", "/logan/task/1/brief.json", nil)
		if brief.Code != 200 || !bytes.Contains(brief.Body.Bytes(), []byte(`"code":200`)) {
			t.Fatalf("brief: %s", brief.Body)
		}
		repo.mu.Lock()
		rows := append([]repository.NativeDetail(nil), repo.details...)
		repo.mu.Unlock()
		if len(rows) != 2 {
			t.Fatalf("SDK records=%d want 2: %s", len(rows), brief.Body)
		}
		found := 0
		for _, row := range rows {
			if row.Content == "SDK_CHAIN_NATIVE_20261016 中文 🌏 + percent%" {
				found++
				// Original Java projects unknown SDK type 7 to UNKNOWN (0).
				if row.LogType != 0 || row.LogTime != 1792108800123 {
					t.Fatalf("semantic mismatch: %+v", row)
				}
				detail := ut.PerformRequest(h.Engine, "GET", fmt.Sprintf("/logan/task/%d/detail.json", row.ID), nil)
				var envelope struct {
					Code int
					Data struct{ FormatContent string }
				}
				if json.Unmarshal(detail.Body.Bytes(), &envelope) != nil || envelope.Code != 200 || envelope.Data.FormatContent != row.Content {
					t.Fatalf("detail %s", detail.Body)
				}
			}
		}
		if found != 1 {
			t.Fatal("target SDK record missing or duplicated")
		}
	}
	mu.Lock()
	if puts != 1 || gets != 1 {
		t.Errorf("puts=%d gets=%d", puts, gets)
	}
	mu.Unlock()
	if r := ut.PerformRequest(h.Engine, "GET", "/readyz", nil); r.Code != 503 {
		t.Fatal("readiness must remain blocked")
	}
	for _, dir := range []string{cfg.Native.TempDir, parseTemp} {
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatalf("temporary files leaked in %s", dir)
		}
	}
}
