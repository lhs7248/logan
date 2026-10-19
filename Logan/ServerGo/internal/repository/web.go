package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// WebRepository coordinates mutable sources and durable jobs across server instances.
// All coordinated paths lock the job before the source; uploads additionally lock identity.
type WebRepository struct {
	db       *gorm.DB
	maxBytes int64
}

// ErrWebLimit distinguishes an exhausted resource budget from legacy DB failures.
var ErrWebLimit = errors.New("web result exceeds resource limit")

func NewWebRepository(db *gorm.DB, budgets ...int64) *WebRepository {
	budget := int64(32 << 20)
	if len(budgets) > 0 && budgets[0] > 0 {
		budget = budgets[0]
	}
	return &WebRepository{db: db, maxBytes: budget}
}

const webNow = "(TIMESTAMPDIFF(MICROSECOND,'1970-01-01 00:00:00',UTC_TIMESTAMP(3)) DIV 1000)"
const webMaxAttempts = 3

var errWebLease = errors.New("web processing ownership or source changed")

type webJob struct {
	TaskID                     int64
	State, Owner, SourceDigest string
	Fence, SourceRevision      int64
	CreatedAt, ObservedAt      int64
	Attempts                   int
}

func digestWeb(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func lockWebJob(tx *gorm.DB, id int64) (webJob, error) {
	var j webJob
	q := tx.Raw("SELECT task_id,state,owner,fence,source_revision,source_digest,attempts FROM logan_processing_job WHERE kind='web' AND task_id=? FOR UPDATE", id).Scan(&j)
	return j, q.Error
}
func lockWebTask(tx *gorm.DB, id int64) (WebTask, error) {
	var t WebTask
	q := tx.Raw("SELECT * FROM web_task WHERE id=? FOR UPDATE", id).Scan(&t)
	if q.Error == nil && q.RowsAffected == 0 {
		return t, gorm.ErrRecordNotFound
	}
	return t, q.Error
}
func (r *WebRepository) Validate(ctx context.Context) error {
	// Probe required columns, rather than creating or silently repairing schema.
	for _, q := range []string{"SELECT kind,task_id,source_revision,source_digest,state,owner,lease_until,fence,attempts,next_run_at,last_error_code,created_at,updated_at FROM logan_processing_job LIMIT 0", "SELECT identity_key FROM logan_web_upload_lock LIMIT 0"} {
		rows, e := r.db.WithContext(ctx).Raw(q).Rows()
		if e != nil {
			return fmt.Errorf("web processing migration required: %w", e)
		}
		rows.Close()
	}
	var engines []struct {
		TableName string
		Engine    string
	}
	if err := r.db.WithContext(ctx).Raw("SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('web_task','web_detail','logan_processing_job','logan_web_upload_lock')").Scan(&engines).Error; err != nil {
		return err
	}
	if len(engines) != 4 {
		return errors.New("web processing requires all four transactional tables")
	}
	for _, table := range engines {
		if table.Engine != "InnoDB" {
			return fmt.Errorf("web processing requires InnoDB: %s", table.TableName)
		}
	}
	return nil
}
func (r *WebRepository) Save(ctx context.Context, t *WebTask) error {
	tuple, _ := json.Marshal([]any{t.LogDate, t.PageNum})
	key := digestWeb(string(tuple))
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("INSERT INTO logan_web_upload_lock(identity_key) VALUES (?) ON DUPLICATE KEY UPDATE identity_key=identity_key", key).Error; e != nil {
			return e
		}
		var identity string
		if e := tx.Raw("SELECT identity_key FROM logan_web_upload_lock WHERE identity_key=? FOR UPDATE", key).Scan(&identity).Error; e != nil {
			return e
		}
		// Date/page coordination is deliberately coarser than page identity: device
		// equality follows the legacy MySQL collation, which can ignore case/accents.
		// Hashing raw device strings could let equivalent identities bypass the lock.
		var old WebTask
		q := tx.Raw("SELECT * FROM web_task WHERE log_date=? AND device_id=? AND page_num=? LIMIT 1", t.LogDate, t.DeviceID, t.PageNum).Scan(&old)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			return tx.Create(t).Error
		}
		j, e := lockWebJob(tx, old.ID)
		if e != nil {
			return e
		}
		old, e = lockWebTask(tx, old.ID)
		if e != nil {
			return e
		}
		if e := tx.Exec("UPDATE web_task SET content=? WHERE id=?", t.Content, old.ID).Error; e != nil {
			return e
		}
		if old.Status != 1 && j.TaskID != 0 {
			if e := tx.Exec("UPDATE logan_processing_job SET source_revision=source_revision+1,source_digest=?,fence=fence+1,state='pending',owner='',lease_until=0,attempts=0,next_run_at=0,last_error_code='',updated_at="+webNow+" WHERE kind='web' AND task_id=?", digestWeb(t.Content), old.ID).Error; e != nil {
				return e
			}
		}
		old.Content = t.Content
		*t = old
		return nil
	})
}
func (r *WebRepository) Tasks(ctx context.Context, ids []int64) ([]WebTask, error) {
	v := []WebTask{}
	if len(ids) == 0 {
		return v, nil
	}
	e := r.db.WithContext(ctx).Omit("content").Where("id IN ?", ids).Order("id ASC").Find(&v).Error
	return v, e
}
func (r *WebRepository) Search(ctx context.Context, b, e int64, device string) ([]WebTask, error) {
	v := []WebTask{}
	// Historical varchar metadata fits within a conservative 4 KiB row surrogate.
	// One extra row detects exhaustion; callers receive an error, never truncation.
	rowBudget := max(int64(1), r.maxBytes/4096)
	if rowBudget >= int64(int(^uint(0)>>1)) {
		rowBudget = int64(int(^uint(0)>>1)) - 1
	}
	err := r.db.WithContext(ctx).Omit("content").Where("add_time BETWEEN ? AND ? AND device_id = ?", b, e, device).Order("id ASC").Limit(int(rowBudget + 1)).Find(&v).Error
	if err != nil {
		return nil, err
	}
	if int64(len(v)) > rowBudget {
		return nil, ErrWebLimit
	}
	return v, nil
}
func (r *WebRepository) Latest(ctx context.Context) ([]WebTask, error) {
	v := []WebTask{}
	var maxID struct{ ID int64 }
	if e := r.db.WithContext(ctx).Raw("SELECT COALESCE(MAX(id),0) AS id FROM web_task").Scan(&maxID).Error; e != nil {
		return nil, e
	}
	e := r.db.WithContext(ctx).Omit("content").Where("id BETWEEN ? AND ?", max(int64(0), maxID.ID-200), maxID.ID).Order("id ASC").Find(&v).Error
	return v, e
}

