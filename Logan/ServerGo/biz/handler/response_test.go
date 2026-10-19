package handler

import (
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/app"
	"testing"
)

func TestEnvelopePreservesNullAndEmptyArray(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{Success[any](nil), `{"code":200,"msg":null,"data":null}`},
		{Success([]int{}), `{"code":200,"msg":null,"data":[]}`},
		{Failure(400, "param illegal"), `{"code":400,"msg":"param illegal","data":null}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.want {
			t.Fatalf("got %s, want %s, err=%v", b, c.want, err)
		}
	}
}

func TestWriteMatchesJavaHTMLEscaping(t *testing.T) {
	c := app.NewContext(0)
	Write(c, 200, Success("<tag>&中文"))
	if string(c.Response.Body()) != `{"code":200,"msg":null,"data":"<tag>&中文"}` {
		t.Fatalf("Java HTML escaping mismatch: %s", c.Response.Body())
	}
}
