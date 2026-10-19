package maintenance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"logan/servergo/internal/config"
	"logan/servergo/internal/storage"
	"sync"
	"testing"
	"time"
)

type fakeRepo struct {
	mu                               sync.Mutex
	events                           []string
	failBegin, failRenew, failFinish bool
	failPrune                        bool
	blockPrune                       bool
	renewCount                       int
	objects                          []Object
	codes                            []string
	renewed                          chan struct{}
}

func (f *fakeRepo) event(s string) { f.mu.Lock(); defer f.mu.Unlock(); f.events = append(f.events, s) }
func (f *fakeRepo) BeginObject(context.Context, string, string) error {
	f.event("begin")
	if f.failBegin {
		return errors.New("db")
	}
	return nil
}
func (f *fakeRepo) Validate(context.Context) error { return nil }
func (f *fakeRepo) Acquire(context.Context, string, string, time.Duration) (Lease, bool, error) {
	f.event("acquire")
	return Lease{Name: "gc", Owner: "owner", Fence: 1}, true, nil
}
func (f *fakeRepo) Renew(context.Context, Lease, time.Duration) error {
	f.event("renew")
	if f.renewed != nil {
		select {
		case f.renewed <- struct{}{}:
		default:
		}
	}
	f.mu.Lock()
	f.renewCount++
	lost := f.failRenew && f.renewCount > 1
	f.mu.Unlock()
	if lost {
		return errors.New("lost")
	}
	return nil
}
func (f *fakeRepo) Release(context.Context, Lease) error { f.event("release"); return nil }
func (f *fakeRepo) Prune(ctx context.Context, _ Lease, _ int64, _ int, _ string) (Counts, error) {
	f.event("prune")
	if f.blockPrune {
		<-ctx.Done()
		return Counts{}, ctx.Err()
	}
	if f.failPrune {
		return Counts{}, errors.New("oversized retention task")
	}
	return Counts{}, nil
}
func (f *fakeRepo) Sweep(context.Context, Lease, string, int64, int) (int, error) {
	f.event("sweep")
	return 0, nil
}
func (f *fakeRepo) Claim(context.Context, Lease, string, time.Duration, int) ([]Object, error) {
	f.event("claim")
	return f.objects, nil
}
func (f *fakeRepo) Finish(_ context.Context, _ Lease, _ Object, code string, _ time.Duration) error {
	f.event("finish")
	f.codes = append(f.codes, code)
	if f.failFinish {
		return errors.New("ack")
	}
	return nil
}
func (f *fakeRepo) Snapshot(context.Context, string) (Snapshot, error) { return Snapshot{}, nil }

type fakeStore struct {
	repo       *fakeRepo
	failDelete bool
	block      bool
}

