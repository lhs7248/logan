package repository

import (
	"context"
	"logan/servergo/internal/config"
	"testing"
)

func TestDisabledDatabaseDoesNotConnect(t *testing.T) {
	db, pool, err := Open(context.Background(), config.Database{Enabled: false})
	if err != nil || db != nil || pool != nil {
		t.Fatalf("disabled DB created resources: %v", err)
	}
}
