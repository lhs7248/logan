package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/router"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/repository"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type streamStore struct {
	mu   sync.Mutex
	data []byte
}

func (s *streamStore) Put(_ context.Context, _ string, r io.ReadSeeker, _ int64) error {
	b, err := io.ReadAll(r)
	s.mu.Lock()
	s.data = b
	s.mu.Unlock()
	return err
}
func (s *streamStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("unused")
}
func (s *streamStore) Sign(context.Context, string) (string, error) {
	return "https://example.test/file", nil
}
func (s *streamStore) Delete(context.Context, string) error { return nil }

type streamTasks struct{}

func (*streamTasks) Create(_ context.Context, task *repository.NativeTask) error {
	task.ID = 1
	return nil
}
func (*streamTasks) Find(context.Context, int64) (*repository.NativeTask, error) { return nil, nil }
func startNativeTCP(t *testing.T, timeout time.Duration, queries ...*handler.NativeQueries) (string, config.Native, *streamStore) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(func(string) string { return "" })
	c.ReadTimeout = 3 * time.Second
	c.Native = config.Native{Enabled: true, Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: t.TempDir(), MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 1, Timeout: timeout}
	store := &streamStore{}
	svc, err := native.NewService(c.Native, c.Timezone, store, &streamTasks{})
	if err != nil {
		t.Fatal(err)
	}
	options := append(serverOptions(c), server.WithListener(ln))
	h := server.New(options...)
	h.Use(handler.StreamBodyGuard("/logan/upload.json"))
	router.RegisterLegacyWithWeb(h, "", handler.NewNative(svc, timeout), nil, queries...)
	h.GET("/livez", func(_ context.Context, c *app.RequestContext) { c.Status(200) })
	done := make(chan error, 1)
	go func() { done <- h.Run() }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + ln.Addr().String() + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	client.CloseIdleConnections()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("HTTP server did not stop")
		}
	})
	return ln.Addr().String(), c.Native, store
}
func TestNativeRealTCPStreamingMatchesJavaLargeFrame(t *testing.T) {
	addr, cfg, store := startNativeTCP(t, time.Second)
	var connections atomic.Int32
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		connections.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	raw, err := os.ReadFile("testdata/protocol/java-native-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases []struct{ Name, Wire, Compressed string }
	}
	_ = json.Unmarshal(raw, &data)
	for _, v := range data.Cases {
		if v.Name == "multi-chunk-frame" {
			wire, _ := base64.StdEncoding.DecodeString(v.Wire)
			expected, _ := base64.StdEncoding.DecodeString(v.Compressed)
			for _, chunked := range []bool{false, true} {
				req, _ := http.NewRequest("POST", "http://"+addr+"/logan/upload.json", bytes.NewReader(wire))
				if chunked {
					req.Body = io.NopCloser(bytes.NewReader(wire))
					req.ContentLength = -1
				}
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if response.Close {
					t.Fatal("successful upload closed connection")
				}
				health, healthErr := client.Get("http://" + addr + "/livez")
				if healthErr != nil {
					t.Fatal(healthErr)
				}
				_, _ = io.Copy(io.Discard, health.Body)
				_ = health.Body.Close()
				if health.Close {
					t.Fatal("bodyless GET closed connection")
				}
				if connections.Load() != 1 {
					t.Fatalf("connection not reused: %d", connections.Load())
				}
				if response.StatusCode != 200 || !strings.Contains(string(body), `"code":200`) {
					t.Fatalf("streaming upload: %d %s", response.StatusCode, body)
				}
				store.mu.Lock()
				equal := bytes.Equal(store.data, expected)
				store.mu.Unlock()
				if !equal {
					t.Fatal("streamed compressed bytes differ from Java")
				}
			}
		}
	}
	files, _ := os.ReadDir(cfg.TempDir)
	if len(files) != 0 {
		t.Fatal("streamed upload temp files leaked")
	}
}
func TestNativeTCPTimeoutAndRejectedBodyStopReading(t *testing.T) {
	for _, mode := range []string{"slow-fixed", "slow-chunked", "rejected-fixed", "rejected-chunked"} {
		t.Run(mode, func(t *testing.T) {
			addr, cfg, _ := startNativeTCP(t, 80*time.Millisecond)
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			payload := append([]byte{1, 0, 2, 0, 0}, make([]byte, 65536)...)
			if strings.HasPrefix(mode, "rejected") {
				payload[1] = 255
				payload[2] = 255
				payload[3] = 255
				payload[4] = 255
			}
			if strings.HasSuffix(mode, "chunked") {
				_, err = fmt.Fprintf(conn, "POST /logan/upload.json HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n", len(payload))
				if err == nil {
					_, err = conn.Write(payload)
				}
				if err == nil {
					_, err = io.WriteString(conn, "\r\n")
				}
			} else {
				_, err = fmt.Fprintf(conn, "POST /logan/upload.json HTTP/1.1\r\nHost: localhost\r\nContent-Length: 500000\r\n\r\n")
				if err == nil {
					_, err = conn.Write(payload)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
			reader := bufio.NewReader(conn)
			response, err := http.ReadResponse(reader, nil)
			if err != nil {
				t.Fatalf("upload did not stop within request budget: %v", err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != 200 || !strings.Contains(string(body), "ERROR_DECRYPT") {
				t.Fatalf("timeout response changed: %s", body)
			}
			_, err = reader.ReadByte()
			if err == nil {
				t.Fatal("connection accepted more data after rejection")
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("server kept draining unread body after rejection")
			}
			entries, _ := os.ReadDir(cfg.TempDir)
			if len(entries) != 0 {
				t.Fatal("timeout left temporary upload file")
			}
		})
	}
}
