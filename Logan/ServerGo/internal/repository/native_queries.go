package repository

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
)

type NativeQueries struct {
	db       *gorm.DB
	maxBytes int64
}

func NewNativeQueries(db *gorm.DB, maxBytes int64) *NativeQueries {
	return &NativeQueries{db: db, maxBytes: maxBytes}
}
func (r *NativeQueries) Find(ctx context.Context, id int64) (*NativeTask, error) {
	return NewNativeTasks(r.db).Find(ctx, id)
}
func (r *NativeQueries) Latest(ctx context.Context) ([]NativeTask, error) {
	out := []NativeTask{}
	e := r.db.WithContext(ctx).Order("id DESC").Limit(20).Find(&out).Error
	return out, e
}
func (r *NativeQueries) Search(ctx context.Context, device string, begin, end int64, platform int) ([]NativeTask, error) {
	out := []NativeTask{}
	budget := max(int64(1), r.maxBytes/4096)
	if budget < 0 {
		return nil, ErrWebLimit
	}
	if budget >= int64(int(^uint(0)>>1)) {
		budget = int64(int(^uint(0)>>1)) - 1
	}
	q := r.db.WithContext(ctx).Where("device_id = ? AND add_time BETWEEN ? AND ?", device, begin, end)
	if platform == 1 || platform == 2 {
		q = q.Where("platform = ?", platform)
	}
	if e := q.Order("id ASC").Limit(int(budget + 1)).Find(&out).Error; e != nil {
		return nil, e
	}
	if int64(len(out)) > budget {
		return nil, ErrWebLimit
	}
	return out, nil
}
func (r *NativeQueries) MatchIndexes(ctx context.Context, id int64, types []int, keyword *string, limit int) ([]NativeDetail, error) {
	if limit < 0 || limit == int(^uint(0)>>1) {
		return nil, ErrWebLimit
	}
	query := "SELECT id,log_type,log_time FROM logan_log_detail WHERE task_id=?"
	args := []any{id}
	if len(types) > 0 {
		query += " AND log_type IN ?"
		args = append(args, types)
	}
	if keyword != nil {
		query += " AND content LIKE CONCAT('%',?,'%')"
		args = append(args, *keyword)
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit+1)
	out := []NativeDetail{}
	if e := r.db.WithContext(ctx).Raw(query, args...).Scan(&out).Error; e != nil {
		return nil, e
	}
	if len(out) > limit {
		return nil, ErrWebLimit
	}
	return out, nil
}
func (r *NativeQueries) Validate(ctx context.Context) error {
	rows, e := r.db.WithContext(ctx).Raw("SELECT kind,task_id,source_revision,source_digest,state,owner,lease_until,fence,attempts,next_run_at,last_error_code,created_at,updated_at FROM logan_processing_job LIMIT 0").Rows()
	if e != nil {
		return fmt.Errorf("native processing migration required: %w", e)
	}
	rows.Close()
	var engines []struct {
		TableName string
		Engine    string
	}
	if e := r.db.WithContext(ctx).Raw("SELECT TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('logan_task','logan_log_detail','logan_processing_job')").Scan(&engines).Error; e != nil {
		return e
	}
	if len(engines) != 3 {
		return errors.New("native processing requires all three transactional tables")
	}
	for _, t := range engines {
		if t.Engine != "InnoDB" {
			return fmt.Errorf("native processing requires InnoDB: %s", t.TableName)
		}
	}
	return nil
}
func (r *NativeQueries) BoundedDetails(ctx context.Context, ids []int64, maxBytes int64) ([]NativeDetail, error) {
	if maxBytes < 0 {
		return nil, ErrWebLimit
	}
	out := []NativeDetail{}
	if len(ids) == 0 {
		return out, nil
	}
	var sizes []struct {
		ID           int64
		ContentBytes int64
	}
	if err := r.db.WithContext(ctx).Raw("SELECT id,COALESCE(OCTET_LENGTH(content),0) AS content_bytes FROM logan_log_detail WHERE id IN ? ORDER BY id ASC", ids).Scan(&sizes).Error; err != nil {
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
		var detail NativeDetail
		remaining := maxBytes - used
		q := r.db.WithContext(ctx).Raw("SELECT id,task_id,log_type,COALESCE(content,'') AS content,log_time,add_time,update_time FROM logan_log_detail WHERE id=? AND COALESCE(OCTET_LENGTH(content),0)<=? AND COALESCE(OCTET_LENGTH(content),0)=?", row.ID, remaining, row.ContentBytes).Scan(&detail)
		if q.Error != nil {
			return nil, q.Error
		}
		n := int64(len(detail.Content))
		if q.RowsAffected != 1 || n != row.ContentBytes || n > remaining {
			return nil, errors.New("native detail source changed")
		}
		used += n
		out = append(out, detail)
	}
	return out, nil
}
