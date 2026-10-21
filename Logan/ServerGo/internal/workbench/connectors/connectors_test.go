package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repository(t *testing.T) (string, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %s: %v", b, e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "Test")
	if e := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// first\nfunc main() {}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("main.go", filepath.Join(root, "linked.go")); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("commit", "-m", "first")
	first := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// second\nfunc main() {}\n"), 0600)
	git("add", ".")
	git("commit", "-m", "second")
	return root, first, git("rev-parse", "HEAD")
}
func TestSourceImmutableBoundedReadAndDiff(t *testing.T) {
	root, first, second := repository(t)
	s, e := NewSource(SourceConfig{Repositories: map[string]string{"app": root}})
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Read(context.Background(), SourceRequest{RepoID: "app", Commit: first, Path: "main.go", Start: 2, End: 2})
	if e != nil {
		t.Fatal(e)
	}
	if f.Content != "// first\n" || f.Commit != first || f.Hash == "" || f.BlobHash == "" || !f.Verified {
		t.Fatalf("bad fragment: %+v", f)
	}
	d, e := s.Diff(context.Background(), DiffRequest{RepoID: "app", LeftCommit: first, RightCommit: second, Path: "main.go"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(d.Content, "+// second") || d.Hash == "" {
		t.Fatalf("bad diff: %+v", d)
	}
	for _, r := range []SourceRequest{{RepoID: "other", Commit: first, Path: "main.go"}, {RepoID: "app", Commit: "HEAD", Path: "main.go"}, {RepoID: "app", Commit: "--help", Path: "main.go"}, {RepoID: "app", Commit: first, Path: "../main.go"}, {RepoID: "app", Commit: first, Path: ".git/config"}, {RepoID: "app", Commit: first, Path: "linked.go"}, {RepoID: "app", Commit: first, Path: "secret.pem"}, {RepoID: "app", Commit: first, Path: "-main.go"}} {
		if _, e := s.Read(context.Background(), r); e == nil {
			t.Errorf("accepted unsafe %+v", r)
		}
	}
	limited, _ := NewSource(SourceConfig{Repositories: map[string]string{"app": root}, MaxFileBytes: 8})
	if _, e := limited.Read(context.Background(), SourceRequest{RepoID: "app", Commit: first, Path: "main.go", Start: 1, End: 1}); !errors.Is(e, ErrLimitExceeded) {
		t.Fatalf("file limit: %v", e)
	}
}
func TestUnconfigured(t *testing.T) {
	s, _ := NewSource(SourceConfig{})
	if s.Ready() {
		t.Fatal("ready")
	}
	if _, e := s.Read(context.Background(), SourceRequest{}); !errors.Is(e, ErrUnconfigured) {
		t.Fatal(e)
	}
	m, _ := NewModel(ModelConfig{})
	if m.Ready() {
		t.Fatal("ready")
	}
	if _, e := m.Investigate(context.Background(), Investigation{}); !errors.Is(e, ErrUnconfigured) {
		t.Fatal(e)
	}
}
func TestModelUsesSnapshotAndValidatesCitations(t *testing.T) {
	answer := `{"summary":"Observed failure","claims":[{"type":"fact","text":"failed","citations":["ev-1"]}],"unknowns":["root cause"],"nextSteps":["inspect"]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing credential")
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "ev-1") || !strings.Contains(string(raw), "immutable failure") {
			t.Error("missing evidence snapshot")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
	}))
	defer server.Close()
	m, e := NewModel(ModelConfig{Endpoint: server.URL, APIKey: "test-secret", Model: "test"})
	if e != nil {
		t.Fatal(e)
	}
	request := Investigation{Question: "why", Evidence: []Evidence{{ID: "ev-1", Content: "immutable failure"}}}
	got, e := m.Investigate(context.Background(), request)
	if e != nil || got.Summary != "Observed failure" {
		t.Fatalf("%+v %v", got, e)
	}
	answer = strings.Replace(answer, "ev-1", "invented", 1)
	if _, e := m.Investigate(context.Background(), request); !errors.Is(e, ErrInvalidResponse) {
		t.Fatalf("citation: %v", e)
	}
	answer = `{"summary":"No citations","claims":[{"type":"fact","text":"invented","citations":[]}],"unknowns":[],"nextSteps":[]}`
	if _, e := m.Investigate(context.Background(), request); !errors.Is(e, ErrInvalidResponse) {
		t.Fatalf("uncited fact: %v", e)
	}
}
func TestModelFailureCancellationAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		limit   int64
		want    error
	}{
		{"status", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "credential-secret", 401) }, 0, ErrUnavailable},
		{"malformed", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("credential-secret")) }, 0, ErrInvalidResponse},
		{"body limit", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 1000))) }, 32, ErrLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test", MaxResponseBytes: tc.limit})
			_, e := m.Investigate(context.Background(), Investigation{Question: "why"})
			if !errors.Is(e, tc.want) || strings.Contains(e.Error(), "credential-secret") {
				t.Fatalf("unsafe error %v", e)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test", Timeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Investigate(ctx, Investigation{Question: "why"}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	m, _ = NewModel(ModelConfig{Endpoint: srv.URL, Model: "test", MaxRequestBytes: 32})
	if _, e := m.Investigate(context.Background(), Investigation{Question: strings.Repeat("large", 100)}); !errors.Is(e, ErrLimitExceeded) {
		t.Fatal(e)
	}
}

func TestSourceConfigurationAndCancellation(t *testing.T) {
	root, commit, _ := repository(t)
	alias := filepath.Join(filepath.Dir(root), "repo-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(alias)
	for _, cfg := range []SourceConfig{{Repositories: map[string]string{"app": "relative"}}, {Repositories: map[string]string{"app": alias}}, {Repositories: map[string]string{"app": root}, AllowedExtensions: []string{"../secret"}}} {
		if _, err := NewSource(cfg); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("configuration: %v", err)
		}
	}
	s, _ := NewSource(SourceConfig{Repositories: map[string]string{"app": root}, AllowedExtensions: []string{".java"}})
	if _, err := s.Read(context.Background(), SourceRequest{RepoID: "app", Commit: commit, Path: "main.go"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("extension: %v", err)
	}
	s, _ = NewSource(SourceConfig{Repositories: map[string]string{"app": root}, MaxOutputBytes: 2})
	if _, err := s.Read(context.Background(), SourceRequest{RepoID: "app", Commit: commit, Path: "main.go", Start: 1, End: 1}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("snippet bound: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx, SourceRequest{RepoID: "app", Commit: commit, Path: "main.go"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
func TestModelConfigurationSchemaAndRedirectSafety(t *testing.T) {
	for _, cfg := range []ModelConfig{{Endpoint: "https://user:secret@example.invalid/v1", Model: "test"}, {Endpoint: "https://example.invalid/v1?key=secret", Model: "test"}, {Endpoint: "http://example.invalid/v1", Model: "test"}, {Endpoint: "https://example.invalid/v1"}} {
		if _, err := NewModel(cfg); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("configuration: %v", err)
		}
	}
	var response string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": response}, "finish_reason": "stop"}}})
	}))
	defer srv.Close()
	m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test"})
	for _, bad := range []string{`{}`, `{"summary":"x","claims":[],"unknowns":[],"nextSteps":[],"extra":"not allowed"}`, `{"summary":"x","claims":[],"unknowns":[],"nextSteps":[]} {}`, `{"summary":"x","claims":[{"type":"verified","text":"x","citations":["ev"]}],"unknowns":[],"nextSteps":[]}`} {
		response = bad
		if _, err := m.Investigate(context.Background(), Investigation{Question: "why", Evidence: []Evidence{{ID: "ev"}}}); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("schema accepted %s: %v", bad, err)
		}
	}
	response = `{"summary":"Insufficient evidence","claims":[],"unknowns":["cause"],"nextSteps":["collect evidence"]}`
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why"}); err != nil {
		t.Fatal(err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://outside.example.invalid", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	m, _ = NewModel(ModelConfig{Endpoint: redirect.URL, Model: "test", APIKey: "secret"})
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect: %v", err)
	}
}
func TestModelInFlightCancellationAndTimeout(t *testing.T) {
	entered := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer srv.Close()
	m, _ := NewModel(ModelConfig{Endpoint: srv.URL, Model: "test", Timeout: 25 * time.Millisecond})
	if _, err := m.Investigate(context.Background(), Investigation{Question: "why"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	<-entered
	m, _ = NewModel(ModelConfig{Endpoint: srv.URL, Model: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Investigate(ctx, Investigation{Question: "why"}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
}

func TestValidateCommitForTrustedMappings(t *testing.T) {
	root, commit, _ := repository(t)
	s, _ := NewSource(SourceConfig{Repositories: map[string]string{"app": root}})
	if err := s.ValidateCommit(context.Background(), "app", commit); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ repo, commit string }{{"unknown", commit}, {"app", "HEAD"}, {"app", strings.Repeat("f", 40)}} {
		if err := s.ValidateCommit(context.Background(), entry.repo, entry.commit); err == nil {
			t.Fatalf("accepted %v", entry)
		}
	}
	c := exec.Command("git", "-C", root, "rev-parse", commit+":main.go")
	b, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateCommit(context.Background(), "app", strings.TrimSpace(string(b))); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("blob accepted %v", err)
	}
	s, _ = NewSource(SourceConfig{})
	if err := s.ValidateCommit(context.Background(), "app", commit); !errors.Is(err, ErrUnconfigured) {
		t.Fatal(err)
	}
}
