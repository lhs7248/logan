package config

import (
	"strings"
	"testing"
)

func nativeQueryEnv() map[string]string {
	return map[string]string{"LOGAN_NATIVE_QUERY_ENABLED": "true", "LOGAN_NATIVE_ENABLED": "true", "LOGAN_DB_ENABLED": "true", "LOGAN_MYSQL_DSN": "u:p@tcp(localhost:3306)/logan", "LOGAN_NATIVE_AES_KEY": "0123456789abcdef", "LOGAN_NATIVE_AES_IV": "fedcba9876543210", "LOGAN_OSS_ENDPOINT": "https://oss.example.invalid", "LOGAN_OSS_REGION": "test", "LOGAN_OSS_BUCKET": "test", "LOGAN_OSS_ACCESS_KEY_ID": "test", "LOGAN_OSS_ACCESS_KEY_SECRET": "test"}
}
func TestNativeQueryConfigDefaultsAndDependencies(t *testing.T) {
	values := nativeQueryEnv()
	get := func(k string) string { return values[k] }
	c, err := Load(get)
	if err != nil || !c.NativeQuery.Enabled || c.NativeQuery.Workers != 4 || c.NativeQuery.MaxPlain != 128<<20 {
		t.Fatalf("query config %+v %v", c.NativeQuery, err)
	}
	values["LOGAN_NATIVE_ENABLED"] = "false"
	if _, err := Load(get); err == nil || !strings.Contains(err.Error(), "LOGAN_NATIVE_ENABLED") {
		t.Fatal("query without OSS/native accepted", err)
	}
}
func TestNativeQueryConfigRejectsInvalidBudgets(t *testing.T) {
	for key, value := range map[string]string{"LOGAN_NATIVE_QUERY_WORKERS": "65", "LOGAN_NATIVE_QUERY_MAX_IDS": "0", "LOGAN_NATIVE_QUERY_MAX_PLAIN_BYTES": "0", "LOGAN_NATIVE_QUERY_MAX_STAGED_BYTES": "-1", "LOGAN_NATIVE_QUERY_MAX_LINE_BYTES": "999999999", "LOGAN_NATIVE_QUERY_MAX_RECORDS": "0", "LOGAN_NATIVE_QUERY_TIMEOUT": "0s", "LOGAN_NATIVE_QUERY_WAIT": "-1s"} {
		t.Run(key, func(t *testing.T) {
			values := nativeQueryEnv()
			values[key] = value
			_, err := Load(func(k string) string { return values[k] })
			if err == nil {
				t.Fatalf("invalid %s=%s accepted", key, value)
			}
		})
	}
}

func TestNativeDefaultBriefWaitCoversConfiguredProcessingBudget(t *testing.T) {
	values := nativeQueryEnv()
	values["LOGAN_NATIVE_QUERY_TIMEOUT"] = "12s"
	c, err := Load(func(k string) string { return values[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.NativeQuery.Wait != c.NativeQuery.Timeout {
		t.Fatalf("default brief wait %s expires before accepted processing budget %s", c.NativeQuery.Wait, c.NativeQuery.Timeout)
	}
	values["LOGAN_NATIVE_QUERY_WAIT"] = "5s"
	c, err = Load(func(k string) string { return values[k] })
	if err != nil || c.NativeQuery.Wait.String() != "5s" {
		t.Fatal("explicit caller budget not preserved", c.NativeQuery, err)
	}
}
