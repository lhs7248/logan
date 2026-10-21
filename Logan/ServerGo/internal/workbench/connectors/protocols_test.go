package connectors

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeWire(t *testing.T, plain []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	z.Write(plain)
	z.Close()
	b := compressed.Bytes()
	pad := 16 - len(b)%16
	b = append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher([]byte("0123456789abcdef"))
	cipher.NewCBCEncrypter(block, []byte("fedcba9876543210")).CryptBlocks(b, b)
	wire := []byte{1, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(wire[1:], uint32(len(b)))
	return append(append(wire, b...), 0)
}
func TestProtocolsStrictNativeAndCleanup(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProtocols(ProtocolConfig{NativeKey: []byte("0123456789abcdef"), NativeIV: []byte("fedcba9876543210"), TempDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	wire := nativeWire(t, []byte("{\"c\":\"observed\",\"f\":2,\"l\":123}\n"))
	events, err := p.Decode(context.Background(), "native", wire)
	if err != nil || len(events) != 1 || events[0].Content != "observed" || events[0].LogTime != 123 {
		t.Fatalf("events %+v: %v", events, err)
	}
	for _, bad := range [][]byte{append([]byte("noise"), wire...), append(append([]byte{}, wire...), 1), wire[:len(wire)-1], nativeWire(t, []byte("{\"c\":\"good\"}\n{broken\n")), nativeWire(t, []byte{'{', '"', 'c', '"', ':', '"', 255, '"', '}'})} {
		if got, err := p.Decode(context.Background(), "native", bad); err == nil || len(got) != 0 {
			t.Fatalf("partial accepted %+v: %v", got, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("temporary plaintext retained")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "protocol", "clogan-regression", "native.bin"))
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := NewProtocols(ProtocolConfig{NativeKey: []byte("0123456789012345"), NativeIV: []byte("0123456789012345"), TempDir: dir})
	if got, err := actual.Decode(context.Background(), "native", raw); err != nil || len(got) == 0 {
		t.Fatalf("CLogan fixture %+v: %v", got, err)
	}
}
func TestProtocolsWebAndUnavailable(t *testing.T) {
	p, _ := NewProtocols(ProtocolConfig{})
	for _, format := range []string{"native", "web"} {
		if _, err := p.Decode(context.Background(), format, []byte("wire")); !errors.Is(err, ErrUnconfigured) {
			t.Fatal(err)
		}
	}
	p, _ = NewProtocols(ProtocolConfig{EnableWeb: true})
	detail := `{"t":2,"c":"hello%20world","d":123}`
	envelope, _ := json.Marshal(map[string]any{"v": 0, "l": base64.StdEncoding.EncodeToString([]byte(detail))})
	wire := url.QueryEscape(string(envelope))
	report, _ := json.Marshal(map[string]any{"logArray": wire, "client": "Web"})
	if got, err := p.Decode(context.Background(), "web", report); err != nil || len(got) != 1 {
		t.Fatalf("SDK report %+v: %v", got, err)
	}
	events, err := p.Decode(context.Background(), "web", []byte(wire))
	if err != nil || len(events) != 1 || events[0].Content != "hello world" {
		t.Fatalf("web %+v: %v", events, err)
	}
	for _, bad := range []string{`%7B%22v%22%3A3%2C%22l%22%3A%22YQ%3D%3D%22%7D`, `%7B%22v%22%3A0%2C%22l%22%3A%22%21%21%21%22%7D`, wire + ",", url.QueryEscape(`{"v":0,"l":"` + base64.StdEncoding.EncodeToString([]byte(`{"t":2,"c":"%FF","d":123}`)) + `"}`), url.QueryEscape(`{"v":0,"l":"` + base64.StdEncoding.EncodeToString([]byte(`{"c":"missing fields"}`)) + `"}`)} {
		if got, err := p.Decode(context.Background(), "web", []byte(bad)); err == nil || len(got) != 0 {
			t.Fatalf("bad web accepted %+v: %v", got, err)
		}
	}
	encrypted := url.QueryEscape(`{"v":1,"iv":"0123456789abcdef","k":"YQ==","l":"YQ=="}`)
	if _, err := p.Decode(context.Background(), "web", []byte(encrypted)); !errors.Is(err, ErrUnconfigured) {
		t.Fatalf("encrypted no key: %v", err)
	}
}
func TestProtocolsLimitsAndCancellation(t *testing.T) {
	p, _ := NewProtocols(ProtocolConfig{EnableWeb: true, MaxInputBytes: 2})
	if _, err := p.Decode(context.Background(), "web", []byte("large")); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal(err)
	}
	p, _ = NewProtocols(ProtocolConfig{NativeKey: []byte("0123456789abcdef"), NativeIV: []byte("fedcba9876543210"), MaxPlainBytes: 2})
	if got, err := p.Decode(context.Background(), "native", nativeWire(t, []byte("{\"c\":\"large\"}"))); !errors.Is(err, ErrLimitExceeded) || len(got) != 0 {
		t.Fatalf("plain limit %+v: %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Decode(ctx, "native", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewProtocols(ProtocolConfig{NativeKey: []byte("short")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestProtocolsConfiguredEncryptedWeb(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProtocols(ProtocolConfig{WebPrivateKey: x509.MarshalPKCS1PrivateKey(key)})
	if err != nil {
		t.Fatal(err)
	}
	aesKey, iv := []byte("0123456789abcdef"), "fedcba9876543210"
	plain := []byte(`{"t":2,"c":"encrypted%20observation","d":123}`)
	block, _ := aes.NewCipher(aesKey)
	cipher.NewCTR(block, []byte(iv)).XORKeyStream(plain, plain)
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, aesKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]any{"v": 1, "iv": iv, "k": base64.StdEncoding.EncodeToString(wrapped), "l": base64.StdEncoding.EncodeToString(plain)})
	if !p.WebEncryptedReady() {
		t.Fatal("encrypted readiness missing")
	}
	events, err := p.Decode(context.Background(), "web", []byte(url.QueryEscape(string(envelope))))
	if err != nil || len(events) != 1 || events[0].Content != "encrypted observation" {
		t.Fatalf("encrypted %+v: %v", events, err)
	}
	if _, err := NewProtocols(ProtocolConfig{WebPrivateKey: []byte("invalid-secret-key")}); !errors.Is(err, ErrInvalidRequest) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("key configuration %v", err)
	}
}
