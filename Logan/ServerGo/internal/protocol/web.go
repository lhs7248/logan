package protocol

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxWebDetails = 100000
const defaultWebBytes int64 = 32 << 20

type WebDecoder struct {
	key      *rsa.PrivateKey
	keyErr   error
	maxBytes int64
}
type WebDetail struct {
	TaskID       int64  `json:"taskId"`
	LogType      int    `json:"logType"`
	Content      string `json:"content"`
	LogTime      int64  `json:"logTime"`
	MinuteOffset int    `json:"minuteOffset"`
}

// NewWebDecoder accepts a PKCS#8 or PKCS#1 RSA key, in PEM or DER form.
// An empty key permits version 0 uploads only. No embedded private key is used.
func NewWebDecoder(privateKey []byte, maxBytes int64) *WebDecoder {
	if maxBytes <= 0 {
		maxBytes = defaultWebBytes
	}
	d := &WebDecoder{maxBytes: maxBytes}
	if len(privateKey) == 0 {
		return d
	}
	if p, _ := pem.Decode(privateKey); p != nil {
		privateKey = p.Bytes
	}
	if k, e := x509.ParsePKCS8PrivateKey(privateKey); e == nil {
		var ok bool
		d.key, ok = k.(*rsa.PrivateKey)
		if !ok {
			d.keyErr = fmt.Errorf("web key is not RSA")
		}
	} else {
		d.key, d.keyErr = x509.ParsePKCS1PrivateKey(privateKey)
	}
	return d
}

// permissiveBase64 matches Commons Codec: ignore noise, accept URL-safe alphabet,
// missing padding and stop at the first padding character.
func permissiveBase64(s string) []byte {
	var b strings.Builder
	for _, c := range s {
		if c == '=' {
			break
		}
		if c == '-' {
			c = '+'
		}
		if c == '_' {
			c = '/'
		}
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' {
			b.WriteRune(c)
		}
	}
	s = b.String()
	if len(s)%4 == 1 {
		s = s[:len(s)-1]
	}
	out, _ := base64.RawStdEncoding.DecodeString(s)
	return out
}
func webText(v json.RawMessage) string {
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}
func validWebUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\ufffd")
}
func (d *WebDecoder) Parse(ctx context.Context, raw string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if int64(len(raw)) > d.maxBytes {
		return "", ErrLimit
	}
	logs := make([]string, 0)
	var total int64
	trimmed := strings.TrimRight(raw, ",")
	if raw != "" && trimmed == "" {
		return "[]", nil
	}
	items := strings.Split(trimmed, ",")
	if len(items) > maxWebDetails {
		return "", ErrLimit
	}
	for _, item := range items {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		s, e := url.QueryUnescape(item)
		if e != nil {
			return "", e
		}
		var obj map[string]json.RawMessage
		if e = json.Unmarshal([]byte(s), &obj); e != nil || obj == nil {
			return "", fmt.Errorf("invalid web envelope")
		}
		v := 0
		if val, ok := obj["v"]; ok {
			if e = json.Unmarshal(val, &v); e != nil {
				return "", fmt.Errorf("invalid web version")
			}
		}
		var plain []byte
		switch v {
		case 0:
			if l := webText(obj["l"]); l != "" {
				plain = permissiveBase64(l)
			} else {
				continue
			}
		case 1:
			iv, k := webText(obj["iv"]), webText(obj["k"])
			if iv == "" || k == "" {
				continue
			}
			if d.keyErr != nil {
				return "", d.keyErr
			}
			if d.key == nil {
				return "", fmt.Errorf("web RSA private key not configured")
			}
			key, e := rsa.DecryptPKCS1v15(rand.Reader, d.key, permissiveBase64(k))
			if e != nil {
				return "", fmt.Errorf("invalid web wrapped key: %w", e)
			}
			block, e := aes.NewCipher(key)
			if e != nil {
				return "", e
			}
			if len(iv) != aes.BlockSize {
				continue
			}
			plain = permissiveBase64(webText(obj["l"]))
			cipher.NewCTR(block, []byte(iv)).XORKeyStream(plain, plain)
		default:
			continue
		}
		text := validWebUTF8(plain)
		total += int64(len(text))
		if total > d.maxBytes {
			return "", ErrLimit
		}
		logs = append(logs, text)
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	e := encoder.Encode(logs)
	out := bytes.TrimSuffix(output.Bytes(), []byte("\n"))
	if int64(len(out)) > d.maxBytes {
		return "", ErrLimit
	}
	return string(out), e
}

func ParseDetails(ctx context.Context, content string, taskID int64, location *time.Location) ([]WebDetail, error) {
	return NewWebDecoder(nil, defaultWebBytes).ParseDetails(ctx, content, taskID, location)
}
func (d *WebDecoder) ParseDetails(ctx context.Context, content string, taskID int64, location *time.Location) ([]WebDetail, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if int64(len(content)) > d.maxBytes {
		return nil, ErrLimit
	}
	if location == nil {
		var e error
		location, e = time.LoadLocation("Asia/Shanghai")
		if e != nil {
			return nil, e
		}
	}
	var logs []json.RawMessage
	if e := json.Unmarshal([]byte(content), &logs); e != nil {
		return nil, e
	}
	if len(logs) > maxWebDetails {
		return nil, ErrLimit
	}
	result := make([]WebDetail, 0, len(logs))
	for _, raw := range logs {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		s := webText(raw)
		var obj map[string]json.RawMessage
		if e := json.Unmarshal([]byte(s), &obj); e != nil || obj == nil {
			return nil, fmt.Errorf("invalid web detail")
		}
		typ, c, ts := webText(obj["t"]), webText(obj["c"]), webText(obj["d"])
		if typ == "" || c == "" || ts == "" {
			continue
		}
		decoded, e := url.QueryUnescape(c)
		if e != nil {
			return nil, e
		}
		n, e := strconv.ParseInt(typ, 10, 32)
		if e != nil {
			n = 0
		}
		millis, e := strconv.ParseInt(ts, 10, 64)
		if e != nil {
			millis = 0
		}
		date := time.UnixMilli(millis).In(location)
		midnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, location)
		minute := int(date.Sub(midnight) / time.Minute)
		result = append(result, WebDetail{taskID, int(n), decoded, millis, minute})
	}
	return result, nil
}
