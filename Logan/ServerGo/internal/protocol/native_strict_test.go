package protocol

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestStrictNativeReparseRejectsPartialOrDamagedSource(t *testing.T) {
	good := recoveryGzip(t, []byte("complete\n"))
	badCRC := append([]byte(nil), good...)
	badCRC[len(badCRC)-8] ^= 1
	second := recoveryGzip(t, []byte("second\n"))
	second[len(second)-8] ^= 1
	for name, payload := range map[string][]byte{"empty": nil, "not-gzip": []byte("bad"), "truncated": good[:len(good)-2], "crc": badCRC, "junk-tail": append(append([]byte(nil), good...), []byte("junk")...), "second-crc": append(append([]byte(nil), good...), second...), "long-zero-tail": append(append([]byte(nil), good...), make([]byte, 16)...)} {
		t.Run(name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "strict")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := DecompressStrict(context.Background(), bytes.NewReader(payload), f, 1024); !errors.Is(err, ErrFrame) {
				t.Fatalf("damaged source accepted: %v", err)
			}
		})
	}
}
func TestStrictNativeReparseAcceptsBoundedHistoricalMembers(t *testing.T) {
	first, second := []byte("first 中文\n"), []byte("second 🌏\n")
	for n := 0; n <= 16; n++ {
		for _, zero := range []bool{false, true} {
			if zero && n == 16 {
				continue
			}
			gap := bytes.Repeat([]byte{byte(n)}, n)
			if zero {
				gap = make([]byte, n)
			}
			payload := append(recoveryGzip(t, first), gap...)
			payload = append(payload, recoveryGzip(t, second)...)
			payload = append(payload, gap...)
			f, err := os.CreateTemp(t.TempDir(), "strict")
			if err != nil {
				t.Fatal(err)
			}
			err = DecompressStrict(context.Background(), &recoveryShortReader{r: bytes.NewReader(payload), n: 1}, f, 1024)
			f.Close()
			if err != nil {
				t.Fatalf("gap=%d zero=%v: %v", n, zero, err)
			}
			got, _ := os.ReadFile(f.Name())
			if !bytes.Equal(got, append(first, second...)) {
				t.Fatalf("incomplete recovery %q", got)
			}
		}
	}
}
func TestStrictNativeReparseRetainsResourceLimits(t *testing.T) {
	payload := append(recoveryGzip(t, []byte("first\n")), recoveryGzip(t, bytes.Repeat([]byte("second\n"), 100))...)
	f, err := os.CreateTemp(t.TempDir(), "strict")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !errors.Is(DecompressStrict(context.Background(), bytes.NewReader(payload), f, 10), ErrLimit) {
		t.Fatal("global plaintext budget ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(DecompressStrict(ctx, bytes.NewReader(payload), f, 1024), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}
