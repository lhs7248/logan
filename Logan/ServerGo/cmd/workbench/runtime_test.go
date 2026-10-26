package main

import (
	"logan/servergo/internal/workbench"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthGrants(t *testing.T) {
	valid := workbench.Config{Projects: []workbench.Project{{ID: "p"}}, Tokens: []workbench.Token{{Token: "0123456789abcdef", Actor: "a", Projects: map[string]string{"p": "viewer"}}}}
	if err := validateAuth(valid); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown", "duplicate-token", "duplicate-project", "empty-project"} {
		t.Run(kind, func(t *testing.T) {
			c := valid
			c.Projects = append([]workbench.Project{}, valid.Projects...)
			c.Tokens = append([]workbench.Token{}, valid.Tokens...)
			switch kind {
			case "unknown":
				c.Tokens[0].Projects = map[string]string{"missing": "admin"}
			case "duplicate-token":
				c.Tokens = append(c.Tokens, c.Tokens[0])
			case "duplicate-project":
				c.Projects = append(c.Projects, c.Projects[0])
			case "empty-project":
				c.Projects[0].ID = ""
			}
			if validateAuth(c) == nil {
				t.Fatal("accepted invalid auth")
			}
		})
	}
}
func TestManualProfile(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "auth.json")
	os.WriteFile(p, []byte("{}"), 0600)
	for _, tc := range []struct {
		profile, addr, ack string
		mode               os.FileMode
		bad                bool
	}{{"", "0.0.0.0:8091", "", 0644, false}, {"manual-v1", "127.0.0.1:8091", "", 0600, false}, {"manual-v1", "localhost:8091", "", 0600, true}, {"manual-v1", "0.0.0.0:8091", "", 0600, true}, {"manual-v1", "0.0.0.0:8091", "true", 0600, false}, {"manual-v1", "127.0.0.1:8091", "", 0640, true}} {
		os.Chmod(p, tc.mode)
		if (validateProfile(tc.profile, p, tc.addr, tc.ack) != nil) != tc.bad {
			t.Fatalf("unexpected result: %+v", tc)
		}
	}
}

func TestManualDataDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(root, 0700)
	private := filepath.Join(root, "data")
	os.Mkdir(private, 0700)
	if err := validateDataDir("manual-v1", private); err != nil {
		t.Fatal(err)
	}
	if err := validateDataDir("manual-v1", filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(private, 0750)
	if validateDataDir("manual-v1", private) == nil {
		t.Fatal("public directory accepted")
	}
	if validateDataDir("manual-v1", "relative") == nil {
		t.Fatal("relative directory accepted")
	}
	link := filepath.Join(root, "link")
	os.Symlink(private, link)
	if validateDataDir("manual-v1", link) == nil {
		t.Fatal("symlink accepted")
	}
	if validateDataDir("", "relative") != nil {
		t.Fatal("default profile changed")
	}
}
