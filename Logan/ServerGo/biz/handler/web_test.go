package handler_test

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/model"
	"logan/servergo/biz/router"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/weblog"
	"strings"
	"testing"
	"time"
)

type webStub struct{}

func (webStub) Upload(context.Context, weblog.Upload) error { return nil }
func (webStub) Search(context.Context, int64, int64, string) ([]model.WebTask, error) {
	return []model.WebTask{}, nil
}
func (webStub) Latest(context.Context) ([]model.WebTask, error)              { return []model.WebTask{}, nil }
func (webStub) Task(context.Context, []int64) (*model.WebTask, error)        { return nil, nil }
func (webStub) Details(context.Context, []int64) ([]*model.WebDetail, error) { return nil, nil }
func (webStub) Detail(context.Context, int64) (*model.WebDetail, error)      { return nil, nil }
func (webStub) Download(context.Context, []int64) ([]byte, error)            { return []byte(`["中文"]`), nil }
func (webStub) Index(context.Context, []int64, []int, string, int, int) ([][]model.WebIndex, error) {
	return nil, weblog.ErrInvalid
}

var _ = repository.WebTask{}

func TestWebRoutesAndBinding(t *testing.T) {
	h := server.New()
	router.RegisterLegacyWithWeb(h, "/logan-web", nil, handler.NewWeb(webStub{}, 1024, time.Second))
	cases := []struct {
		method, path, body string
		status, code       int
		msg                string
	}{
		{"POST", "/logan/web/upload.json", `{"deviceId":"d","logPageNo":1,"fileDate":"2021-01-01","logArray":"x"}`, 200, 200, ""},
		{"POST", "/logan/web/upload.json", `{`, 400, 0, ""},
		{"GET", "/logan/web/search.json?beginTime=abc", "", 400, 0, ""},
		{"GET", "/logan/web/search.json?beginTime=0&endTime=0", "", 200, 400, "invalid param"},
		{"GET", "/logan/web/latest.json", "", 200, 200, ""},
		{"GET", "/logan/web/taskDetail.json?tasks=abc", "", 200, 400, "task not found"},
		{"GET", "/logan/web/detailIndex.json?tasks=1", "", 200, 400, "empty tasks"},
		{"GET", "/logan/web/details.json", "", 200, 400, "not details for null"},
		{"GET", "/logan/web/logDetail.json?detailId=abc", "", 200, 400, "log detail not found"},
		{"GET", "/logan/web/exception.json", "", 200, 200, ""},
		{"GET", "/logan/web/getDownLoadUrl.json", "", 200, 200, ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			var body *ut.Body
			if c.body != "" {
				body = &ut.Body{Body: strings.NewReader(c.body), Len: len(c.body)}
			}
			w := ut.PerformRequest(h.Engine, c.method, "/logan-web"+c.path, body, ut.Header{Key: "Content-Type", Value: "application/json"})
			if w.Code != c.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if c.code != 0 {
				var v struct {
					Code int
					Msg  string
				}
				if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || v.Code != c.code || (c.msg != "" && v.Msg != c.msg) {
					t.Fatalf("response=%s err=%v", w.Body, err)
				}
			}
		})
	}
	w := ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/web/download.json?tasks=2,1", nil)
	b, _ := io.ReadAll(w.Body)
	if w.Code != 201 || string(b) != `["中文"]` || string(w.Header().Peek("Content-Disposition")) != `form-data; name="attachment"; filename="2,1"` {
		t.Fatalf("download %d %s %v", w.Code, b, w.Header())
	}
}
