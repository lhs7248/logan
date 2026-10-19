package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"logan/servergo/internal/config"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"os"
	"time"
)

var ErrDecrypt = errors.New("ERROR_DECRYPT")
var ErrDatabase = errors.New("ERROR_DATABASE")
var ErrException = errors.New("EXCEPTION")
var ErrTask = errors.New("param illegal")

type TaskRepository interface {
	Create(context.Context, *repository.NativeTask) error
	Find(context.Context, int64) (*repository.NativeTask, error)
}
type Service struct {
	cfg      config.Native
	decoder  *protocol.NativeDecoder
	store    storage.Store
	tasks    TaskRepository
	slots    chan struct{}
	location *time.Location
}

func NewService(c config.Native, timezone string, store storage.Store, tasks TaskRepository) (*Service, error) {
	decoder, err := protocol.NewNativeDecoder(c.Key, c.IV, protocol.Limits{MaxInput: c.MaxInput, MaxFrame: c.MaxFrame})
	if err != nil {
		return nil, err
	}
	if c.Concurrency < 1 || c.Timeout <= 0 || store == nil || tasks == nil {
		return nil, fmt.Errorf("invalid native service dependencies or budgets")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid native timezone")
	}
	return &Service{cfg: c, decoder: decoder, store: store, tasks: tasks, slots: make(chan struct{}, c.Concurrency), location: location}, nil
}
func (s *Service) Upload(ctx context.Context, headers map[string]string, length int64, body io.Reader) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	if length > s.cfg.MaxInput {
		return "", ErrDecrypt
	}
	if closer, ok := body.(io.ReadCloser); ok {
		done := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { _ = closer.Close(); close(done) })
		defer func() {
			if !stop() {
				<-done
			}
		}()
	}

	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return "", ErrDecrypt
	}
	id, err := newUUID()
	if err != nil {
		return "", ErrException
	}
	task := NewTask(headers, length, time.Now(), s.location, id)
	file, err := os.CreateTemp(s.cfg.TempDir, "logan-native-*.gz")
	if err != nil {
		return "", ErrDecrypt
	}
	defer func() {
		_ = file.Close()
		if err := os.Remove(file.Name()); err != nil {
			slog.Warn("native temporary file cleanup failed")
		}
	}()
	if err := s.decoder.Decode(ctx, body, file); err != nil {
		return "", ErrDecrypt
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", ErrDecrypt
	}
	if _, err := file.Seek(0, 0); err != nil {
		return "", ErrDecrypt
	}
	if err := s.store.Put(ctx, *task.LogFileName, file, size); err != nil {
		if errors.Is(err, storage.ErrRegistration) {
			return "", ErrDatabase
		}
		return "", ErrDecrypt
	}
	if err := s.tasks.Create(ctx, task); err != nil || task.ID <= 0 {
		// A commit may succeed even if its acknowledgement is lost. Deleting this
		// object could break a committed task. Retain it for later orphan reconciliation.
		slog.Error("native task persistence failed", "object_retained", true)
		return "", ErrDatabase
	}
	return downloadURL(task.LogFileName), nil
}
func downloadURL(name *string) string {
	if name == nil || *name == "" {
		return ""
	}
	return "/logan/downing?name=" + *name
}
func (s *Service) DownloadURL(ctx context.Context, id int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	task, err := s.tasks.Find(ctx, id)
	if err != nil || task == nil {
		return "", ErrTask
	}
	return downloadURL(task.LogFileName), nil
}
func (s *Service) Sign(ctx context.Context, name string) (string, error) {
	return s.store.Sign(ctx, name)
}
