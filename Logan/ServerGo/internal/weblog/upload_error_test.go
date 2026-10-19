package weblog

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	driver "github.com/go-sql-driver/mysql"
	"log/slog"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"net/url"
	"strings"
	"testing"
	"time"
)

type saveErrorRepository struct{ Repository }

func (saveErrorRepository) Save(context.Context, *repository.WebTask) error {
	return &driver.MySQLError{Number: 1213, Message: "private-device private-SQL private-secret"}
}
func TestUploadPersistenceErrorRemainsCompatibleAndSanitized(t *testing.T) {
	r := &memoryRepo{}
	s, err := NewService(Config{MaxBytes: 1 << 20, MaxTasks: 100, Workers: 1, Timeout: time.Second, Wait: time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 1<<20), saveErrorRepository{r})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(before)
	line := `{"t":"1","c":"hello","d":"1609459200000"}`
	raw := url.QueryEscape(fmt.Sprintf(`{"l":%q}`, base64.StdEncoding.EncodeToString([]byte(line))))
	if err = s.Upload(context.Background(), Upload{DeviceID: "private-device", LogPageNo: 1, FileDate: "2021-01-01", LogArray: raw}); err != ErrSave {
		t.Fatalf("legacy error changed: %v", err)
	}
	if !strings.Contains(buf.String(), "mysql_1213") || strings.Contains(buf.String(), "private-") {
		t.Fatal("missing safe class or leaked details", buf.String())
	}
}
