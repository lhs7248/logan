package repository

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"logan/servergo/internal/maintenance"
	"time"
	"unicode/utf8"
)

var errMaintenanceLease = errors.New("maintenance ownership lost")
var errManagedObject = errors.New("managed object unavailable")

type Maintenance struct {
	db    *gorm.DB
	scope string
	bound bool
}

func NewMaintenance(db *gorm.DB, scopes ...string) *Maintenance {
	r := &Maintenance{db: db}
	if len(scopes) > 0 {
		r.scope = scopes[0]
		r.bound = true
	}
	return r
}
func (r *Maintenance) checkScope(scope string) error {
	if r.bound && (r.scope == "" || r.scope != scope) {
		return errors.New("maintenance storage namespace mismatch")
	}
	return nil
}

var _ maintenance.Repository = (*Maintenance)(nil)

func boundedTTL(ttl time.Duration) int64 {
	return max(int64(1), min(ttl.Milliseconds(), int64(300000)))
}
func boundedBatch(n int) int { return max(1, min(n, 1000)) }
func validObject(scope, name string) bool {
	return scope != "" && len(scope) <= 64 && name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 1024
}
func (r *Maintenance) BeginObject(ctx context.Context, scope, name string) error {
	if r.checkScope(scope) != nil || !validObject(scope, name) {
		return errManagedObject
	}
	return r.db.WithContext(ctx).Exec("INSERT INTO logan_managed_object(scope,key_hash,name,state,created_at,updated_at) VALUES (?,?,?,'uploading',"+webNow+","+webNow+")", scope, digestWeb(name), name).Error
}
func (r *Maintenance) Validate(ctx context.Context) error {
	queries := []string{
		"SELECT name,owner,fence,lease_until FROM logan_maintenance_lease LIMIT 0",
		"SELECT scope,key_hash,name,state,owner,fence,lease_until,attempts,next_run_at,created_at,updated_at,last_error_code FROM logan_managed_object LIMIT 0",
		"SELECT kind,task_id,state,owner,fence,lease_until FROM logan_processing_job LIMIT 0",
		"SELECT id,add_time,log_file_name FROM logan_task LIMIT 0", "SELECT id,task_id FROM logan_log_detail LIMIT 0", "SELECT id,add_time FROM web_task LIMIT 0", "SELECT id,task_id FROM web_detail LIMIT 0",
	}
	if r.bound {
		queries = append(queries, "SELECT id,scope FROM logan_storage_namespace LIMIT 0")
	}
	for _, q := range queries {
		rows, e := r.db.WithContext(ctx).Raw(q).Rows()
		if e != nil {
			return errors.New("maintenance migration required")
		}
		if e = rows.Close(); e != nil {
			return e
		}
	}
	var tables []struct{ TableName, Engine string }
	names := "'logan_maintenance_lease','logan_managed_object','logan_processing_job','logan_task','logan_log_detail','web_task','web_detail'"
	expected := 7
	if r.bound {
		names += ",'logan_storage_namespace'"
		expected++
	}
	if e := r.db.WithContext(ctx).Raw("SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN (" + names + ")").Scan(&tables).Error; e != nil {
		return e
	}
	if len(tables) != expected {
		return errors.New("maintenance transactional tables required")
	}
	for _, t := range tables {
		if t.Engine != "InnoDB" {
			return errors.New("maintenance requires InnoDB")
		}
	}
	if r.bound {
		if r.scope == "" || len(r.scope) > 64 {
			return errors.New("maintenance storage namespace invalid")
		}
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var other string
			q := tx.Raw("SELECT scope FROM logan_managed_object WHERE scope<>? LIMIT 1", r.scope).Scan(&other)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 0 {
				return errors.New("maintenance registry namespace mismatch")
			}
			// The singleton primary key serializes first startup from two namespaces.
			if e := tx.Exec("INSERT IGNORE INTO logan_storage_namespace(id,scope) VALUES (1,?)", r.scope).Error; e != nil {
				return e
			}
			var stored string
			q = tx.Raw("SELECT scope FROM logan_storage_namespace WHERE id=1 FOR UPDATE").Scan(&stored)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 || stored != r.scope {
				return errors.New("maintenance storage namespace mismatch")
			}
			return nil
		})
	}
	return nil
}
func (r *Maintenance) Acquire(ctx context.Context, name, owner string, ttl time.Duration) (l maintenance.Lease, ok bool, err error) {
	if name == "" || owner == "" || len(name) > 128 || len(owner) > 64 {
		return l, false, errMaintenanceLease
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Exec("INSERT IGNORE INTO logan_maintenance_lease(name,owner,fence,lease_until) VALUES (?,'',0,0)", name).Error; e != nil {
			return e
		}
		q := tx.Exec("UPDATE logan_maintenance_lease SET owner=?,fence=fence+1,lease_until="+webNow+"+? WHERE name=? AND lease_until<="+webNow, owner, boundedTTL(ttl), name)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return nil
		}
		l = maintenance.Lease{Name: name, Owner: owner}
		q = tx.Raw("SELECT fence FROM logan_maintenance_lease WHERE name=? AND owner=? FOR UPDATE", name, owner).Scan(&l.Fence)
		if q.Error != nil {
			return q.Error
		}
		ok = q.RowsAffected == 1
		return nil
	})
	if err != nil {
		ok = false
	}
	return
}
func (r *Maintenance) Renew(ctx context.Context, l maintenance.Lease, ttl time.Duration) error {
	return maintenanceCAS(r.db.WithContext(ctx).Exec("UPDATE logan_maintenance_lease SET lease_until=GREATEST(lease_until+1,"+webNow+"+?) WHERE name=? AND owner=? AND fence=? AND lease_until>"+webNow, boundedTTL(ttl), l.Name, l.Owner, l.Fence))
}
func (r *Maintenance) Release(ctx context.Context, l maintenance.Lease) error {
	return maintenanceCAS(r.db.WithContext(ctx).Exec("UPDATE logan_maintenance_lease SET owner='',lease_until=0 WHERE name=? AND owner=? AND fence=? AND lease_until>"+webNow, l.Name, l.Owner, l.Fence))
}
func maintenanceCAS(q *gorm.DB) error {
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return errMaintenanceLease
	}
	return nil
}
func lockMaintenance(tx *gorm.DB, l maintenance.Lease) error {
	var name string
	q := tx.Raw("SELECT name FROM logan_maintenance_lease WHERE name=? AND owner=? AND fence=? AND lease_until>"+webNow+" FOR UPDATE", l.Name, l.Owner, l.Fence).Scan(&name)
	return maintenanceCAS(q)
}
func (r *Maintenance) transaction(ctx context.Context, l maintenance.Lease, fn func(*gorm.DB) error) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		if e := lockMaintenance(tx, l); e != nil {
			return e
		}
		return fn(tx)
	})
}

