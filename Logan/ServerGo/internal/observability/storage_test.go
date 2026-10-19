package observability

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeObservedStore struct {
	err  error
	body io.ReadCloser
	seen []string
}

func (s *fakeObservedStore) Put(_ context.Context, key string, _ io.ReadSeeker, _ int64) error {
	s.seen = append(s.seen, key)
	return s.err
}
func (s *fakeObservedStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.seen = append(s.seen, key)
	return s.body, s.err
}
func (s *fakeObservedStore) Sign(_ context.Context, key string) (string, error) {
	s.seen = append(s.seen, key)
	return "https://secret-signature", s.err
}
func (s *fakeObservedStore) Delete(_ context.Context, key string) error {
	s.seen = append(s.seen, key)
	return s.err
}
func TestObservedStoreMetricsAndPassthrough(t *testing.T) {
	r := New()
	f := &fakeObservedStore{body: io.NopCloser(strings.NewReader("privatebody"))}
	if NewObservedStore(f, nil) != f {
		t.Fatal("nil registry should pass through")
	}
	s := NewObservedStore(f, r)
	ctx := context.Background()
	if e := s.Put(ctx, "secret-key", strings.NewReader("privatebody"), 11); e != nil {
		t.Fatal(e)
	}
	body, e := s.Get(ctx, "secret-key")
	if e != nil || body != f.body {
		t.Fatal("get body wrapped/changed")
	}
	body.Close()
	if signed, e := s.Sign(ctx, "secret-key"); e != nil || signed != "https://secret-signature" {
		t.Fatal(signed, e)
	}
	if e = s.Delete(ctx, "secret-key"); e != nil {
		t.Fatal(e)
	}
	failure := errors.New("secret-service-error")
	f.err = failure
	if e = s.Delete(ctx, "another-secret"); e != failure {
		t.Fatal("error changed")
	}
	f.err = context.Canceled
	_, _ = s.Get(ctx, "secret-key")
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	metrics := w.Body.String()
	for _, want := range []string{`logan_oss_operation_duration_seconds_count{operation="put",outcome="success"} 1`, `logan_oss_operation_duration_seconds_count{operation="get",outcome="canceled"} 1`, `logan_oss_operation_duration_seconds_count{operation="delete",outcome="error"} 1`} {
		if !strings.Contains(metrics, want) {
			t.Fatal(metrics)
		}
	}
	for _, secret := range []string{"secret-key", "another-secret", "secret-service-error", "secret-signature", "privatebody"} {
		if strings.Contains(metrics, secret) {
			t.Fatal("secret label", secret)
		}
	}
}
func TestRuntimeAndProcessCollectors(t *testing.T) {
	r := New()
	w := httptest.NewRecorder()
	r.HTTPHandler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	s := w.Body.String()
	for _, name := range []string{"go_goroutines", "go_memstats_heap_alloc_bytes", "process_cpu_seconds_total"} {
		if !strings.Contains(s, name) {
			t.Fatalf("missing %s", name)
		}
	}
}
