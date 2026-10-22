package connectors

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"logan/servergo/internal/protocol"
)

type ProtocolConfig struct {
	NativeKey, NativeIV, WebPrivateKey []byte
	EnableWeb                          bool
	MaxInputBytes, MaxPlainBytes       int64
	MaxLineBytes, MaxRecords           int
	TempDir                            string
}
type Protocols struct {
	config       ProtocolConfig
	native       *protocol.NativeDecoder
	web          *protocol.WebDecoder
	webEncrypted bool
}
type ProtocolEvent struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
	LogType int    `json:"logType"`
	LogTime int64  `json:"logTime"`
}

func NewProtocols(c ProtocolConfig) (*Protocols, error) {
	if c.MaxInputBytes <= 0 || c.MaxInputBytes > 64<<20 {
		c.MaxInputBytes = 32 << 20
	}
	if c.MaxPlainBytes <= 0 || c.MaxPlainBytes > 128<<20 {
		c.MaxPlainBytes = 64 << 20
	}
	if c.MaxLineBytes <= 0 || c.MaxLineBytes > 4<<20 {
		c.MaxLineBytes = 1 << 20
	}
	if c.MaxRecords <= 0 || c.MaxRecords > 100000 {
		c.MaxRecords = 100000
	}
	p := &Protocols{config: c}
	if len(c.NativeKey) > 0 || len(c.NativeIV) > 0 {
		var err error
		p.native, err = protocol.NewNativeDecoder(c.NativeKey, c.NativeIV, protocol.Limits{MaxInput: c.MaxInputBytes, MaxFrame: c.MaxInputBytes})
		if err != nil {
			return nil, ErrInvalidRequest
		}
	}
	if len(c.WebPrivateKey) > 0 {
		der := c.WebPrivateKey
		if block, _ := pem.Decode(der); block != nil {
			der = block.Bytes
		}
		if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
			if _, ok := key.(*rsa.PrivateKey); !ok {
				return nil, ErrInvalidRequest
			}
		} else if _, err := x509.ParsePKCS1PrivateKey(der); err != nil {
			return nil, ErrInvalidRequest
		}
		p.webEncrypted = true
	}
	if c.EnableWeb || p.webEncrypted {
		p.web = protocol.NewWebDecoder(c.WebPrivateKey, c.MaxPlainBytes)
	}
	return p, nil
}
func (p *Protocols) NativeReady() bool       { return p != nil && p.native != nil }
func (p *Protocols) WebReady() bool          { return p != nil && p.web != nil }
func (p *Protocols) WebEncryptedReady() bool { return p != nil && p.webEncrypted }
func protocolError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, protocol.ErrLimit) || errors.Is(err, ErrLimitExceeded) {
		return ErrLimitExceeded
	}
	return ErrInvalidRequest
}
func (p *Protocols) Decode(ctx context.Context, format string, raw []byte) ([]ProtocolEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch format {
	case "native":
		if !p.NativeReady() {
			return nil, ErrUnconfigured
		}
	case "web":
		if !p.WebReady() {
			return nil, ErrUnconfigured
		}
	default:
		return nil, ErrInvalidRequest
	}
	if int64(len(raw)) > p.config.MaxInputBytes {
		return nil, ErrLimitExceeded
	}
	if len(raw) == 0 {
		return nil, ErrInvalidRequest
	}
	if format == "native" {
		return p.decodeNative(ctx, raw)
	}
	return p.decodeWeb(ctx, raw)
}
func (p *Protocols) decodeNative(ctx context.Context, raw []byte) ([]ProtocolEvent, error) {
	// The compatible decoder tolerates noise; immutable imports require complete
	// contiguous frames, including every SDK trailer, before any records publish.
	for at := 0; at < len(raw); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(raw)-at < 6 || raw[at] != 1 {
			return nil, ErrInvalidRequest
		}
		n := int64(binary.BigEndian.Uint32(raw[at+1 : at+5]))
		if n <= 0 || n%aes.BlockSize != 0 || n > int64(len(raw)-at-6) {
			return nil, ErrInvalidRequest
		}
		at += 5 + int(n)
		if raw[at] != 0 {
			return nil, ErrInvalidRequest
		}
		at++
	}
	compressed := &boundedBuffer{max: p.config.MaxInputBytes}
	if err := p.native.Decode(ctx, bytes.NewReader(raw), compressed); err != nil {
		return nil, protocolError(ctx, err)
	}
	plain, err := os.CreateTemp(p.config.TempDir, "logan-workbench-decode-*")
	if err != nil {
		return nil, ErrUnavailable
	}
	defer os.Remove(plain.Name())
	defer plain.Close()
	if err = protocol.DecompressStrict(ctx, bytes.NewReader(compressed.buffer.Bytes()), plain, p.config.MaxPlainBytes); err != nil {
		return nil, protocolError(ctx, err)
	}
	if _, err = plain.Seek(0, 0); err != nil {
		return nil, ErrUnavailable
	}
	plaintext, err := io.ReadAll(io.LimitReader(plain, p.config.MaxPlainBytes+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(plaintext)) > p.config.MaxPlainBytes {
		return nil, ErrLimitExceeded
	}
	if !utf8.Valid(plaintext) {
		return nil, ErrInvalidRequest
	}
	events := []ProtocolEvent{}
	err = protocol.ParseNativeRecords(ctx, bytes.NewReader(plaintext), p.config.MaxLineBytes, p.config.MaxRecords, func(record protocol.NativeRecord) error {
		if record.Content == nil || !utf8.ValidString(*record.Content) {
			return ErrInvalidRequest
		}
		events = append(events, ProtocolEvent{Line: len(events) + 1, Content: *record.Content, LogType: record.LogType, LogTime: record.LogTime})
		return nil
	})
	if err != nil {
		return nil, protocolError(ctx, err)
	}
	if len(events) == 0 {
		return nil, ErrInvalidRequest
	}
	return events, nil
}
func strictBase64(s string) ([]byte, error) {
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return nil, ErrInvalidRequest
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict(), base64.URLEncoding.Strict(), base64.RawURLEncoding.Strict()} {
		if b, err := encoding.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, ErrInvalidRequest
}
func (p *Protocols) decodeWeb(ctx context.Context, raw []byte) ([]ProtocolEvent, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		var report struct {
			LogArray *string `json:"logArray"`
		}
		if json.Unmarshal(trimmed, &report) != nil || report.LogArray == nil {
			return nil, ErrInvalidRequest
		}
		raw = []byte(*report.LogArray)
	}
	items := strings.Split(string(raw), ",")
	if len(items) > p.config.MaxRecords {
		return nil, ErrLimitExceeded
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		decoded, err := url.QueryUnescape(item)
		if err != nil || !utf8.ValidString(decoded) {
			return nil, ErrInvalidRequest
		}
		var envelope struct {
			V  int    `json:"v"`
			L  string `json:"l"`
			K  string `json:"k"`
			IV string `json:"iv"`
		}
		if json.Unmarshal([]byte(decoded), &envelope) != nil {
			return nil, ErrInvalidRequest
		}
		payload, err := strictBase64(envelope.L)
		if err != nil {
			return nil, err
		}
		switch envelope.V {
		case 0:
			if !utf8.Valid(payload) {
				return nil, ErrInvalidRequest
			}
		case 1:
			if !p.webEncrypted {
				return nil, ErrUnconfigured
			}
			if len(envelope.IV) != aes.BlockSize {
				return nil, ErrInvalidRequest
			}
			if _, err := strictBase64(envelope.K); err != nil {
				return nil, err
			}
		default:
			return nil, ErrInvalidRequest
		}
	}
	content, err := p.web.Parse(ctx, string(raw))
	if err != nil {
		return nil, protocolError(ctx, err)
	}
	var details []string
	if json.Unmarshal([]byte(content), &details) != nil || len(details) != len(items) {
		return nil, ErrInvalidRequest
	}
	for _, detail := range details {
		if len(detail) > p.config.MaxLineBytes {
			return nil, ErrLimitExceeded
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(detail), &fields) != nil || fields == nil {
			return nil, ErrInvalidRequest
		}
		values := map[string]string{}
		for _, field := range []string{"t", "c", "d"} {
			v, ok := fields[field]
			if !ok || string(v) == "null" {
				return nil, ErrInvalidRequest
			}
			var value string
			if json.Unmarshal(v, &value) != nil {
				value = string(v)
			}
			if value == "" {
				return nil, ErrInvalidRequest
			}
			values[field] = value
		}
		if _, err := strconv.ParseInt(values["t"], 10, 32); err != nil {
			return nil, ErrInvalidRequest
		}
		if _, err := strconv.ParseInt(values["d"], 10, 64); err != nil {
			return nil, ErrInvalidRequest
		}
		if decoded, err := url.QueryUnescape(values["c"]); err != nil || !utf8.ValidString(decoded) {
			return nil, ErrInvalidRequest
		}
	}
	parsed, err := p.web.ParseDetails(ctx, content, 0, time.UTC)
	if err != nil {
		return nil, protocolError(ctx, err)
	}
	if len(parsed) != len(items) {
		return nil, ErrInvalidRequest
	}
	events := make([]ProtocolEvent, len(parsed))
	for i, event := range parsed {
		events[i] = ProtocolEvent{Line: i + 1, Content: event.Content, LogType: event.LogType, LogTime: event.LogTime}
	}
	return events, nil
}
