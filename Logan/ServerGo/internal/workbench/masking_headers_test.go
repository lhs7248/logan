package workbench

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestTransportCredentialMasking(t *testing.T) {
	for _, name := range []string{"X-Logan-Upload-Token", "x-logan-upload-token", "X_LOGAN_UPLOAD_TOKEN", "X-Api-Key", "x-api-key"} {
		t.Run(name, func(t *testing.T) {
			input := Record{"headers": []any{Record{name: "opaque-credential-value", "deviceId": "installation-42", "X-Logan-Upload-ID": "retry-42"}}}
			masked := maskValue(input)
			b, _ := json.Marshal(masked)
			if bytes.Contains(b, []byte("opaque-credential-value")) {
				t.Fatal("structured transport credential was not masked")
			}
			for _, keep := range []string{"installation-42", "retry-42"} {
				if !bytes.Contains(b, []byte(keep)) {
					t.Fatalf("innocuous identity %s was masked", keep)
				}
			}
			text, _ := json.Marshal(Record{name: "opaque-credential-value"})
			if bytes.Contains([]byte(maskText(string(text))), []byte("opaque-credential-value")) {
				t.Fatal("compact JSON transport credential was not masked")
			}
		})
	}
}

func TestTransportCredentialsMaskedInDecodedRoutes(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	content := `{"content":"request","headers":{"X-Logan-Upload-Token":"UPLOAD_CREDENTIAL_VALUE","x-api-key":"API_CREDENTIAL_VALUE","deviceId":"installation-42","X-Logan-Upload-ID":"retry-42"}}` + "\n"
	aid := upload(t, s, "p", "headers.jsonl", content)
	code, out := req(t, s, "POST", "/assets/"+aid+"/parse-runs", "p", "parse-headers", "", Record{"format": "jsonl"})
	if code != 201 {
		t.Fatal(code, out)
	}
	rid := str(data(t, out)["id"])
	if run := awaitRun(t, s, rid); run["status"] != "succeeded" {
		t.Fatal(run)
	}
	code, out = req(t, s, "POST", "/exports", "p", "export-headers", "", Record{"kind": "events", "runId": rid})
	if code != 201 {
		t.Fatal(code, out)
	}
	exportID := str(data(t, out)["id"])
	for _, route := range []struct{ path, token string }{
		{"/parse-runs/" + rid + "/events", "reader"},
		{"/events/" + rid + "-1/context", "reader"},
		{"/exports/" + exportID + "/content", "secret"},
	} {
		r := httptest.NewRequest("GET", Base+route.path, nil)
		r.Header.Set("Authorization", "Bearer "+route.token)
		r.Header.Set("X-Project-ID", "p")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", route.path, w.Code, w.Body.String())
		}
		for _, secret := range []string{"UPLOAD_CREDENTIAL_VALUE", "API_CREDENTIAL_VALUE"} {
			if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
				t.Errorf("%s exposed transport credential", route.path)
			}
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("installation-42")) || !bytes.Contains(w.Body.Bytes(), []byte("retry-42")) {
			t.Errorf("%s lost innocuous identity metadata", route.path)
		}
	}
	// Explicitly authorized raw access must retain the original file bytes.
	r := httptest.NewRequest("GET", Base+"/assets/"+aid+"/raw", nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("X-Project-ID", "p")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != content {
		t.Fatal("authorized original download changed")
	}
}
