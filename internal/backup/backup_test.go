package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/model"
)

// TestACopyAndBack: the daily copy is taken once a day and seven are kept;
// a copy put back brings its rows, keeping the replaced database; a file
// that is not Notif's is refused; PostgreSQL is pg_dump's.
func TestACopyAndBack(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, Driver: config.DriverSQLite, DSN: filepath.Join(dir, "notif.db")}
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gdb.Create(&model.Channel{Kind: "http", Name: "kept", Enabled: true})
	day := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	for i := range 9 {
		if err := Daily(gdb, cfg, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = Daily(gdb, cfg, day.AddDate(0, 0, 8).Add(time.Hour))
	entries, _ := os.ReadDir(Dir(cfg))
	if len(entries) != Keep || entries[0].Name() != "notif-20261003.db" {
		t.Fatalf("copies: %d, first %s", len(entries), entries[0].Name())
	}
	copyPath := filepath.Join(Dir(cfg), entries[len(entries)-1].Name())
	gdb.Create(&model.Channel{Kind: "http", Name: "after", Enabled: true})
	if sqlDB, e := gdb.DB(); e == nil {
		sqlDB.Close()
	}
	if err := Restore(cfg, copyPath); err != nil {
		t.Fatal(err)
	}
	back, _ := db.Open(cfg)
	var n int64
	back.Model(&model.Channel{}).Count(&n)
	if n != 1 {
		t.Fatalf("%d channels after the restore", n)
	}
	if _, err := os.Stat(cfg.DSN + ".before-restore"); err != nil {
		t.Fatal("the replaced database was not kept")
	}
	junk := filepath.Join(dir, "junk.db")
	_ = os.WriteFile(junk, []byte("not a database"), 0o600)
	if err := Restore(cfg, junk); err == nil {
		t.Fatal("a file that is not Notif's was restored")
	}
	if err := Snapshot(back, config.Config{Driver: config.DriverPostgres}, filepath.Join(dir, "x.db")); err != ErrPostgres {
		t.Fatalf("postgres: %v", err)
	}
}
