package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/biz/router"
	"logan/servergo/internal/native"
	"os"
	"testing"
)

type javaService struct{ failure error }

func (s *javaService) Upload(context.Context, map[string]string, int64, io.Reader) (string, error) {
	return "", s.failure
}
func (s *javaService) DownloadURL(_ context.Context, id int64) (string, error) {
	if id == 404 {
		return "", native.ErrTask
	}
	return "/logan/downing?name=test.log", nil
}
func (s *javaService) Sign(context.Context, string) (string, error) { return "", native.ErrTask }
func TestNativeResponsesMatchJavaWithServletFilters(t *testing.T) {
	raw, err := os.ReadFile("../../tests/contract/golden/java-native-http.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Method, Path string
		Status           int
		Headers          map[string]string
		Body             string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			s := &javaService{failure: native.ErrDecrypt}
			if c.ID == "upload-database" {
				s.failure = native.ErrDatabase
			}
			h := server.New()
			router.RegisterLegacy(h, "", handler.NewNative(s))
			headers := []ut.Header{{Key: "Origin", Value: "http://localhost:3000"}}
			if c.Method == "OPTIONS" {
				headers = append(headers, ut.Header{Key: "Access-Control-Request-Method", Value: "POST"})
			}
			w := ut.PerformRequest(h.Engine, c.Method, c.Path, nil, headers...)
			if w.Code != c.Status {
				t.Fatalf("status %d want %d", w.Code, c.Status)
			}
			for key, value := range c.Headers {
				if actual := string(w.Header().Peek(key)); actual != value {
					t.Errorf("header %s differs: %q want %q", key, actual, value)
				}
			}
			body, _ := base64.StdEncoding.DecodeString(c.Body)
			if !bytes.Equal(w.Body.Bytes(), body) {
				t.Errorf("Java body differs: %s", w.Body.String())
			}
			if _, ok := c.Headers["Content-Type"]; !ok && len(w.Header().Peek("Content-Type")) != 0 {
				t.Fatal("introduced content type on empty response")
			}
		})
	}
}
