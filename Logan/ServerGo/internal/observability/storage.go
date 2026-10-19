package observability

import (
	"context"
	"errors"
	"io"
	"logan/servergo/internal/storage"
	"time"
)

// NewObservedStore preserves storage results and records bounded operation timing.
// Get measures the call until response headers/body handle are returned; body read
// duration and later body errors are deliberately excluded. Nil registry is a no-op.
func NewObservedStore(store storage.Store, registry *Registry) storage.Store {
	if registry == nil || store == nil {
		return store
	}
	return &observedStore{store: store, registry: registry}
}

type observedStore struct {
	store    storage.Store
	registry *Registry
}

func (s *observedStore) observe(ctx context.Context, operation string, start time.Time, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	}
	s.registry.oss.WithLabelValues(operation, outcome).Observe(time.Since(start).Seconds())
}
func (s *observedStore) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	start := time.Now()
	err := s.store.Put(ctx, key, body, size)
	s.observe(ctx, "put", start, err)
	return err
}
func (s *observedStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	start := time.Now()
	body, err := s.store.Get(ctx, key)
	s.observe(ctx, "get", start, err)
	return body, err
}
func (s *observedStore) Sign(ctx context.Context, key string) (string, error) {
	start := time.Now()
	signed, err := s.store.Sign(ctx, key)
	s.observe(ctx, "sign", start, err)
	return signed, err
}
func (s *observedStore) Delete(ctx context.Context, key string) error {
	start := time.Now()
	err := s.store.Delete(ctx, key)
	s.observe(ctx, "delete", start, err)
	return err
}
