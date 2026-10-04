package db

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/model"
)

// TestTheStoredLinksGo: a database of Notif 0.1.0 kept every
// subscription link, secret and all; opening it drops them.
func TestTheStoredLinksGo(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, Driver: config.DriverSQLite, DSN: dir + "/notif.db"}
	gdb, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The column as GORM made it from the field the model had then.
	if err := gdb.Exec("ALTER TABLE `users` ADD `sub_url` text NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Exec("INSERT INTO users (id, name, enable, sub_url) VALUES (1, 'ana', true, 'https://x.io/sub/secret-token')").Error; err != nil {
		t.Fatal(err)
	}
	if gdb, err = Open(cfg); err != nil {
		t.Fatal(err)
	}
	var cols int64
	gdb.Raw("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'sub_url'").Scan(&cols)
	if cols != 0 {
		t.Fatal("the stored links are still there")
	}
	var n int64
	gdb.Table("users").Count(&n)
	if n != 1 {
		t.Fatalf("%d accounts after the migration", n)
	}
	// Nor is any of it left in the file's free pages or the log.
	for _, f := range []string{"notif.db", "notif.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if bytes.Contains(b, []byte("secret-token")) {
			t.Fatalf("%s still holds a link", f)
		}
	}
}

// TestTheChannelsAreCarriedOver: Notif 0.1.0 dialled any address; on the
// upgrade a channel on the admin's own network has "private" turned on, a
// bot's proxy on this server is kept, a public one is left off, and a
// channel saved since keeps what it was saved with.
func TestTheChannelsAreCarriedOver(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, Driver: config.DriverSQLite, DSN: dir + "/notif.db"}
	gdb, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stored := []model.Channel{
		{Kind: "ntfy", Name: "compose", Config: model.JSON[map[string]string]{V: map[string]string{"server": "http://ntfy"}}},
		{Kind: "smtp", Name: "lan", Config: model.JSON[map[string]string]{V: map[string]string{"host": "10.0.0.5", "from": "a@b.c"}}},
		{Kind: "telegram", Name: "local proxy", Config: model.JSON[map[string]string]{V: map[string]string{
			"token": "1:a", "apiBase": "https://149.154.167.220", "proxy": "socks5://127.0.0.1:10808",
		}}},
		{Kind: "telegram", Name: "lan proxy", Config: model.JSON[map[string]string]{V: map[string]string{
			"token": "1:a", "apiBase": "https://149.154.167.220", "proxy": "socks5://192.168.1.4:1080",
		}}},
		{Kind: "http", Name: "public", Config: model.JSON[map[string]string]{V: map[string]string{"url": "https://203.0.113.9/send"}}},
		{Kind: "http", Name: "saved since", Config: model.JSON[map[string]string]{V: map[string]string{"url": "http://10.0.0.9/send", "private": "off"}}},
	}
	for i := range stored {
		if err := gdb.Create(&stored[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if gdb, err = Open(cfg); err != nil {
		t.Fatal(err)
	}
	want := []struct{ private, kept string }{
		{"on", ""}, {"on", ""}, {"off", "socks5://127.0.0.1:10808"}, {"on", ""}, {"off", ""}, {"off", ""},
	}
	var got []model.Channel
	gdb.Order("id").Find(&got)
	for i, w := range want {
		if c := got[i].Config.V; c["private"] != w.private || c[channel.KeptProxy] != w.kept {
			t.Errorf("%s: private %q kept %q", got[i].Name, c["private"], c[channel.KeptProxy])
		}
	}
}
