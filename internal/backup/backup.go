// Package backup is Notif's own backup (P16 (a): an addon backs itself
// up). On SQLite: a consistent copy of the database once a day in
// <data>/backups, the last seven kept, and `notif backup` / `notif restore`
// for a copy taken or put back by hand. On PostgreSQL the database is the
// operator's, and so is its backup: pg_dump, which a copy here would only
// duplicate badly.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/nexora-vpn/notif/internal/config"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Keep is how many daily copies are kept.
const Keep = 7

// ErrPostgres is a backup asked of a PostgreSQL install.
var ErrPostgres = errors.New("Notif runs on PostgreSQL here: back it up with pg_dump, as the rest of that database")

// Snapshot writes a consistent copy of the SQLite database to path, which
// must not exist: VACUUM INTO, safe while Notif runs.
func Snapshot(gdb *gorm.DB, cfg config.Config, path string) error {
	if cfg.Driver != config.DriverSQLite {
		return ErrPostgres
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := gdb.Exec("VACUUM INTO ?", path).Error; err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// Dir is where the daily copies go.
func Dir(cfg config.Config) string { return filepath.Join(cfg.DataDir, "backups") }

// Latest is when the newest daily copy was taken; zero when there is none
// (or on PostgreSQL, which Notif does not copy itself).
func Latest(cfg config.Config) time.Time {
	var latest time.Time
	entries, _ := os.ReadDir(Dir(cfg))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "notif-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// Daily takes today's copy unless there is one, and keeps the last Keep.
func Daily(gdb *gorm.DB, cfg config.Config, now time.Time) error {
	if cfg.Driver != config.DriverSQLite {
		return nil
	}
	dir := Dir(cfg)
	path := filepath.Join(dir, "notif-"+now.UTC().Format("20060102")+".db")
	if _, err := os.Stat(path); err != nil {
		if err := Snapshot(gdb, cfg, path); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(dir)
	var copies []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "notif-") && strings.HasSuffix(e.Name(), ".db") {
			copies = append(copies, e.Name())
		}
	}
	sort.Strings(copies)
	for len(copies) > Keep {
		_ = os.Remove(filepath.Join(dir, copies[0]))
		copies = copies[1:]
	}
	return nil
}

// Run takes the daily copy now and every day until ctx ends.
func Run(ctx context.Context, gdb *gorm.DB, cfg config.Config) {
	for {
		if err := Daily(gdb, cfg, time.Now()); err != nil {
			log.Printf("backup: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

// Restore puts a copy back in place of the database — with Notif stopped.
// The copy is opened first and must be a Notif database; the one it
// replaces is kept beside it as notif.db.before-restore.
func Restore(cfg config.Config, from string) error {
	if cfg.Driver != config.DriverSQLite {
		return ErrPostgres
	}
	probe, err := gorm.Open(sqlite.Open("file:"+from+"?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return fmt.Errorf("%s does not open as a database: %w", from, err)
	}
	var n int64
	err = probe.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('deliveries', 'channels', 'admins')").Scan(&n).Error
	if sqlDB, e := probe.DB(); e == nil {
		sqlDB.Close()
	}
	if err != nil || n != 3 {
		return fmt.Errorf("%s is not a Notif database", from)
	}
	if _, err := os.Stat(cfg.DSN); err == nil {
		if err := os.Rename(cfg.DSN, cfg.DSN+".before-restore"); err != nil {
			return err
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(cfg.DSN + suffix)
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(cfg.DSN, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
