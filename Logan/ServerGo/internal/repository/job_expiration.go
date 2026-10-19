package repository

import (
	"context"
	"gorm.io/gorm"
)

// expireExhaustedJobs reads candidates without locking a secondary-index range.
// Every write starts with an exact primary key, like claim/publication paths.
// Ownership and the observed lease must still match after the read/write gap.
func expireExhaustedJobs(ctx context.Context, db *gorm.DB, kind string, limit int) error {
	var candidates []struct {
		TaskID            int64
		Owner             string
		Fence, LeaseUntil int64
	}
	if err := db.WithContext(ctx).Raw("SELECT task_id,owner,fence,lease_until FROM logan_processing_job WHERE kind=? AND state='running' AND lease_until<="+webNow+" AND attempts>=? ORDER BY lease_until,task_id LIMIT ?", kind, webMaxAttempts, limit).Scan(&candidates).Error; err != nil {
		return err
	}
	for _, job := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := db.WithContext(ctx).Exec("UPDATE logan_processing_job SET state='failed',last_error_code='attempts_exhausted',updated_at="+webNow+" WHERE kind=? AND task_id=? AND state='running' AND owner=? AND fence=? AND lease_until=? AND lease_until<="+webNow+" AND attempts>=?", kind, job.TaskID, job.Owner, job.Fence, job.LeaseUntil, webMaxAttempts).Error; err != nil {
			return err
		}
	}
	return nil
}
