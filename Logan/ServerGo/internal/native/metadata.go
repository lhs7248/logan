package native

import (
	"crypto/rand"
	"fmt"
	"logan/servergo/internal/repository"
	"regexp"
	"strconv"
	"time"
)

var javaDate = regexp.MustCompile(`^\s*([0-9]+)-([0-9]+)-([0-9]+)`)

func stringHeader(headers map[string]string, key string) *string {
	v, ok := headers[key]
	if !ok {
		return nil
	}
	return &v
}
func javaString(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}
func parseDate(value string, location *time.Location) int64 {
	m := javaDate.FindStringSubmatch(value)
	if m == nil {
		return 0
	}
	nums := [3]int{}
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseInt(m[i+1], 10, 32)
		if err != nil {
			return 0
		}
		nums[i] = int(n)
	}
	return time.Date(nums[0], time.Month(nums[1]), nums[2], 0, 0, 0, 0, location).UnixMilli()
}
func NewTask(headers map[string]string, length int64, now time.Time, location *time.Location, uuid string) *repository.NativeTask {
	platform, err := strconv.ParseInt(headers["platform"], 10, 32)
	if err != nil {
		platform = 0
	}
	if platform < -1 || platform > 2 {
		platform = -1
	}
	appID, deviceID := stringHeader(headers, "appid"), stringHeader(headers, "deviceid")
	date := parseDate(headers["filedate"], location)
	amount := strconv.FormatInt(length, 10)
	name := fmt.Sprintf("%s_%s_%d_%s.log", javaString(appID), javaString(deviceID), date, uuid)
	return &repository.NativeTask{Amount: &amount, AppID: appID, UnionID: stringHeader(headers, "unionid"), Platform: int(platform), BuildVersion: stringHeader(headers, "buildversion"), AppVersion: stringHeader(headers, "appversion"), DeviceID: deviceID, LogDate: &date, LogFileName: &name, AddTime: now.UnixMilli(), Status: 0}
}
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
