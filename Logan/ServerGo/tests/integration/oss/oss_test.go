//go:build integration

package ossintegration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"logan/servergo/internal/config"
	"logan/servergo/internal/storage"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

// required reports names only: credentials and SDK errors must never enter test output.
func required(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("required environment variable missing: %s", name)
	}
	return v
}

func TestOSSRealIntegration(t *testing.T) {
	if required(t, "LOGAN_IT_ALLOW_WRITES") != "1" {
		t.Fatal("LOGAN_IT_ALLOW_WRITES must be 1")
	}
	if required(t, "LOGAN_IT_OSS_PRIVATE_BUCKET") != "1" {
		t.Fatal("LOGAN_IT_OSS_PRIVATE_BUCKET must be 1 for access-control acceptance")
	}
	mode := required(t, "LOGAN_IT_OSS_MODE")
	if mode != "aliyun" && mode != "compatible" {
		t.Fatal("LOGAN_IT_OSS_MODE must be aliyun or compatible")
	}
	endpoint := required(t, "LOGAN_IT_OSS_ENDPOINT")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		t.Fatal("invalid OSS endpoint")
	}
	if mode == "aliyun" && (u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".aliyuncs.com")) {
		t.Fatal("aliyun mode requires HTTPS endpoint under aliyuncs.com; use compatible mode for custom endpoints")
	}
	prefix := required(t, "LOGAN_IT_OSS_PREFIX")
	if !strings.HasPrefix(prefix, "logan-it/") || !strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\\?#\r\n") {
		t.Fatal("LOGAN_IT_OSS_PREFIX must start with logan-it/, end with /, and contain no traversal or URL delimiters")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatal("random prefix generation failed")
	}
	c := config.OSS{Endpoint: endpoint, Region: required(t, "LOGAN_IT_OSS_REGION"), Bucket: required(t, "LOGAN_IT_OSS_BUCKET"), AccessKeyID: required(t, "LOGAN_IT_OSS_ACCESS_KEY_ID"), AccessKeySecret: required(t, "LOGAN_IT_OSS_ACCESS_KEY_SECRET"), SessionToken: os.Getenv("LOGAN_IT_OSS_SESSION_TOKEN"), Prefix: prefix + hex.EncodeToString(random) + "/", Timeout: 20 * time.Second, PathStyle: mode == "compatible"}
	s, err := storage.NewOSS(c)
	if err != nil {
		t.Fatal("OSS initialization failed")
	}
	defer s.Close()
	t.Logf("configured endpoint mode: %s (compatible mode is not real Aliyun cloud evidence)", mode)
	// Register exact keys before any upload, including uploads whose outcome is uncertain.
	keys := []string{"bytes.bin", "interrupted.bin"}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		for _, key := range keys {
			if err := s.Delete(ctx, key); err != nil {
				t.Error("exact-key cleanup failed; inspect isolated test prefix privately")
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	payload := bytes.Repeat([]byte{0, 1, 0xff, 0xfe, 'L', 'o', 'g', 'a', 'n', '\n'}, 262144)
	if s.Put(ctx, keys[0], bytes.NewReader(payload), int64(len(payload))) != nil {
		t.Fatal("PUT failed")
	}
	body, err := s.Get(ctx, keys[0])
	if err != nil {
		t.Fatal("GET failed")
	}
	got, readErr := io.ReadAll(io.LimitReader(body, int64(len(payload))+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, payload) {
		t.Fatal("GET bytes or body close mismatch")
	}
	signed, err := s.Sign(ctx, keys[0])
	if err != nil {
		t.Fatal("presign failed")
	}
	signedURL := validatedSignedURL(t, signed, u, c.Bucket, c.PathStyle)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL.String(), nil)
	if err != nil {
		t.Fatal("presigned request construction failed")
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		t.Fatal("presigned GET failed")
	}
	got, readErr = io.ReadAll(io.LimitReader(res.Body, int64(len(payload))+1))
	closeErr = res.Body.Close()
	if res.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || !bytes.Equal(got, payload) {
		t.Fatal("presigned GET status, bytes, or close mismatch; redirects are forbidden")
	}
	// Denial assertions require an explicitly provisioned private bucket. A public
	// bucket can legitimately serve an unsigned object and is not security evidence.
	anonymous := *signedURL
	anonymous.RawQuery = ""
	assertDenied(t, ctx, client, anonymous.String(), "anonymous GET")
	tampered := *signedURL
	query := tampered.Query()
	signatureKey := "x-oss-signature"
	if query.Get(signatureKey) == "" {
		signatureKey = "Signature"
	}
	if query.Get(signatureKey) == "" {
		t.Fatal("presign URL has no recognized signature")
	}
	signature := query.Get(signatureKey)
	replacement := "0"
	if signature[0] == '0' {
		replacement = "1"
	}
	query.Set(signatureKey, replacement+signature[1:])
	tampered.RawQuery = query.Encode()
	assertDenied(t, ctx, client, tampered.String(), "tampered signature GET")
	// Exercise expiration without waiting for the production one-hour TTL. This
	// direct SDK helper signs the same exact key with a five-second TTL.
	sdk := oss.NewClient(oss.LoadDefaultConfig().WithRegion(c.Region).WithEndpoint(c.Endpoint).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret, c.SessionToken)).
		WithUsePathStyle(c.PathStyle))
	expired, err := sdk.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(c.Bucket), Key: oss.Ptr(c.Prefix + keys[0])}, oss.PresignExpires(5*time.Second))
	if err != nil {
		t.Fatal("expired URL construction failed")
	}
	expiredURL := validatedSignedURL(t, expired.URL, u, c.Bucket, c.PathStyle)
	// Prove this URL was usable before expiration, so a later rejection cannot
	// accidentally count a malformed signature or incorrect object as TTL evidence.
	shortReq, err := http.NewRequestWithContext(ctx, http.MethodGet, expiredURL.String(), nil)
	if err != nil {
		t.Fatal("short-lived request construction failed")
	}
	shortRes, err := client.Do(shortReq)
	if err != nil {
		t.Fatal("short-lived presigned GET transport failed")
	}
	shortBytes, shortReadErr := io.ReadAll(io.LimitReader(shortRes.Body, int64(len(payload))+1))
	shortCloseErr := shortRes.Body.Close()
	if shortRes.StatusCode != http.StatusOK || shortReadErr != nil || shortCloseErr != nil || !bytes.Equal(shortBytes, payload) {
		t.Fatal("short-lived URL must retrieve exact bytes before expiry")
	}
	timer := time.NewTimer(time.Until(expired.Expiration.Add(2 * time.Second)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("expired URL check exceeded test deadline")
	}
	assertDenied(t, ctx, client, expiredURL.String(), "expired presigned GET")
	getCtx, getCancel := context.WithCancel(ctx)
	body, err = s.Get(getCtx, keys[0])
	if err != nil {
		getCancel()
		t.Fatal("GET before cancellation failed")
	}
	getCancel()
	// A transport may already have buffered bytes; drain boundedly and always close.
	_, _ = io.Copy(io.Discard, io.LimitReader(body, int64(len(payload))+1))
	if body.Close() != nil {
		t.Fatal("canceled GET body close failed")
	}
	if body, err = s.Get(getCtx, keys[0]); err == nil {
		body.Close()
		t.Fatal("pre-canceled GET unexpectedly succeeded")
	}
	putCtx, putCancel := context.WithCancel(ctx)
	interrupted := &cancelReader{Reader: bytes.NewReader(payload), cancel: putCancel}
	err = s.Put(putCtx, keys[1], interrupted, int64(len(payload)))
	putCancel()
	if err == nil || !interrupted.read {
		t.Fatal("interrupted PUT did not observe body read and cancellation")
	}
	if s.Delete(ctx, keys[1]) != nil {
		t.Fatal("interrupted PUT exact-key cleanup failed")
	}
	for i := 0; i < 2; i++ {
		if s.Delete(ctx, keys[0]) != nil {
			t.Fatal("idempotent DELETE failed")
		}
	}
	if body, err = s.Get(ctx, keys[0]); err == nil {
		body.Close()
		t.Fatal("missing object GET unexpectedly succeeded")
	}
}

