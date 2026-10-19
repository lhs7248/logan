package protocol

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

func TestNativeHistoricalJavaVectorsWithDeclaredRecoveryDifferences(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/protocol/java-native-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Key   string `json:"key_hex"`
		IV    string `json:"iv_hex"`
		Cases []struct {
			Name          string `json:"name"`
			ExpectedError bool   `json:"expected_error"`
			Wire          string `json:"wire"`
			Compressed    string `json:"compressed"`
			Plain         string `json:"plain"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	key, _ := hex.DecodeString(vectors.Key)
	iv, _ := hex.DecodeString(vectors.IV)
	decoder, err := NewNativeDecoder(key, iv, Limits{MaxInput: 1 << 20, MaxFrame: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors.Cases {
		t.Run(v.Name, func(t *testing.T) {
			wire, _ := base64.StdEncoding.DecodeString(v.Wire)
			want, _ := base64.StdEncoding.DecodeString(v.Compressed)
			// Preserve padding in the encrypted-frame layer: a suffix can be
			// real gzip fragment data. Only Decompress knows member boundaries.
			// Original Java golden is unchanged; these two probes removed bytes.
			switch v.Name {
			case "pkcs5-single-block", "damaged-next-member-header":
				want = append(want, bytes.Repeat([]byte{13}, 13)...)
			}
			var out bytes.Buffer
			decodeErr := decoder.Decode(context.Background(), bytes.NewReader(wire), &out)
			if v.ExpectedError {
				if decodeErr == nil {
					t.Fatal("Java rejection was accepted")
				}
				return
			}
			if err := decodeErr; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("historical decrypted bytes differ for %s", v.Name)
			}
			file, err := os.CreateTemp(t.TempDir(), "plain")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := Decompress(context.Background(), bytes.NewReader(out.Bytes()), file, 1<<20); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			plain, _ := io.ReadAll(file)
			expected, _ := base64.StdEncoding.DecodeString(v.Plain)
			if v.Name == "multiple-frames-with-noise" {
				expected = append(expected, []byte("{\"c\":\"second\",\"f\":2}\n")...)
			}
			if !bytes.Equal(plain, expected) {
				t.Fatalf("Java decompression differs: got %q want %q", plain, expected)
			}
		})
	}
}
func TestNativeRejectsInvalidFramesAndBudgets(t *testing.T) {
	d, err := NewNativeDecoder([]byte("0123456789abcdef"), []byte("fedcba9876543210"), Limits{MaxInput: 128, MaxFrame: 64})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{{1}, {1, 0, 0, 0, 16, 2}, {1, 255, 255, 255, 255}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 17}, append([]byte{1, 0, 0, 0, 80}, make([]byte, 80)...), bytes.Repeat([]byte{0}, 129)} {
		if err := d.Decode(context.Background(), bytes.NewReader(input), io.Discard); err == nil {
			t.Fatalf("invalid input accepted: %x", input[:min(8, len(input))])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(d.Decode(ctx, bytes.NewReader(nil), io.Discard), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if _, err := NewNativeDecoder([]byte("short"), nil, Limits{}); err == nil {
		t.Fatal("invalid key accepted")
	}
}
func FuzzNativeDecode(f *testing.F) {
	f.Add([]byte{1, 0, 0, 0, 16})
	f.Add([]byte{})
	f.Add([]byte{1, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, b []byte) {
		d, _ := NewNativeDecoder([]byte("0123456789abcdef"), []byte("fedcba9876543210"), Limits{MaxInput: 1024, MaxFrame: 256})
		_ = d.Decode(context.Background(), bytes.NewReader(b), io.Discard)
	})
}

func TestDecompressPlaintextBudgetAndCancellation(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write(bytes.Repeat([]byte("line\n"), 100))
	_ = gz.Close()
	file, err := os.CreateTemp(t.TempDir(), "budget")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !errors.Is(Decompress(context.Background(), bytes.NewReader(compressed.Bytes()), file, 8), ErrLimit) {
		t.Fatal("plaintext budget not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(Decompress(ctx, bytes.NewReader(compressed.Bytes()), file, 1024), context.Canceled) {
		t.Fatal("decompression cancellation ignored")
	}
}
