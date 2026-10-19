package repository

import (
	"fmt"
	"gorm.io/gorm"
)

// The caller holds the job and task primary-key locks and has checked its fence
// and lease. Read actual detail IDs without locking empty task-index gaps, then
// delete only those existing primary keys. All supported writers serialize on
// the task lock; keyset batches bound memory for historical partial results.
func clearPublicationDetails(tx *gorm.DB, table string, taskID int64) error {
	if table != "logan_log_detail" && table != "web_detail" {
		return fmt.Errorf("invalid publication detail table")
	}
	var after int64
	for {
		var ids []int64
		if err := tx.Raw("SELECT id FROM "+table+" WHERE task_id=? AND id>? ORDER BY id LIMIT 500", taskID, after).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Exec("DELETE d FROM "+table+" AS d FORCE INDEX(PRIMARY) WHERE d.id IN ? AND d.task_id=?", ids, taskID).Error; err != nil {
			return err
		}
		after = ids[len(ids)-1]
		if len(ids) < 500 {
			return nil
		}
	}
}
