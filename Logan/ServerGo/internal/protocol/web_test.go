package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestWebBasics(t *testing.T) {
	d := NewWebDecoder(nil, 1024)
	got, e := d.Parse(context.Background(), url.QueryEscape(`{"l":"aGVs!bG8"}`))
	if e != nil || got != `["hello"]` {
		t.Fatalf("%q %v", got, e)
	}
	if _, e = d.Parse(context.Background(), "%xx"); e == nil {
		t.Fatal("bad URL accepted")
	}
	ctx, c := context.WithCancel(context.Background())
	c()
	if _, e = d.Parse(ctx, "x"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = d.Parse(context.Background(), strings.Repeat("a", 1025)); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}
func TestWebJavaVectors(t *testing.T) {
	t.Setenv("GODEBUG", "rsa1024min=0") // Legacy Java fixture key is 1023 bits; production keeps Go key policy.
	b, e := os.ReadFile("../../testdata/protocol/java-web-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Cases []struct {
			Name, Raw, Output string
			Error             bool
		}
		Details []struct {
			Name, Content string
			Output        []WebDetail
			Error         bool
		}
	}
	if e = json.Unmarshal(b, &f); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile("../../../Server/src/main/java/com/meituan/logan/web/parser/WebLogDecryptHelper.java")
	if e != nil {
		t.Fatal(e)
	}
	m := regexp.MustCompile(`RSA_PRIVATE_KEY_H5 = "([^"]+)"`).FindSubmatch(source)
	key, _ := base64.StdEncoding.DecodeString(string(m[1]))
	d := NewWebDecoder(key, 1<<20)
	for _, v := range f.Cases {
		t.Run(v.Name, func(t *testing.T) {
			got, e := d.Parse(context.Background(), v.Raw)
			if (e != nil) != v.Error || e == nil && got != v.Output {
				t.Fatalf("got %q %v want %q error=%v", got, e, v.Output, v.Error)
			}
		})
	}
	for _, v := range f.Details {
		t.Run(v.Name, func(t *testing.T) {
			got, e := d.ParseDetails(context.Background(), v.Content, 42, nil)
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(v.Output)
			if (e != nil) != v.Error || e == nil && string(a) != string(b) {
				t.Fatalf("got %s %v want %s", a, e, b)
			}
		})
	}
}

func FuzzWebDecode(f *testing.F) {
	for _, s := range []string{"%xx", ",,,", url.QueryEscape(`{"l":"aGVsbG8="}`), url.QueryEscape(`{"v":1,"k":"bad","iv":"bad"}`)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 65536 {
			t.Skip()
		}
		d := NewWebDecoder(nil, 8192)
		out, err := d.Parse(context.Background(), raw)
		if err == nil {
			if len(out) > 8192 || !json.Valid([]byte(out)) {
				t.Fatal("unbounded or invalid result")
			}
			_, _ = d.ParseDetails(context.Background(), out, 1, nil)
		}
	})
}
