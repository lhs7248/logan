package weblog

import (
	"context"
	"errors"
	"logan/servergo/internal/protocol"
	"testing"
	"time"
)

type drainRepo struct {
	Repository
	entered chan int64
	release chan struct{}
	result  error
}

func (r *drainRepo) Pending(context.Context, int) ([]int64, error) { return []int64{1, 2}, nil }
func (r *drainRepo) Process(ctx context.Context, id int64, _ ParseTask) error {
	r.entered <- id
	select {
	case <-r.release:
		return r.result
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestGracefulDrainAndObserver(t *testing.T) {
	r := &drainRepo{entered: make(chan int64, 4), release: make(chan struct{})}
	q, e := NewService(Config{MaxBytes: 1024, MaxTasks: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "UTC"}, protocol.NewWebDecoder(nil, 1024), r)
	if e != nil {
		t.Fatal(e)
	}
	out := make(chan string, 2)
	q.SetJobObserver(func(kind, result string, d time.Duration) { out <- kind + ":" + result })
	q.Start(context.Background())
	defer q.Close()
	<-r.entered
	q.StopClaims()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if q.Wait(ctx) != context.DeadlineExceeded {
		t.Fatal("active work did not drain")
	}
	close(r.release)
	if e = q.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got := <-out; got != "web:success" {
		t.Fatal(got)
	}
	select {
	case <-r.entered:
		t.Fatal("claimed after stop")
	default:
	}
}
func TestCancellationObserver(t *testing.T) {
	r := &drainRepo{entered: make(chan int64, 4), release: make(chan struct{})}
	q, _ := NewService(Config{MaxBytes: 1024, MaxTasks: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "UTC"}, protocol.NewWebDecoder(nil, 1024), r)
	out := make(chan string, 2)
	q.SetJobObserver(func(_, result string, _ time.Duration) { out <- result })
	q.Start(context.Background())
	<-r.entered
	q.Cancel()
	q.Close()
	if got := <-out; got != "canceled" {
		t.Fatal(got)
	}
}

func TestErrorAttemptAndStopBeforeStart(t *testing.T) {
	r := &drainRepo{entered: make(chan int64, 4), release: make(chan struct{}), result: errors.New("private failure")}
	q, e := NewService(Config{MaxBytes: 1024, MaxTasks: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "UTC"}, protocol.NewWebDecoder(nil, 1024), r)
	if e != nil {
		t.Fatal(e)
	}
	out := make(chan string, 4)
	q.SetJobObserver(func(_, result string, _ time.Duration) { out <- result })
	q.Start(context.Background())
	<-r.entered
	q.StopClaims()
	close(r.release)
	q.Wait(context.Background())
	if got := <-out; got != "error" {
		t.Fatal(got)
	}
	q.Close()
	q2, e := NewService(Config{MaxBytes: 1024, MaxTasks: 10, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "UTC"}, protocol.NewWebDecoder(nil, 1024), r)
	if e != nil {
		t.Fatal(e)
	}
	q2.StopClaims()
	q2.Start(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = q2.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	q2.Close()
}
