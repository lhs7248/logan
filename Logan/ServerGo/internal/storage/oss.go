// Package storage stores the existing decrypted compressed bytes under unchanged OSS keys.
package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"io"
	"logan/servergo/internal/config"
	"net/http"
	"time"
)

// ErrRegistration identifies durable upload metadata failures without exposing DB details.
var ErrRegistration = errors.New("object registration failed")

type Store interface {
	Put(context.Context, string, io.ReadSeeker, int64) error
	Get(context.Context, string) (io.ReadCloser, error)
	Sign(context.Context, string) (string, error)
	Delete(context.Context, string) error
}
type OSS struct {
	client    *oss.Client
	config    config.OSS
	transport *http.Transport
}

func NewOSS(c config.OSS) (*OSS, error) {
	if c.Endpoint == "" || c.Region == "" || c.Bucket == "" || c.AccessKeyID == "" || c.AccessKeySecret == "" || c.Timeout <= 0 {
		return nil, fmt.Errorf("incomplete OSS configuration")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Transport: transport, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sdkConfig := oss.LoadDefaultConfig().WithRegion(c.Region).WithEndpoint(c.Endpoint).WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret, c.SessionToken)).WithHttpClient(client).WithRetryMaxAttempts(2).WithUsePathStyle(c.PathStyle)
	return &OSS{client: oss.NewClient(sdkConfig), config: c, transport: transport}, nil
}
func (s *OSS) Put(ctx context.Context, name string, body io.ReadSeeker, length int64) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	_, err := s.client.PutObject(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.config.Prefix + name), Body: body, ContentLength: oss.Ptr(length), ContentType: oss.Ptr("application/octet-stream")})
	if err != nil {
		return fmt.Errorf("OSS upload failed")
	}
	return nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { err := b.ReadCloser.Close(); b.cancel(); return err }
func (s *OSS) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	result, err := s.client.GetObject(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.config.Prefix + name)})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("OSS download failed")
	}
	return &cancelBody{ReadCloser: result.Body, cancel: cancel}, nil
}
func (s *OSS) Sign(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	result, err := s.client.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.config.Prefix + name)}, oss.PresignExpires(time.Hour))
	if err != nil {
		return "", fmt.Errorf("OSS signing failed")
	}
	return result.URL, nil
}
func (s *OSS) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	_, err := s.client.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(s.config.Bucket), Key: oss.Ptr(s.config.Prefix + name)})
	if err != nil {
		var serviceErr *oss.ServiceError
		if errors.As(err, &serviceErr) && serviceErr.StatusCode == http.StatusNotFound && serviceErr.Code == "NoSuchKey" {
			return nil
		}
		return fmt.Errorf("OSS delete failed")
	}
	return nil
}
func (s *OSS) Close() { s.transport.CloseIdleConnections() }
