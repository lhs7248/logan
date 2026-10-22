package workbench

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"time"
)

func keyProblem(key string) *apiError {
	if key == "" {
		return fail(400, "IDEMPOTENCY_REQUIRED", "A UUIDv7 Idempotency-Key is required")
	}
	if len(key) != 36 || key[8] != '-' || key[13] != '-' || key[18] != '-' || key[23] != '-' {
		return fail(400, "INVALID_INPUT", "Idempotency-Key must be a UUIDv7")
	}
	b, err := hex.DecodeString(strings.ReplaceAll(key, "-", ""))
	if err != nil || len(b) != 16 || b[6]>>4 != 7 || b[8]>>6 != 2 {
		return fail(400, "INVALID_INPUT", "Idempotency-Key must be a UUIDv7")
	}
	var millis int64
	for _, v := range b[:6] {
		millis = (millis << 8) | int64(v)
	}
	at := time.UnixMilli(millis)
	if at.Before(time.Now().Add(-7 * 24 * time.Hour)) {
		return fail(409, "IDEMPOTENCY_EXPIRED", "This attempt key expired; recover its business object before starting a new attempt")
	}
	if at.After(time.Now().Add(5 * time.Minute)) {
		return fail(400, "INVALID_INPUT", "Attempt key is more than five minutes in the future")
	}
	return nil
}
func validKey(key string) bool { return keyProblem(key) == nil }

func normalizedBody(contentType string, body []byte) []byte {
	if strings.HasPrefix(contentType, "multipart/") {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return body
		}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		parts := []Record{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return body
			}
			value, err := io.ReadAll(part)
			if err != nil {
				return body
			}
			if part.FormName() == "metadata" {
				value = normalizedBody("application/json", value)
			}
			parts = append(parts, Record{"field": part.FormName(), "filename": part.FileName(), "hash": digest(value)})
		}
		b, _ := json.Marshal(parts)
		return b
	}
	var value any
	if json.Unmarshal(body, &value) == nil {
		b, _ := json.Marshal(value)
		return b
	}
	return body
}
