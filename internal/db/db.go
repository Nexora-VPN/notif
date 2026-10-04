// Package db opens Notif's database — SQLite by default, PostgreSQL by the
// install's choice — and migrates it. The pure-Go SQLite driver keeps the
// build free of cgo, as the panel's is.
package db

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/nexora-vpn/notif/internal/channel"
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
	// Notif 0.1.0 kept every subscription link, its secret token
	// with it, in the copy of the accounts; it is read from the panel when
	// a notice needs it now, and the stored copies go.
	if gdb.Migrator().HasColumn(&model.User{}, "sub_url") {
		// 0.1.0 also wrote its own clock into users.updated_at at every
		// save, later than the panel's for every account nobody edited
		// since: a read now keeps the later state, so it would never write
		// those accounts again (their traffic, the time they were seen, the
		// chats their cards name). The copy forgets its times, and the next
		// read writes every account. Done before the drop, so a stop
		// between the two does it again rather than never.
		if err := gdb.Model(&model.User{}).Where("updated_at <> 0").Update("updated_at", 0).Error; err != nil {
			return nil, fmt.Errorf("migrate: reset users.updated_at: %w", err)
		}
		if err := gdb.Migrator().DropColumn(&model.User{}, "sub_url"); err != nil {
			return nil, fmt.Errorf("migrate: drop users.sub_url: %w", err)
		}
		// SQLite leaves the dropped values in its free pages and the
		// write-ahead log until they are written over: the file is rebuilt
		// once, and the log emptied. (PostgreSQL's copy of a row that held
		// one goes as the next full read rewrites the row and autovacuum
		// passes.) The daily copies taken before stay until they age out
		// (internal/backup), as the docs' upgrade note says.
		if c.Driver != config.DriverPostgres {
			if err := gdb.Exec("VACUUM").Error; err != nil {
				return nil, fmt.Errorf("migrate: vacuum after dropping users.sub_url: %w", err)
			}
			if err := gdb.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
				return nil, fmt.Errorf("migrate: checkpoint after dropping users.sub_url: %w", err)
			}
		}
	}
	if err := carryChannels(gdb); err != nil {
		return nil, fmt.Errorf("migrate: carry the channels over: %w", err)
	}
	return gdb, nil
}

// carryChannels carries the channels Notif 0.1.0 stored over to the check
// on their addresses (channel.CarryOver): that version dialled any address,
// so a channel on the admin's own network has "private" turned on, and a
// bot's proxy on this server is kept, rather than either stopping on the
// upgrade. A channel saved since has the field, and is left alone; this
// one is given it, so it is carried once.
func carryChannels(gdb *gorm.DB) error {
	var chans []model.Channel
	if err := gdb.Find(&chans).Error; err != nil {
		return err
	}
	lookup := func(host string) []netip.Addr {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		addrs, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		return addrs
	}
	for _, c := range chans {
		cfg, carried := channel.CarryOver(c.Config.V, lookup)
		if !carried {
			continue
		}
		if err := gdb.Model(&model.Channel{ID: c.ID}).Update("config", model.JSON[map[string]string]{V: cfg}).Error; err != nil {
			return err
		}
		log.Printf("upgrade: channel %q carried over: private %s, proxy on this server kept %v", c.Name, cfg["private"], cfg[channel.KeptProxy] != "")
	}
	return nil
}

// sqliteDSN turns on WAL (a write does not stop reads), a busy timeout (a
// contended writer waits instead of failing) and immediate transactions (a
// transaction that reads then writes takes the lock at BEGIN instead of
// deadlocking on the upgrade).
func sqliteDSN(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_txlock=immediate"
}
