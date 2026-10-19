package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestWebConfigurationRequiresExplicitMigrationAndKeyFile(t *testing.T) {
	values := map[string]string{"LOGAN_WEB_ENABLED": "true"}
	get := func(k string) string { return values[k] }
	if _, err := Load(get); err == nil {
		t.Fatal("enabled Web without DB accepted")
	}
	values["LOGAN_DB_ENABLED"] = "true"
	values["LOGAN_MYSQL_DSN"] = "u:p@tcp(localhost:3306)/logan"
	if _, err := Load(get); err == nil {
		t.Fatal("missing key accepted")
	}
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(keyPath, []byte("not an RSA key"), 0600); err != nil {
		t.Fatal(err)
	}
	values["LOGAN_WEB_RSA_PRIVATE_KEY_FILE"] = keyPath
	if _, err := Load(get); err == nil {
		t.Fatal("invalid key accepted")
	}
	values["LOGAN_WEB_WORKERS"] = "0"
	if _, err := Load(get); err == nil {
		t.Fatal("zero workers accepted")
	}
}

func TestWebLegacyRSARequiresExplicitRuntimeCompatibility(t *testing.T) {
	source, err := os.ReadFile("../../../Server/src/main/java/com/meituan/logan/web/parser/WebLogDecryptHelper.java")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`RSA_PRIVATE_KEY_H5 = "([^"]+)"`).FindSubmatch(source)
	der, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.der")
	if err := os.WriteFile(path, der, 0600); err != nil {
		t.Fatal(err)
	}
	get := func(k string) string {
		return map[string]string{"LOGAN_WEB_ENABLED": "true", "LOGAN_DB_ENABLED": "true", "LOGAN_MYSQL_DSN": "u:p@tcp(localhost:3306)/logan", "LOGAN_WEB_RSA_PRIVATE_KEY_FILE": path}[k]
	}
	t.Setenv("GODEBUG", "rsa1024min=1")
	if _, err := Load(get); err == nil {
		t.Fatal("unusable legacy RSA accepted at startup")
	}
	t.Setenv("GODEBUG", "rsa1024min=0")
	c, err := Load(get)
	if err != nil || !c.Web.Enabled || len(c.Web.PrivateKey) == 0 {
		t.Fatalf("explicit legacy compatibility failed: %v", err)
	}
}
