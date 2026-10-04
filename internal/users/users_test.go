package users

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

type fake struct {
	mu    sync.Mutex
	items []map[string]any
	// hidden are accounts a page leaves out though the panel still has them
	// (one slid back across a page boundary as another was deleted).
	hidden map[uint]map[string]any
	// pause, when set, holds a list read until it is closed.
	pause chan struct{}
	// fail is how many list reads to come fail, as a panel that does not
	// answer.
	fail int
}

func (f *fake) Get(_ context.Context, path string, out any) error {
	if f.pause != nil && strings.HasPrefix(path, "/users?") {
		<-f.pause
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 && strings.HasPrefix(path, "/users?") {
		f.fail--
		return &panel.Error{Status: 502, Message: "the panel did not answer"}
	}
	if id, ok := strings.CutPrefix(path, "/users/"); ok {
		for _, it := range f.items {
			if fmt.Sprint(it["id"]) == id {
				b, _ := json.Marshal(it)
				return json.Unmarshal(b, out)
			}
		}
		for hid, it := range f.hidden {
			if fmt.Sprint(hid) == id {
				b, _ := json.Marshal(it)
				return json.Unmarshal(b, out)
			}
		}
		return &panel.Error{Status: 404, Message: "no such user"}
	}
	b, _ := json.Marshal(map[string]any{"items": f.items, "total": len(f.items)})
	return json.Unmarshal(b, out)
}

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, _ := openCfg(t)
	return gdb
}

// openCfg is openDB with the configuration, to open the database again.
func openCfg(t *testing.T) (*gorm.DB, config.Config) {
	t.Helper()
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
		gdb.Exec("TRUNCATE settings, users, chats, links, blocks, deliveries RESTART IDENTITY")
	}
	return gdb, cfg
}

