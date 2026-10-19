// Package tempfiles owns process sessions. Close must run only after all users drain.
package tempfiles

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const marker = "logan-servergo-temp-v1\n"

type Session struct {
	path   string
	lock   *os.File
	dir    *os.Root
	parent *os.Root
	name   string
	once   sync.Once
	err    error
}

func dedicated(base string) (*os.Root, error) {
	abs, e := filepath.Abs(base)
	if e != nil {
		return nil, e
	}
	baseInfo, e := os.Lstat(abs)
	if e != nil {
		return nil, e
	}
	if !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe base directory")
	}
	r, e := os.OpenRoot(abs)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if e = r.Mkdir("logan-servergo", 0700); e != nil && !os.IsExist(e) {
		return nil, e
	}
	i, e := r.Lstat("logan-servergo")
	if e != nil {
		return nil, e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm() != 0700 || !owned(i) {
		return nil, errors.New("unsafe temp root")
	}
	return r.OpenRoot("logan-servergo")
}
func owned(i os.FileInfo) bool {
	st, ok := i.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

func Open(base string) (*Session, error) {
	p, e := dedicated(base)
	if e != nil {
		return nil, e
	}
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		p.Close()
		return nil, e
	}
	name := "session-" + hex.EncodeToString(b[:])
	if e = p.Mkdir(name, 0700); e != nil {
		p.Close()
		return nil, e
	}
	d, e := p.OpenRoot(name)
	if e != nil {
		p.Close()
		return nil, e
	}
	f, e := d.OpenFile(".lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if e == nil {
		e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if e == nil {
		e = d.WriteFile(".owner", []byte(marker), 0600)
	}
	if e != nil {
		if f != nil {
			f.Close()
		}
		d.Close()
		p.Close()
		return nil, e
	}
	abs, _ := filepath.Abs(base)
	return &Session{path: filepath.Join(abs, "logan-servergo", name), lock: f, dir: d, parent: p, name: name}, nil
}
func (s *Session) Path() string { return s.path }

// clean refuses nested directories, symlinks, and special files; it never traverses them.
func clean(d *os.Root) error {
	f, e := d.Open(".")
	if e != nil {
		return e
	}
	entries, e := f.ReadDir(-1)
	f.Close()
	if e != nil {
		return e
	}
	for _, x := range entries {
		i, e := d.Lstat(x.Name())
		if e != nil {
			return e
		}
		if !i.Mode().IsRegular() {
			return errors.New("unsafe session entry")
		}
	}
	for _, x := range entries {
		if x.Name() == ".lock" || x.Name() == ".owner" {
			continue
		}
		if e = d.Remove(x.Name()); e != nil {
			return e
		}
	}
	if e = d.Remove(".owner"); e != nil {
		return e
	}
	return d.Remove(".lock")
}
func (s *Session) Close() error {
	s.once.Do(func() {
		s.err = clean(s.dir)
		if s.err == nil {
			s.err = s.parent.Remove(s.name)
		}
		s.lock.Close()
		s.dir.Close()
		s.parent.Close()
	})
	return s.err
}
func Sweep(ctx context.Context, base string, minAge time.Duration, batch int, now time.Time) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if batch <= 0 {
		return 0, nil
	}
	p, e := dedicated(base)
	if e != nil {
		return 0, e
	}
	defer p.Close()
	f, e := p.Open(".")
	if e != nil {
		return 0, e
	}
	defer f.Close()
	n := 0
	for n < batch {
		if e = ctx.Err(); e != nil {
			return n, e
		}
		entries, readErr := f.ReadDir(1)
		if len(entries) == 0 {
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					return n, nil
				}
				return n, readErr
			}
			continue
		}
		x := entries[0]
		name := x.Name()
		if !strings.HasPrefix(name, "session-") || len(name) != 40 {
			continue
		}
		if _, e = hex.DecodeString(strings.TrimPrefix(name, "session-")); e != nil {
			continue
		}
		i, e := p.Lstat(name)
		if e != nil || !i.IsDir() || i.Mode().Perm() != 0700 || !owned(i) || now.Sub(i.ModTime()) < minAge {
			continue
		}
		d, e := p.OpenRoot(name)
		if e != nil {
			continue
		}
		func() {
			defer d.Close()
			lock, e := d.OpenFile(".lock", os.O_RDWR|unix.O_NOFOLLOW, 0)
			if e != nil {
				return
			}
			defer lock.Close()
			if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
				return
			}
			defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			li, e := lock.Stat()
			if e != nil || !li.Mode().IsRegular() {
				return
			}
			oi, e := d.Lstat(".owner")
			if e != nil || !oi.Mode().IsRegular() {
				return
			}
			owner, e := d.OpenFile(".owner", os.O_RDONLY|unix.O_NOFOLLOW, 0)
			if e != nil {
				return
			}
			data, e := io.ReadAll(io.LimitReader(owner, 128))
			owner.Close()
			if e != nil || string(data) != marker {
				return
			}
			lockInfo, e := d.Lstat(".lock")
			if e != nil || !os.SameFile(li, lockInfo) {
				return
			}
			current, e := p.Lstat(name)
			df, e2 := d.Stat(".")
			if e != nil || e2 != nil || !os.SameFile(current, df) || !os.SameFile(i, current) || now.Sub(current.ModTime()) < minAge {
				return
			}
			if clean(d) == nil && p.Remove(name) == nil {
				n++
			}
		}()
	}
	return n, nil
}
