package protocol

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
	"os"
	"testing"
)

func recoveryGzip(t *testing.T, plain []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err := z.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func recoveryFrame(t *testing.T, plain []byte, padded bool) []byte {
	t.Helper()
	p := append([]byte(nil), plain...)
	if padded {
		n := 16 - len(p)%16
		p = append(p, bytes.Repeat([]byte{byte(n)}, n)...)
	} else if len(p)%16 != 0 {
		t.Fatal("unaligned raw fixture")
	}
	b, _ := aes.NewCipher([]byte("0123456789abcdef"))
	cipher.NewCBCEncrypter(b, []byte("fedcba9876543210")).CryptBlocks(p, p)
	out := make([]byte, 5)
	out[0] = 1
	binary.BigEndian.PutUint32(out[1:], uint32(len(p)))
	return append(append(out, p...), 0)
}
func recoveryPlain(t *testing.T, wire []byte, chunk int) []byte {
	t.Helper()
	d, _ := NewNativeDecoder([]byte("0123456789abcdef"), []byte("fedcba9876543210"), Limits{MaxInput: 2 << 20, MaxFrame: 1 << 20})
	var b bytes.Buffer
	var r io.Reader = bytes.NewReader(wire)
	if chunk > 0 {
		r = &recoveryShortReader{r: r, n: chunk}
	}
	if err := d.Decode(context.Background(), r, &b); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Decompress(context.Background(), &b, f, 2<<20); err != nil {
		t.Fatal(err)
	}
	p, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type recoveryShortReader struct {
	r io.Reader
	n int
}

func (r *recoveryShortReader) Read(p []byte) (int, error) {
	if len(p) > r.n {
		p = p[:r.n]
	}
	return r.r.Read(p)
}
func TestNativeRecoversConsecutiveSDKFrames(t *testing.T) {
	a, b := []byte("first 中文\n"), []byte("second 🌏\n")
	wire := append(recoveryFrame(t, recoveryGzip(t, a), true), recoveryFrame(t, recoveryGzip(t, b), true)...)
	for _, chunk := range []int{0, 1, 7, 16, 65536} {
		if got := recoveryPlain(t, wire, chunk); !bytes.Equal(got, append(a, b...)) {
			t.Fatalf("chunk %d: got %q", chunk, got)
		}
	}
}
func TestNativeGzipSpansFrames(t *testing.T) {
	want := bytes.Repeat([]byte("continued 中文\n"), 10000)
	gz := recoveryGzip(t, want)
	wire := append(recoveryFrame(t, gz[:16], false), recoveryFrame(t, gz[16:], true)...)
	if got := recoveryPlain(t, wire, 1); !bytes.Equal(got, want) {
		t.Fatalf("got %d want %d", len(got), len(want))
	}
}
func TestNativeLargeCBCFrameAndFullPaddingBlock(t *testing.T) {
	p := make([]byte, 65552)
	for i := range p {
		p[i] = byte(i * 31)
	}
	d, _ := NewNativeDecoder([]byte("0123456789abcdef"), []byte("fedcba9876543210"), Limits{MaxInput: 1 << 20, MaxFrame: 1 << 20})
	var out bytes.Buffer
	if err := d.Decode(context.Background(), bytes.NewReader(recoveryFrame(t, p, true)), &out); err != nil {
		t.Fatal(err)
	}
	p = append(p, bytes.Repeat([]byte{16}, 16)...)
	if !bytes.Equal(out.Bytes(), p) {
		t.Fatalf("large frame bytes %d want %d", out.Len(), len(p))
	}
}
func TestNativeLegacyZeroPaddingBetweenMembers(t *testing.T) {
	a, b := []byte("legacy-first\n"), []byte("legacy-second\n")
	var wire []byte
	for _, p := range [][]byte{a, b} {
		gz := recoveryGzip(t, p)
		gz = append(gz, make([]byte, (16-len(gz)%16)%16)...)
		wire = append(wire, recoveryFrame(t, gz, false)...)
	}
	if got := recoveryPlain(t, wire, 3); !bytes.Equal(got, append(a, b...)) {
		t.Fatalf("got %q", got)
	}
}

// Old Java/Go uploads persisted decrypted gzip with PKCS#7 left between members.
func TestDecompressHistoricalPersistedPadding(t *testing.T) {
	for gap := 1; gap <= 16; gap++ {
		a, b := []byte("historical-first\n"), []byte("historical-second\n")
		compressed := append(recoveryGzip(t, a), bytes.Repeat([]byte{byte(gap)}, gap)...)
		compressed = append(compressed, recoveryGzip(t, b)...)
		f, err := os.CreateTemp(t.TempDir(), "old-object")
		if err != nil {
			t.Fatal(err)
		}
		err = Decompress(context.Background(), bytes.NewReader(compressed), f, 1024)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(f.Name())
		if !bytes.Equal(got, append(a, b...)) {
			t.Fatalf("padding %d got %q", gap, got)
		}
	}
}

func TestActualCLoganRegressionAndHistoricalObject(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		p, err := os.ReadFile("../../testdata/protocol/clogan-regression/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	want := read("native-plain.bin")
	d, _ := NewNativeDecoder([]byte("0123456789012345"), []byte("0123456789012345"), Limits{MaxInput: 1 << 20, MaxFrame: 1 << 20})
	var compressed bytes.Buffer
	if err := d.Decode(context.Background(), &recoveryShortReader{r: bytes.NewReader(read("native.bin")), n: 1}, &compressed); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{"new-upload": compressed.Bytes(), "historical-object": read("legacy-padded.gz")} {
		t.Run(name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "sdk")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := Decompress(context.Background(), bytes.NewReader(payload), f, 1024); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(f.Name())
			if !bytes.Equal(got, want) {
				t.Fatalf("actual SDK records differ: got %q want %q", got, want)
			}
		})
	}
}
func TestDecompressDoesNotScanDamagedMemberGaps(t *testing.T) {
	first, second := []byte("first\n"), []byte("must-not-recover\n")
	for _, gap := range [][]byte{[]byte("junk"), {2, 3}, bytes.Repeat([]byte{0}, 16), bytes.Repeat([]byte{17}, 17), {1, 1}} {
		payload := append(recoveryGzip(t, first), gap...)
		payload = append(payload, recoveryGzip(t, second)...)
		f, err := os.CreateTemp(t.TempDir(), "damaged")
		if err != nil {
			t.Fatal(err)
		}
		err = Decompress(context.Background(), bytes.NewReader(payload), f, 1024)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(f.Name())
		if !bytes.Equal(got, first) {
			t.Fatalf("gap %x recovered %q", gap, got)
		}
	}
}

func TestNativePreservesEveryPaddingLikeSuffix(t *testing.T) {
	for n := 1; n <= 16; n++ {
		plain := bytes.Repeat([]byte{'x'}, 32-n)
		d, _ := NewNativeDecoder([]byte("0123456789abcdef"), []byte("fedcba9876543210"), Limits{MaxInput: 1024, MaxFrame: 1024})
		var got bytes.Buffer
		if err := d.Decode(context.Background(), &recoveryShortReader{r: bytes.NewReader(recoveryFrame(t, plain, true)), n: 1}, &got); err != nil {
			t.Fatal(err)
		}
		plain = append(plain, bytes.Repeat([]byte{byte(n)}, n)...)
		if !bytes.Equal(got.Bytes(), plain) {
			t.Fatalf("padding %d got %x", n, got.Bytes())
		}
	}
}
func TestDecompressRecoveredMemberStillHonorsGlobalBudget(t *testing.T) {
	first := recoveryGzip(t, []byte("first\n"))
	second := recoveryGzip(t, bytes.Repeat([]byte("second\n"), 100))
	payload := append(first, bytes.Repeat([]byte{16}, 16)...)
	payload = append(payload, second...)
	f, err := os.CreateTemp(t.TempDir(), "budget")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Decompress(context.Background(), bytes.NewReader(payload), f, 16); err != ErrLimit {
		t.Fatalf("budget error=%v", err)
	}
}

func TestNativeGzipFragmentEndingInPaddingLikeByte(t *testing.T) {
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	z.Header.Extra = []byte{0, 0, 0, 1}
	want := []byte("must preserve gzip extra field\n")
	if _, err := z.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	gz := compressed.Bytes()
	wire := append(recoveryFrame(t, gz[:16], false), recoveryFrame(t, gz[16:], true)...)
	if got := recoveryPlain(t, wire, 1); !bytes.Equal(got, want) {
		t.Fatalf("fragment lost real byte: %q", got)
	}
}