type cancelReader struct {
	*bytes.Reader
	cancel context.CancelFunc
	read   bool
}

func (r *cancelReader) Read(p []byte) (int, error) {
	r.read = true
	r.cancel()
	return 0, context.Canceled
}

// Never include a URL or transport error in a failure: query credentials are secret.
func validatedSignedURL(t *testing.T, raw string, endpoint *url.URL, bucket string, pathStyle bool) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 16384 || u.User != nil || u.Fragment != "" || u.Scheme != endpoint.Scheme {
		t.Fatal("unsafe presigned URL")
	}
	expectedHost := endpoint.Host
	if !pathStyle {
		expectedHost = bucket + "." + endpoint.Host
	}
	if u.Host != expectedHost || u.RawQuery == "" {
		t.Fatal("presigned URL escaped configured endpoint or lacks credentials")
	}
	return u
}

func assertDenied(t *testing.T, ctx context.Context, client *http.Client, target, operation string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(operation + " request construction failed")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(operation + " transport failed")
	}
	// A bounded drain permits connection reuse without storing server error bodies.
	_, readErr := io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024))
	closeErr := res.Body.Close()
	if res.StatusCode != http.StatusForbidden || readErr != nil || closeErr != nil {
		t.Fatal(operation + " must return 403 and close cleanly")
	}
}
