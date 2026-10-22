// Package workbench implements the isolated, project-scoped investigation service.
package workbench

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Record map[string]any
type State struct {
	Objects     map[string]map[string]Record `json:"objects"`
	Idempotency map[string]Receipt           `json:"idempotency"`
}
type Receipt struct {
	Hash   string          `json:"hash"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// Persistence atomically commits business objects and idempotency receipts together.
// Implementations must hold an exclusive runtime lease and reject stale writers.
type Persistence interface {
	Load() (*State, error)
	Save(*State) error
	Close() error
}
type FileStore struct {
	dir    string
	lock   *os.File
	mu     sync.Mutex
	closed bool
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("workbench data directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("workbench store already locked: %w", err)
	}
	return &FileStore{dir: dir, lock: lock}, nil
}
func emptyState() *State {
	return &State{Objects: map[string]map[string]Record{}, Idempotency: map[string]Receipt{}}
}
func (f *FileStore) Load() (*State, error) {
	b, err := os.ReadFile(filepath.Join(f.dir, "state.json"))
	if os.IsNotExist(err) {
		return emptyState(), nil
	}
	if err != nil {
		return nil, err
	}
	s := emptyState()
	if err = json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("corrupt workbench state: %w", err)
	}
	if s.Objects == nil || s.Idempotency == nil {
		return nil, errors.New("invalid workbench state")
	}
	return s, nil
}
func (f *FileStore) Save(s *State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("store closed")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.dir, ".state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(f.dir, "state.json")); err != nil {
		return err
	}
	d, err := os.Open(f.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (f *FileStore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	return f.lock.Close()
}
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func now() string      { return time.Now().UTC().Format(time.RFC3339Nano) }
func str(v any) string { s, _ := v.(string); return s }
func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
func stringsOf(v any) []string {
	a := []string{}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		for _, v := range xs {
			if s, ok := v.(string); ok {
				a = append(a, s)
			}
		}
	}
	return a
}
func record(v any) Record {
	switch m := v.(type) {
	case Record:
		return m
	case map[string]any:
		return Record(m)
	}
	return Record{}
}
func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func (s *State) bucket(kind string) map[string]Record {
	if s.Objects[kind] == nil {
		s.Objects[kind] = map[string]Record{}
	}
	return s.Objects[kind]
}
func (s *State) add(kind, project string, r Record) Record {
	r["id"] = id()
	r["projectId"] = project
	r["createdAt"] = now()
	s.bucket(kind)[str(r["id"])] = r
	return r
}

func afterTimestamp(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	return ea == nil && eb == nil && ta.After(tb)
}
