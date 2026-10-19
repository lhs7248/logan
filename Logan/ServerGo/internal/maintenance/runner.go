package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"logan/servergo/internal/storage"
	"sync"
	"time"
)

type Config struct {
	Scope                                                                            string
	Interval, RunTimeout, LeaseTTL, ObjectLeaseTTL, Retention, OrphanAge, RetryDelay time.Duration
	Batch                                                                            int
	// OnError receives only generic, sanitized operational failures.
	OnError func(error)
	Observe func(op, result string)
}
type Runner struct {
	repo  Repository
	store storage.Store
	cfg   Config
	owner string
	mu    sync.Mutex
}

func NewRunner(repo Repository, store storage.Store, cfg Config) (*Runner, error) {
	if repo == nil || store == nil || cfg.Scope == "" || len(cfg.Scope) > 64 {
		return nil, errors.New("invalid maintenance dependencies")
	}
	if cfg.Interval == 0 {
		cfg.Interval = time.Minute
	}
	if cfg.RunTimeout == 0 {
		cfg.RunTimeout = 30 * time.Second
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.ObjectLeaseTTL == 0 {
		cfg.ObjectLeaseTTL = 30 * time.Second
	}
	if cfg.Retention == 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	if cfg.OrphanAge == 0 {
		cfg.OrphanAge = 24 * time.Hour
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = time.Minute
	}
	if cfg.Batch == 0 {
		cfg.Batch = 100
	}
	if cfg.Interval < 0 || cfg.RunTimeout < 0 || cfg.LeaseTTL < 3*time.Millisecond || cfg.LeaseTTL > 5*time.Minute || cfg.ObjectLeaseTTL < 2*time.Millisecond || cfg.ObjectLeaseTTL > 5*time.Minute || cfg.Retention < 0 || cfg.OrphanAge < 0 || cfg.RetryDelay < 0 || cfg.Batch < 1 || cfg.Batch > 1000 {
		return nil, errors.New("invalid maintenance bounds")
	}
	token := make([]byte, 16)
	if _, e := rand.Read(token); e != nil {
		return nil, e
	}
	return &Runner{repo: repo, store: store, cfg: cfg, owner: hex.EncodeToString(token)}, nil
}
func (r *Runner) Run(ctx context.Context) error {
	if e := r.repo.Validate(ctx); e != nil {
		return errors.New("maintenance schema validation failed")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if e := r.RunOnce(ctx); e != nil && ctx.Err() == nil && r.cfg.OnError != nil {
				r.cfg.OnError(errors.New("maintenance cycle failed"))
			}
			timer.Reset(r.cfg.Interval)
		}
	}
}
func (r *Runner) RunOnce(ctx context.Context) (err error) {
	defer func() {
		result := "success"
		if err != nil {
			result = "error"
		}
		r.observe("cycle", result)
	}()

	// Calls from the background loop and manual triggers share the same owner.
	r.mu.Lock()
	defer r.mu.Unlock()
	cycle, cancel := context.WithTimeout(ctx, r.cfg.RunTimeout)
	defer cancel()
	lease, ok, e := r.repo.Acquire(cycle, "maintenance", r.owner, r.cfg.LeaseTTL)
	if e != nil {
		return errors.New("maintenance lease acquisition failed")
	}
	if !ok {
		return nil
	}
	stop := make(chan struct{})
	joined := make(chan struct{})
	renewFailure := make(chan struct{}, 1)
	go func() {
		defer close(joined)
		ticker := time.NewTicker(r.cfg.LeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-cycle.Done():
				return
			case <-ticker.C:
				if e := r.repo.Renew(cycle, lease, r.cfg.LeaseTTL); e != nil {
					renewFailure <- struct{}{}
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(stop)
		<-joined
		// All work and renewal are joined before releasing ownership. An interrupted
		// deletion remains in deleting state for fenced, idempotent recovery.
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer releaseCancel()
		if e := r.repo.Release(releaseCtx, lease); e != nil {
			err = errors.Join(err, errors.New("maintenance lease release failed"))
		}
		select {
		case <-renewFailure:
			err = errors.Join(err, errors.New("maintenance lease renewal failed"))
		default:
		}
	}()

	now := time.Now()
	stageBudget := max(time.Millisecond, r.cfg.RunTimeout/3)
	pruneCtx, pruneCancel := context.WithTimeout(cycle, stageBudget)
	_, e = r.repo.Prune(pruneCtx, lease, now.Add(-r.cfg.Retention).UnixMilli(), r.cfg.Batch, r.cfg.Scope)
	pruneCancel()
	var stageErr error
	if e != nil {
		r.observe("retention", "error")
		stageErr = errors.New("maintenance retention failed")
	} else {
		r.observe("retention", "success")
	}
	if e := cycle.Err(); e != nil {
		return errors.Join(stageErr, e)
	}
	sweepCtx, sweepCancel := context.WithTimeout(cycle, stageBudget)
	_, e = r.repo.Sweep(sweepCtx, lease, r.cfg.Scope, now.Add(-r.cfg.OrphanAge).UnixMilli(), r.cfg.Batch)
	sweepCancel()
	if e != nil {
		stageErr = errors.Join(stageErr, errors.New("maintenance orphan recovery failed"))
	}
	if e := cycle.Err(); e != nil {
		return errors.Join(stageErr, e)
	}

	var deleteErr error
	for i := 0; i < r.cfg.Batch; i++ {
		if e := cycle.Err(); e != nil {
			return e
		}
		// Claim one at a time: no later object can sit in a batch while its lease
		// expires behind a slow provider request.
		objects, e := r.repo.Claim(cycle, lease, r.cfg.Scope, r.cfg.ObjectLeaseTTL, 1)
		if e != nil {
			return errors.New("maintenance deletion claim failed")
		}
		if len(objects) == 0 {
			break
		}
		object := objects[0]
		// DB fencing cannot undo an OSS delete. Recheck global ownership immediately
		// before entering the provider, and bound it well inside the object lease.
		if e = r.repo.Renew(cycle, lease, r.cfg.LeaseTTL); e != nil {
			cancel()
			return errors.New("maintenance lease validation failed")
		}
		deleteCtx, deleteCancel := context.WithTimeout(cycle, min(r.cfg.ObjectLeaseTTL, r.cfg.LeaseTTL)/2)
		e = r.store.Delete(deleteCtx, object.Name)
		deleteCancel()
		code := ""
		if e != nil {
			code = "delete_error"
			deleteErr = errors.New("maintenance object deletion failed")
			r.observe("delete", "error")
		} else {
			r.observe("delete", "success")
		}
		if e = r.repo.Finish(cycle, lease, object, code, r.cfg.RetryDelay); e != nil {
			return errors.New("maintenance deletion acknowledgement failed")
		}
		if code != "" {
			r.observe("retry", "scheduled")
		}
	}

	return errors.Join(stageErr, deleteErr)
}

func (r *Runner) observe(op, result string) {
	if r.cfg.Observe != nil {
		r.cfg.Observe(op, result)
	}
}
