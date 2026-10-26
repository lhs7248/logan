package connectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPushConfigurationBoundary(t *testing.T) {
	if _, e := NewPush(PushConfig{Endpoint: "http://example.com/push", Token: "01234567890123456789012345678901", CallbackSecret: "abcdefghijklmnopqrstuvwxyz123456", CallbackURL: "http://localhost/logan-web/api/workbench/v1/integrations/push/events", Projects: []string{"p"}}); e == nil {
		t.Fatal("insecure endpoint accepted")
	}
	p, e := NewPush(PushConfig{})
	if e != nil || p.Ready("p") {
		t.Fatal("default must be disabled")
	}
}

func TestPushConfigRejectsDuplicateGrantsAndSecrets(t *testing.T) {
	c := PushConfig{Endpoint: "http://127.0.0.1:1234/jobs", Token: "01234567890123456789012345678901", CallbackSecret: "abcdefghijklmnopqrstuvwxyz123456", CallbackURL: "http://localhost/logan-web/api/workbench/v1/integrations/push/events", Projects: []string{"p", "p"}}
	if _, e := NewPush(c); e == nil {
		t.Fatal("duplicate grants accepted")
	}
	c.Projects = []string{"p"}
	c.CallbackSecret = c.Token
	if _, e := NewPush(c); e == nil {
		t.Fatal("shared secrets accepted")
	}
	c.CallbackSecret = "abcdefghijklmnopqrstuvwxyz123456"
	c.DeadlineSeconds = 1
	if _, e := NewPush(c); e == nil {
		t.Fatal("unbounded deadline")
	}
}

func TestPushPrivateConfigFile(t *testing.T) {
	dir, e := os.MkdirTemp("", "logan-push-config-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	dir, e = filepath.EvalSymlinks(dir)
	if e != nil {
		t.Fatal(e)
	}
	c := PushConfig{Endpoint: "http://127.0.0.1:1234/jobs", Token: "01234567890123456789012345678901", CallbackSecret: "abcdefghijklmnopqrstuvwxyz123456", CallbackURL: "http://localhost/logan-web/api/workbench/v1/integrations/push/events", Projects: []string{"p"}}
	body, _ := json.Marshal(c)
	file := filepath.Join(dir, "push.json")
	if e := os.WriteFile(file, body, 0600); e != nil {
		t.Fatal(e)
	}
	p, e := LoadPushConfig(file, []string{"p"})
	if e != nil || !p.Ready("p") {
		t.Fatal("private configuration rejected", e)
	}
	if _, e := LoadPushConfig(file, []string{"different"}); e == nil {
		t.Fatal("unknown project allowed")
	}
	os.Chmod(file, 0644)
	if _, e := LoadPushConfig(file, []string{"p"}); e == nil {
		t.Fatal("public secret file allowed")
	}
	os.Chmod(file, 0600)
	link := filepath.Join(dir, "symlink.json")
	if e := os.Symlink(file, link); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadPushConfig(link, []string{"p"}); e == nil {
		t.Fatal("symlink accepted")
	}
}
