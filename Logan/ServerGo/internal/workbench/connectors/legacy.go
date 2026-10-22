package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"logan/servergo/biz/model"
)

type LegacyConfig struct {
	BaseURL, Token   string
	Mappings         []LegacyMapping
	Timeout          time.Duration
	MaxResponseBytes int64
}

// LegacyMapping is trusted operator configuration, never a client-supplied scope.
// WebSource pairs an exact legacy source with an operator-confirmed AppID.
type LegacyMapping struct {
	ProjectID, Kind                      string
	TaskID                               int64
	AppID, Platform, DeviceID, WebSource string
}
type Legacy struct {
	config   LegacyConfig
	client   *http.Client
	mappings []LegacyMapping
}
type LegacyRef struct {
	Kind           string `json:"kind"`
	TaskID         string `json:"taskId"`
	DetailID       string `json:"detailId,omitempty"`
	SourceRevision string `json:"sourceRevision"`
}
type LegacyTask struct {
	Ref      LegacyRef `json:"legacyRef"`
	AppID    string    `json:"appId"`
	Platform string    `json:"platform"`
	DeviceID string    `json:"deviceId"`
	Version  string    `json:"version"`
	Build    string    `json:"build"`
	Status   int       `json:"status"`
	LogDate  int64     `json:"logDate"`
}
type LegacyDetail struct {
	Ref     LegacyRef `json:"legacyRef"`
	Content string    `json:"content"`
	LogType int       `json:"logType"`
	LogTime int64     `json:"logTime"`
}

