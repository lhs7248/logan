package processingtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTraceAggregatesDurationsAndKeepsTasksSeparate(t *testing.T) {
	ctx, a := New(context.Background())
	other, b := New(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); Add(ctx, "decode", time.Millisecond) }()
	}
	wg.Wait()
	Add(other, "decode", 2*time.Millisecond)
	Add(context.Background(), "decode", time.Hour)
	if a.Milliseconds()["decode"] != 20 || b.Milliseconds()["decode"] != 2 {
		t.Fatal(a.Milliseconds(), b.Milliseconds())
	}
	snapshot := a.Milliseconds()
	snapshot["decode"] = 99
	if a.Milliseconds()["decode"] != 20 {
		t.Fatal("snapshot aliases mutable trace")
	}
}
