package repository

import (
	"context"
	"errors"
	"gorm.io/gorm"
)

type NativeTasks struct{ db *gorm.DB }

func NewNativeTasks(db *gorm.DB) *NativeTasks { return &NativeTasks{db: db} }
func (r *NativeTasks) Create(ctx context.Context, task *NativeTask) error {
	return r.db.WithContext(ctx).Create(task).Error
}
func (r *NativeTasks) Find(ctx context.Context, id int64) (*NativeTask, error) {
	var task NativeTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}
