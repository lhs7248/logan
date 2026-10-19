package native

import (
	"context"
	"errors"
	"io"
	"logan/servergo/internal/repository"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type overlappingScanRepo struct {
	QueryRepository
	scans, calls                                 atomic.Int32
	barrier, entered, duplicate, rescan, release chan struct{}
	once                                         sync.Once
}

func (r *overlappingScanRepo) Pending(ctx context.Context, _ int) ([]int64, error) {
	n := r.scans.Add(1)
	if n == 4 {
		close(r.barrier)
	}
	if n > 4 {
		r.once.Do(func() { close(r.rescan) })
		return nil, nil
	}
	select {
	case <-r.barrier:
		return []int64{1}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (r *overlappingScanRepo) Process(ctx context.Context, _ int64, _ func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error {
	n := r.calls.Add(1)
	if n == 1 {
		close(r.entered)
	} else if n == 2 {
		close(r.duplicate)
	}
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestOverlappingNativeScansDoNotBlockPoolWithDuplicateClaims(t *testing.T) {
	q, _, _, cfg := queryFixture(t, "")
	q.Close()
	r := &overlappingScanRepo{barrier: make(chan struct{}), entered: make(chan struct{}), duplicate: make(chan struct{}), rescan: make(chan struct{}), release: make(chan struct{})}
	cfg.Workers = 4
	cfg.Timeout = 3 * time.Second
	q, err := NewQueryService(cfg, &queryStore{}, r)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	defer q.Close()
	defer close(r.release)
	select {
	case <-r.entered:
	case <-time.After(time.Second):
		t.Fatal("claim never entered")
	}
	select {
	case <-r.duplicate:
		t.Fatal("same task entered Process concurrently; redundant claims can block connections behind publication lock")
	case <-r.rescan:
	case <-time.After(time.Second):
		t.Fatal("workers blocked rather than skipping locally active task")
	}
	if r.calls.Load() != 1 {
		t.Fatal("duplicate calls", r.calls.Load())
	}
}

type readinessPollRepo struct {
	QueryRepository
	polls atomic.Int32
}

func (r *readinessPollRepo) Ready(context.Context, int64) (bool, error) {
	r.polls.Add(1)
	return false, nil
}
func (r *readinessPollRepo) Ensure(context.Context, int64) error { return nil }
func TestBriefWaitingDoesNotHammerDatabase(t *testing.T) {
	q, repo, store, cfg := queryFixture(t, "")
	q.Close()
	r := &readinessPollRepo{QueryRepository: repo}
	cfg.Wait = 150 * time.Millisecond
	q, err := NewQueryService(cfg, store, r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.Brief(context.Background(), 1, nil, nil)
	if err == nil {
		t.Fatal("unfinished task reported queryable")
	}
	if n := r.polls.Load(); n > 4 {
		t.Fatalf("brief issued %d readiness scans in 150ms while no work completed", n)
	}
}

type reservationRetryRepo struct {
	QueryRepository
	calls    atomic.Int32
	retry    chan struct{}
	deadline bool
}

func (r *reservationRetryRepo) Pending(context.Context, int) ([]int64, error) {
	if r.calls.Load() >= 2 {
		return nil, nil
	}
	return []int64{1}, nil
}
func (r *reservationRetryRepo) Process(ctx context.Context, _ int64, _ func(context.Context, repository.NativeTask) (io.ReadCloser, error)) error {
	if r.calls.Add(1) == 1 {
		if r.deadline {
			<-ctx.Done()
			return ctx.Err()
		}
		return errors.New("synthetic first attempt failure")
	}
	close(r.retry)
	return nil
}
func TestNativeReservationReleasedAfterFailedOrExpiredAttempt(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		q, _, store, cfg := queryFixture(t, "")
		q.Close()
		cfg.Workers = 1
		cfg.Timeout = 20 * time.Millisecond
		r := &reservationRetryRepo{retry: make(chan struct{}), deadline: deadline}
		q, err := NewQueryService(cfg, store, r)
		if err != nil {
			t.Fatal(err)
		}
		q.Start(context.Background())
		select {
		case <-r.retry:
		case <-time.After(time.Second):
			q.Close()
			t.Fatal("failed attempt stranded local task reservation")
		}
		q.Close()
	}
}
