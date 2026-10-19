package handler

import (
	"bytes"
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app"
)

// Envelope keeps explicit null fields. HTTP status is chosen independently of Code.
type Envelope[T any] struct {
	Code int     `json:"code"`
	Msg  *string `json:"msg"`
	Data T       `json:"data"`
}

func Success[T any](data T) Envelope[T] { return Envelope[T]{Code: 200, Data: data} }
func Failure(code int, msg string) Envelope[any] {
	return Envelope[any]{Code: code, Msg: &msg, Data: nil}
}
func Write(c *app.RequestContext, status int, v any) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(v)
	b := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if err != nil {
		c.Data(500, "application/json", []byte(`{"code":500,"msg":"serialization failed","data":null}`))
		return
	}
	c.Data(status, "application/json", b)
}
