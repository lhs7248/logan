package tempfiles

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSubprocessLockRecovery(t *testing.T) {
	if os.Getenv("TEMP_HELPER") == "1" {
		s, e := Open(os.Getenv("TEMP_BASE"))
		if e != nil {
			panic(e)
		}
		fmt.Println(s.Path())
		os.Stdout.Sync()
		select {}
	}
	base := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSubprocessLockRecovery$")
	cmd.Env = append(os.Environ(), "TEMP_HELPER=1", "TEMP_BASE="+base)
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	sc := bufio.NewScanner(out)
	if !sc.Scan() {
		t.Fatal("no path")
	}
	p := sc.Text()
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	n, e := Sweep(context.Background(), base, time.Minute, 10, time.Now())
	if e != nil || n != 0 {
		t.Fatalf("active %d %v", n, e)
	}
	cmd.Process.Kill()
	cmd.Wait()
	n, e = Sweep(context.Background(), base, time.Minute, 10, time.Now())
	if e != nil || n != 1 {
		t.Fatalf("crashed %d %v", n, e)
	}
}
func TestSafetyAndBatch(t *testing.T) {
	base := t.TempDir()
	a, _ := Open(base)
	b, _ := Open(base)
	a.lock.Close()
	b.lock.Close()
	old := time.Now().Add(-time.Hour)
	os.Chtimes(a.Path(), old, old)
	os.Chtimes(b.Path(), old, old)
	foreign := filepath.Join(base, "logan-servergo", "foreign")
	os.WriteFile(foreign, []byte("keep"), 0600)
	os.Symlink(t.TempDir(), filepath.Join(a.Path(), "link"))
	n, e := Sweep(context.Background(), base, time.Minute, 1, time.Now())
	if e != nil || n > 1 {
		t.Fatalf("%d %v", n, e)
	}
	if _, e = os.Stat(foreign); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Sweep(ctx, base, 0, 10, time.Now()); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestSweepSkipsFirstPageAndLimitsDeletions(t *testing.T) {
	base := t.TempDir()
	active, e := Open(base)
	if e != nil {
		t.Fatal(e)
	}
	defer active.Close()
	for i := 0; i < 3; i++ {
		s, e := Open(base)
		if e != nil {
			t.Fatal(e)
		}
		s.lock.Close()
		s.dir.Close()
		s.parent.Close()
		old := time.Now().Add(-time.Hour)
		os.Chtimes(s.Path(), old, old)
	}
	n, e := Sweep(context.Background(), base, 0, 1, time.Now())
	if e != nil || n != 1 {
		t.Fatalf("first sweep %d %v", n, e)
	}
	n, e = Sweep(context.Background(), base, 0, 1, time.Now())
	if e != nil || n != 1 {
		t.Fatalf("second sweep %d %v", n, e)
	}
}

func TestRejectsUnknownSessionAndSymlink(t *testing.T) {
	base := t.TempDir()
	s, e := Open(base)
	if e != nil {
		t.Fatal(e)
	}
	s.lock.Close()
	s.dir.Close()
	s.parent.Close()
	target := t.TempDir()
	sentinel := filepath.Join(target, "keep")
	os.WriteFile(sentinel, []byte("keep"), 0600)
	os.Symlink(target, filepath.Join(s.Path(), "link"))
	unknown := filepath.Join(base, "logan-servergo", "session-00000000000000000000000000000000")
	os.Mkdir(unknown, 0700)
	os.WriteFile(filepath.Join(unknown, ".lock"), nil, 0600)
	n, e := Sweep(context.Background(), base, 0, 10, time.Now())
	if e != nil || n != 0 {
		t.Fatalf("unsafe reclaimed %d %v", n, e)
	}
	for _, p := range []string{sentinel, s.Path(), unknown} {
		if _, e = os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.Symlink(target, filepath.Join(base, "alias")); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(filepath.Join(base, "alias")); e == nil {
		t.Fatal("accepted symlink base")
	}
}