func NewLegacy(c LegacyConfig) (*Legacy, error) {
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		c.Timeout = 5 * time.Second
	}
	if c.MaxResponseBytes <= 0 || c.MaxResponseBytes > 4<<20 {
		c.MaxResponseBytes = 1 << 20
	}
	l := &Legacy{config: c}
	if c.BaseURL == "" && c.Token == "" && len(c.Mappings) == 0 {
		return l, nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalidRequest
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, ErrInvalidRequest
		}
	}
	if strings.ContainsAny(c.Token, "\r\n") || len(c.Mappings) > 100 {
		return nil, ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, m := range c.Mappings {
		if m.ProjectID == "" || m.AppID == "" || m.DeviceID == "" || m.Platform == "" || m.TaskID <= 0 {
			return nil, ErrInvalidRequest
		}
		switch m.Kind {
		case "native":
			if _, err := strconv.ParseInt(m.Platform, 10, 32); err != nil {
				return nil, ErrInvalidRequest
			}
		case "web":
			if m.Platform != "web" || m.WebSource == "" {
				return nil, ErrInvalidRequest
			}
		default:
			return nil, ErrInvalidRequest
		}
		key := m.ProjectID + "\x00" + m.Kind + "\x00" + strconv.FormatInt(m.TaskID, 10)
		if seen[key] {
			return nil, ErrInvalidRequest
		}
		seen[key] = true
	}
	l.mappings = append([]LegacyMapping(nil), c.Mappings...)
	l.client = &http.Client{Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return l, nil
}
func (l *Legacy) Ready(projectID string) bool {
	if l == nil || l.client == nil {
		return false
	}
	for _, m := range l.mappings {
		if m.ProjectID == projectID {
			return true
		}
	}
	return false
}
func (l *Legacy) fetch(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	endpoint := strings.TrimRight(l.config.BaseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if l.config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, l.config.MaxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnavailable
	}
	if int64(len(raw)) > l.config.MaxResponseBytes {
		return nil, ErrLimitExceeded
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Code != 200 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, ErrInvalidResponse
	}
	return envelope.Data, nil
}
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (l *Legacy) task(ctx context.Context, m LegacyMapping) (LegacyTask, error) {
	result := LegacyTask{Ref: LegacyRef{Kind: m.Kind, TaskID: strconv.FormatInt(m.TaskID, 10)}, AppID: m.AppID, Platform: m.Platform, DeviceID: m.DeviceID}
	if m.Kind == "native" {
		raw, err := l.fetch(ctx, "/logan/task/"+result.Ref.TaskID+"/info.json", nil)
		if err != nil {
			return LegacyTask{}, err
		}
		var task model.NativeTask
		if json.Unmarshal(raw, &task) != nil {
			return LegacyTask{}, ErrInvalidResponse
		}
		if task.TaskID != m.TaskID || stringValue(task.AppID) != m.AppID || strconv.Itoa(task.Platform) != m.Platform || stringValue(task.DeviceID) != m.DeviceID {
			return LegacyTask{}, ErrUnavailable
		}
		result.Ref.SourceRevision = digest(raw)
		result.Version = stringValue(task.AppVersion)
		result.Build = stringValue(task.BuildVersion)
		result.Status = task.Status
		result.LogDate = task.LogDate
	} else {
		raw, err := l.fetch(ctx, "/logan/web/taskDetail.json", url.Values{"tasks": {result.Ref.TaskID}})
		if err != nil {
			return LegacyTask{}, err
		}
		var task model.WebTask
		if json.Unmarshal(raw, &task) != nil {
			return LegacyTask{}, ErrInvalidResponse
		}
		if task.TaskID != m.TaskID || stringValue(task.DeviceID) != m.DeviceID || stringValue(task.WebSource) != m.WebSource {
			return LegacyTask{}, ErrUnavailable
		}
		result.Ref.SourceRevision = digest(raw)
		result.Status = task.Status
		result.LogDate = task.LogDate
	}
	return result, nil
}
func (l *Legacy) List(ctx context.Context, projectID string) ([]LegacyTask, error) {
	if !l.Ready(projectID) {
		return nil, ErrUnconfigured
	}
	ctx, cancel := context.WithTimeout(ctx, l.config.Timeout)
	defer cancel()
	result := []LegacyTask{}
	for _, m := range l.mappings {
		if m.ProjectID != projectID {
			continue
		}
		task, err := l.task(ctx, m)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, nil
}
func (l *Legacy) Detail(ctx context.Context, projectID, kind string, taskID, detailID int64) (LegacyDetail, error) {
	if !l.Ready(projectID) {
		return LegacyDetail{}, ErrUnconfigured
	}
	if detailID <= 0 {
		return LegacyDetail{}, ErrInvalidRequest
	}
	var mapping *LegacyMapping
	for i := range l.mappings {
		m := &l.mappings[i]
		if m.ProjectID == projectID && m.Kind == kind && m.TaskID == taskID {
			mapping = m
			break
		}
	}
	if mapping == nil {
		return LegacyDetail{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, l.config.Timeout)
	defer cancel()
	if _, err := l.task(ctx, *mapping); err != nil {
		return LegacyDetail{}, err
	}
	var raw json.RawMessage
	var err error
	id := strconv.FormatInt(detailID, 10)
	if kind == "native" {
		raw, err = l.fetch(ctx, "/logan/task/"+id+"/detail.json", nil)
	} else {
		raw, err = l.fetch(ctx, "/logan/web/logDetail.json", url.Values{"detailId": {id}})
	}
	if err != nil {
		return LegacyDetail{}, err
	}
	var detail struct {
		ID      int64   `json:"id"`
		TaskID  int64   `json:"taskId"`
		Content *string `json:"content"`
		LogType int     `json:"logType"`
		LogTime int64   `json:"logTime"`
	}
	if json.Unmarshal(raw, &detail) != nil {
		return LegacyDetail{}, ErrInvalidResponse
	}
	if detail.ID != detailID || detail.TaskID != taskID || detail.Content == nil {
		return LegacyDetail{}, ErrUnavailable
	}
	return LegacyDetail{Ref: LegacyRef{Kind: kind, TaskID: strconv.FormatInt(taskID, 10), DetailID: id, SourceRevision: digest(raw)}, Content: *detail.Content, LogType: detail.LogType, LogTime: detail.LogTime}, nil
}
