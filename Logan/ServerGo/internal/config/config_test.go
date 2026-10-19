package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsAndProductionValidation(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != ":8080" || c.ContextPath != "/logan-web" || c.Database.Enabled {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	for _, env := range []map[string]string{
		{"LOGAN_ENV": "production"},
		{"LOGAN_DB_ENABLED": "maybe"},
		{"LOGAN_HTTP_READ_TIMEOUT": "0s"},
		{"LOGAN_HTTP_ADDRESS": "invalid"},
		{"LOGAN_CONTEXT_PATH": "/a/../b"},
		{"LOGAN_DB_MAX_OPEN": "2", "LOGAN_DB_MAX_IDLE": "3"},
		{"LOGAN_ENV": "unknown"},
		{"LOGAN_TIMEZONE": "Invalid/Zone"},
	} {
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("accepted invalid config: %v", env)
		}
	}
}
func TestDSNErrorsDoNotExposeSecrets(t *testing.T) {
	env := map[string]string{"LOGAN_DB_ENABLED": "true", "LOGAN_MYSQL_DSN": "super-secret-invalid-dsn"}
	_, err := Load(func(k string) string { return env[k] })
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}
func TestDSNParsesTimesAndHasBoundedTimeouts(t *testing.T) {
	env := map[string]string{"LOGAN_DB_ENABLED": "true", "LOGAN_MYSQL_DSN": "user:secret@tcp(localhost:3306)/logan"}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Database.DSN, "parseTime=true") || !strings.Contains(c.Database.DSN, "timeout=") {
		t.Fatal("missing driver safeguards")
	}
}

func TestNativeConfigurationIsExplicitAndValidated(t *testing.T) {
	base := map[string]string{"LOGAN_NATIVE_ENABLED": "true", "LOGAN_DB_ENABLED": "true", "LOGAN_MYSQL_DSN": "user:secret@tcp(localhost:3306)/logan", "LOGAN_NATIVE_AES_KEY": "0123456789abcdef", "LOGAN_NATIVE_AES_IV": "fedcba9876543210", "LOGAN_OSS_ENDPOINT": "https://oss-cn-hangzhou.aliyuncs.com", "LOGAN_OSS_REGION": "cn-hangzhou", "LOGAN_OSS_BUCKET": "test-bucket", "LOGAN_OSS_ACCESS_KEY_ID": "test-only-id", "LOGAN_OSS_ACCESS_KEY_SECRET": "test-only-secret"}
	c, err := Load(func(k string) string { return base[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !c.Native.Enabled || c.OSS.Prefix != "logan-logs/" || c.Native.Concurrency < 1 {
		t.Fatal("native config not loaded")
	}
	for _, key := range []string{"LOGAN_DB_ENABLED", "LOGAN_NATIVE_AES_KEY", "LOGAN_NATIVE_AES_IV", "LOGAN_OSS_ENDPOINT", "LOGAN_OSS_REGION", "LOGAN_OSS_BUCKET", "LOGAN_OSS_ACCESS_KEY_ID", "LOGAN_OSS_ACCESS_KEY_SECRET"} {
		copy := map[string]string{}
		for k, v := range base {
			copy[k] = v
		}
		copy[key] = ""
		if _, err := Load(func(k string) string { return copy[k] }); err == nil {
			t.Errorf("accepted missing %s", key)
		}
	}
	base["LOGAN_NATIVE_CONCURRENCY"] = "0"
	if _, err := Load(func(k string) string { return base[k] }); err == nil {
		t.Fatal("unbounded concurrency accepted")
	}
}
