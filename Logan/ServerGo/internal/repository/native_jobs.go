package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"logan/servergo/internal/processingtime"
	"time"
)

func nativeDigest(t NativeTask) string {
	if t.LogFileName == nil {
		return digestWeb("")
	}
	return digestWeb(*t.LogFileName)
}
func lockNativeJob(tx *gorm.DB, id int64) (webJob, error) {
	var j webJob
	q := tx.Raw("SELECT task_id,state,owner,fence,source_revision,source_digest,attempts,created_at,"+webNow+" AS observed_at FROM logan_processing_job WHERE kind='native' AND task_id=? FOR UPDATE", id).Scan(&j)
	return j, q.Error
}
func lockNativeTask(tx *gorm.DB, id int64) (NativeTask, error) {
	var t NativeTask
	q := tx.Raw("SELECT * FROM logan_task WHERE id=? FOR UPDATE", id).Scan(&t)
	if q.Error == nil && q.RowsAffected == 0 {
		return t, gorm.ErrRecordNotFound
	}
	return t, q.Error
}
func (r *NativeQueries) Ensure(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("INSERT INTO logan_processing_job(kind,task_id,created_at,updated_at) VALUES ('native',?,"+webNow+","+webNow+") ON DUPLICATE KEY UPDATE task_id=task_id", id).Error; e != nil {
			return e
		}
		j, e := lockNativeJob(tx, id)
		if e != nil {
			return e
		}
		t, e := lockNativeTask(tx, id)
		if e != nil {
			return e
		}
		if t.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,updated_at="+webNow+" WHERE kind='native' AND task_id=?", id).Error
		}
		if j.SourceDigest == "" {
			return tx.Exec("UPDATE logan_processing_job SET source_digest=?,updated_at="+webNow+" WHERE kind='native' AND task_id=?", nativeDigest(t), id).Error
		}
		return nil
	})
}
func (r *NativeQueries) Pending(ctx context.Context, limit int) ([]int64, error) {
	v := []int64{}
	if limit < 1 {
		return v, nil
	}
	if limit > 1000 {
		limit = 1000
	}
	// Exhausted abandoned claims become terminal as well as explicit parse failures.
	if e := expireExhaustedJobs(ctx, r.db, "native", limit); e != nil {
		return nil, e
	}
	e := r.db.WithContext(ctx).Raw("SELECT task_id FROM logan_processing_job WHERE kind='native' AND attempts<? AND ((state IN ('pending','retry_wait') AND next_run_at<="+webNow+") OR (state='running' AND lease_until<="+webNow+")) ORDER BY next_run_at,task_id LIMIT ?", webMaxAttempts, limit).Scan(&v).Error
	return v, e
}
func (r *NativeQueries) Ready(ctx context.Context, id int64) (bool, error) {
	t, e := r.Find(ctx, id)
	if e != nil {
		return false, e
	}
	if t == nil || t.Status == 1 {
		return true, nil
	}
	var j webJob
	if e = r.db.WithContext(ctx).Raw("SELECT state FROM logan_processing_job WHERE kind='native' AND task_id=?", id).Scan(&j).Error; e != nil {
		return false, e
	}
	if j.State == "failed" {
		return false, errors.New("native processing failed")
	}
	return false, nil
}
func (r *NativeQueries) Process(ctx context.Context, id int64, parse func(context.Context, NativeTask) (io.ReadCloser, error)) error {
	token := make([]byte, 16)
	if _, e := rand.Read(token); e != nil {
		return e
	}
	owner := hex.EncodeToString(token)
	claim := r.db.WithContext(ctx).Exec("UPDATE logan_processing_job SET state='running',owner=?,fence=fence+1,attempts=attempts+1,lease_until="+webNow+"+30000,updated_at="+webNow+" WHERE kind='native' AND task_id=? AND attempts<? AND ((state IN ('pending','retry_wait') AND next_run_at<="+webNow+") OR (state='running' AND lease_until<="+webNow+"))", owner, id, webMaxAttempts)
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil
	}
	ctx, trace := processingtime.New(ctx)
	started := time.Now()
	err := r.processOwned(ctx, id, owner, parse)
	slog.Info("native processing stages", "task_id", id, "outcome", ErrorClass(err), "duration_ms", float64(time.Since(started))/float64(time.Millisecond), "stages_ms", trace.Milliseconds())
	return err
}

