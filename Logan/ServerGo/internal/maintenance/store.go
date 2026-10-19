package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"logan/servergo/internal/config"
	"logan/servergo/internal/storage"
)

// Scope binds the durable ownership registry to an exact OSS namespace without
// storing endpoint, bucket or credentials in the maintenance tables.
func Scope(c config.OSS) string {
	b, _ := json.Marshal([]string{c.Endpoint, c.Region, c.Bucket, c.Prefix})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type trackedStore struct {
	storage.Store
	registry Registry
	scope    string
}

func NewTrackedStore(store storage.Store, registry Registry, scope string) storage.Store {
	return &trackedStore{Store: store, registry: registry, scope: scope}
}
func (s *trackedStore) Put(ctx context.Context, name string, body io.ReadSeeker, n int64) error {
	// Even a failed/ambiguous upload retains its intent for conservative recovery.
	if err := s.registry.BeginObject(ctx, s.scope, name); err != nil {
		return storage.ErrRegistration
	}
	return s.Store.Put(ctx, name, body, n)
}
