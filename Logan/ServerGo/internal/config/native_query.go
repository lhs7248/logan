package config

import (
	"fmt"
	"strconv"
	"time"
)

type NativeQuery struct {
	Enabled                              bool
	Workers, MaxIDs, MaxLine, MaxRecords int
	MaxPlain, MaxStaged                  int64
	Timeout, Wait                        time.Duration
}

func loadNativeQuery(c *Config, get func(string) string) error {
	c.NativeQuery = NativeQuery{Workers: 4, MaxIDs: 200, MaxLine: 4 << 20, MaxRecords: 100000, MaxPlain: 128 << 20, MaxStaged: 256 << 20, Timeout: 30 * time.Second, Wait: 30 * time.Second}
	q := &c.NativeQuery
	if value := get("LOGAN_NATIVE_QUERY_ENABLED"); value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid LOGAN_NATIVE_QUERY_ENABLED")
		}
		q.Enabled = v
	}
	if !q.Enabled {
		return nil
	}
	if !c.Native.Enabled {
		return fmt.Errorf("native queries require LOGAN_NATIVE_ENABLED=true")
	}
	for key, dst := range map[string]*int{"LOGAN_NATIVE_QUERY_WORKERS": &q.Workers, "LOGAN_NATIVE_QUERY_MAX_IDS": &q.MaxIDs, "LOGAN_NATIVE_QUERY_MAX_LINE_BYTES": &q.MaxLine, "LOGAN_NATIVE_QUERY_MAX_RECORDS": &q.MaxRecords} {
		if value := get(key); value != "" {
			v, err := strconv.Atoi(value)
			if err != nil || v < 1 {
				return fmt.Errorf("invalid %s", key)
			}
			*dst = v
		}
	}
	for key, dst := range map[string]*int64{"LOGAN_NATIVE_QUERY_MAX_PLAIN_BYTES": &q.MaxPlain, "LOGAN_NATIVE_QUERY_MAX_STAGED_BYTES": &q.MaxStaged} {
		if value := get(key); value != "" {
			v, err := strconv.ParseInt(value, 10, 64)
			if err != nil || v < 1 {
				return fmt.Errorf("invalid %s", key)
			}
			*dst = v
		}
	}
	for key, dst := range map[string]*time.Duration{"LOGAN_NATIVE_QUERY_TIMEOUT": &q.Timeout, "LOGAN_NATIVE_QUERY_WAIT": &q.Wait} {
		if value := get(key); value != "" {
			v, err := time.ParseDuration(value)
			if err != nil || v <= 0 {
				return fmt.Errorf("invalid %s", key)
			}
			*dst = v
		}
	}
	// First-query processing is synchronous in the reference API. Unless the
	// operator supplies a shorter wait, allow the worker its configured budget.
	if get("LOGAN_NATIVE_QUERY_WAIT") == "" {
		q.Wait = q.Timeout
	}
	if q.Workers > 64 || int64(q.MaxLine) > q.MaxPlain {
		return fmt.Errorf("native query workers must be at most 64 and line budget must not exceed plaintext budget")
	}
	return nil
}
