//go:build crossruntime

// Package crossruntime rehearses original Java -> production Go data services ->
// original Java against one disposable MySQL database. It is not an HTTP or OSS test.
package crossruntime

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata"

	mysqldriver "github.com/go-sql-driver/mysql"
	"logan/servergo/internal/config"
	"logan/servergo/internal/native"
	"logan/servergo/internal/protocol"
	"logan/servergo/internal/repository"
	"logan/servergo/internal/weblog"
)

type localFixtureReader string

func (d localFixtureReader) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if filepath.Base(key) != key {
		return nil, os.ErrPermission
	}
	return os.Open(filepath.Join(string(d), key))
}

type seedManifest struct {
	NativePending         int64  `json:"nativePending"`
	NativeCompleted       int64  `json:"nativeCompleted"`
	NativeCompletedDetail int64  `json:"nativeCompletedDetail"`
	WebPending            int64  `json:"webPending"`
	WebCompleted          int64  `json:"webCompleted"`
	WebCompletedDetail    int64  `json:"webCompletedDetail"`
	Payload               string `json:"payload"`
	Time                  int64  `json:"time"
 _ "time/tzdata"`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestOriginalJavaGoJavaDataRoundTrip(t *testing.T) {
	if os.Getenv("CROSSRUNTIME_DISPOSABLE") != "YES" {
		t.Skip("requires explicitly acknowledged disposable database")
	}
	dsn := os.Getenv("CROSSRUNTIME_DSN")
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid rehearsal DSN")
	}
	if !regexp.MustCompile(`^logan_crossruntime_[a-z0-9]+$`).MatchString(parsed.DBName) {
		t.Fatal("only random logan_crossruntime_ database permitted")
	}
	dir := os.Getenv("CROSSRUNTIME_DIR")
	if dir == "" {
		t.Fatal("CROSSRUNTIME_DIR required")
	}
	b, err := os.ReadFile(filepath.Join(dir, "java-seed.json"))
	must(t, err)
	var seed seedManifest
	must(t, json.Unmarshal(b, &seed))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, pool, err := repository.Open(ctx, config.Database{Enabled: true, DSN: dsn, MaxOpen: 8, MaxIdle: 4, MaxLifetime: time.Minute, PingTimeout: 5 * time.Second})
	must(t, err)
	defer pool.Close()
	nr := repository.NewNativeQueries(db, 8<<20)
	must(t, nr.Validate(ctx))
	wr := repository.NewWebRepository(db, 8<<20)
	must(t, wr.Validate(ctx))
	temp := t.TempDir()
	ns, err := native.NewQueryService(native.QueryConfig{MaxCompressed: 8 << 20, MaxPlain: 8 << 20, MaxStaged: 8 << 20, MaxLine: 1 << 20, MaxRecords: 100, MaxIDs: 100, Workers: 1, Timeout: 10 * time.Second, Wait: 10 * time.Second, TempDir: temp}, localFixtureReader(dir), nr)
	must(t, err)
	ws, err := weblog.NewService(weblog.Config{MaxBytes: 8 << 20, MaxTasks: 100, Workers: 1, Timeout: 10 * time.Second, Wait: 10 * time.Second, Timezone: "Asia/Shanghai"}, protocol.NewWebDecoder(nil, 8<<20), wr)
	must(t, err)
	ns.Start(ctx)
	ws.Start(ctx)
	defer ns.Close()
	defer ws.Close()
	// Existing completed details retain both the exact Java IDs and UTF-8 payloads.
	nd, err := ns.Detail(ctx, seed.NativeCompletedDetail)
	must(t, err)
	if nd == nil || nd.Content == nil || *nd.Content != seed.Payload || nd.ID != seed.NativeCompletedDetail {
		t.Fatalf("Java native detail changed: %#v", nd)
	}
	wd, err := ws.Detail(ctx, seed.WebCompletedDetail)
	must(t, err)
	if wd == nil || wd.Content == nil || *wd.Content != seed.Payload || wd.ID != seed.WebCompletedDetail {
		t.Fatalf("Java web detail changed: %#v", wd)
	}
	nb, err := ns.Brief(ctx, seed.NativePending, []int{1}, &seed.Payload)
	must(t, err)
	if len(nb) != 1 || len(nb[0]) != 1 {
		t.Fatalf("pending Java native not published: %#v", nb)
	}
	wi, err := ws.Index(ctx, []int64{seed.WebPending}, []int{1}, seed.Payload, 0, 1440)
	must(t, err)
	if len(wi) != 1 || len(wi[0]) != 1 {
		t.Fatalf("pending Java web not published: %#v", wi)
	}
	javaNative, err := ns.Detail(ctx, nb[0][0].ID)
	must(t, err)
	javaWeb, err := ws.Detail(ctx, wi[0][0].ID)
	must(t, err)
	if javaNative.Content == nil || *javaNative.Content != seed.Payload || javaWeb.Content == nil || *javaWeb.Content != seed.Payload {
		t.Fatal("Java pending payload changed during Go processing")
	}
	// NativeTasks is the actual production metadata repository. Object bytes here
	// are an explicit fixture; no real OSS request or encryption upload is claimed.
	payload := "Go generated 中文 + percent% emoji 🐱"
	key := "go-produced.gz"
	file, err := os.Create(filepath.Join(dir, key))
	must(t, err)
	gz := gzip.NewWriter(file)
	line, err := json.Marshal(map[string]any{"c": payload, "f": 1, "l": seed.Time + 120000, "n": "go-fixture", "i": 1, "m": true})
	must(t, err)
	_, err = gz.Write(append(line, '\n'))
	must(t, err)
	must(t, gz.Close())
	must(t, file.Close())
	nativeDevice := "go-native-produced"
	nt := repository.NativeTask{Platform: 1, DeviceID: &nativeDevice, LogFileName: &key, LogDate: &seed.Time, AddTime: time.Now().UnixMilli()}
	must(t, repository.NewNativeTasks(db).Create(ctx, &nt))
	newNative, err := ns.Brief(ctx, nt.ID, []int{1}, &payload)
	must(t, err)
	if len(newNative) != 1 || len(newNative[0]) != 1 {
		t.Fatal("Go native publication missing")
	}
	webDevice := "go-web-produced"
	webPayload := "Go generated 中文 + percent%"
	rawLog, err := json.Marshal(map[string]string{"t": "1", "c": url.QueryEscape(webPayload), "d": strconv.FormatInt(seed.Time+120000, 10)})
	must(t, err)
	envelope, err := json.Marshal(map[string]any{"v": 0, "l": base64.StdEncoding.EncodeToString(rawLog)})
	must(t, err)
	must(t, ws.Upload(ctx, weblog.Upload{DeviceID: webDevice, LogPageNo: 1, FileDate: "2026-10-15", LogArray: url.QueryEscape(string(envelope))}))
	rows, err := wr.Search(ctx, 0, time.Now().Add(time.Hour).UnixMilli(), webDevice)
	must(t, err)
	if len(rows) != 1 {
		t.Fatalf("Go web task missing: %d", len(rows))
	}
	newWeb, err := ws.Index(ctx, []int64{rows[0].ID}, []int{1}, webPayload, 0, 1440)
	must(t, err)
	if len(newWeb) != 1 || len(newWeb[0]) != 1 {
		t.Fatal("Go web publication missing")
	}
	output := map[string]any{"nativeTask": nt.ID, "nativeDetail": newNative[0][0].ID, "nativeDevice": nativeDevice, "nativePayload": payload, "webTask": rows[0].ID, "webDetail": newWeb[0][0].ID, "webDevice": webDevice, "webPayload": webPayload, "evidence": "Production Go repositories and native.QueryService/weblog.Service; local gzip fixtures, no real OSS or HTTP server"}
	b, err = json.MarshalIndent(output, "", "  ")
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "go-produced.json"), b, 0600))
	t.Log("GO_DATA_ROUNDTRIP_VERIFIED: Java IDs and payloads preserved; pending native/web published by Go; Go task/detail IDs handed to original Java recheck")
}
