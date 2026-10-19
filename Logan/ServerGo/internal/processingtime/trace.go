// Package processingtime collects one bounded, content-free processing trace per
// claimed task. Stage names are constants at call sites, never user input.
package processingtime

import (
	"context"
	"sync"
	"time"
)

type key struct{}
type Trace struct {
	mu     sync.Mutex
	stages map[string]time.Duration
}

func New(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{stages: make(map[string]time.Duration)}
	return context.WithValue(ctx, key{}, t), t
}
func Add(ctx context.Context, stage string, d time.Duration) {
	t, _ := ctx.Value(key{}).(*Trace)
	if t == nil {
		return
	}
	t.mu.Lock()
	t.stages[stage] += d
	t.mu.Unlock()
}
func (t *Trace) Milliseconds() map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]float64, len(t.stages))
	for k, v := range t.stages {
		out[k] = float64(v) / float64(time.Millisecond)
	}
	return out
}
