package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCollectionDraftIsDurableScopedAndNeverSends(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	_, o := req(t, s, "POST", "/issues", "p", "draft-issue", "", Record{"title": "Aliyun draft", "context": Record{"device": "ordinary-device-is-not-push-id"}})
	iid := str(data(t, o)["id"])
	code, o := req(t, s, "POST", "/collection-drafts", "p", "capture-draft", "", Record{"app": "com.hxl.xlb.test", "targetType": "account", "targetValue": "synthetic-account", "bindingConfirmed": true, "issueId": iid, "title": "Preserve unsent request", "begin": "2026-10-22T09:00:00+08:00", "end": "2026-10-22T09:05:00+08:00"})
	if code != 201 {
		t.Fatalf("draft %d %v", code, o)
	}
	draft := data(t, o)
	id := str(draft["id"])
	if draft["status"] != "draft" || draft["channelReceipt"] != nil {
		t.Fatal("draft fabricated a send")
	}
	code, _ = req(t, s, "PATCH", "/collection-drafts/"+id, "p", "stale-capture", "0", Record{"title": "overwrite"})
	if code != 409 {
		t.Fatal("stale draft overwrote")
	}
	code, o = req(t, s, "PATCH", "/collection-drafts/"+id, "p", "edit-capture", "1", Record{"title": "Changed unsent draft"})
	if code != 200 || data(t, o)["revision"] != float64(2) {
		t.Fatal(o)
	}
	_ = s.Close()
	s = openTest(t, dir)
	defer s.Close()
	code, o = req(t, s, "GET", "/collection-drafts/"+id, "p", "", "", nil)
	if code != 200 || data(t, o)["title"] != "Changed unsent draft" {
		t.Fatal("draft lost on restart", o)
	}
	viewerRequest := httptest.NewRequest("GET", Base+"/collection-drafts/"+id, nil)
	viewerRequest.Header.Set("Authorization", "Bearer reader")
	viewerRequest.Header.Set("X-Project-ID", "p")
	viewerResponse := httptest.NewRecorder()
	s.ServeHTTP(viewerResponse, viewerRequest)
	if viewerResponse.Code != 404 {
		t.Fatal("another actor read private draft")
	}
	code, _ = req(t, s, "GET", "/collection-drafts/"+id, "demo", "", "", nil)
	if code != 404 {
		t.Fatal("draft crossed project")
	}
	code, _ = req(t, s, "POST", "/collections", "p", "send-deferred", "", Record{"draftId": id})
	if code != 503 {
		t.Fatal("deferred connector sent")
	}
	s.mu.Lock()
	if len(s.state.bucket("collections")) != 0 {
		t.Fatal("draft created collection")
	}
	s.state.bucket("collectionDrafts")[id]["expiresAt"] = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	s.mu.Unlock()
	code, _ = req(t, s, "PATCH", "/collection-drafts/"+id, "p", "expired-capture", "2", Record{"title": "expired edit"})
	if code != 410 {
		t.Fatal("expired target editable")
	}
	_, o = req(t, s, "GET", "/collection-drafts/"+id, "p", "", "", nil)
	if data(t, o)["targetValue"] != nil {
		t.Fatal("expired target remained readable")
	}
	s.mu.Lock()
	s.purgeExpired()
	raw, _ := json.Marshal(s.state)
	s.mu.Unlock()
	if bytes.Contains(raw, []byte("synthetic-account")) {
		t.Fatal("expired draft target remained in persistent copies")
	}

}
func TestCollectionDraftRejectsInventedTargetsAndUnknownFields(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	for n, b := range []Record{{"app": "other-app"}, {"targetType": "tag"}, {"status": "completed"}, {"channelReceipt": "fake"}, {"issueId": "foreign"}, {"begin": "2026-10-22T09:00:00+08:00"}} {
		code, _ := req(t, s, "POST", "/collection-drafts", "p", fmt.Sprint("invalid-", n), "", b)
		if code < 400 {
			t.Fatalf("unsafe draft accepted %v", b)
		}
	}
	code, o := req(t, s, "POST", "/collection-drafts", "p", "empty-draft", "", Record{})
	if code != 201 || data(t, o)["targetValue"] != "" {
		t.Fatal("blank draft should remain unknown", o)
	}
}