// Contents checks byte lengths before any body is loaded, then applies SQL byte
// guards to every fetch so concurrent uploads cannot escape the download budget.
func (r *WebRepository) Contents(ctx context.Context, ids []int64, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, ErrWebLimit
	}
	if len(ids) == 0 {
		return []byte{}, nil
	}
	var pages []struct {
		ID           int64
		PageNum      int
		ContentBytes int64
	}
	if err := r.db.WithContext(ctx).Raw("SELECT id,page_num,COALESCE(OCTET_LENGTH(content),0) AS content_bytes FROM web_task WHERE id IN ? ORDER BY page_num ASC,id ASC", ids).Scan(&pages).Error; err != nil {
		return nil, err
	}
	var total int64
	for _, p := range pages {
		if p.ContentBytes < 0 || p.ContentBytes > maxBytes-total {
			return nil, ErrWebLimit
		}
		total += p.ContentBytes
	}
	out := []byte{}
	for _, p := range pages {
		var body struct{ Content string }
		remaining := maxBytes - int64(len(out))
		q := r.db.WithContext(ctx).Raw("SELECT content FROM web_task WHERE id=? AND COALESCE(OCTET_LENGTH(content),0)<=? AND COALESCE(OCTET_LENGTH(content),0)=?", p.ID, remaining, p.ContentBytes).Scan(&body)
		if q.Error != nil {
			return nil, q.Error
		}
		if q.RowsAffected != 1 || int64(len(body.Content)) != p.ContentBytes || int64(len(body.Content)) > remaining {
			return nil, errors.New("web download source changed")
		}
		out = append(out, body.Content...)
	}
	return out, nil
}
func (r *WebRepository) Details(ctx context.Context, ids []int64) ([]WebDetail, error) {
	v := []WebDetail{}
	if len(ids) == 0 {
		return v, nil
	}
	e := r.db.WithContext(ctx).Where("id IN ?", ids).Order("id ASC").Find(&v).Error
	return v, e
}

