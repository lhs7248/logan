package router

import (
	"context"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"logan/servergo/biz/handler"
	"strings"
)

type Route struct{ Method, Path string }

// LegacyRoutes lists controller paths without the deployment context prefix.
func LegacyRoutes() []Route {
	return []Route{
		{"POST", "/logan/upload.json"},
		{"GET", "/logan/latest.json"},
		{"GET", "/logan/task/search.json"},
		{"GET", "/logan/task/:taskId/info.json"},
		{"GET", "/logan/meta/logtypes.json"},
		{"GET", "/logan/task/:taskId/brief.json"},
		{"GET", "/logan/task/query/details.json"},
		{"GET", "/logan/task/:detailId/detail.json"},
		{"GET", "/logan/task/:taskId/download.json"},
		{"GET", "/logan/downing"},
		{"POST", "/logan/web/upload.json"},
		{"GET", "/logan/web/search.json"},
		{"GET", "/logan/web/latest.json"},
		{"GET", "/logan/web/detailIndex.json"},
		{"GET", "/logan/web/taskDetail.json"},
		{"GET", "/logan/web/details.json"},
		{"GET", "/logan/web/logDetail.json"},
		{"GET", "/logan/web/exception.json"},
		{"GET", "/logan/web/getDownLoadUrl.json"},
		{"GET", "/logan/web/download.json"},
	}
}
func RegisterLegacy(h *server.Hertz, prefix string, natives ...*handler.Native) {
	var n *handler.Native
	if len(natives) > 0 {
		n = natives[0]
	}
	RegisterLegacyWithWeb(h, prefix, n, nil)
}
func RegisterLegacyWithWeb(h *server.Hertz, prefix string, n *handler.Native, w *handler.Web, queries ...*handler.NativeQueries) {
	var q *handler.NativeQueries
	if len(queries) > 0 {
		q = queries[0]
	}
	implemented := map[string]app.HandlerFunc{}
	if n != nil {
		implemented["POST /logan/upload.json"] = n.Upload
		implemented["GET /logan/task/:taskId/download.json"] = n.DownloadURL
		implemented["GET /logan/downing"] = n.Download
	}

	if w != nil {
		for path, action := range map[string]app.HandlerFunc{
			"POST /logan/web/upload.json": w.Upload, "GET /logan/web/search.json": w.Search, "GET /logan/web/latest.json": w.Latest, "GET /logan/web/detailIndex.json": w.Index, "GET /logan/web/taskDetail.json": w.Task, "GET /logan/web/details.json": w.Details, "GET /logan/web/logDetail.json": w.Detail, "GET /logan/web/exception.json": w.Exception, "GET /logan/web/getDownLoadUrl.json": w.DownloadURL, "GET /logan/web/download.json": w.Download,
		} {
			implemented[path] = action
		}
	}
	if q != nil {
		for path, action := range map[string]app.HandlerFunc{"GET /logan/latest.json": q.Latest, "GET /logan/task/search.json": q.Search, "GET /logan/task/:taskId/info.json": q.Info, "GET /logan/meta/logtypes.json": q.LogTypes, "GET /logan/task/:taskId/brief.json": q.Brief, "GET /logan/task/query/details.json": q.Details, "GET /logan/task/:detailId/detail.json": q.Detail} {
			implemented[path] = action
		}
	}
	for _, route := range LegacyRoutes() {
		if action, ok := implemented[route.Method+" "+route.Path]; ok {
			cors := app.HandlerFunc(nCORS)
			if w != nil && strings.HasPrefix(route.Path, "/logan/web/") {
				cors = w.CORS
			} else if q != nil && (strings.HasSuffix(route.Path, "/brief.json") || strings.HasSuffix(route.Path, "/info.json") || strings.HasSuffix(route.Path, "/detail.json") || route.Path == "/logan/latest.json" || route.Path == "/logan/task/search.json" || route.Path == "/logan/meta/logtypes.json" || route.Path == "/logan/task/query/details.json") {
				cors = q.CORS
			} else if n != nil {
				cors = n.CORS
			}
			h.Handle(route.Method, prefix+route.Path, cors, action)
			method := route.Method
			h.OPTIONS(prefix+route.Path, cors, func(_ context.Context, c *app.RequestContext) {
				if requested := string(c.Request.Header.Peek("Access-Control-Request-Method")); requested != "" && requested != method {
					c.Status(405)
					return
				}
				c.Response.Header.SetNoDefaultContentType(true)
				c.Status(200)
			})
		} else {
			h.Handle(route.Method, prefix+route.Path, notImplemented)
		}
	}
}
func notImplemented(_ context.Context, c *app.RequestContext) {
	handler.Write(c, 501, handler.Failure(501, "Go rewrite: endpoint not implemented"))
}

func nCORS(ctx context.Context, c *app.RequestContext) { c.Next(ctx) }
