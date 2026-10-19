package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"logan/servergo/internal/native"
	"testing"
)

type stubNative struct {
	uploadErr, errorSign error
	id                   int64
	body                 []byte
}

func (s *stubNative) Upload(_ context.Context, _ map[string]string, _ int64, reader io.Reader) (string, error) {
	s.body, _ = io.ReadAll(reader)
	return "/logan/downing?name=test.log", s.uploadErr
}
func (s *stubNative) DownloadURL(_ context.Context, id int64) (string, error) {
	s.id = id
	if id == 404 {
		return "", native.ErrTask
	}
	return "/logan/downing?name=test.log", nil
}
func (s *stubNative) Sign(context.Context, string) (string, error) {
	return "https://bucket.example/test.log?signature=fixed", s.errorSign
}
func TestNativeUploadLegacyResponses(t *testing.T) {
	for _, err := range []error{nil, native.ErrDecrypt, native.ErrDatabase} {
		s := &stubNative{uploadErr: err}
		h := server.New()
		n := NewNative(s)
		h.POST("/upload", n.CORS, n.Upload)
		w := ut.PerformRequest(h.Engine, "POST", "/upload", &ut.Body{Body: bytes.NewReader([]byte{1}), Len: 1})
		if w.Code != 200 {
			t.Fatalf("changed business HTTP status %d", w.Code)
		}
		var e struct {
			Code int     `json:"code"`
			Msg  *string `json:"msg"`
			Data *string `json:"data"`
		}
		if json.Unmarshal(w.Body.Bytes(), &e) != nil {
			t.Fatal("invalid envelope")
		}
		if err == nil {
			if e.Code != 200 || e.Msg != nil || e.Data == nil {
				t.Fatal("invalid success")
			}
		} else {
			if e.Code != 500 || e.Msg == nil || *e.Msg != err.Error() || e.Data != nil {
				t.Fatal("invalid failure")
			}
		}
	}
}
func TestNativeDownloadHTTPContracts(t *testing.T) {
	s := &stubNative{}
	n := NewNative(s)
	h := server.New()
	h.GET("/task/:taskId/download.json", n.CORS, n.DownloadURL)
	h.GET("/downing", n.CORS, n.Download)
	for _, c := range []struct {
		path   string
		status int
		code   int
	}{
		{"/task/42/download.json", 200, 200}, {"/task/0/download.json", 200, 400}, {"/task/404/download.json", 200, 400},
		{"/task/2147483648/download.json", 400, 0}, {"/task/abc/download.json", 400, 0}, {"/downing", 400, 0}, {"/downing?name=test.log", 302, 0},
	} {
		w := ut.PerformRequest(h.Engine, "GET", c.path, nil)
		if w.Code != c.status {
			t.Fatalf("%s status %d", c.path, w.Code)
		}
		if c.code > 0 {
			var e struct {
				Code int `json:"code"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &e)
			if e.Code != c.code {
				t.Fatalf("%s business %d", c.path, e.Code)
			}
		}
		if c.status == 302 {
			if string(w.Header().Peek("Location")) != "https://bucket.example/test.log?signature=fixed" || w.Body.Len() != 0 {
				t.Fatal("redirect changed")
			}
		}
	}
	s.errorSign = errors.New("private detail")
	w := ut.PerformRequest(h.Engine, "GET", "/downing?name=test.log", nil)
	if w.Code != 404 || w.Body.Len() != 0 {
		t.Fatal("signing failure contract changed")
	}
	w = ut.PerformRequest(h.Engine, "GET", "/downing", nil, ut.Header{Key: "Origin", Value: "http://localhost:3000"})
	if string(w.Header().Peek("Access-Control-Allow-Origin")) != "http://localhost:3000" || string(w.Header().Peek("Access-Control-Allow-Credentials")) != "true" {
		t.Fatal("legacy CORS changed")
	}
}

func TestNativeBufferedRequestBodyIsNotLost(t *testing.T) {
	s := &stubNative{}
	h := server.New()
	h.POST("/upload", NewNative(s).Upload)
	ut.PerformRequest(h.Engine, "POST", "/upload", &ut.Body{Body: bytes.NewReader([]byte{1, 2, 3}), Len: 3})
	if !bytes.Equal(s.body, []byte{1, 2, 3}) {
		t.Fatalf("buffered bytes lost: %v", s.body)
	}
}
