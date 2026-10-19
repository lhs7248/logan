// Package config loads validated environment configuration without logging secrets.
package config

import (
	"fmt"

	"github.com/go-sql-driver/mysql"
	"net"
	"path"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Database struct {
	Enabled     bool
	DSN         string
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	PingTimeout time.Duration
}
type Config struct {
	Maintenance                        Maintenance
	MaintenanceEnabled, MetricsEnabled bool
	Environment                        string
	Address                            string
	ContextPath                        string
	Timezone                           string
	ReadTimeout                        time.Duration
	WriteTimeout                       time.Duration
	IdleTimeout                        time.Duration
	ExitWait                           time.Duration
	MaxRequestBody                     int
	NativeQuery                        NativeQuery
	Web                                Web
	Native                             Native
	OSS                                OSS
	Database                           Database
}

func Load(get func(string) string) (Config, error) {
	c := Config{Environment: "development", Address: ":8080", ContextPath: "/logan-web", Timezone: "Asia/Shanghai", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, ExitWait: 60 * time.Second, MaxRequestBody: 32 << 20, Database: Database{MaxOpen: 20, MaxIdle: 10, MaxLifetime: 30 * time.Minute, PingTimeout: 5 * time.Second}}
	for key, dst := range map[string]*string{"LOGAN_ENV": &c.Environment, "LOGAN_HTTP_ADDRESS": &c.Address, "LOGAN_CONTEXT_PATH": &c.ContextPath, "LOGAN_TIMEZONE": &c.Timezone} {
		if s := get(key); s != "" {
			*dst = s
		}
	}
	if c.ContextPath == "/" {
		c.ContextPath = ""
	}
	if c.Environment != "development" && c.Environment != "production" {
		return c, fmt.Errorf("LOGAN_ENV must be development or production")
	}
	host, port, err := net.SplitHostPort(c.Address)
	_ = host
	if err != nil {
		return c, fmt.Errorf("LOGAN_HTTP_ADDRESS must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("invalid HTTP port")
	}
	if c.ContextPath != "" && (!strings.HasPrefix(c.ContextPath, "/") || path.Clean(c.ContextPath) != c.ContextPath || strings.ContainsAny(c.ContextPath, "?#:{}*\\ ")) {
		return c, fmt.Errorf("invalid LOGAN_CONTEXT_PATH")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return c, fmt.Errorf("invalid LOGAN_TIMEZONE")
	}
	for key, dst := range map[string]*time.Duration{
		"LOGAN_HTTP_READ_TIMEOUT": &c.ReadTimeout, "LOGAN_HTTP_WRITE_TIMEOUT": &c.WriteTimeout,
		"LOGAN_HTTP_IDLE_TIMEOUT": &c.IdleTimeout, "LOGAN_EXIT_WAIT": &c.ExitWait,
		"LOGAN_DB_CONN_MAX_LIFETIME": &c.Database.MaxLifetime, "LOGAN_DB_PING_TIMEOUT": &c.Database.PingTimeout,
	} {
		if s := get(key); s != "" {
			v, err := time.ParseDuration(s)
			if err != nil || v <= 0 {
				return c, fmt.Errorf("%s must be a positive duration", key)
			}
			*dst = v
		}
	}
	for key, dst := range map[string]*int{"LOGAN_DB_MAX_OPEN": &c.Database.MaxOpen, "LOGAN_DB_MAX_IDLE": &c.Database.MaxIdle, "LOGAN_HTTP_MAX_BODY_BYTES": &c.MaxRequestBody} {
		if s := get(key); s != "" {
			v, err := strconv.Atoi(s)
			if err != nil || v < 0 {
				return c, fmt.Errorf("%s must be a non-negative integer", key)
			}
			*dst = v
		}
	}
	if c.Database.MaxOpen < 1 || c.Database.MaxIdle > c.Database.MaxOpen || c.MaxRequestBody < 1 {
		return c, fmt.Errorf("invalid pool or request body limits")
	}
	if s := get("LOGAN_DB_ENABLED"); s != "" {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return c, fmt.Errorf("invalid LOGAN_DB_ENABLED")
		}
		c.Database.Enabled = v
	}
	if c.Environment == "production" && !c.Database.Enabled {
		return c, fmt.Errorf("production requires LOGAN_DB_ENABLED=true")
	}
	if c.Database.Enabled {
		dsn, err := mysql.ParseDSN(get("LOGAN_MYSQL_DSN"))
		if err != nil {
			return c, fmt.Errorf("invalid LOGAN_MYSQL_DSN")
		}
		if dsn.DBName == "" || dsn.User == "" {
			return c, fmt.Errorf("LOGAN_MYSQL_DSN requires user and database")
		}
		loc, _ := time.LoadLocation(c.Timezone)
		dsn.ParseTime = true
		dsn.Loc = loc
		if dsn.Timeout <= 0 {
			dsn.Timeout = 5 * time.Second
		}
		if dsn.ReadTimeout <= 0 {
			dsn.ReadTimeout = 10 * time.Second
		}
		if dsn.WriteTimeout <= 0 {
			dsn.WriteTimeout = 10 * time.Second
		}
		c.Database.DSN = dsn.FormatDSN()
	}
	if err := loadNative(&c, get); err != nil {
		return c, err
	}
	if err := loadNativeQuery(&c, get); err != nil {
		return c, err
	}
	if err := loadWeb(&c, get); err != nil {
		return c, err
	}
	if err := loadMaintenance(&c, get); err != nil {
		return c, err
	}
	return c, nil
}
