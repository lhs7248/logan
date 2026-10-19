package storage

import (
	"bytes"
	"context"
	"io"
	"logan/servergo/internal/config"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOSSActualSDKUploadReadSignAndDelete(t *testing.T) {
	var mu sync.Mutex
	var object []byte
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/test-bucket/logan-logs/test.log" {
			t.Errorf("wrong key %s", r.URL.Path)
		}
		if r.Method == "PUT" {
			if r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Content-Encoding") != "" {
				t.Error("changed compressed object metadata")
			}
			object, _ = io.ReadAll(r.Body)
			w.Header().Set("ETag", `"test"`)
		}
		if r.Method == "GET" {
			_, _ = w.Write(object)
		}
		if r.Method == "DELETE" {
			deleted = true
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	store, err := NewOSS(config.OSS{Endpoint: srv.URL, Region: "cn-hangzhou", Bucket: "test-bucket", Prefix: "logan-logs/", AccessKeyID: "test-only-id", AccessKeySecret: "test-only-secret", Timeout: time.Second, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payload := []byte{31, 139, 8, 0, 0, 0, 0, 0}
	if err := store.Put(context.Background(), "test.log", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	body, err := store.Get(context.Background(), "test.log")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if !bytes.Equal(got, payload) {
		t.Fatal("object bytes changed")
	}
	signed, err := store.Sign(context.Background(), "test.log")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed)
	if err != nil || u.Path != "/test-bucket/logan-logs/test.log" || u.Query().Get("x-oss-expires") != "3600" || u.Query().Get("x-oss-signature") == "" {
		t.Fatalf("invalid signed URL structure")
	}
	if err := store.Delete(context.Background(), "test.log"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !deleted {
		t.Fatal("delete not sent")
	}
}
func TestOSSErrorDoesNotExposeCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>test-only-secret</Message></Error>`))
	}))
	defer srv.Close()
	s, err := NewOSS(config.OSS{Endpoint: srv.URL, Region: "cn-hangzhou", Bucket: "test-bucket", AccessKeyID: "test-id", AccessKeySecret: "test-only-secret", Timeout: time.Second, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Get(context.Background(), "test.log")
	if err == nil || strings.Contains(err.Error(), "test-only-secret") {
		t.Fatalf("unsafe error %v", err)
	}
}

func TestOSSGetBodyRetainsContextUntilClose(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write([]byte("data"))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	s, err := NewOSS(config.OSS{Endpoint: srv.URL, Region: "cn-hangzhou", Bucket: "test-bucket", AccessKeyID: "test-id", AccessKeySecret: "test-secret", Timeout: time.Second, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body, err := s.Get(context.Background(), "test.log")
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "data" {
		t.Fatalf("download context ended before body consumption: %q %v", data, err)
	}
}
func TestOSSRequestTimeoutAndCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer srv.Close()
	s, err := NewOSS(config.OSS{Endpoint: srv.URL, Region: "cn-hangzhou", Bucket: "test-bucket", AccessKeyID: "test-id", AccessKeySecret: "test-secret", Timeout: 100 * time.Millisecond, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now()
	if s.Put(context.Background(), "test.log", bytes.NewReader([]byte("data")), 4) == nil {
		t.Fatal("OSS timeout ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("OSS operation exceeded timeout budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Get(ctx, "test.log"); err == nil {
		t.Fatal("canceled download accepted")
	}
}

func TestOSSDeleteAlreadyMissingIsIdempotent(t *testing.T) {
	for _, code := range []string{"NoSuchKey", "NoSuchBucket"} {
		t.Run(code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				_, _ = w.Write([]byte("<Error><Code>" + code + "</Code></Error>"))
			}))
			defer srv.Close()
			s, e := NewOSS(config.OSS{Endpoint: srv.URL, Region: "test", Bucket: "test-bucket", AccessKeyID: "id", AccessKeySecret: "secret", Timeout: time.Second, PathStyle: true})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			e = s.Delete(context.Background(), "missing")
			if (e == nil) != (code == "NoSuchKey") {
				t.Fatalf("delete %s: %v", code, e)
			}
		})
	}
}