type managedObject struct {
	CreatedAt, UpdatedAt               int64
	Scope, KeyHash, Name, State, Owner string
	Fence, LeaseUntil, Attempts        int64
}

func lockObject(tx *gorm.DB, scope, hash string) (o managedObject, e error) {
	q := tx.Raw("SELECT scope,key_hash,name,state,owner,fence,lease_until,attempts,created_at,updated_at FROM logan_managed_object WHERE scope=? AND key_hash=? FOR UPDATE", scope, hash).Scan(&o)
	e = q.Error
	if e == nil && q.RowsAffected != 1 {
		e = errManagedObject
	}
	return
}
func objectReferenced(tx *gorm.DB, name string) (bool, error) {
	var id int64
	q := tx.Raw("SELECT id FROM logan_task WHERE BINARY log_file_name=BINARY ? LIMIT 1", name).Scan(&id)
	return q.RowsAffected > 0, q.Error
}
func markReferenced(tx *gorm.DB, o managedObject) error {
	return tx.Exec("UPDATE logan_managed_object SET state='referenced',owner='',lease_until=0,updated_at="+webNow+" WHERE scope=? AND key_hash=?", o.Scope, o.KeyHash).Error
}
func (r *Maintenance) Sweep(ctx context.Context, l maintenance.Lease, scope string, before int64, batch int) (int, error) {
	if e := r.checkScope(scope); e != nil {
		return 0, e
	}

	var hashes []string
	e := r.db.WithContext(ctx).Raw("SELECT key_hash FROM logan_managed_object WHERE scope=? AND state IN ('uploading','referenced') AND created_at<? AND updated_at<? ORDER BY key_hash LIMIT ?", scope, before, before, boundedBatch(batch)).Scan(&hashes).Error
	if e != nil {
		return 0, e
	}
	n := 0
	for _, h := range hashes {
		changed := false
		e = r.transaction(ctx, l, func(tx *gorm.DB) error {
			o, e := lockObject(tx, scope, h)
			if e != nil {
				return e
			}
			if (o.State != "uploading" && o.State != "referenced") || o.CreatedAt >= before || o.UpdatedAt >= before {
				return nil
			}
			ref, e := objectReferenced(tx, o.Name)
			if e != nil {
				return e
			}
			if ref {
				return markReferenced(tx, o)
			}
			e = tx.Exec("UPDATE logan_managed_object SET state='pending',next_run_at=0,updated_at="+webNow+" WHERE scope=? AND key_hash=?", scope, h).Error
			changed = e == nil
			return e
		})
		if e != nil {
			return n, e
		}
		if changed {
			n++
		}
	}
	return n, nil
}
func (r *Maintenance) Claim(ctx context.Context, l maintenance.Lease, scope string, ttl time.Duration, batch int) ([]maintenance.Object, error) {
	if e := r.checkScope(scope); e != nil {
		return nil, e
	}

	var hashes []string
	out := []maintenance.Object{}
	e := r.db.WithContext(ctx).Raw("SELECT key_hash FROM logan_managed_object WHERE scope=? AND ((state='pending' AND next_run_at<="+webNow+") OR (state='deleting' AND lease_until<="+webNow+")) ORDER BY key_hash LIMIT ?", scope, boundedBatch(batch)).Scan(&hashes).Error
	if e != nil {
		return nil, e
	}
	for _, h := range hashes {
		var claimed *maintenance.Object
		e = r.transaction(ctx, l, func(tx *gorm.DB) error {
			o, e := lockObject(tx, scope, h)
			if e != nil {
				return e
			}
			if o.State != "pending" && o.State != "deleting" {
				return nil
			}

			ref, e := objectReferenced(tx, o.Name)
			if e != nil {
				return e
			}
			if ref {
				return markReferenced(tx, o)
			}
			if o.Attempts >= 12 {
				return tx.Exec("UPDATE logan_managed_object SET state='failed',owner='',lease_until=0,last_error_code='attempts_exhausted',updated_at="+webNow+" WHERE scope=? AND key_hash=? AND attempts>=12 AND ((state='pending' AND next_run_at<="+webNow+") OR (state='deleting' AND lease_until<="+webNow+"))", scope, h).Error
			}
			q := tx.Exec("UPDATE logan_managed_object SET state='deleting',owner=?,fence=fence+1,attempts=attempts+1,lease_until="+webNow+"+?,updated_at="+webNow+" WHERE scope=? AND key_hash=? AND attempts<12 AND ((state='pending' AND next_run_at<="+webNow+") OR (state='deleting' AND lease_until<="+webNow+"))", l.Owner, boundedTTL(ttl), scope, h)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected == 1 {
				claimed = &maintenance.Object{Scope: scope, Name: o.Name, Owner: l.Owner, Fence: o.Fence + 1}
			}
			return nil
		})
		if e != nil {
			return out, e
		}
		if claimed != nil {
			out = append(out, *claimed)
		}
	}
	return out, nil
}
func (r *Maintenance) Finish(ctx context.Context, l maintenance.Lease, o maintenance.Object, code string, retry time.Duration) error {
	if e := r.checkScope(o.Scope); e != nil {
		return e
	}

	return r.transaction(ctx, l, func(tx *gorm.DB) error {
		if code == "" {
			return maintenanceCAS(tx.Exec("UPDATE logan_managed_object SET state='deleted',name='',owner='',lease_until=0,last_error_code='',updated_at="+webNow+" WHERE scope=? AND key_hash=? AND state='deleting' AND owner=? AND fence=? AND lease_until>"+webNow, o.Scope, digestWeb(o.Name), o.Owner, o.Fence))
		}
		return maintenanceCAS(tx.Exec("UPDATE logan_managed_object SET state=IF(attempts>=12,'failed','pending'),owner='',lease_until=0,next_run_at="+webNow+"+?,last_error_code='delete_error',updated_at="+webNow+" WHERE scope=? AND key_hash=? AND state='deleting' AND owner=? AND fence=? AND lease_until>"+webNow, boundedTTL(retry), o.Scope, digestWeb(o.Name), o.Owner, o.Fence))
	})
}
func (r *Maintenance) Snapshot(ctx context.Context, scope string) (s maintenance.Snapshot, e error) {
	var rows []struct {
		State string
		N     int64
	}
	e = r.db.WithContext(ctx).Raw("SELECT state,COUNT(*) AS n FROM logan_managed_object WHERE scope=? AND state IN ('pending','deleting','uploading','failed') GROUP BY state", scope).Scan(&rows).Error
	for _, row := range rows {
		switch row.State {
		case "pending":
			s.Pending = row.N
		case "uploading":
			s.Uploading = row.N
		case "deleting":
			s.Deleting = row.N
		case "failed":
			s.Failed = row.N
		}
	}
	return
}

