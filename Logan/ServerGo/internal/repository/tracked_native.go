package repository

import (
	"context"
	"gorm.io/gorm"
)

// TrackedNativeTasks serializes publication against GC using the managed object
// row. An intent is single use: deleted tombstones also reject UUID reuse.
type TrackedNativeTasks struct {
	db    *gorm.DB
	scope string
}

func NewTrackedNativeTasks(db *gorm.DB, scope string) *TrackedNativeTasks {
	return &TrackedNativeTasks{db: db, scope: scope}
}
func (r *TrackedNativeTasks) Find(ctx context.Context, id int64) (*NativeTask, error) {
	return NewNativeTasks(r.db).Find(ctx, id)
}
func (r *TrackedNativeTasks) Create(ctx context.Context, t *NativeTask) error {
	if t == nil || t.LogFileName == nil || !validObject(r.scope, *t.LogFileName) {
		return errManagedObject
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, e := lockObject(tx, r.scope, digestWeb(*t.LogFileName))
		if e != nil {
			return e
		}
		if o.State != "uploading" || o.Name != *t.LogFileName {
			return errManagedObject
		}
		if e := tx.Create(t).Error; e != nil {
			return e
		}
		return maintenanceCAS(tx.Exec("UPDATE logan_managed_object SET state='referenced',updated_at="+webNow+" WHERE scope=? AND key_hash=? AND state='uploading'", r.scope, o.KeyHash))
	})
}
