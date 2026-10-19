package sdk_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"logan/servergo/internal/protocol"
)

func TestNativeSDKFlushHasNoEmptyFrame(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler required for actual CLogan regression")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	croot := filepath.Join(root, "Clogan")
	mbed := filepath.Join(root, "mbedtls")
	binaryPath := filepath.Join(t.TempDir(), "producer")
	args := []string{"-I", croot, "-I", filepath.Join(mbed, "include"), "driver/native-flush-producer.c"}
	for _, n := range []string{"clogan_core", "mmap_util", "cJSON", "construct_data", "zlib_util", "json_util", "aes_util", "directory_util", "base_util", "console_util"} {
		args = append(args, filepath.Join(croot, n+".c"))
	}
	args = append(args, filepath.Join(mbed, "library/aes.c"), filepath.Join(mbed, "library/aesni.c"), "-lz", "-o", binaryPath)
	if output, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile SDK: %v: %s", err, output)
	}
	cases := []struct{ rows, interval int }{{3, 0}, {6, 3}, {64, 1}, {64, 3}, {64, 0}}
	if os.Getenv("LOGAN_SDK_LARGE_REGRESSION") == "true" {
		cases = append(cases, struct{ rows, interval int }{8192, 0})
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("rows%d_flush%d", tc.rows, tc.interval), func(t *testing.T) {
			dir := t.TempDir()
			if output, err := exec.Command(binaryPath, dir, fmt.Sprint(tc.rows), fmt.Sprint(tc.interval)).CombinedOutput(); err != nil {
				t.Fatalf("SDK: %v %s", err, output)
			}
			wire, err := os.ReadFile(filepath.Join(dir, "native.bin"))
			if err != nil {
				t.Fatal(err)
			}
			for pos := 0; pos < len(wire); {
				if len(wire)-pos < 6 || wire[pos] != 1 {
					t.Fatalf("incomplete SDK frame at %d, remaining=%x", pos, wire[pos:])
				}
				n := int(binary.BigEndian.Uint32(wire[pos+1 : pos+5]))
				if n <= 0 || n%16 != 0 || n > len(wire)-pos-6 || wire[pos+5+n] != 0 {
					t.Fatalf("invalid SDK frame at %d length=%d", pos, n)
				}
				pos += n + 6
			}
			decoder, err := protocol.NewNativeDecoder([]byte("0123456789012345"), []byte("0123456789012345"), protocol.Limits{MaxInput: 32 << 20, MaxFrame: 32 << 20})
			if err != nil {
				t.Fatal(err)
			}
			var compressed bytes.Buffer
			if err := decoder.Decode(context.Background(), bytes.NewReader(wire), &compressed); err != nil {
				t.Fatal(err)
			}
			plain, err := os.CreateTemp(t.TempDir(), "plain")
			if err != nil {
				t.Fatal(err)
			}
			defer plain.Close()
			if err := protocol.DecompressStrict(context.Background(), &compressed, plain, 64<<20); err != nil {
				t.Fatal(err)
			}
			if _, err := plain.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			count := 0
			if err := protocol.ParseNativeRecords(context.Background(), plain, 8192, tc.rows+1, func(r protocol.NativeRecord) error {
				if count == 0 {
					if r.Content == nil || *r.Content != "clogan header" {
						return fmt.Errorf("missing SDK header")
					}
				} else if r.Content == nil || len(*r.Content) != 4096 || !strings.HasPrefix(*r.Content, fmt.Sprintf("P26|large32|%08d|", count-1)) {
					return fmt.Errorf("missing/out-of-order SDK record %d", count-1)
				}
				count++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if count != tc.rows+1 {
				t.Fatalf("records=%d want %d", count, tc.rows+1)
			}
		})
	}
}
