package config

import (
	"fmt"
	"strconv"
	"time"
)

type Maintenance struct {
	Interval, RunTimeout, LeaseTTL, ObjectLeaseTTL, Retention, OrphanAge, RetryDelay time.Duration
	Batch                                                                            int
}

func loadMaintenance(c *Config, get func(string) string) error {
	c.Maintenance = Maintenance{Interval: time.Minute, RunTimeout: 30 * time.Second, LeaseTTL: 2 * time.Minute, ObjectLeaseTTL: 2 * time.Minute, Retention: 7 * 24 * time.Hour, OrphanAge: 24 * time.Hour, RetryDelay: time.Minute, Batch: 100}
	for key, dst := range map[string]*bool{"LOGAN_MAINTENANCE_ENABLED": &c.MaintenanceEnabled, "LOGAN_METRICS_ENABLED": &c.MetricsEnabled} {
		if s := get(key); s != "" {
			v, e := strconv.ParseBool(s)
			if e != nil {
				return fmt.Errorf("invalid %s", key)
			}
			*dst = v
		}
	}
	for key, dst := range map[string]*time.Duration{"LOGAN_RETENTION": &c.Maintenance.Retention, "LOGAN_MAINTENANCE_INTERVAL": &c.Maintenance.Interval, "LOGAN_MAINTENANCE_TIMEOUT": &c.Maintenance.RunTimeout, "LOGAN_ORPHAN_AGE": &c.Maintenance.OrphanAge} {
		if s := get(key); s != "" {
			v, e := time.ParseDuration(s)
			if e != nil || v <= 0 {
				return fmt.Errorf("invalid %s", key)
			}
			*dst = v
		}
	}
	if s := get("LOGAN_MAINTENANCE_BATCH"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > 1000 {
			return fmt.Errorf("invalid maintenance batch")
		}
		c.Maintenance.Batch = v
	}
	if c.MaintenanceEnabled && (!c.Native.Enabled || !c.Database.Enabled) {
		return fmt.Errorf("maintenance requires native storage and database")
	}
	if c.MaintenanceEnabled && (c.Maintenance.OrphanAge <= c.Native.Timeout || c.Maintenance.OrphanAge-c.Native.Timeout <= c.OSS.Timeout) {
		return fmt.Errorf("orphan age must exceed upload and OSS timeout")
	}
	return nil
}