func (f *fakeStore) Put(context.Context, string, io.ReadSeeker, int64) error {
	f.repo.event("put")
	return nil
}
func (f *fakeStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (f *fakeStore) Sign(context.Context, string) (string, error) { return "", nil }
func (f *fakeStore) Delete(ctx context.Context, _ string) error {
	f.repo.event("delete")
	if f.block {
		<-ctx.Done()
		f.repo.event("delete joined")
		return ctx.Err()
	}
	if f.failDelete {
		return errors.New("provider secret")
	}
	return nil
}
func testConfig() Config {
	return Config{Scope: "scope", Interval: time.Second, RunTimeout: time.Second, LeaseTTL: 60 * time.Millisecond, ObjectLeaseTTL: 100 * time.Millisecond, Retention: 24 * time.Hour, OrphanAge: time.Hour, RetryDelay: time.Millisecond, Batch: 1}
}
func TestTrackedPutIntentFirst(t *testing.T) {
	for _, fail := range []bool{false, true} {
		r := &fakeRepo{failBegin: fail}
		s := NewTrackedStore(&fakeStore{repo: r}, r, "scope")
		e := s.Put(context.Background(), "a", bytes.NewReader(nil), 0)
		if fail {
			if !errors.Is(e, storage.ErrRegistration) || len(r.events) != 1 {
				t.Fatal(e, r.events)
			}
		} else if e != nil || len(r.events) != 2 || r.events[0] != "begin" || r.events[1] != "put" {
			t.Fatal(e, r.events)
		}
	}
}
func TestRunnerOrderedOutbox(t *testing.T) {
	r := &fakeRepo{objects: []Object{{Name: "a"}}}
	runner, e := NewRunner(r, &fakeStore{repo: r}, testConfig())
	if e != nil {
		t.Fatal(e)
	}
	if e = runner.RunOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	want := []string{"acquire", "prune", "sweep", "claim", "renew", "delete", "finish", "release"}
	if len(r.events) != len(want) {
		t.Fatal(r.events)
	}
	for i, v := range want {
		if r.events[i] != v {
			t.Fatal(r.events)
		}
	}
}
func TestRunnerDeleteFailurePersisted(t *testing.T) {
	r := &fakeRepo{objects: []Object{{Name: "a"}}}
	runner, _ := NewRunner(r, &fakeStore{repo: r, failDelete: true}, testConfig())
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("missing error")
	}
	if len(r.codes) != 1 || r.codes[0] != "delete_error" {
		t.Fatal(r.codes)
	}
}
func TestRunnerRenewFailureCancelsAndJoinsBeforeRelease(t *testing.T) {
	r := &fakeRepo{failRenew: true, objects: []Object{{Name: "a"}}}
	runner, _ := NewRunner(r, &fakeStore{repo: r, block: true}, testConfig())
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("missing ownership error")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	joined, released := -1, -1
	for i, e := range r.events {
		if e == "delete joined" {
			joined = i
		}
		if e == "release" {
			released = i
		}
	}
	if joined < 0 || released <= joined {
		t.Fatal(r.events)
	}
}
func TestRunnerAckLossStopsAndReleases(t *testing.T) {
	r := &fakeRepo{failFinish: true, objects: []Object{{Name: "a"}, {Name: "b"}}}
	runner, _ := NewRunner(r, &fakeStore{repo: r}, testConfig())
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("missing acknowledgement failure")
	}
	deletes := 0
	for _, e := range r.events {
		if e == "delete" {
			deletes++
		}
	}
	if deletes != 1 || r.events[len(r.events)-1] != "release" {
		t.Fatal(r.events)
	}
}
func TestRunnerTimeoutJoinsDeletionAndReleases(t *testing.T) {
	r := &fakeRepo{objects: []Object{{Name: "a"}}}
	cfg := testConfig()
	cfg.RunTimeout = 10 * time.Millisecond
	runner, _ := NewRunner(r, &fakeStore{repo: r, block: true}, cfg)
	start := time.Now()
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("missing timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout unbounded")
	}
	if r.events[len(r.events)-1] != "release" {
		t.Fatal(r.events)
	}
}
func TestRunnerRetryUsesPersistentRepositoryAgain(t *testing.T) {
	r := &fakeRepo{objects: []Object{{Name: "a"}}}
	store := &fakeStore{repo: r, failDelete: true}
	runner, _ := NewRunner(r, store, testConfig())
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("missing deletion failure")
	}
	store.failDelete = false
	if e := runner.RunOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(r.codes) != 2 || r.codes[0] != "delete_error" || r.codes[1] != "" {
		t.Fatal(r.codes)
	}
}
func TestScopeBindsNamespaceAndExcludesCredentials(t *testing.T) {
	c := config.OSS{Endpoint: "https://example.test", Region: "region", Bucket: "bucket", Prefix: "logs/"}
	h := Scope(c)
	c.AccessKeySecret = "rotated"
	if Scope(c) != h {
		t.Fatal("credentials changed ownership")
	}
	c.Prefix = "other/"
	if Scope(c) == h {
		t.Fatal("prefix escaped scope")
	}
}
func TestRunnerObserverReportsPersistentRetry(t *testing.T) {
	r := &fakeRepo{objects: []Object{{Name: "a"}}}
	cfg := testConfig()
	var events []string
	cfg.Observe = func(op, result string) { events = append(events, op+":"+result) }
	runner, _ := NewRunner(r, &fakeStore{repo: r, failDelete: true}, cfg)
	_ = runner.RunOnce(context.Background())
	want := []string{"retention:success", "delete:error", "retry:scheduled", "cycle:error"}
	if len(events) != len(want) {
		t.Fatal(events)
	}
	for i, e := range want {
		if events[i] != e {
			t.Fatal(events)
		}
	}
}

func TestRunnerPruneFailureStillDrainsOutbox(t *testing.T) {
	r := &fakeRepo{failPrune: true, objects: []Object{{Name: "a"}}}
	runner, _ := NewRunner(r, &fakeStore{repo: r}, testConfig())
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("retention failure hidden")
	}
	found := false
	for _, e := range r.events {
		if e == "finish" {
			found = true
		}
	}
	if !found {
		t.Fatal("retention poison blocked independent outbox", r.events)
	}
}

func TestRunnerNativeBacklogBudgetReservesOutbox(t *testing.T) {
	r := &fakeRepo{blockPrune: true, objects: []Object{{Name: "a"}}}
	cfg := testConfig()
	cfg.RunTimeout = 600 * time.Millisecond
	cfg.LeaseTTL = time.Second
	cfg.ObjectLeaseTTL = time.Second
	runner, _ := NewRunner(r, &fakeStore{repo: r}, cfg)
	if runner.RunOnce(context.Background()) == nil {
		t.Fatal("timeout hidden")
	}
	found := false
	for _, e := range r.events {
		if e == "finish" {
			found = true
		}
	}
	if !found {
		t.Fatal("busy retention starved outbox", r.events)
	}
}
