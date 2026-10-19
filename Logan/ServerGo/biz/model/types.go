// Package model defines legacy HTTP DTOs independently of GORM table models.
package model

type NativeTask struct {
	TaskID       int64   `json:"taskId"`
	Amount       *string `json:"amount"`
	AppID        *string `json:"appId"`
	UnionID      *string `json:"unionId"`
	Platform     int     `json:"platform"`
	BuildVersion *string `json:"buildVersion"`
	AppVersion   *string `json:"appVersion"`
	DeviceID     *string `json:"deviceId"`
	LogDate      int64   `json:"logDate"`
	LogFileName  *string `json:"logFileName"`
	AddTime      int64   `json:"addTime"`
	Status       int     `json:"status"`
}
type NativeDetail struct {
	ID            int64   `json:"id"`
	TaskID        int64   `json:"taskId"`
	LogType       int     `json:"logType"`
	Content       *string `json:"content"`
	SimpleContent *string `json:"simpleContent"`
	FormatContent *string `json:"formatContent"`
	LogTime       int64   `json:"logTime"`
}
type NativeSimple struct {
	ID      int64 `json:"id"`
	LogType int   `json:"logType"`
	LogTime int64 `json:"logTime"`
}
type WebTask struct {
	TaskID           int64   `json:"taskId"`
	DeviceID         *string `json:"deviceId"`
	WebSource        *string `json:"webSource"`
	Environment      *string `json:"environment"`
	PageNum          int     `json:"pageNum"`
	AddTime          int64   `json:"addTime"`
	LogDate          int64   `json:"logDate"`
	Status           int     `json:"status"`
	CustomReportInfo *string `json:"customReportInfo"`
	Tasks            *string `json:"tasks"`
	UpdateTime       *int64  `json:"updateTime"`
}
type WebDetail struct {
	ID           int64   `json:"id"`
	TaskID       int64   `json:"taskId"`
	LogType      int     `json:"logType"`
	Content      *string `json:"content"`
	LogTime      int64   `json:"logTime"`
	LogLevel     int     `json:"logLevel"`
	MinuteOffset int     `json:"minuteOffset"`
	AddTime      int64   `json:"addTime"`
}

// WebIndex uses the original WebLogIndex field name, distinct from native summaries.
type WebIndex struct {
	ID      int64 `json:"detailId"`
	LogType int   `json:"logType"`
	LogTime int64 `json:"logTime"`
}