// Prune locks the maintenance lease, processing job and source before the object.
// Counts are published only after COMMIT; any detail failure rolls the task back.
func (r *Maintenance) Prune(ctx context.Context, l maintenance.Lease, cutoff int64, batch int, scope string) (counts maintenance.Counts, err error) {
	if e := r.checkScope(scope); e != nil {
		return counts, e
	}

	for i, kind := range []string{"native", "web"} {
		if e := ctx.Err(); e != nil {
			return counts, errors.Join(err, e)
		}
		remaining := 10 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		kindCtx, cancel := context.WithTimeout(ctx, max(time.Millisecond, remaining/time.Duration(2-i)))
		part, e := r.pruneKind(kindCtx, l, cutoff, batch, scope, kind)
		cancel()
		counts.NativeTasks += part.NativeTasks
		counts.NativeDetails += part.NativeDetails
		counts.WebTasks += part.WebTasks
		counts.WebDetails += part.WebDetails
		if e != nil {
			err = errors.Join(err, e)
			if ctx.Err() != nil {
				return counts, errors.Join(err, ctx.Err())
			}
		}
	}
	return counts, err
}
func (r *Maintenance) pruneKind(ctx context.Context, l maintenance.Lease, cutoff int64, batch int, scope, kind string) (counts maintenance.Counts, err error) {

	table, detail := "logan_task", "logan_log_detail"
	if kind == "web" {
		table, detail = "web_task", "web_detail"
	}
	var ids []int64
	if e := r.db.WithContext(ctx).Raw("SELECT id FROM "+table+" WHERE add_time<? ORDER BY id LIMIT ?", cutoff, boundedBatch(batch)).Scan(&ids).Error; e != nil {
		return counts, e
	}
	for _, id := range ids {
		var removed bool
		var details int64
		e := r.transaction(ctx, l, func(tx *gorm.DB) error {
			if e := tx.Exec("INSERT IGNORE INTO logan_processing_job(kind,task_id,created_at,updated_at) VALUES (?,?,"+webNow+","+webNow+")", kind, id).Error; e != nil {
				return e
			}
			var job struct{ Active int }
			if e := tx.Raw("SELECT (state='running' AND lease_until>"+webNow+") AS active FROM logan_processing_job WHERE kind=? AND task_id=? FOR UPDATE", kind, id).Scan(&job).Error; e != nil {
				return e
			}
			if job.Active != 0 {
				return nil
			}
			var source struct {
				ID, AddTime int64
				LogFileName *string
			}
			columns := "id,add_time"
			if kind == "native" {
				columns += ",log_file_name"
			}
			q := tx.Raw("SELECT "+columns+" FROM "+table+" WHERE id=? FOR UPDATE", id).Scan(&source)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected == 0 || source.AddTime >= cutoff {
				return nil
			}
			// Legacy ownership can be adopted only from this locked expired source.
			if source.LogFileName != nil && *source.LogFileName != "" {
				name := *source.LogFileName
				if !validObject(scope, name) {
					return errManagedObject
				}
				if e := tx.Exec("INSERT IGNORE INTO logan_managed_object(scope,key_hash,name,state,created_at,updated_at) VALUES (?,?,?,'pending',"+webNow+","+webNow+")", scope, digestWeb(name), name).Error; e != nil {
					return e
				}
				o, e := lockObject(tx, scope, digestWeb(name))
				if e != nil {
					return e
				}
				if o.Name != name && o.State != "deleted" {
					return errManagedObject
				}
				if o.State == "uploading" || o.State == "referenced" {
					if e := tx.Exec("UPDATE logan_managed_object SET state='pending',next_run_at=0,updated_at="+webNow+" WHERE scope=? AND key_hash=?", scope, o.KeyHash).Error; e != nil {
						return e
					}
				}
			}
			for {
				if e := tx.Statement.Context.Err(); e != nil {
					return e
				}
				q := tx.Exec("DELETE FROM "+detail+" WHERE task_id=? LIMIT 500", id)
				if q.Error != nil {
					return q.Error
				}
				details += q.RowsAffected
				if q.RowsAffected < 500 {
					break
				}
			}
			if e := tx.Exec("DELETE FROM "+table+" WHERE id=?", id).Error; e != nil {
				return e
			}
			if e := tx.Exec("DELETE FROM logan_processing_job WHERE kind=? AND task_id=?", kind, id).Error; e != nil {
				return e
			}
			removed = true
			return nil
		})
		if e != nil {
			return counts, e
		}
		if removed {
			if kind == "native" {
				counts.NativeTasks++
				counts.NativeDetails += details
			} else {
				counts.WebTasks++
				counts.WebDetails += details
			}
		}
	}
	// Old Java cleanup could leave details behind. The candidate scan and deletion
	// are bounded and recheck parent absence; no multi-table DELETE LIMIT is used.
	var orphanCount int64
	e := r.transaction(ctx, l, func(tx *gorm.DB) error {
		var orphanIDs []int64
		if e := tx.Raw("SELECT d.id FROM "+detail+" d LEFT JOIN "+table+" t ON t.id=d.task_id WHERE t.id IS NULL ORDER BY d.id LIMIT ?", min(boundedBatch(batch), 500)).Scan(&orphanIDs).Error; e != nil {
			return e
		}
		if len(orphanIDs) == 0 {
			return nil
		}
		q := tx.Exec("DELETE FROM "+detail+" WHERE id IN ? AND NOT EXISTS (SELECT 1 FROM "+table+" WHERE "+table+".id="+detail+".task_id)", orphanIDs)
		orphanCount = q.RowsAffected
		return q.Error
	})
	if e != nil {
		return counts, e
	}
	if kind == "native" {
		counts.NativeDetails += orphanCount
	} else {
		counts.WebDetails += orphanCount
	}
	return counts, nil
}
