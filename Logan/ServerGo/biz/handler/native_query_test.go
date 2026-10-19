package handler_test

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/model"
	"logan/servergo/biz/router"
	"testing"
	"time"
)

type nativeQueryStub struct {
	begin, end int64
	platform   int
	ids        []int64
	types      []int
	keyword    *string
}

func (s *nativeQueryStub) Latest(context.Context) ([]model.NativeTask, error) {
	return []model.NativeTask{}, nil
}
func (s *nativeQueryStub) Search(_ context.Context, _ string, b, e int64, p int) ([]model.NativeTask, error) {
	s.begin = b
	s.end = e
	s.platform = p
	return []model.NativeTask{}, nil
}
func (s *nativeQueryStub) Info(context.Context, int64) (*model.NativeTask, error) { return nil, nil }
func (s *nativeQueryStub) Brief(_ context.Context, _ int64, types []int, keyword *string) ([][]model.NativeSimple, error) {
	s.types = types
	s.keyword = keyword
	return [][]model.NativeSimple{}, nil
}
func (s *nativeQueryStub) Details(_ context.Context, ids []int64) ([]*model.NativeDetail, error) {
	s.ids = ids
	return []*model.NativeDetail{}, nil
}
func (s *nativeQueryStub) Detail(context.Context, int64) (*model.NativeDetail, error) {
	return nil, nil
}
func TestNativeQueryRouteBindingAndDefaults(t *testing.T) {
	s := &nativeQueryStub{}
	h := server.New()
	router.RegisterLegacyWithWeb(h, "/logan-web", nil, nil, handler.NewNativeQueries(s, time.Second))
	cases := []struct {
		path         string
		status, code int
	}{{"/logan/latest.json", 200, 200}, {"/logan/task/search.json", 200, 400}, {"/logan/task/search.json?deviceId=d", 500, 0}, {"/logan/task/search.json?deviceId=d&beginTime=x&endTime=0", 400, 0}, {"/logan/task/search.json?deviceId=d&endTime=0", 200, 200}, {"/logan/task/9007199254740993/info.json", 200, 200}, {"/logan/task/2147483648/brief.json", 400, 0}, {"/logan/task/0/brief.json", 200, 400}, {"/logan/task/query/details.json?detailIds=%20%20", 200, 400}, {"/logan/task/abc/detail.json", 400, 0}, {"/logan/task/2147483648/detail.json", 400, 0}, {"/logan/meta/logtypes.json", 200, 200}}
	for _, v := range cases {
		w := ut.PerformRequest(h.Engine, "GET", "/logan-web"+v.path, nil)
		if w.Code != v.status {
			t.Fatalf("%s=%d body=%s", v.path, w.Code, w.Body)
		}
		if v.code != 0 {
			var r struct{ Code int }
			if json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Code != v.code {
				t.Fatalf("%s body=%s", v.path, w.Body)
			}
		}
	}
	ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/task/search.json?deviceId=d&endTime=0", nil)
	if s.end != 86400000 || s.begin != 86400000-7*86400000 || s.platform != 0 {
		t.Fatal(s)
	}
	ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/task/query/details.json?detailIds=2,abc,0,2,", nil)
	if len(s.ids) != 4 || s.ids[1] != 0 || s.ids[2] != 0 {
		t.Fatal(s.ids)
	}
	ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/task/1/brief.json?logTypes=abc,1,&keyword=", nil)
	if len(s.types) != 2 || s.types[0] != 0 || s.types[1] != 1 || s.keyword == nil || *s.keyword != "" {
		t.Fatal(s.types, s.keyword)
	}
}
