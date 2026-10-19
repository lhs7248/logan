package repository

import (
	"context"
	"database/sql"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"logan/servergo/internal/config"
)

// Open configures a pool and verifies connectivity. It performs no DDL.
// Underlying driver errors are intentionally not returned: they may include credentials.
func Open(ctx context.Context, c config.Database) (*gorm.DB, *sql.DB, error) {
	if !c.Enabled {
		return nil, nil, nil
	}
	db, err := gorm.Open(mysql.Open(c.DSN), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		if db != nil {
			if pool, poolErr := db.DB(); poolErr == nil {
				_ = pool.Close()
			}
		}
		return nil, nil, fmt.Errorf("MySQL initialization failed; check connection settings and server availability")
	}
	pool, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("MySQL pool initialization failed")
	}
	pool.SetMaxOpenConns(c.MaxOpen)
	pool.SetMaxIdleConns(c.MaxIdle)
	pool.SetConnMaxLifetime(c.MaxLifetime)
	pingCtx, cancel := context.WithTimeout(ctx, c.PingTimeout)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, nil, fmt.Errorf("MySQL startup check failed")
	}
	return db, pool, nil
}
