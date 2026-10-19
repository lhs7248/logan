// Package repository maps existing tables explicitly; it never migrates schema at startup.
package repository

import "time"

type NativeTask struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	Platform     int       `gorm:"column:platform"`
	Amount       *string   `gorm:"column:amount"`
	AppID        *string   `gorm:"column:app_id"`
	UnionID      *string   `gorm:"column:union_id"`
	DeviceID     *string   `gorm:"column:device_id"`
	AppVersion   *string   `gorm:"column:app_version"`
	BuildVersion *string   `gorm:"column:build_version"`
	LogFileName  *string   `gorm:"column:log_file_name"`
	LogDate      *int64    `gorm:"column:log_date"`
	AddTime      int64     `gorm:"column:add_time"`
	Status       int       `gorm:"column:status"`
	UpdateTime   time.Time `gorm:"column:update_time;->"`
}

func (NativeTask) TableName() string { return "logan_task" }

type NativeDetail struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	TaskID     int64     `gorm:"column:task_id"`
	LogType    int       `gorm:"column:log_type"`
	Content    string    `gorm:"column:content"`
	LogTime    int64     `gorm:"column:log_time"`
	AddTime    time.Time `gorm:"column:add_time;->"`
	UpdateTime time.Time `gorm:"column:update_time;->"`
}

func (NativeDetail) TableName() string { return "logan_log_detail" }

type WebTask struct {
	ID               int64     `gorm:"column:id;primaryKey;autoIncrement"`
	DeviceID         string    `gorm:"column:device_id"`
	WebSource        *string   `gorm:"column:web_source"`
	Environment      *string   `gorm:"column:environment"`
	PageNum          int       `gorm:"column:page_num"`
	Content          string    `gorm:"column:content"`
	AddTime          int64     `gorm:"column:add_time"`
	LogDate          int64     `gorm:"column:log_date"`
	Status           int       `gorm:"column:status"`
	CustomReportInfo *string   `gorm:"column:custom_report_info"`
	UpdateTime       time.Time `gorm:"column:update_time;->"`
}

func (WebTask) TableName() string { return "web_task" }

type WebDetail struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	TaskID       int64  `gorm:"column:task_id"`
	LogType      int    `gorm:"column:log_type"`
	Content      string `gorm:"column:content"`
	LogTime      int64  `gorm:"column:log_time"`
	LogLevel     *int   `gorm:"column:log_level"`
	AddTime      int64  `gorm:"column:add_time"`
	MinuteOffset int    `gorm:"column:minute_offset"`
}

func (WebDetail) TableName() string { return "web_detail" }
