package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlagsAndIsolation(t *testing.T) {
	for _, args := range [][]string{{}, {"--task-id", "1", "--plan", "/tmp/p"}, {"--action", "apply", "--plan", "/tmp/p", "--task-id", "1"}, {"--action", "rollback", "--operation-id", "x"}} {
		var out, errout bytes.Buffer
		if run(args, func(string) string { return "secret-password" }, &out, &errout) == 0 {
			t.Fatal("unsafe command accepted")
		}
		if strings.Contains(errout.String(), "secret-password") {
			t.Fatal("secret leaked")
		}
	}
}
func TestPrivatePlan(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := writePlan(p, map[string]int{"task_id": 1}); err != nil {
		t.Fatal(err)
	}
	if err := writePlan(p, map[string]int{}); err == nil {
		t.Fatal("overwrite accepted")
	}
	var got map[string]int
	if err := readPlan(p, &got); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if err := readPlan(p, &got); err == nil {
		t.Fatal("public file accepted")
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if err := readPlan(link, &got); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestStrictPlanJSON(t *testing.T) {
	for _, body := range []string{`{"unknown":1}`, `{} {}`, `{"task_id":1} trailing`} {
		p := filepath.Join(t.TempDir(), "p")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var v struct {
			TaskID int64 `json:"task_id"`
		}
		if readPlan(p, &v) == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestRollbackNeedsNoOSSOrAES(t *testing.T) {
	env := map[string]string{"LOGAN_MYSQL_DSN": "u:p@tcp(localhost:3306)/logan_reparse_it_unit", "LOGAN_NATIVE_ENABLED": "true", "LOGAN_NATIVE_QUERY_ENABLED": "true", "LOGAN_OSS_ENDPOINT": "invalid secret endpoint", "LOGAN_NATIVE_AES_KEY": "invalid"}
	c, err := loadConfig("rollback", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !c.Database.Enabled || c.Native.Enabled || c.NativeQuery.Enabled || c.OSS.AccessKeyID != "" {
		t.Fatal("rollback loaded object credentials")
	}
}

func TestProductionAndMissingWriteAttestationRejected(t *testing.T) {
	cases := []struct {
		dsn  string
		args []string
	}{
		{"u:secret@tcp(localhost:1)/logan", []string{"--task-id", "1", "--plan", "/tmp/never-create-reparse-plan"}},
		{"u:secret@tcp(localhost:1)/logan_reparse_it_bad-name", []string{"--task-id", "1", "--plan", "/tmp/never-create-reparse-plan"}},
		{"u:secret@tcp(localhost:1)/logan_reparse_it_unit", []string{"--action", "apply", "--task-id", "1", "--plan", "/tmp/never-create-reparse-plan", "--allow-isolated-writes"}},
		{"u:secret@tcp(localhost:1)/logan_reparse_it_unit", []string{"--action", "rollback", "--operation-id", "a", "--offline"}},
	}
	for _, tc := range cases {
		var out, errs bytes.Buffer
		get := func(k string) string {
			if k == "LOGAN_REPARSE_ISOLATED" {
				return "YES"
			}
			if k == "LOGAN_MYSQL_DSN" {
				return tc.dsn
			}
			return ""
		}
		if run(tc.args, get, &out, &errs) == 0 {
			t.Fatal("guard failed")
		}
		if out.Len() != 0 || strings.Contains(errs.String(), "secret") {
			t.Fatal("unsafe error output")
		}
	}
}

func TestDryRunAndApplyLoadStoredObjectConfigWithoutAES(t *testing.T) {
	env := map[string]string{"LOGAN_MYSQL_DSN": "u:p@tcp(localhost:3306)/logan_reparse_it_unit", "LOGAN_OSS_ENDPOINT": "http://localhost:9000", "LOGAN_OSS_REGION": "test", "LOGAN_OSS_BUCKET": "test", "LOGAN_OSS_ACCESS_KEY_ID": "test", "LOGAN_OSS_ACCESS_KEY_SECRET": "test"}
	for _, action := range []string{"dry-run", "apply"} {
		c, err := loadConfig(action, func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("%s configuration failed: %v", action, err)
		}
		if !c.Database.Enabled || !c.Native.Enabled || !c.NativeQuery.Enabled {
			t.Fatalf("%s configuration incomplete", action)
		}
	}
}
