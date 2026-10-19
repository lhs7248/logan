package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Native struct {
	Enabled            bool
	Key, IV            []byte
	TempDir            string
	MaxInput, MaxFrame int64
	Concurrency        int
	Timeout            time.Duration
}
type OSS struct {
	Endpoint, Region, Bucket, Prefix           string
	AccessKeyID, AccessKeySecret, SessionToken string
	Timeout                                    time.Duration
	PathStyle                                  bool
}

func loadNative(c *Config, get func(string) string) error {
	c.Native = Native{TempDir: os.TempDir(), MaxInput: int64(c.MaxRequestBody), MaxFrame: min(int64(c.MaxRequestBody), 32<<20), Concurrency: 4, Timeout: 30 * time.Second}
	if value := get("LOGAN_NATIVE_ENABLED"); value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid LOGAN_NATIVE_ENABLED")
		}
		c.Native.Enabled = v
	}
	if !c.Native.Enabled {
		return nil
	}
	if !c.Database.Enabled {
		return fmt.Errorf("native routes require an enabled database")
	}
	c.Native.Key = []byte(get("LOGAN_NATIVE_AES_KEY"))
	c.Native.IV = []byte(get("LOGAN_NATIVE_AES_IV"))
	if len(c.Native.Key) != 16 || len(c.Native.IV) != 16 {
		return fmt.Errorf("native AES key and IV must each contain 16 bytes")
	}
	if v := get("LOGAN_NATIVE_TEMP_DIR"); v != "" {
		c.Native.TempDir = v
	}
	if info, err := os.Stat(c.Native.TempDir); err != nil || !info.IsDir() {
		return fmt.Errorf("native temporary directory must exist")
	}
	if v := get("LOGAN_NATIVE_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid native concurrency")
		}
		c.Native.Concurrency = n
	}
	if v := get("LOGAN_NATIVE_MAX_FRAME_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 16 || n > c.Native.MaxInput {
			return fmt.Errorf("invalid native frame limit")
		}
		c.Native.MaxFrame = n
	}
	if v := get("LOGAN_NATIVE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid native timeout")
		}
		c.Native.Timeout = d
	}
	c.OSS = OSS{Endpoint: get("LOGAN_OSS_ENDPOINT"), Region: get("LOGAN_OSS_REGION"), Bucket: get("LOGAN_OSS_BUCKET"), Prefix: "logan-logs/", AccessKeyID: get("LOGAN_OSS_ACCESS_KEY_ID"), AccessKeySecret: get("LOGAN_OSS_ACCESS_KEY_SECRET"), SessionToken: get("LOGAN_OSS_SESSION_TOKEN"), Timeout: 30 * time.Second}
	if v := get("LOGAN_OSS_PREFIX"); v != "" {
		c.OSS.Prefix = v
	}
	u, err := url.Parse(c.OSS.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("invalid OSS endpoint URL")
	}
	if c.OSS.Region == "" || c.OSS.Bucket == "" || c.OSS.AccessKeyID == "" || c.OSS.AccessKeySecret == "" {
		return fmt.Errorf("OSS region, bucket and credentials are required")
	}
	if v := get("LOGAN_OSS_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid OSS timeout")
		}
		c.OSS.Timeout = d
	}
	if v := get("LOGAN_OSS_PATH_STYLE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid OSS path style")
		}
		c.OSS.PathStyle = b
	}
	return nil
}
