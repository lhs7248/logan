package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityFlags(t *testing.T) {
	base := identityFlags{action: "identity-migrate", witness: filepath.Join(t.TempDir(), "w"), digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", source: "source", target: "target", offline: true, writes: true, migration: true, fenced: true}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"offline", "writes", "migration", "fenced", "digest", "source", "target", "witness"} {
		v := base
		switch field {
		case "offline":
			v.offline = false
		case "writes":
			v.writes = false
		case "migration":
			v.migration = false
		case "fenced":
			v.fenced = false
		case "digest":
			v.digest = ""
		case "source":
			v.source = ""
		case "target":
			v.target = ""
		case "witness":
			v.witness = ""
		}
		if v.validate() == nil {
			t.Fatalf("accepted missing %s", field)
		}
	}
	for _, action := range []string{"dry-run", "apply", "rollback"} {
		v := base
		v.action = action
		if v.validate() == nil {
			t.Fatalf("accepted identity flags on %s", action)
		}
	}
	v := base
	v.action = "identity-dry-run"
	v.writes = false
	v.migration = false
	v.fenced = false
	if v.validate() != nil {
		t.Fatal("read only rejected")
	}
	v.writes = true
	if v.validate() == nil {
		t.Fatal("write flag on read only accepted")
	}
}
func TestIdentityConfigNeedsNoObjects(t *testing.T) {
	for _, action := range []string{"identity-export", "identity-dry-run", "identity-migrate"} {
		c, err := loadConfig(action, func(k string) string {
			if k == "LOGAN_MYSQL_DSN" {
				return "u:p@tcp(localhost:3306)/logan_reparse_it_unit"
			}
			if k == "LOGAN_NATIVE_AES_KEY" || k == "LOGAN_OSS_ENDPOINT" {
				return "invalid"
			}
			return ""
		})
		if err != nil || c.Native.Enabled || c.NativeQuery.Enabled || c.OSS.AccessKeyID != "" {
			t.Fatalf("%s loaded objects: %v", action, err)
		}
	}
}

func TestIdentityExportFlags(t *testing.T) {
	base := identityFlags{action: "identity-export", witness: filepath.Join(t.TempDir(), "w"), offline: true}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*identityFlags){func(v *identityFlags) { v.offline = false }, func(v *identityFlags) { v.writes = true }, func(v *identityFlags) { v.migration = true }, func(v *identityFlags) { v.fenced = true }, func(v *identityFlags) { v.digest = "extra" }, func(v *identityFlags) { v.source = "extra" }, func(v *identityFlags) { v.target = "extra" }, func(v *identityFlags) { v.witness = "relative" }} {
		v := base
		alter(&v)
		if v.validate() == nil {
			t.Fatal("unsafe export flags accepted")
		}
	}
}

func TestIdentityPrivateWitness(t *testing.T) {
	p := filepath.Join(t.TempDir(), "witness.json")
	type witness struct {
		Digest string `json:"digest"`
	}
	for _, body := range []string{`{"digest":"ok","unknown":true}`, `{} {}`, strings.Repeat(" ", maxPlanBytes) + `{}`} {
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var w witness
		if readPlan(p, &w) == nil {
			t.Fatal("unsafe witness accepted")
		}
	}
}
func TestIdentityCLIRejectsBeforeConnection(t *testing.T) {
	get := func(k string) string {
		switch k {
		case "LOGAN_REPARSE_ISOLATED":
			return "YES"
		case "LOGAN_MYSQL_DSN":
			return "u:private-secret@tcp(localhost:1)/logan_reparse_it_unit"
		}
		return ""
	}
	for _, args := range [][]string{
		{"--action", "identity-export", "--task-id", "1", "--plan", "/tmp/absent", "--witness", "/tmp/out", "--offline", "--allow-isolated-writes"},
		{"--action", "identity-migrate", "--witness", "/tmp/absent", "--offline", "--allow-isolated-writes", "--allow-identity-migration"},
		{"--action", "identity-dry-run", "--witness", "/tmp/absent", "--offline", "--source-fenced"},
		{"--action", "apply", "--task-id", "1", "--plan", "/tmp/absent", "--offline", "--allow-isolated-writes", "--source-fenced"},
		{"--action", "identity-export", "--unknown-identity-bypass"},
	} {
		var out, errs bytes.Buffer
		if run(args, get, &out, &errs) == 0 || out.Len() != 0 || strings.Contains(errs.String(), "private-secret") {
			t.Fatal("unsafe CLI result")
		}
	}
}

func TestWitnessOutputBound(t *testing.T) {
	var out bytes.Buffer
	if encodeWitness(&out, map[string]string{"digest": strings.Repeat("x", maxPlanBytes)}) == nil || out.Len() != 0 {
		t.Fatal("oversized witness published")
	}
	if err := encodeWitness(&out, map[string]string{"digest": "ok"}); err != nil {
		t.Fatal(err)
	}
}
