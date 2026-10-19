package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBurstBounds(t *testing.T) {
	c := defaults()
	if c.ArrivalBurst != 1 {
		t.Fatal("default burst changed")
	}
	c.AllowTarget = true
	c.AllowWrites = true
	c.Marker = "logan-load-test"
	c.Mode = "all20"
	c.Workers = 2
	c.ArrivalBurst = 2
	if err := validateConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"native", "web", "mixed", "read"} {
		c.Mode = mode
		if validateConfig(c) == nil {
			t.Fatal("burst outside all20", mode)
		}
	}
	c.Mode = "all20"
	c.ArrivalBurst = 3
	if validateConfig(c) == nil {
		t.Fatal("burst exceeds workers")
	}
	c.ArrivalBurst = 0
	if validateConfig(c) == nil {
		t.Fatal("zero burst")
	}
}

func TestBurstConcurrentFairCountAndDrain(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.ArrivalBurst = 2
	c.Workers = 2
	c.Queue = 4
	c.QPS = 5
	c.Duration = 250 * time.Millisecond
	c.Drain = time.Second
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	e := &engine{c: c, stats: newStats()}
	e.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":500}`))}, nil
	})}
	done := make(chan struct{})
	go func() { e.run(context.Background()); close(done) }()
	defer once.Do(func() { close(release) })
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(150 * time.Millisecond):
			t.Fatal("burst did not start simultaneous workflows")
		}
	}
	time.Sleep(300 * time.Millisecond)
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish")
	}
	if e.stats.Attempted != 2 || e.stats.Dropped != 0 || e.stats.Workflows.Sent != 2 {
		t.Fatal(e.stats.Attempted, e.stats.Dropped, e.stats.Workflows.Sent)
	}
	if e.snapshot(true)["max_concurrent_workflows"] != 2 {
		t.Fatal("concurrency evidence missing", e.snapshot(true)["max_concurrent_workflows"])
	}
}

func TestBurstQueueDropAndWindowBoundary(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.ArrivalBurst = 2
	c.Workers = 2
	c.Queue = 2
	c.QPS = 200
	c.Duration = 35 * time.Millisecond
	c.Drain = time.Second
	e := &engine{c: c, stats: newStats(), client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		time.Sleep(100 * time.Millisecond)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":500}`))}, nil
	})}}
	e.run(context.Background())
	if e.stats.Attempted != 7 || e.stats.Workflows.Sent > 2 || e.stats.Dropped != 7-e.stats.Workflows.Sent {
		t.Fatal(e.stats.Attempted, e.stats.Workflows.Sent, e.stats.Dropped)
	}
}
func TestBurstParentCancelledBeforeArrival(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.ArrivalBurst = 2
	c.Duration = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &engine{c: c, stats: newStats()}
	e.run(ctx)
	if !e.stats.Interrupted || e.stats.Attempted != 0 || e.stats.Workflows.Sent != 0 {
		t.Fatal("cancelled arrivals offered", e.stats.Attempted)
	}
}

func TestBurstDrainDeadlineCancelsActiveRequests(t *testing.T) {
	c := defaults()
	c.Mode = "all20"
	c.ArrivalBurst = 2
	c.Workers = 2
	c.Queue = 4
	c.QPS = 2
	c.Duration = 30 * time.Millisecond
	c.Drain = 50 * time.Millisecond
	e := &engine{c: c, stats: newStats(), client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	started := time.Now()
	e.run(context.Background())
	if time.Since(started) > time.Second || e.stats.Workflows.Sent != 1 || e.stats.Workflows.Errors != 1 || e.stats.Attempted != 1 {
		t.Fatal("drain deadline/accounting", e.stats.Workflows, e.stats.Attempted)
	}
}
func TestBurstArrivalMath(t *testing.T) {
	for _, v := range []struct {
		duration time.Duration
		qps      float64
		want     int
	}{{35 * time.Millisecond, 200, 7}, {660 * time.Second, 4, 2640}, {220 * time.Second, .2, 44}, {time.Nanosecond, 1, 1}} {
		if got := scheduledArrivals(v.duration, v.qps); got != v.want {
			t.Fatal(v, got)
		}
	}
}
