package native

import (
	"bytes"
	"context"
	"errors"
	"io"
	"logan/servergo/internal/config"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"os"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	put     int
	data    []byte
	key     string
	err     error
	deleted bool
}

func (s *memoryStore) Put(ctx context.Context, key string, r io.ReadSeeker, n int64) error {
	s.put++
	s.key = key
	s.data, _ = io.ReadAll(r)
	return s.err
}
func (s *memoryStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s *memoryStore) Sign(context.Context, string) (string, error) {
	return "https://example.test/signed", s.err
}
func (s *memoryStore) Delete(context.Context, string) error { s.deleted = true; return nil }

type memoryTasks struct {
	saved *repository.NativeTask
	err   error
}

func (s *memoryTasks) Create(_ context.Context, task *repository.NativeTask) error {
	if s.err != nil {
		return s.err
	}
	task.ID = 1
	copy := *task
	s.saved = &copy
	return nil
}
func (s *memoryTasks) Find(context.Context, int64) (*repository.NativeTask, error) {
	return s.saved, s.err
}
func TestUploadFailureBoundariesAndTemporaryCleanup(t *testing.T) {
	for _, stage := range []string{"success", "decrypt", "oss", "database", "registration"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			store := &memoryStore{}
			tasks := &memoryTasks{}
			if stage == "oss" {
				store.err = errors.New("private OSS detail")
			}
			if stage == "registration" {
				store.err = storage.ErrRegistration
			}
			if stage == "database" {
				tasks.err = errors.New("private DB detail")
			}
			cfg := config.Native{Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: dir, MaxInput: 1024, MaxFrame: 1024, Concurrency: 1, Timeout: time.Second}
			s, err := NewService(cfg, "Asia/Shanghai", store, tasks)
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte{}
			if stage == "decrypt" {
				payload = []byte{1}
			}
			result, err := s.Upload(context.Background(), map[string]string{"appid": "app", "deviceid": "device", "filedate": "2026-10-15", "platform": "1"}, int64(len(payload)), bytes.NewReader(payload))
			if stage == "success" {
				if err != nil || !strings.HasPrefix(result, "/logan/downing?name=app_device_") || tasks.saved == nil {
					t.Fatalf("upload: %q %v", result, err)
				}
			} else {
				expected := ErrDecrypt
				if stage == "database" || stage == "registration" {
					expected = ErrDatabase
				}
				if !errors.Is(err, expected) {
					t.Fatalf("wrong failure: %v", err)
				}
			}
			if stage == "decrypt" && store.put != 0 {
				t.Fatal("uploaded invalid protocol")
			}
			if stage == "oss" && tasks.saved != nil {
				t.Fatal("saved task before OSS success")
			}
			if stage == "database" && store.deleted {
				t.Fatal("deleted object after ambiguous DB failure")
			}
			files, _ := os.ReadDir(dir)
			if len(files) != 0 {
				t.Fatal("temporary file leaked")
			}
		})
	}
}

func TestCancelInterruptsPipeAndReleasesUploadSlot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewService(config.Native{Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: dir, MaxInput: 1024, MaxFrame: 1024, Concurrency: 1, Timeout: time.Second}, "Asia/Shanghai", &memoryStore{}, &memoryTasks{})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	observed := &notifyingReader{ReadCloser: reader, entered: make(chan struct{})}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Upload(ctx, nil, -1, observed); done <- err }()
	if _, err := writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	<-observed.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrDecrypt) {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cancel did not unblock upload reader")
	}
	if _, err := s.Upload(context.Background(), nil, 0, bytes.NewReader(nil)); err != nil {
		t.Fatalf("slot not reusable: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("cancelled upload left a temporary file")
	}
}

type notifyingReader struct {
	io.ReadCloser
	reads   int
	entered chan struct{}
}

func (r *notifyingReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 2 {
		close(r.entered)
	}
	return r.ReadCloser.Read(p)
}

func TestQueuedUploadCancellationDoesNotCreateAnotherFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewService(config.Native{Key: []byte("0123456789abcdef"), IV: []byte("fedcba9876543210"), TempDir: dir, MaxInput: 1024, MaxFrame: 1024, Concurrency: 1, Timeout: time.Second}, "Asia/Shanghai", &memoryStore{}, &memoryTasks{})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	observed := &notifyingReader{ReadCloser: reader, entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = s.Upload(ctx, nil, -1, observed); close(done) }()
	_, _ = writer.Write([]byte{1})
	<-observed.entered
	queued, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := s.Upload(queued, nil, 0, bytes.NewReader(nil)); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("queued cancellation: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("waiting upload created a file: %d", len(entries))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("active upload did not release")
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("active upload file not removed")
	}
}

// Keep rejecting historical empty/truncated frame tails even when their prefix
// contains complete SDK gzip members. Producer repair must not weaken uploads.
func TestSDKEmptyOrTruncatedTailIsNeverPublished(t *testing.T) {
	good, err := os.ReadFile("../../testdata/protocol/clogan-regression/native.bin")
	if err != nil {
		t.Fatal(err)
	}
	for name, tail := range map[string][]byte{
		"partial-header":     {1},
		"empty-header":       {1, 0, 0, 0, 0},
		"empty-frame":        {1, 0, 0, 0, 0, 0},
		"missing-ciphertext": {1, 0, 0, 0, 16, 0},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store := &memoryStore{}
			tasks := &memoryTasks{}
			s, err := NewService(config.Native{Key: []byte("0123456789012345"), IV: []byte("0123456789012345"), TempDir: dir, MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 1, Timeout: time.Second}, "Asia/Shanghai", store, tasks)
			if err != nil {
				t.Fatal(err)
			}
			wire := append(append([]byte(nil), good...), tail...)
			if _, err := s.Upload(context.Background(), nil, int64(len(wire)), bytes.NewReader(wire)); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("malformed SDK tail accepted: %v", err)
			}
			if store.put != 0 || tasks.saved != nil {
				t.Fatal("partially decoded source published")
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("temporary files leaked: %v %v", files, err)
			}
		})
	}
}
