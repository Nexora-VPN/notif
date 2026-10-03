// Package db opens Notif's database — SQLite by default, PostgreSQL by the
// install's choice — and migrates it. The pure-Go SQLite driver keeps the
// build free of cgo, as the panel's is.
package db

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open connects and migrates.
func Open(c config.Config) (*gorm.DB, error) {
	gc := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var (
		gdb *gorm.DB
		err error
	)
	if c.Driver == config.DriverPostgres {
		gdb, err = gorm.Open(postgres.Open(c.DSN), gc)
		if err == nil {
			if sqlDB, e := gdb.DB(); e == nil {
				sqlDB.SetMaxOpenConns(10)
				sqlDB.SetMaxIdleConns(3)
				sqlDB.SetConnMaxLifetime(30 * time.Minute)
			}
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(c.DSN), 0o700); err != nil {
			return nil, err
		}
		gdb, err = gorm.Open(sqlite.Open(sqliteDSN(c.DSN)), gc)
		if err == nil {
			if sqlDB, e := gdb.DB(); e == nil {
				sqlDB.SetMaxOpenConns(4)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open the %s database: %w", c.Driver, err)
	}
	if err := gdb.AutoMigrate(model.All()...); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return gdb, nil
}

// sqliteDSN turns on WAL (a write does not stop reads), a busy timeout (a
// contended writer waits instead of failing) and immediate transactions (a
// transaction that reads then writes takes the lock at BEGIN instead of
// deadlocking on the upgrade).
func sqliteDSN(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_txlock=immediate"
}
