package main

import (
	"encoding/json"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"logan/servergo/biz/router"
	"logan/servergo/internal/config"
	"logan/servergo/internal/contract"
	"regexp"
	"testing"
)

func TestFoundationHealthAndLegacyRoutes(t *testing.T) {
	c, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	h := newServer(c)
	for _, v := range []struct {
		path   string
		status int
	}{{"/ping", 200}, {"/livez", 200}, {"/readyz", 503}} {
		w := ut.PerformRequest(h.Engine, "GET", v.path, nil)
		if w.Code != v.status {
			t.Errorf("%s status=%d", v.path, w.Code)
		}
	}
	for _, r := range router.LegacyRoutes() {
		target := regexp.MustCompile(`:[A-Za-z]+`).ReplaceAllString(r.Path, "1")
		w := ut.PerformRequest(h.Engine, r.Method, c.ContextPath+target, nil)
		if w.Code != 501 {
			t.Errorf("%s %s status=%d", r.Method, target, w.Code)
		}
		var e struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Code != 501 {
			t.Errorf("invalid foundation response: %s", w.Body.String())
		}
	}
}
func TestScaffoldRoutesMatchFrozenManifest(t *testing.T) {
	m, err := contract.LoadManifest("tests/contract/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	braces := regexp.MustCompile(`\{([A-Za-z]+)\}`)
	for _, r := range m.Routes {
		seen[r.Method+" "+braces.ReplaceAllString(r.Path, ":$1")] = true
	}
	routes := router.LegacyRoutes()
	if len(routes) != len(m.Routes) {
		t.Fatal("route count mismatch")
	}
	for _, r := range routes {
		if !seen[r.Method+" "+r.Path] {
			t.Errorf("unexpected route %+v", r)
		}
	}
}
func TestRootContextDoesNotDuplicatePrefix(t *testing.T) {
	c, err := config.Load(func(k string) string {
		if k == "LOGAN_CONTEXT_PATH" {
			return "/"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newServer(c)
	if w := ut.PerformRequest(h.Engine, "GET", "/logan/latest.json", nil); w.Code != 501 {
		t.Fatalf("root mount: %d", w.Code)
	}
	if w := ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/latest.json", nil); w.Code != 404 {
		t.Fatalf("unexpected alias: %d", w.Code)
	}
}