func (r *NativeQueries) processOwned(ctx context.Context, id int64, owner string, parse func(context.Context, NativeTask) (io.ReadCloser, error)) error {
	var job webJob
	var task NativeTask
	e := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		job, err = lockNativeJob(tx, id)
		if err != nil {
			return err
		}
		if job.Owner != owner || job.State != "running" {
			return errWebLease
		}
		task, err = lockNativeTask(tx, id)
		if err != nil {
			return err
		}
		if task.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,updated_at="+webNow+" WHERE kind='native' AND task_id=? AND owner=? AND fence=?", id, owner, job.Fence).Error
		}
		if nativeDigest(task) != job.SourceDigest {
			return errWebLease
		}
		q := tx.Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000) WHERE kind='native' AND task_id=? AND state='running' AND owner=? AND fence=? AND lease_until>"+webNow, id, owner, job.Fence)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errWebLease
		}
		return nil
	})
	if e != nil {
		return r.fail(ctx, id, owner, job.Fence, e)
	}
	if task.Status == 1 {
		return nil
	}
	// First-attempt enqueue-to-owned-snapshot uses only the database clock.
	// Retry age includes previous work/backoff, so do not label it queue wait.
	if job.Attempts == 1 && job.CreatedAt > 0 {
		processingtime.Add(ctx, "queue", time.Duration(max(int64(0), job.ObservedAt-job.CreatedAt))*time.Millisecond)
	}
	parseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	ended := make(chan struct{})
	leaseErrors := make(chan error, 1)
	go func() {
		defer close(ended)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-parseCtx.Done():
				return
			case <-ticker.C:
				q := r.db.WithContext(parseCtx).Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000),updated_at="+webNow+" WHERE kind='native' AND task_id=? AND state='running' AND owner=? AND fence=? AND lease_until>"+webNow, id, owner, job.Fence)
				if q.Error != nil || q.RowsAffected != 1 {
					err := q.Error
					if err == nil {
						err = errWebLease
					}
					leaseErrors <- err
					cancel()
					return
				}
			}
		}
	}()
	details, parseErr := parse(parseCtx, task)
	if details != nil {
		defer details.Close()
	}
	if parseErr == nil && details == nil {
		parseErr = errors.New("native parser returned no stream")
	}
	close(stop)
	<-ended
	select {
	case leaseErr := <-leaseErrors:
		parseErr = leaseErr
	default:
	}
	if parseErr != nil {
		return r.fail(ctx, id, owner, job.Fence, parseErr)
	}
	if e := ctx.Err(); e != nil {
		return r.fail(ctx, id, owner, job.Fence, e)
	}
	publicationStarted := time.Now()
	var decodeElapsed time.Duration
	e = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockNativeJob(tx, id)
		if err != nil {
			return err
		}
		if current.State == "done" {
			return nil
		}
		if current.State != "running" || current.Owner != owner || current.Fence != job.Fence || current.SourceRevision != job.SourceRevision || current.SourceDigest != job.SourceDigest {
			return errWebLease
		}
		t, err := lockNativeTask(tx, id)
		if err != nil {
			return err
		}
		if t.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0 WHERE kind='native' AND task_id=?", id).Error
		}
		if nativeDigest(t) != job.SourceDigest {
			return errWebLease
		}
		// The conditional write checks lease validity using the database clock after locks.
		q := tx.Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000) WHERE kind='native' AND task_id=? AND owner=? AND fence=? AND lease_until>"+webNow, id, owner, job.Fence)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errWebLease
		}
		if err := clearPublicationDetails(tx, "logan_log_detail", id); err != nil {
			return err
		}
		// The job row stays locked until commit. Its owner cannot be replaced
		// during publication, so renewal here need not require an unexpired
		// previous lease. The transaction context bounds publication time.
		renewPublicationLease := func() error {
			q := tx.Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000),updated_at="+webNow+" WHERE kind='native' AND task_id=? AND state='running' AND owner=? AND fence=?", id, owner, job.Fence)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 {
				return errWebLease
			}
			return nil
		}
		decoder := json.NewDecoder(details)
		const batchByteBudget = 1 << 20
		batch := make([]NativeDetail, 0, 200)
		var batchBytes int
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := renewPublicationLease(); err != nil {
				return err
			}
			insertStarted := time.Now()
			insertErr := tx.CreateInBatches(batch, 200).Error
			processingtime.Add(ctx, "db_insert", time.Since(insertStarted))
			if err := insertErr; err != nil {
				return err
			}
			clear(batch)
			batch = batch[:0]
			batchBytes = 0
			return nil
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var d NativeDetail
			decodeStarted := time.Now()
			err := decoder.Decode(&d)
			decodeElapsed += time.Since(decodeStarted)
			if err == io.EOF {
				break
			}
			if err != nil {
				return errors.New("native staging stream invalid")
			}
			d.ID = 0
			d.TaskID = id
			// A lone historical record may exceed the batch budget; the parser
			// already enforces the configured per-record limit.
			if len(batch) > 0 && (len(batch) == 200 || len(d.Content) > batchByteBudget-batchBytes) {
				if err := flush(); err != nil {
					return err
				}
			}
			batch = append(batch, d)
			batchBytes += len(d.Content)
		}
		if err := flush(); err != nil {
			return err
		}

		if err := renewPublicationLease(); err != nil {
			return err
		}
		if err := tx.Exec("UPDATE logan_task SET status=1 WHERE id=?", id).Error; err != nil {
			return err
		}
		q = tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,last_error_code='',updated_at="+webNow+" WHERE kind='native' AND task_id=? AND state='running' AND owner=? AND fence=?", id, owner, job.Fence)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errWebLease
		}
		return nil
	})
	processingtime.Add(ctx, "publication", time.Since(publicationStarted))
	processingtime.Add(ctx, "stage_decode", decodeElapsed)
	if e != nil {
		return r.fail(ctx, id, owner, job.Fence, e)
	}
	return nil
}
func (r *NativeQueries) fail(ctx context.Context, id int64, owner string, fence int64, cause error) error {
	// Cleanup has a separate bounded lifetime when the worker context expired. The
	// ownership CAS also makes this safe after an ambiguous successful COMMIT.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var cleanupErr error
	for attempt := 0; attempt < 3; attempt++ {
		q := r.db.WithContext(cleanup).Exec("UPDATE logan_processing_job SET state=IF(attempts>=?,'failed','retry_wait'),owner='',lease_until=0,next_run_at="+webNow+"+1000,last_error_code='processing_error',updated_at="+webNow+" WHERE kind='native' AND task_id=? AND state='running' AND owner=? AND fence=?", webMaxAttempts, id, owner, fence)
		cleanupErr = q.Error
		if cleanupErr == nil {
			return cause
		}
		if cleanup.Err() != nil {
			break
		}
	}
	return errors.Join(cause, cleanupErr)
}
