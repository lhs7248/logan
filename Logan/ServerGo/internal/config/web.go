package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

type Web struct {
	Enabled           bool
	PrivateKey        []byte
	Workers, MaxTasks int
	Timeout, Wait     time.Duration
}

func loadWeb(c *Config, get func(string) string) error {
	c.Web = Web{Workers: 4, MaxTasks: 200, Timeout: 30 * time.Second, Wait: 5 * time.Second}
	if v := get("LOGAN_WEB_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid LOGAN_WEB_ENABLED")
		}
		c.Web.Enabled = enabled
	}
	if !c.Web.Enabled {
		return nil
	}
	if !c.Database.Enabled {
		return fmt.Errorf("Web routes require an enabled database")
	}
	for name, dst := range map[string]*int{"LOGAN_WEB_WORKERS": &c.Web.Workers, "LOGAN_WEB_MAX_TASKS": &c.Web.MaxTasks} {
		if v := get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return fmt.Errorf("invalid %s", name)
			}
			*dst = n
		}
	}
	for name, dst := range map[string]*time.Duration{"LOGAN_WEB_TIMEOUT": &c.Web.Timeout, "LOGAN_WEB_WAIT": &c.Web.Wait} {
		if v := get(name); v != "" {
			n, err := time.ParseDuration(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid %s", name)
			}
			*dst = n
		}
	}
	file, err := os.Open(get("LOGAN_WEB_RSA_PRIVATE_KEY_FILE"))
	if err != nil {
		return fmt.Errorf("Web RSA private key file is required")
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(key) > 64<<10 {
		return fmt.Errorf("Web RSA private key file is required and must be at most 64KiB")
	}
	der := key
	if block, _ := pem.Decode(key); block != nil {
		der = block.Bytes
	}
	var rsaKey *rsa.PrivateKey
	if parsed, e := x509.ParsePKCS8PrivateKey(der); e == nil {
		rsaKey, _ = parsed.(*rsa.PrivateKey)
	} else {
		rsaKey, _ = x509.ParsePKCS1PrivateKey(der)
	}
	if rsaKey == nil || rsaKey.Validate() != nil {
		return fmt.Errorf("invalid Web RSA private key")
	}
	probe, err := rsa.EncryptPKCS1v15(rand.Reader, &rsaKey.PublicKey, []byte("logan-web-key-probe"))
	if err == nil {
		_, err = rsa.DecryptPKCS1v15(rand.Reader, rsaKey, probe)
	}
	if err != nil {
		return fmt.Errorf("Web RSA key is unusable by current runtime; legacy short keys require explicit GODEBUG=rsa1024min=0 compatibility configuration")
	}
	c.Web.PrivateKey = key
	return nil
}
