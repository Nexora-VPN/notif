package users

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/model"
)

type fake struct{ items []map[string]any }

func (f *fake) Get(_ context.Context, path string, out any) error {
	b, _ := json.Marshal(map[string]any{"items": f.items, "total": len(f.items)})
	if strings.HasPrefix(path, "/users/") {
		b, _ = json.Marshal(f.items[0])
	}
	return json.Unmarshal(b, out)
}

// TestReadsTellWhatChanged: the first read tells nothing; a later read of
// an edit tells the old and the new account; a read an event asked for
// tells nothing; an account the panel no longer lists is gone.
func TestReadsTellWhatChanged(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, Driver: config.DriverSQLite, DSN: dir + "/notif.db"}
	if dsn := os.Getenv("NEXORA_TEST_POSTGRES_DSN"); dsn != "" {
		cfg.Driver, cfg.DSN = config.DriverPostgres, dsn
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver == config.DriverPostgres {
		gdb.Exec("TRUNCATE users RESTART IDENTITY")
	}
	f := &fake{items: []map[string]any{{"id": 1, "name": "ana", "volume": 10, "up": 1, "down": 2, "totalUp": 5, "totalDown": 5, "subId": "s1", "enable": true}}}
	var told [][2]model.User
	now := time.Unix(1000, 0)
	s := &Sync{DB: gdb, Panel: func() Getter { return f }, Now: func() time.Time { return now }, Changed: func(o, n model.User) { told = append(told, [2]model.User{o, n}) }}
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(told) != 0 {
		t.Fatalf("the first read told %d", len(told))
	}
	var u model.User
	gdb.First(&u, 1)
	if u.Used != 3 || u.TotalUsed != 10 || u.SubIDHash != SubHash("s1") {
		t.Fatalf("copy %+v", u)
	}
	f.items[0]["volume"] = 20
	now = now.Add(time.Minute)
	_ = s.Edits(context.Background())
	if len(told) != 1 || told[0][0].Volume != 10 || told[0][1].Volume != 20 {
		t.Fatalf("an edit told %+v", told)
	}
	f.items[0]["volume"] = 30
	_, _ = s.One(context.Background(), 1)
	if len(told) != 1 {
		t.Fatal("a read for an event told a change")
	}
	f.items = []map[string]any{{"id": 2, "name": "bob"}}
	now = now.Add(time.Minute)
	_ = s.Full(context.Background())
	gdb.First(&u, 1)
	if u.GoneAt == 0 {
		t.Fatal("an account the panel no longer lists is not gone")
	}
}
