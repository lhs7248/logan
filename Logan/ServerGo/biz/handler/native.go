package handler

import (
	"bytes"
	"context"
	"github.com/cloudwego/hertz/pkg/app"
	"io"
	"logan/servergo/internal/native"
	"strconv"
	"strings"
	"time"
)

type NativeService interface {
	Upload(context.Context, map[string]string, int64, io.Reader) (string, error)
	DownloadURL(context.Context, int64) (string, error)
	Sign(context.Context, string) (string, error)
}
type Native struct {
	service NativeService
	timeout time.Duration
}

func NewNative(service NativeService, timeouts ...time.Duration) *Native {
	timeout := 30 * time.Second
	if len(timeouts) > 0 && timeouts[0] > 0 {
		timeout = timeouts[0]
	}
	return &Native{service: service, timeout: timeout}
}
func (n *Native) Upload(ctx context.Context, c *app.RequestContext) {
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
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

	headers := map[string]string{}
	c.Request.Header.VisitAll(func(key, value []byte) { headers[strings.ToLower(string(key))] = string(value) })
	body := c.RequestBodyStream()
	if !c.Request.IsBodyStream() {
		body = bytes.NewReader(c.Request.Body())
	}
	result, err := n.service.Upload(ctx, headers, int64(c.Request.Header.ContentLength()), body)
	if err != nil {
		message := native.ErrException.Error()
		if err == native.ErrDecrypt || err == native.ErrDatabase || err == native.ErrException {
			message = err.Error()
		}
		Write(c, 200, Failure(500, message))
		return
	}
	success = true
	Write(c, 200, Success(result))
}
func (n *Native) DownloadURL(ctx context.Context, c *app.RequestContext) {
	id, err := strconv.ParseInt(c.Param("taskId"), 10, 32)
	if err != nil {
		empty(c, 400)
		return
	}
	if id <= 0 {
		Write(c, 200, Failure(400, "param illegal"))
		return
	}
	result, err := n.service.DownloadURL(ctx, id)
	if err != nil {
		Write(c, 200, Failure(400, "param illegal"))
		return
	}
	Write(c, 200, Success(result))
}
func (n *Native) Download(ctx context.Context, c *app.RequestContext) {
	name := c.Query("name")
	if name == "" {
		empty(c, 400)
		return
	}
	result, err := n.service.Sign(ctx, name)
	if err != nil {
		empty(c, 404)
		return
	}
	c.Response.Header.Set("Location", result)
	empty(c, 302)
}
func empty(c *app.RequestContext, status int) {
	c.Response.Header.SetNoDefaultContentType(true)
	c.Status(status)
}

// Match the servlet CORSFilter declared in web.xml, including its origin allowlist.
func (n *Native) CORS(ctx context.Context, c *app.RequestContext) {
	origin := string(c.Request.Header.Peek("Origin"))
	allowed := ""
	if origin == "http://localhost:3000" || origin == "http://localhost:5001" {
		allowed = origin
	}
	c.Response.Header.Set("Access-Control-Allow-Origin", allowed)
	c.Response.Header.Set("Vary", "Origin")
	c.Response.Header.Set("Access-Control-Max-Age", "3600")
	c.Response.Header.Set("Access-Control-Allow-Credentials", "true")
	c.Response.Header.Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, DELETE")
	c.Response.Header.Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, X-CSRF-TOKEN")
	c.Next(ctx)
}

// StopRequestBody prevents Hertz ReleaseBodyStream from draining unbounded input
// after an early response. The response is flushed before Hertz releases the body.
func StopRequestBody(c *app.RequestContext) {
	if !c.Request.IsBodyStream() || c.Request.Header.ContentLength() == 0 {
		return
	}
	c.SetConnectionClose()
	if conn := c.GetConn(); conn != nil {
		_ = conn.SetReadDeadline(time.Now())
	}
}
func StreamBodyGuard(uploadPaths ...string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		c.Next(ctx)
		allowed := false
		for _, path := range uploadPaths {
			if string(c.Path()) == path && string(c.Method()) == "POST" {
				allowed = true
				break
			}
		}
		if !allowed {
			StopRequestBody(c)
		}
	}
}
