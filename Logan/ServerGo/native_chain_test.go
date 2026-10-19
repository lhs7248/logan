package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"logan/servergo/biz/handler"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeUploadToSDKAndDatabaseThenDownload(t *testing.T) {
	raw, err := os.ReadFile("testdata/protocol/java-native-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Key   string                              `json:"key_hex"`
		IV    string                              `json:"iv_hex"`
		Cases []struct{ Wire, Compressed string } `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	wire, _ := base64.StdEncoding.DecodeString(vectors.Cases[0].Wire)
	want, _ := base64.StdEncoding.DecodeString(vectors.Cases[0].Compressed)
	var mu sync.Mutex
	key := ""
	puts := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("unexpected OSS network call %s", r.Method)
			return
		}
		data, _ := io.ReadAll(r.Body)
		if !bytes.Equal(data, want) {
			t.Error("OSS did not receive Java-compatible compressed bytes")
		}
		mu.Lock()
		key = strings.TrimPrefix(r.URL.Path, "/test-bucket/logan-logs/")
		puts++
		mu.Unlock()
		w.Header().Set("ETag", `"test"`)
	}))
	defer cloud.Close()
	store, err := storage.NewOSS(config.OSS{Endpoint: cloud.URL, Region: "cn-hangzhou", Bucket: "test-bucket", Prefix: "logan-logs/", AccessKeyID: "test-id", AccessKeySecret: "test-secret", Timeout: time.Second, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(func(string) string { return "" })
	c.Native = config.Native{Enabled: true, Key: mustHex(vectors.Key), IV: mustHex(vectors.IV), TempDir: t.TempDir(), MaxInput: 1 << 20, MaxFrame: 1 << 20, Concurrency: 2, Timeout: 2 * time.Second}
	service, err := native.NewService(c.Native, c.Timezone, store, repository.NewNativeTasks(db))
	if err != nil {
		t.Fatal(err)
	}
	h := newServer(c, handler.NewNative(service))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `logan_task`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	response := ut.PerformRequest(h.Engine, "POST", "/logan-web/logan/upload.json", &ut.Body{Body: bytes.NewReader(wire), Len: len(wire)}, ut.Header{Key: "appId", Value: "app"}, ut.Header{Key: "deviceId", Value: "device"}, ut.Header{Key: "fileDate", Value: "2026-10-15"})
	var envelope struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Code != 200 || response.Code != 200 {
		t.Fatalf("upload failed: %s", response.Body.String())
	}
	mu.Lock()
	name := key
	mu.Unlock()
	if envelope.Data != "/logan/downing?name="+name || !strings.HasPrefix(name, "app_device_") {
		t.Fatal("legacy upload URL mismatch")
	}
	mock.ExpectQuery("SELECT ").WithArgs(int64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "log_file_name"}).AddRow(7, name))
	response = ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/task/7/download.json", nil)
	var download struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &download)
	if download.Code != 200 || download.Data != envelope.Data {
		t.Fatal("task download URL mismatch")
	}
	response = ut.PerformRequest(h.Engine, "GET", "/logan-web/logan/downing?name="+url.QueryEscape(name), nil)
	signed, err := url.Parse(string(response.Header().Peek("Location")))
	if err != nil || response.Code != 302 || !strings.HasSuffix(signed.Path, name) || signed.Query().Get("x-oss-signature") == "" {
		t.Fatal("signed redirect failed")
	}
	response = ut.PerformRequest(h.Engine, "POST", "/logan-web/logan/upload.json", &ut.Body{Body: bytes.NewReader([]byte{1}), Len: 1})
	if !strings.Contains(response.Body.String(), "ERROR_DECRYPT") {
		t.Fatal("bad protocol accepted")
	}
	mu.Lock()
	if puts != 1 {
		t.Errorf("invalid protocol uploaded %d objects", puts)
	}
	mu.Unlock()
	entries, _ := os.ReadDir(c.Native.TempDir)
	if len(entries) != 0 {
		t.Fatal("temporary files leaked")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
