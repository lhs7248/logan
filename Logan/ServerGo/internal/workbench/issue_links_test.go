package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestLogLinkContextConfirmationPreservesAssetAndHistory(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, out := req(t, s, "POST", "/issues", "p", "link-issue", "", Record{"title": "Context check", "context": Record{"device": "device-A", "build": "42"}})
	i := data(t, out)
	iid := str(i["id"])
	a := s.state.add("assets", "p", Record{"name": "other.jsonl", "device": "device-B", "build": "43"})
	aid := str(a["id"])
	path := "/issues/" + iid + "/log-links"
	code, out := req(t, s, "POST", path, "p", "link-no-confirm", "", Record{"assetId": aid})
	if code != 409 {
		t.Fatalf("mismatching log linked without confirmation: %d %v", code, out)
	}
	code, _ = req(t, s, "POST", path, "p", "link-no-reason", fmt.Sprint(i["revision"]), Record{"assetId": aid, "confirmed": true})
	if code != 422 {
		t.Fatalf("confirmation needs an explicit explanation: %d", code)
	}
	code, _ = req(t, s, "POST", path, "p", "link-stale", "0", Record{"assetId": aid, "confirmed": true, "reason": "Compare a different build"})
	if code != 409 {
		t.Fatalf("stale confirmation accepted: %d", code)
	}
	code, out = req(t, s, "POST", path, "p", "link-confirm", fmt.Sprint(i["revision"]), Record{"assetId": aid, "confirmed": true, "reason": "Compare a different build; identity stays unchanged"})
	if code != 200 {
		t.Fatalf("confirmed comparison refused: %d %v", code, out)
	}
	linked := data(t, out)
	if !contains(stringsOf(linked["assetIds"]), aid) || a["device"] != "device-B" || a["build"] != "43" || a["issueId"] != nil {
		t.Fatal("link did not preserve the original asset identity", linked, a)
	}
	code, out = req(t, s, "POST", path, "p", "link-again", "", Record{"assetId": aid})
	if code != 200 || data(t, out)["revision"] != linked["revision"] {
		t.Fatal("already linked log created another change", code, out)
	}
	_, out = req(t, s, "GET", "/issues/"+iid+"/history", "p", "", "", nil)
	rows, ok := out["data"].([]any)
	if !ok || len(rows) != 2 || record(rows[1])["reason"] != "Compare a different build; identity stays unchanged" {
		t.Fatal("link confirmation history missing", out)
	}
}

func TestLogLinkUnknownIdentityIsNotInvented(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, out := req(t, s, "POST", "/issues", "p", "unknown-link-issue", "", Record{"title": "Unknown log", "context": Record{"device": "device-A", "build": "42"}})
	i := data(t, out)
	a := s.state.add("assets", "p", Record{"name": "unknown.jsonl"})
	code, out := req(t, s, "POST", "/issues/"+str(i["id"])+"/log-links", "p", "unknown-link", "", Record{"assetId": a["id"]})
	if code != 200 || a["device"] != nil || a["build"] != nil {
		t.Fatal("unknown identity should be linked as unknown", code, out, a)
	}
}

func TestImportContextConfirmationIsAtomicForWholeBatch(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	_, out := req(t, s, "POST", "/issues", "p", "import-link-issue", "", Record{"title": "Context check", "context": Record{"device": "A", "build": "42"}})
	i := data(t, out)
	meta := Record{"issueId": i["id"], "device": "B", "build": "43"}
	post := func(key string) (int, Record) {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		m, _ := json.Marshal(meta)
		_ = w.WriteField("metadata", string(m))
		for _, name := range []string{"one.jsonl", "two.jsonl"} {
			part, _ := w.CreateFormFile("files", name)
			_, _ = io.WriteString(part, "{\"message\":\"synthetic comparison\"}\n")
		}
		_ = w.Close()
		r := httptest.NewRequest("POST", Base+"/imports", &body)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Project-ID", "p")
		r.Header.Set("Idempotency-Key", keyFor(key))
		r.Header.Set("Content-Type", w.FormDataContentType())
		rw := httptest.NewRecorder()
		s.ServeHTTP(rw, r)
		var result Record
		_ = json.Unmarshal(rw.Body.Bytes(), &result)
		return rw.Code, result
	}
	code, out := post("import-unconfirmed")
	if code != 409 || len(s.state.bucket("assets")) != 0 {
		t.Fatalf("upload bypassed context confirmation or partially stored files: %d %v", code, out)
	}
	meta["confirmed"], meta["reason"], meta["issueRevision"] = true, "Explicit multi-file comparison", 0
	code, _ = post("import-confirm-stale")
	if code != 409 {
		t.Fatalf("stale batch confirmation accepted: %d", code)
	}
	meta["issueRevision"] = i["revision"]
	code, out = post("import-confirmed")
	if code != 201 || len(s.state.bucket("assets")) != 2 {
		t.Fatalf("batch confirmation must authorize all files atomically: %d %v", code, out)
	}
	for _, a := range s.state.bucket("assets") {
		if a["device"] != "B" || a["build"] != "43" {
			t.Fatal("import borrowed problem identity", a)
		}
	}
	_, history := req(t, s, "GET", "/issues/"+str(i["id"])+"/history", "p", "", "", nil)
	rows, _ := history["data"].([]any)
	if len(rows) != 2 || record(rows[1])["reason"] != meta["reason"] {
		t.Fatal("batch link confirmation history missing", history)
	}
}
