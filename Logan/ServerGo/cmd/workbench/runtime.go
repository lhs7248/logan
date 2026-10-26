package main

import (
	"errors"
	"logan/servergo/internal/workbench"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func validateAuth(c workbench.Config) error {
	projects := map[string]bool{}
	tokens := map[string]bool{}
	if len(c.Projects) == 0 || len(c.Tokens) == 0 {
		return errors.New("projects and tokens required")
	}
	for _, p := range c.Projects {
		if strings.TrimSpace(p.ID) == "" || projects[p.ID] {
			return errors.New("project IDs must be nonempty and unique")
		}
		projects[p.ID] = true
	}
	for _, t := range c.Tokens {
		if len(t.Token) < 16 || strings.TrimSpace(t.Actor) == "" || len(t.Projects) == 0 || tokens[t.Token] {
			return errors.New("invalid or duplicate token configuration")
		}
		tokens[t.Token] = true
		for p, r := range t.Projects {
			if !projects[p] {
				return errors.New("token grant references unknown project")
			}
			switch r {
			case "viewer", "investigator", "reviewer", "admin":
			default:
				return errors.New("unknown project role")
			}
		}
	}
	return nil
}
func validateProfile(profile, auth, addr, ack string) error {
	if profile == "" {
		return nil
	}
	if profile != "manual-v1" {
		return errors.New("unknown workbench profile")
	}
	if err := noSymlinks(auth); err != nil {
		return err
	}
	info, err := os.Lstat(auth)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return errors.New("manual-v1 requires a private regular auth file (0600)")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("invalid workbench binding")
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && ack != "true" {
		return errors.New("manual-v1 requires loopback binding or WORKBENCH_INTRANET_GATEWAY_ACK=true")
	}
	if os.Getenv("WORKBENCH_MYSQL_DSN") != "" {
		return errors.New("manual-v1 requires single-instance local file storage")
	}
	return nil
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}
func noSymlinks(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, e := os.Lstat(absolute)
		if e != nil {
			return errors.New("manual-v1 path must exist")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("manual-v1 forbids symlink paths")
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			break
		}
		absolute = parent
	}
	return nil
}
func validateDataDir(profile, dir string) error {
	if profile != "manual-v1" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return errors.New("manual-v1 requires an absolute local data directory")
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		dir = filepath.Dir(dir)
		info, err = os.Lstat(dir)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return errors.New("manual-v1 data directory or new directory parent must be private and owned by this user")
	}
	return noSymlinks(dir)
}