// BoundedDetails preflights byte lengths before loading any bodies and rechecks
// each row in SQL. Nullable legacy content is treated as the empty string.
func (r *WebRepository) BoundedDetails(ctx context.Context, ids []int64, maxBytes int64) ([]WebDetail, error) {
	if maxBytes < 0 {
		return nil, ErrWebLimit
	}
	out := []WebDetail{}
	if len(ids) == 0 {
		return out, nil
	}
	var sizes []struct {
		ID           int64
		ContentBytes int64
	}
	if err := r.db.WithContext(ctx).Raw("SELECT id,COALESCE(OCTET_LENGTH(content),0) AS content_bytes FROM web_detail WHERE id IN ? ORDER BY id ASC", ids).Scan(&sizes).Error; err != nil {
		return nil, err
	}
	var total int64
	for _, row := range sizes {
		if row.ContentBytes < 0 || row.ContentBytes > maxBytes-total {
			return nil, ErrWebLimit
		}
		total += row.ContentBytes
	}
	var used int64
	for _, row := range sizes {
		var detail WebDetail
		remaining := maxBytes - used
		q := r.db.WithContext(ctx).Raw("SELECT id,task_id,log_type,COALESCE(content,'') AS content,log_time,log_level,add_time,minute_offset FROM web_detail WHERE id=? AND COALESCE(OCTET_LENGTH(content),0)<=? AND COALESCE(OCTET_LENGTH(content),0)=?", row.ID, remaining, row.ContentBytes).Scan(&detail)
		if q.Error != nil {
			return nil, q.Error
		}
		n := int64(len(detail.Content))
		if q.RowsAffected != 1 || n != row.ContentBytes || n > remaining {
			return nil, errors.New("web detail source changed")
		}
		used += n
		out = append(out, detail)
	}
	return out, nil
}
func (r *WebRepository) Match(ctx context.Context, id int64, b, e int, types []int) ([]WebDetail, error) {
	v := []WebDetail{}
	q := r.db.WithContext(ctx).Where("task_id = ? AND minute_offset BETWEEN ? AND ?", id, b, e)
	if len(types) > 0 {
		q = q.Where("log_type IN ?", types)
	}
	err := q.Order("id ASC").Find(&v).Error
	return v, err
}

// MatchIndexes filters literal, case-sensitive keywords in SQL and transfers no
// detail bodies. The extra row detects a result budget overflow without truncation.
func (r *WebRepository) MatchIndexes(ctx context.Context, id int64, b, e int, types []int, keyword string, limit int) ([]WebDetail, error) {
	if limit < 0 || limit == int(^uint(0)>>1) {
		return nil, errors.New("invalid web index limit")
	}
	query := "SELECT id,log_type,log_time FROM web_detail WHERE task_id=? AND minute_offset BETWEEN ? AND ?"
	args := []any{id, b, e}
	if len(types) > 0 {
		query += " AND log_type IN ?"
		args = append(args, types)
	}
	if keyword != "" {
		query += " AND INSTR(BINARY content, BINARY ?) > 0"
		args = append(args, keyword)
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit+1)
	out := []WebDetail{}
	if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&out).Error; err != nil {
		return nil, err
	}
	if len(out) > limit {
		return nil, ErrWebLimit
	}
	return out, nil
}
func (r *WebRepository) Ensure(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("INSERT INTO logan_processing_job(kind,task_id,created_at,updated_at) VALUES ('web',?,"+webNow+","+webNow+") ON DUPLICATE KEY UPDATE task_id=task_id", id).Error; e != nil {
			return e
		}
		j, e := lockWebJob(tx, id)
		if e != nil {
			return e
		}
		t, e := lockWebTask(tx, id)
		if e != nil {
			return e
		}
		if t.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,updated_at="+webNow+" WHERE kind='web' AND task_id=?", id).Error
		}
		if j.SourceDigest == "" {
			return tx.Exec("UPDATE logan_processing_job SET source_digest=?,updated_at="+webNow+" WHERE kind='web' AND task_id=?", digestWeb(t.Content), id).Error
		}
		return nil
	})
}
func (r *WebRepository) Pending(ctx context.Context, limit int) ([]int64, error) {
	v := []int64{}
	if limit < 1 {
		return v, nil
	}
	if limit > 1000 {
		limit = 1000
	}
	// Exhausted abandoned claims become terminal as well as explicit parse failures.
	if e := expireExhaustedJobs(ctx, r.db, "web", limit); e != nil {
		return nil, e
	}
	e := r.db.WithContext(ctx).Raw("SELECT task_id FROM logan_processing_job WHERE kind='web' AND attempts<? AND ((state IN ('pending','retry_wait') AND next_run_at<="+webNow+") OR (state='running' AND lease_until<="+webNow+")) ORDER BY next_run_at,task_id LIMIT ?", webMaxAttempts, limit).Scan(&v).Error
	return v, e
}
func (r *WebRepository) Ready(ctx context.Context, ids []int64) (bool, error) {
	tasks, e := r.Tasks(ctx, ids)
	if e != nil {
		return false, e
	}
	ready := true
	for _, t := range tasks {
		if t.Status == 1 {
			continue
		}
		var j webJob
		if e := r.db.WithContext(ctx).Raw("SELECT state FROM logan_processing_job WHERE kind='web' AND task_id=?", t.ID).Scan(&j).Error; e != nil {
			return false, e
		}
		if j.State == "failed" {
			return false, errors.New("web processing failed")
		}
		ready = false
	}
	return ready, nil
}
func (r *WebRepository) Process(ctx context.Context, id int64, parse func(context.Context, WebTask) ([]WebDetail, error)) error {
	token := make([]byte, 16)
	if _, e := rand.Read(token); e != nil {
		return e
	}
	owner := hex.EncodeToString(token)
	claim := r.db.WithContext(ctx).Exec("UPDATE logan_processing_job SET state='running',owner=?,fence=fence+1,attempts=attempts+1,lease_until="+webNow+"+30000,updated_at="+webNow+" WHERE kind='web' AND task_id=? AND attempts<? AND ((state IN ('pending','retry_wait') AND next_run_at<="+webNow+") OR (state='running' AND lease_until<="+webNow+"))", owner, id, webMaxAttempts)
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected != 1 {
		return nil
	}
	return r.processOwned(ctx, id, owner, parse)
}

