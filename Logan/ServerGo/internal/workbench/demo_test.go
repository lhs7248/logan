package workbench

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestDemoFixturesStayIsolatedAndDownloadOriginalSample(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	code, _ := req(t, s, "POST", "/demo-fixtures/web-example/load", "p", "real-demo", "", Record{})
	if code != 403 {
		t.Fatalf("real project accepted demo fixture: %d", code)
	}
	_, o := req(t, s, "GET", "/workspace", "p", "", "", nil)
	if len(sliceOf(data(t, o)["assets"])) != 0 {
		t.Fatal("rejected demo left assets")
	}
	code, o = req(t, s, "POST", "/demo-fixtures/web-example/load", "demo", "demo-load", "", Record{})
	if code != 201 {
		t.Fatal(o)
	}
	a := record(sliceOf(data(t, o)["assets"])[0])
	if a["origin"] != "demo" || a["projectId"] != "demo" || a["rawHash"] != digest([]byte(demoContent)) {
		t.Fatal("demo provenance/hash missing")
	}
	r := httptest.NewRequest("GET", Base+"/demo-fixtures/web-example/download", nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "demo")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), []byte(demoContent)) {
		t.Fatal("download changed sample")
	}
	code, _ = req(t, s, "GET", "/assets/"+str(a["id"])+"/raw", "p", "", "", nil)
	if code != 404 {
		t.Fatal("demo asset accessible from another authorized project")
	}
}