// TestReadsTellWhatChanged: the first read tells nothing; a later read of
// an edit tells the old and the new account, and so does a read an event
// asked for; an account the panel no longer has is gone, one a page only
// left out is not; the chats a card names are found by an index.
func TestReadsTellWhatChanged(t *testing.T) {
	gdb := openDB(t)
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
	if _, err := s.One(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(told) != 2 || told[1][1].Volume != 30 {
		t.Fatalf("a read for an event told %+v", told)
	}
	f.items = []map[string]any{{"id": 2, "name": "bob", "contact": map[string]string{"telegram_id": "42", "phone": "0912"}}}
	f.hidden = map[uint]map[string]any{3: {"id": 3, "name": "cy"}}
	gdb.Create(&model.User{ID: 3, Name: "cy", Contact: model.JSON[map[string]string]{V: map[string]string{}}})
	now = now.Add(time.Minute)
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	gdb.First(&u, 1)
	if u.GoneAt == 0 {
		t.Fatal("an account the panel no longer has is not gone")
	}
	var cy model.User
	gdb.First(&cy, 3)
	if cy.ID != 3 || cy.GoneAt != 0 {
		t.Fatal("an account a page left out was marked gone")
	}
	var chats []model.Chat
	gdb.Find(&chats)
	if len(chats) != 1 || chats[0].UserID != 2 || chats[0].Key != "telegram_id" || chats[0].Value != "42" {
		t.Fatalf("chats %+v", chats)
	}
}

// TestReadPassesDoNotOverlap: an edit read that starts while another is
// still reading waits for it, and the mark edits are read since never
// moves back.
func TestReadPassesDoNotOverlap(t *testing.T) {
	gdb := openDB(t)
	f := &fake{items: []map[string]any{{"id": 1, "name": "ana"}}}
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	s := &Sync{DB: gdb, Panel: func() Getter { return f }, Now: clock}
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.pause = make(chan struct{})
	first := make(chan error, 1)
	go func() { first <- s.Edits(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	second := make(chan error, 1)
	go func() { second <- s.Edits(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	close(f.pause)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	last := s.lastEdit
	s.mu.Unlock()
	if last != 1060 {
		t.Fatalf("lastEdit %d after the later pass", last)
	}
}

// TestAnOlderReadKeepsTheNewer: a page read before an event's read of the
// same account, and saved after it, does not write the older state back
// nor tell its change again.
func TestAnOlderReadKeepsTheNewer(t *testing.T) {
	gdb := openDB(t)
	var told [][2]model.User
	s := &Sync{DB: gdb, Changed: func(o, n model.User) { told = append(told, [2]model.User{o, n}) }}
	base := account{ID: 1, Name: "ana", Expiry: 100, UpdatedAt: 1}
	if err := s.save([]account{base}, 1, true, false); err != nil {
		t.Fatal(err)
	}
	newer := base
	newer.Expiry, newer.UpdatedAt = 300, 5
	if err := s.save([]account{newer}, 2, true, false); err != nil {
		t.Fatal(err)
	}
	older := base
	older.Expiry, older.UpdatedAt = 200, 3
	if err := s.save([]account{older}, 3, true, false); err != nil {
		t.Fatal(err)
	}
	var u model.User
	gdb.First(&u, 1)
	if u.Expiry != 300 || u.UpdatedAt != 5 || len(told) != 1 || told[0][1].Expiry != 300 {
		t.Fatalf("copy %+v, told %+v", u, told)
	}
}

// TestAnUpgradeFrom010Refreshes: Notif 0.1.0 wrote its own clock into
// users.updated_at, later than the panel's for an account nobody edited
// since. Opened by this version, such a copy is written again by the next
// read — its traffic, the time it was seen, the chats its card names —
// where it would otherwise be kept as "the later state" for ever.
func TestAnUpgradeFrom010Refreshes(t *testing.T) {
	gdb, cfg := openCfg(t)
	// The database as 0.1.0 left it: the stored link's column, the
	// account saved at Notif's clock (5000), long after the panel's last
	// edit of it (1000), and no chats table rows (0.1.0 had none).
	if err := gdb.Exec("ALTER TABLE users ADD sub_url text NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	gdb.Create(&model.User{
		ID: 1, Name: "ana", Used: 1, UpdatedAt: 5000, SeenAt: 4000,
		Contact: model.JSON[map[string]string]{V: map[string]string{"telegram_id": "42"}},
	})
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{items: []map[string]any{{"id": 1, "name": "ana", "up": 10, "down": 20, "updatedAt": 1000, "contact": map[string]string{"telegram_id": "42"}}}}
	s := &Sync{DB: gdb, Panel: func() Getter { return f }, Now: func() time.Time { return time.Unix(6000, 0) }}
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	var u model.User
	gdb.First(&u, 1)
	if u.Used != 30 || u.SeenAt != 6000 || u.UpdatedAt != 1000 {
		t.Fatalf("the copy after the upgrade: used %d, seen %d, updated %d", u.Used, u.SeenAt, u.UpdatedAt)
	}
	var chats []model.Chat
	gdb.Find(&chats)
	if len(chats) != 1 || chats[0].UserID != 1 || chats[0].Value != "42" {
		t.Fatalf("chats %+v", chats)
	}
}

// TestARestoreAndAnotherPanel: after the panel is restored, its accounts
// carry the backup's earlier times; a full read keeps the later state, a
// rebase writes what the panel has now and tells nobody. Registered with
// another panel, the copy is forgotten with all that was tied to it, and
// the deliveries still waiting are cancelled.
func TestARestoreAndAnotherPanel(t *testing.T) {
	gdb := openDB(t)
	f := &fake{items: []map[string]any{{"id": 1, "name": "ana", "expiry": 300, "updatedAt": 50, "contact": map[string]string{"telegram_id": "42"}}}}
	var told int
	now := time.Unix(1000, 0)
	s := &Sync{DB: gdb, Panel: func() Getter { return f }, Now: func() time.Time { return now }, Changed: func(model.User, model.User) { told++ }}
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.items[0]["expiry"], f.items[0]["updatedAt"] = 100, 10
	now = now.Add(time.Minute)
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	var u model.User
	gdb.First(&u, 1)
	if u.Expiry != 300 {
		t.Fatalf("a full read wrote an earlier state: %d", u.Expiry)
	}
	if err := s.Rebase(context.Background()); err != nil {
		t.Fatal(err)
	}
	gdb.First(&u, 1)
	if u.Expiry != 100 || u.UpdatedAt != 10 || told != 0 {
		t.Fatalf("after the rebase: expiry %d, updated %d, told %d", u.Expiry, u.UpdatedAt, told)
	}

	if err := s.MarkRebase(); err != nil {
		t.Fatal(err)
	}
	gdb.Create(&model.Link{UserID: 1, Key: "telegram_id", Value: "42", At: 1})
	gdb.Create(&model.Block{UserID: 1, ChannelID: 3, Value: "42", At: 1})
	gdb.Create(&model.Delivery{Key: "k1", UserID: 1, Kind: "custom", Status: model.DeliveryQueued})
	gdb.Create(&model.Delivery{Key: "k2", UserID: 1, Kind: "custom", Status: model.DeliverySent})
	if err := s.Forget(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []any{&[]model.User{}, &[]model.Chat{}, &[]model.Link{}, &[]model.Block{}} {
		if n := gdb.Find(m).RowsAffected; n != 0 {
			t.Fatalf("%T: %d rows of the old panel left", m, n)
		}
	}
	var ds []model.Delivery
	gdb.Order("key").Find(&ds)
	if len(ds) != 2 || ds[0].Status != model.DeliveryCancelled || ds[1].Status != model.DeliverySent {
		t.Fatalf("deliveries %+v", ds)
	}
	if s.hasRead() {
		t.Fatal("edits would be read since the old panel's read")
	}
	if mark, _ := settings.RebaseOwed(gdb); mark != "" {
		t.Fatal("a rebase is owed to the old panel's restore")
	}
	if last, _ := settings.ForgottenThrough(gdb); last != ds[1].ID {
		t.Fatalf("forgotten through delivery %d, not %d", last, ds[1].ID)
	}
}

// TestARebaseIsOwedUntilItCompletes: the panel's restore is recorded as a
// rebase owed; one whose first page fails stays owed — no full read stands
// in for it, keeping the copy's later times — and the next pass makes it
// and clears it. Run makes one owed at its start.
func TestARebaseIsOwedUntilItCompletes(t *testing.T) {
	gdb := openDB(t)
	f := &fake{items: []map[string]any{{"id": 1, "name": "ana", "expiry": 300, "updatedAt": 50}}}
	var told int
	s := &Sync{DB: gdb, Panel: func() Getter { return f }, Changed: func(model.User, model.User) { told++ }}
	if err := s.Full(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.items[0]["expiry"], f.items[0]["updatedAt"] = 100, 10
	f.fail = 1
	if err := s.MarkRebase(); err != nil {
		t.Fatal(err)
	}
	if !s.owed(context.Background()) {
		t.Fatal("the rebase was not owed")
	}
	var u model.User
	gdb.First(&u, 1)
	if mark, _ := settings.RebaseOwed(gdb); mark == "" || u.Expiry != 300 {
		t.Fatalf("after a failed pass: owed %q, expiry %d", mark, u.Expiry)
	}
	if !s.owed(context.Background()) {
		t.Fatal("the failed rebase was not owed again")
	}
	gdb.First(&u, 1)
	if mark, _ := settings.RebaseOwed(gdb); mark != "" || u.Expiry != 100 || told != 0 {
		t.Fatalf("after the retry: owed %q, expiry %d, told %d", mark, u.Expiry, told)
	}
	if s.owed(context.Background()) {
		t.Fatal("a completed rebase is still owed")
	}

	// Owed across a stop: the next start makes it.
	f.items[0]["expiry"], f.items[0]["updatedAt"] = 50, 5
	if err := settings.MarkRebase(gdb); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Sync{DB: gdb, Panel: func() Getter { return f }}).Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		gdb.First(&u, 1)
		if mark, _ := settings.RebaseOwed(gdb); mark == "" && u.Expiry == 50 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the start did not make the owed rebase: expiry %d", u.Expiry)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}
