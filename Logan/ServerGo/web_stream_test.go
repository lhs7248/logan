package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/router"
	"logan/servergo/internal/config"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/weblog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebRealTCPStreamUploadParseAndReuse(t *testing.T) {
	repo := &webMemory{tasks: map[int64]repository.WebTask{}, pending: map[int64]bool{}}
	svc := newWebTestService(t, repo)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(func(string) string { return "" })
	cfg.ContextPath = ""
	cfg.Web = config.Web{Enabled: true, Timeout: time.Second}
	h := server.New(append(serverOptions(cfg), server.WithListener(ln))...)
	h.Use(handler.StreamBodyGuard("/logan/web/upload.json"))
	router.RegisterLegacyWithWeb(h, "", nil, handler.NewWeb(svc, 1<<20, time.Second))
	done := make(chan error, 1)
	go func() { done <- h.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Web server failed to stop")
		}
	})
	var connections atomic.Int32
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		connections.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	base := "http://" + ln.Addr().String()
	line := `{"t":1,"c":"` + strings.Repeat("x", 70000) + `","d":1609459200000}`
	envelope, _ := json.Marshal(map[string]any{"l": base64.StdEncoding.EncodeToString([]byte(line))})
	for page := 1; page <= 2; page++ {
		payload, _ := json.Marshal(weblog.Upload{DeviceID: "d", LogPageNo: int32(page), FileDate: "2021-01-01", LogArray: url.QueryEscape(string(envelope))})
		request, _ := http.NewRequest("POST", base+"/logan/web/upload.json", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		if page == 2 {
			request.Body = io.NopCloser(bytes.NewReader(payload))
			request.ContentLength = -1
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || !bytes.Contains(body, []byte(`"code":200`)) || response.Close {
			t.Fatalf("stream upload %d %s close=%v", response.StatusCode, body, response.Close)
		}
	}
	response, err := client.Get(base + "/logan/web/detailIndex.json?tasks=1,2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(body, []byte(`"detailId":7`)) || !bytes.Contains(body, []byte(`"detailId":8`)) {
		t.Fatalf("parse chain: %d %s", response.StatusCode, body)
	}
	if response.Close || connections.Load() != 1 {
		t.Fatalf("Web connection not reused: %d close=%v", connections.Load(), response.Close)
	}
	for _, chunked := range []bool{false, true} {
		payload := strings.Repeat("x", (1<<20)+1)
		req, _ := http.NewRequest("POST", base+"/logan/web/upload.json", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if chunked {
			req.Body = io.NopCloser(strings.NewReader(payload))
			req.ContentLength = -1
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 413 || !response.Close {
			t.Fatalf("oversized Web body accepted or drained: %d close=%v", response.StatusCode, response.Close)
		}
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.details) != 2 || len(repo.details[0].Content) != 70000 || repo.parseCalls != 2 {
		t.Fatal("stream upload content lost or repeated")
	}
}
