// Package maintenance coordinates bounded, recoverable retention and object reclamation.
package maintenance

import (
	"context"
	"time"
)

type Lease struct {
	Name, Owner string
	Fence       int64
}
type Object struct {
	Scope, Name, Owner string
	Fence              int64
}
type Counts struct{ NativeTasks, WebTasks, NativeDetails, WebDetails int64 }
type Snapshot struct{ Pending, Deleting, Uploading, Failed int64 }
type Registry interface {
	BeginObject(context.Context, string, string) error
}
type Repository interface {
	Registry
	Validate(context.Context) error
	Acquire(context.Context, string, string, time.Duration) (Lease, bool, error)
	Renew(context.Context, Lease, time.Duration) error
	Release(context.Context, Lease) error
	Prune(context.Context, Lease, int64, int, string) (Counts, error)
	Sweep(context.Context, Lease, string, int64, int) (int, error)
	Claim(context.Context, Lease, string, time.Duration, int) ([]Object, error)
	Finish(context.Context, Lease, Object, string, time.Duration) error
	Snapshot(context.Context, string) (Snapshot, error)
}
