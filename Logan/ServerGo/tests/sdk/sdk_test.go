package sdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"logan/servergo/internal/protocol"
)

// This opt-in test consumes actual SDK artifacts; it never manufactures protocol bytes.
func TestActualSDKArtifacts(t *testing.T) {
	dir := os.Getenv("LOGAN_SDK_FIXTURE_DIR")
	if dir == "" {
		t.Skip("set LOGAN_SDK_FIXTURE_DIR to generate.py output; actual SDK fixtures required")
	}
	read := func(name string) []byte {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	var manifest struct {
		Native struct {
			Marker, Key, IV string
			Type            int
			Timestamp       int64
		}
		Web struct {
			Marker string
			Type   int
		}
	}
	if err := json.Unmarshal(read("manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	t.Run("native", func(t *testing.T) {
		decoder, err := protocol.NewNativeDecoder([]byte(manifest.Native.Key), []byte(manifest.Native.IV), protocol.Limits{MaxInput: 1 << 20, MaxFrame: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		if err = decoder.Decode(context.Background(), bytes.NewReader(read("native.bin")), &compressed); err != nil {
			t.Fatal(err)
		}
		decoded, err := os.CreateTemp(t.TempDir(), "native-plain-")
		if err != nil {
			t.Fatal(err)
		}
		defer decoded.Close()
		if err = protocol.Decompress(context.Background(), &compressed, decoded, 1<<20); err != nil {
			t.Fatal(err)
		}
		plain, err := os.ReadFile(decoded.Name())
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, line := range bytes.Split(plain, []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var row struct {
				Content string `json:"c"`
				Type    int    `json:"f"`
				Time    int64  `json:"l"`
			}
			if err = json.Unmarshal(line, &row); err != nil {
				t.Fatal(err)
			}
			if row.Content == manifest.Native.Marker {
				found++
				if row.Type != manifest.Native.Type || row.Time != manifest.Native.Timestamp {
					t.Fatalf("native semantic mismatch: %+v", row)
				}
			}
		}
		if found != 1 {
			t.Fatalf("native marker count=%d", found)
		}
	})
	t.Run("web", func(t *testing.T) {
		var err error
		var upload struct {
			LogArray string `json:"logArray"`
		}
		if err = json.Unmarshal(read("web-upload.json"), &upload); err != nil {
			t.Fatal(err)
		}
		private := read("test-rsa-private.pem")
		web := protocol.NewWebDecoder(private, 1<<20)
		content, err := web.Parse(context.Background(), upload.LogArray)
		if err != nil {
			t.Fatal(err)
		}
		details, err := web.ParseDetails(context.Background(), content, 1, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if len(details) != 1 || details[0].Content != manifest.Web.Marker || details[0].LogType != manifest.Web.Type {
			t.Fatalf("web semantic mismatch: %+v", details)
		}
	})
}