func (r *WebRepository) processOwned(ctx context.Context, id int64, owner string, parse func(context.Context, WebTask) ([]WebDetail, error)) error {
	var job webJob
	var task WebTask
	e := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		job, err = lockWebJob(tx, id)
		if err != nil {
			return err
		}
		if job.Owner != owner || job.State != "running" {
			return errWebLease
		}
		task, err = lockWebTask(tx, id)
		if err != nil {
			return err
		}
		if task.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,updated_at="+webNow+" WHERE kind='web' AND task_id=? AND owner=? AND fence=?", id, owner, job.Fence).Error
		}
		if digestWeb(task.Content) != job.SourceDigest {
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
				q := r.db.WithContext(parseCtx).Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000),updated_at="+webNow+" WHERE kind='web' AND task_id=? AND state='running' AND owner=? AND fence=? AND lease_until>"+webNow, id, owner, job.Fence)
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
	e = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := lockWebJob(tx, id)
		if err != nil {
			return err
		}
		if current.State == "done" {
			return nil
		}
		if current.State != "running" || current.Owner != owner || current.Fence != job.Fence || current.SourceRevision != job.SourceRevision || current.SourceDigest != job.SourceDigest {
			return errWebLease
		}
		t, err := lockWebTask(tx, id)
		if err != nil {
			return err
		}
		if t.Status == 1 {
			return tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0 WHERE kind='web' AND task_id=?", id).Error
		}
		if digestWeb(t.Content) != job.SourceDigest {
			return errWebLease
		}
		// The conditional write checks lease validity using the database clock after locks.
		q := tx.Exec("UPDATE logan_processing_job SET lease_until=GREATEST(lease_until+1,"+webNow+"+30000) WHERE kind='web' AND task_id=? AND owner=? AND fence=? AND lease_until>"+webNow, id, owner, job.Fence)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errWebLease
		}
		if err := clearPublicationDetails(tx, "web_detail", id); err != nil {
			return err
		}
		for i := 0; i < len(details); i += 200 {
			end := i + 200
			if end > len(details) {
				end = len(details)
			}
			batch := details[i:end]
			for k := range batch {
				batch[k].ID = 0
				batch[k].TaskID = id
			}
			if err := tx.CreateInBatches(batch, 200).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("UPDATE web_task SET status=1 WHERE id=?", id).Error; err != nil {
			return err
		}
		q = tx.Exec("UPDATE logan_processing_job SET state='done',owner='',lease_until=0,last_error_code='',updated_at="+webNow+" WHERE kind='web' AND task_id=? AND owner=? AND fence=?", id, owner, job.Fence)
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
	return nil
}
func (r *WebRepository) fail(ctx context.Context, id int64, owner string, fence int64, cause error) error {
	// Cleanup has a separate bounded lifetime when the worker context expired. The
	// ownership CAS also makes this safe after an ambiguous successful COMMIT.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	q := r.db.WithContext(cleanup).Exec("UPDATE logan_processing_job SET state=IF(attempts>=?,'failed','retry_wait'),owner='',lease_until=0,next_run_at="+webNow+"+1000,last_error_code='processing_error',updated_at="+webNow+" WHERE kind='web' AND task_id=? AND state='running' AND owner=? AND fence=?", webMaxAttempts, id, owner, fence)
	if q.Error != nil {
		return errors.Join(cause, q.Error)
	}
	return cause
}
