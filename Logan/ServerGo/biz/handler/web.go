package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/hertz/pkg/app"
	"io"
	"logan/servergo/biz/model"
	"logan/servergo/internal/weblog"
	"strconv"
	"strings"
	"time"
)

type WebService interface {
	Upload(context.Context, weblog.Upload) error
	Search(context.Context, int64, int64, string) ([]model.WebTask, error)
	Latest(context.Context) ([]model.WebTask, error)
	Task(context.Context, []int64) (*model.WebTask, error)
	Details(context.Context, []int64) ([]*model.WebDetail, error)
	Detail(context.Context, int64) (*model.WebDetail, error)
	Download(context.Context, []int64) ([]byte, error)
	Index(context.Context, []int64, []int, string, int, int) ([][]model.WebIndex, error)
}
type Web struct {
	service WebService
	maxBody int64
	timeout time.Duration
}

func NewWeb(s WebService, maxBody int64, timeout time.Duration) *Web {
	return &Web{s, maxBody, timeout}
}
func (w *Web) CORS(ctx context.Context, c *app.RequestContext) {
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	(&Native{}).CORS(ctx, c)
}
func (w *Web) Upload(ctx context.Context, c *app.RequestContext) {
	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	success := false
	if conn := c.GetConn(); conn != nil {
		deadline, _ := ctx.Deadline()
		_ = conn.SetReadDeadline(deadline)
		done := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()); close(done) })
		defer func() {
			if !stop() {
				<-done
			}
			if success {
				_ = conn.SetReadDeadline(time.Time{})
			} else {
				StopRequestBody(c)
			}
		}()
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(string(c.Request.Header.ContentType()), ";")[0]))
	if contentType != "application/json" && !strings.HasSuffix(contentType, "+json") {
		empty(c, 415)
		return
	}
	var r io.Reader = c.RequestBodyStream()
	if !c.Request.IsBodyStream() {
		r = strings.NewReader(string(c.Request.Body()))
	}
	body, err := io.ReadAll(io.LimitReader(r, w.maxBody+1))
	if err != nil {
		empty(c, 400)
		return
	}
	if int64(len(body)) > w.maxBody {
		empty(c, 413)
		return
	}
	var u *weblog.Upload
	if json.Unmarshal(body, &u) != nil {
		empty(c, 400)
		return
	}
	if u == nil {
		empty(c, 400)
		success = true
		return
	}
	err = w.service.Upload(ctx, *u)
	switch {
	case err == nil:
		Write(c, 200, Success(true))
		success = true
	case errors.Is(err, weblog.ErrInvalid):
		Write(c, 200, Failure(400, "invalid params"))
		success = true
	case errors.Is(err, weblog.ErrSave):
		Write(c, 200, Failure(500, "save log error"))
		success = true
	default:
		empty(c, 500)
	}
}
func javaLong(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
func ids(s string) []int64 {
	out := []int64{}
	for _, part := range strings.Split(s, ",") {
		if n := javaLong(part); n != 0 {
			out = append(out, n)
		}
	}
	return out
}
func logTypes(s string) []int {
	out := []int{}
	if s == "" {
		return out
	}
	trimmed := strings.TrimRight(s, ",")
	if trimmed == "" {
		return out
	}
	parts := strings.Split(trimmed, ",")
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 32)
		if err != nil {
			n = 0
		}
		out = append(out, int(n))
	}
	return out
}
func offset(s string, defaultValue int) int {
	if s == "" {
		return defaultValue
	}
	parts := strings.Split(strings.TrimRight(s, ":"), ":")
	if len(parts) != 2 {
		return defaultValue
	}
	a, err := strconv.ParseInt(parts[0], 10, 32)
	if err != nil {
		a = 0
	}
	b, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		b = 0
	}
	return int(int32(a*60 + b))
}
func nullableQuery(c *app.RequestContext, key string) string {
	if !c.QueryArgs().Has(key) {
		return "null"
	}
	return c.Query(key)
}
func (w *Web) Search(ctx context.Context, c *app.RequestContext) {
	b, e := c.Query("beginTime"), c.Query("endTime")
	begin, err := strconv.ParseInt(b, 10, 64)
	end, err2 := strconv.ParseInt(e, 10, 64)
	if err != nil || err2 != nil {
		empty(c, 400)
		return
	}
	if begin <= 0 || c.Query("deviceId") == "" {
		Write(c, 200, Failure(400, "invalid param"))
		return
	}
	result, err := w.service.Search(ctx, begin, end, c.Query("deviceId"))
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(result))
}
func (w *Web) Latest(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Latest(ctx)
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(v))
}
func (w *Web) Task(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Task(ctx, ids(c.Query("tasks")))
	if err != nil {
		empty(c, 500)
		return
	}
	if v == nil {
		Write(c, 200, Failure(400, "task not found"))
		return
	}
	Write(c, 200, Success(v))
}
func (w *Web) Details(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Details(ctx, ids(c.Query("detailIds")))
	if err != nil {
		empty(c, 500)
		return
	}
	if len(v) == 0 {
		Write(c, 200, Failure(400, "not details for "+nullableQuery(c, "detailIds")))
		return
	}
	Write(c, 200, Success(v))
}
func (w *Web) Detail(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Detail(ctx, javaLong(c.Query("detailId")))
	if err != nil {
		empty(c, 500)
		return
	}
	if v == nil {
		Write(c, 200, Failure(400, "log detail not found"))
		return
	}
	Write(c, 200, Success(v))
}
func (w *Web) Index(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Index(ctx, ids(c.Query("tasks")), logTypes(c.Query("logTypes")), c.Query("keyword"), offset(c.Query("beginTime"), 0), offset(c.Query("endTime"), 1439))
	if errors.Is(err, weblog.ErrInvalid) {
		Write(c, 200, Failure(400, "empty tasks"))
		return
	}
	if err != nil {
		empty(c, 500)
		return
	}
	Write(c, 200, Success(v))
}
func (w *Web) Exception(_ context.Context, c *app.RequestContext) {
	Write(c, 200, Success("exception"))
}
func (w *Web) DownloadURL(_ context.Context, c *app.RequestContext) {
	Write(c, 200, Success("/logan/web/download.json?tasks="+nullableQuery(c, "tasks")))
}
func (w *Web) Download(ctx context.Context, c *app.RequestContext) {
	v, err := w.service.Download(ctx, ids(c.Query("tasks")))
	if err != nil {
		empty(c, 500)
		return
	}
	name := c.Query("tasks")
	name = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "", "\n", "").Replace(name)
	disposition := `form-data; name="attachment"`
	if c.QueryArgs().Has("tasks") {
		disposition += `; filename="` + name + `"`
	}
	c.Response.Header.Set("Content-Disposition", disposition)
	c.Data(201, "application/octet-stream", v)
}
