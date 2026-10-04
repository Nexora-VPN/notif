package watch

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/outbox"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

const day = int64(86400)

func setup(t *testing.T) (*Watch, *gorm.DB, *time.Time) {
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
		gdb.Exec("TRUNCATE settings, users, channels, deliveries, attempts, sends, notices, once_keys, chats, links, blocks RESTART IDENTITY")
	}
	// A Telegram channel: an account with a telegram_id is reachable.
	gdb.Create(&model.Channel{
		Kind: "telegram", Name: "tg", Enabled: true, Position: 1,
		Config: model.JSON[map[string]string]{V: map[string]string{"token": "1:a"}},
	})
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	o := outbox.New(gdb)
	o.Now = func() time.Time { return now }
	return &Watch{DB: gdb, Outbox: o, Now: func() time.Time { return now }}, gdb, &now
}

func user(gdb *gorm.DB, id uint, mod func(*model.User)) model.User {
	u := model.User{ID: id, Name: "u", Enable: true, Contact: model.JSON[map[string]string]{V: map[string]string{"telegram_id": "5"}}}
	mod(&u)
	gdb.Save(&u)
	return u
}

func queued(gdb *gorm.DB, kind string) []model.Delivery {
	var ds []model.Delivery
	gdb.Where("kind = ?", kind).Order("id").Find(&ds)
	return ds
}

// TestTheExpirySchedule: "3 and 1" says nothing at 4½ days; "5 and 1" says
// it once; at half a day the 1-day line speaks; a renewal re-arms it.
func TestTheExpirySchedule(t *testing.T) {
	w, gdb, now := setup(t)
	exp := now.Unix() + 4*day + day/2
	user(gdb, 1, func(u *model.User) { u.Expiry = exp })
	w.Tick()
	if n := len(queued(gdb, "expiring")); n != 0 {
		t.Fatalf("%d notices at 4½ days with 3 and 1", n)
	}
	if err := settings.SaveSchedule(gdb, settings.Schedule{ExpiryDays: []int{1, 5}, TrafficPercents: []int{80}}); err != nil {
		t.Fatal(err)
	}
	w.Tick()
	w.Tick()
	got := queued(gdb, "expiring")
	if len(got) != 1 || got[0].Vars.V["days"] != "5" {
		t.Fatalf("after 5 and 1: %+v", got)
	}
	*now = now.Add(4 * 24 * time.Hour)
	w.Tick()
	if got := queued(gdb, "expiring"); len(got) != 2 || got[1].Vars.V["days"] != "1" {
		t.Fatalf("at half a day: %+v", got)
	}
	// Renewed: thirty days more, then four and a half days before the new
	// date, the 5-day line speaks again.
	user(gdb, 1, func(u *model.User) { u.Expiry = exp + 30*day })
	*now = time.Unix(exp+30*day-4*day-day/2, 0)
	w.Tick()
	if got := queued(gdb, "expiring"); len(got) != 3 {
		t.Fatalf("after a renewal: %d", len(got))
	}
	// A plan that waits for its first connection has no real date yet.
	user(gdb, 2, func(u *model.User) { u.Expiry = now.Unix() + day; u.Duration = 30 * day })
	w.Tick()
	for _, d := range queued(gdb, "expiring") {
		if d.UserID == 2 {
			t.Fatal("a pending plan was warned")
		}
	}
}

// TestTheTrafficSchedule: 85% says the 80 line once, 96% the 95 line, and
// a top-up re-arms.
func TestTheTrafficSchedule(t *testing.T) {
	w, gdb, _ := setup(t)
	user(gdb, 1, func(u *model.User) { u.Volume = 100 << 30; u.Used = 85 << 30; u.TotalUsed = 85 << 30 })
	w.Tick()
	w.Tick()
	if got := queued(gdb, "traffic_warning"); len(got) != 1 || got[0].Vars.V["percent"] != "85" {
		t.Fatalf("at 85%%: %+v", got)
	}
	user(gdb, 1, func(u *model.User) { u.Volume = 100 << 30; u.Used = 96 << 30; u.TotalUsed = 96 << 30 })
	w.Tick()
	if got := queued(gdb, "traffic_warning"); len(got) != 2 {
		t.Fatalf("at 96%%: %d", len(got))
	}
	user(gdb, 1, func(u *model.User) { u.Volume = 110 << 30; u.Used = 96 << 30; u.TotalUsed = 96 << 30 })
	w.Tick()
	if got := queued(gdb, "traffic_warning"); len(got) != 3 {
		t.Fatalf("after a top-up at 87%%: %d", len(got))
	}
}

// TestEditsEventsAndTheirOverlap: traffic added by hand is told after a
// wait; a renewal event arriving meanwhile takes its place; the enforcer's
// own disabling is not told as "disabled", a reseller's is; an account
// nobody can reach, or a notice switched off, queues nothing.
func TestEditsEventsAndTheirOverlap(t *testing.T) {
	w, gdb, now := setup(t)
	old := user(gdb, 1, func(u *model.User) { u.Volume = 50 << 30; u.Expiry = now.Unix() + 10*day; u.UpdatedAt = 1 })
	nu := old
	nu.Volume, nu.UpdatedAt = 60<<30, 2
	w.Changed(old, nu)
	got := queued(gdb, "traffic_added")
	if len(got) != 1 || got[0].Vars.V["added"] != "10737418240" || got[0].NextAt != now.Add(EditDelay).Unix() {
		t.Fatalf("traffic added: %+v", got)
	}
	data, _ := json.Marshal(map[string]any{"userId": 1, "expiry": now.Unix() + 40*day, "volume": 60 << 30})
	w.Event(addon.Event{ID: 9, Time: 1000, Event: "user.renewed", Data: data}, nu)
	if got := queued(gdb, "traffic_added"); got[0].Status != model.DeliveryCancelled {
		t.Fatalf("the edit was not superseded: %s", got[0].Status)
	}
	if got := queued(gdb, "renewed"); len(got) != 1 || got[0].Key != "renewed:1:9:1000" {
		t.Fatalf("renewed: %+v", got)
	}
	// The same delivery again is the same notice; a panel restored to an
	// earlier state, counting its ids from 9 again, raises a new one.
	w.Event(addon.Event{ID: 9, Time: 1000, Event: "user.renewed", Data: data}, nu)
	w.Event(addon.Event{ID: 9, Time: 5000, Event: "user.renewed", Data: data}, nu)
	if got := queued(gdb, "renewed"); len(got) != 2 {
		t.Fatalf("renewed again: %d", len(got))
	}
	resold := nu
	resold.Enable = false
	for _, reason := range []string{"expiry", "resale-volume"} {
		data, _ := json.Marshal(map[string]any{"userId": 1, "reason": reason})
		w.Event(addon.Event{ID: 10, Event: "user.disabled", Data: data}, resold)
	}
	if got := queued(gdb, "disabled"); len(got) != 1 {
		t.Fatalf("disabled: %d", len(got))
	}
	off := nu
	off.Enable, off.DisabledReason, off.UpdatedAt = false, "manual", 3
	w.Changed(nu, off)
	enforced := nu
	enforced.Enable, enforced.DisabledReason, enforced.UpdatedAt = false, "volume", 4
	w.Changed(nu, enforced)
	if got := queued(gdb, "admin_disabled"); len(got) != 1 {
		t.Fatalf("switched off by hand: %d", len(got))
	}
	reset := nu
	reset.Used, reset.TotalUsed = 0, nu.TotalUsed
	nu2 := nu
	nu2.Used = 30 << 30
	w.Changed(nu2, reset)
	if got := queued(gdb, "usage_reset"); len(got) != 1 {
		t.Fatalf("usage reset: %d", len(got))
	}

	lonely := user(gdb, 2, func(u *model.User) { u.Contact.V = map[string]string{"phone": "0912"}; u.Volume = 1 })
	more := lonely
	more.Volume = 2
	w.Changed(lonely, more)
	gdb.Create(&model.Notice{Kind: "expiry_changed", Enabled: false})
	moved := nu
	moved.Expiry += day
	w.Changed(nu, moved)
	if len(queued(gdb, "traffic_added")) != 1 || len(queued(gdb, "expiry_changed")) != 0 {
		t.Fatal("an unreachable account or a notice switched off was queued")
	}
}

// TestTheAdminsWords: a text of the admin's for a language, an override
// for a channel kind, the figures written for the language.
func TestTheAdminsWords(t *testing.T) {
	_, gdb, _ := setup(t)
	gdb.Create(&model.Notice{Kind: "renewed", Enabled: true, Texts: model.JSON[map[string]map[string]model.Text]{V: map[string]map[string]model.Text{
		"fa": {"": {Title: "تمدید", Body: "{name} تا {expiry}، {traffic_total}"}, "kavenegar": {Body: "کوتاه {days}"}},
	}}})
	book := notices.Load(gdb)
	u := model.User{Name: "ana", Expiry: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC).Unix(), Volume: 50 << 30}
	d := model.Delivery{Kind: "renewed", Vars: model.JSON[map[string]string]{V: map[string]string{}}}
	m := book.Render(d, u, model.Channel{Kind: "telegram"}, "fa")
	if m.Title != "تمدید" || m.Text != "ana تا ۱۴۰۵/۰۷/۱۲، ۵۰ گیگابایت" {
		t.Fatalf("fa: %q %q", m.Title, m.Text)
	}
	if m := book.Render(d, u, model.Channel{Kind: "kavenegar"}, "fa"); m.Text[:len("کوتاه")] != "کوتاه" || m.Title != "تمدید" {
		t.Fatalf("override: %q %q", m.Title, m.Text)
	}
	gdb.Save(&model.Notice{Kind: "expired", Enabled: true, Texts: model.JSON[map[string]map[string]model.Text]{V: map[string]map[string]model.Text{
		"en": {"": {Body: "Gone, {name}."}},
	}}})
	if m := notices.Load(gdb).Render(model.Delivery{Kind: "expired"}, u, model.Channel{Kind: "smtp"}, "en"); m.Title != "Account expired" || m.Text != "Gone, ana." {
		t.Fatalf("a text with no title: %q %q", m.Title, m.Text)
	}
	if m := book.Render(d, u, model.Channel{Kind: "telegram"}, "en"); m.Title != "Account renewed" {
		t.Fatalf("default en: %q", m.Title)
	}
}

// TestAnEventCancelsOnlyWhatItTells: a renewal takes the place of the date
// and traffic edits, not of a switch-off by hand; a delayed user.expired
// cancels nothing and says nothing; a notice switched off replaces nothing.
func TestAnEventCancelsOnlyWhatItTells(t *testing.T) {
	w, gdb, now := setup(t)
	old := user(gdb, 1, func(u *model.User) { u.Volume = 50 << 30; u.Expiry = now.Unix() + 10*day; u.UpdatedAt = 1 })
	off := old
	off.Enable, off.DisabledReason, off.UpdatedAt = false, "manual", 2
	w.Changed(old, off)
	more := off
	more.Volume, more.UpdatedAt = 60<<30, 3
	w.Changed(off, more)
	// Expired long ago by the event's word, renewed since by the account's.
	if err := w.Event(addon.Event{ID: 20, Event: "user.expired"}, more); err != nil {
		t.Fatal(err)
	}
	if got := queued(gdb, "expired"); len(got) != 0 {
		t.Fatalf("an outdated user.expired was told: %+v", got)
	}
	if got := queued(gdb, "traffic_added"); got[0].Status != model.DeliveryQueued {
		t.Fatalf("user.expired cancelled the traffic added: %s", got[0].Status)
	}
	if err := w.Event(addon.Event{ID: 21, Event: "user.renewed"}, more); err != nil {
		t.Fatal(err)
	}
	if got := queued(gdb, "traffic_added"); got[0].Status != model.DeliveryCancelled {
		t.Fatalf("the renewal did not replace the traffic added: %s", got[0].Status)
	}
	if got := queued(gdb, "admin_disabled"); len(got) != 1 || got[0].Status != model.DeliveryQueued {
		t.Fatalf("the renewal cancelled the switch-off by hand: %+v", got)
	}
	// "restored" is off by default: user.enabled does not cancel the edit
	// that tells the same thing.
	back := more
	back.Enable, back.UpdatedAt = true, 4
	w.Changed(more, back)
	if err := w.Event(addon.Event{ID: 22, Event: "user.enabled"}, back); err != nil {
		t.Fatal(err)
	}
	if got := queued(gdb, "admin_enabled"); len(got) != 1 || got[0].Status != model.DeliveryQueued {
		t.Fatalf("an event whose notice is off cancelled the edit: %+v", got)
	}
}

// TestAClockBehindThePanels: a user.expired for an expiry a few minutes
// ahead of Notif's clock is the panel's clock running ahead, and is told;
// an expiry well ahead is a renewal since, and is not.
func TestAClockBehindThePanels(t *testing.T) {
	w, gdb, now := setup(t)
	u := user(gdb, 1, func(u *model.User) { u.Expiry = now.Unix() + 5*60 })
	if err := w.Event(addon.Event{ID: 30, Event: "user.expired"}, u); err != nil {
		t.Fatal(err)
	}
	renewed := user(gdb, 2, func(u *model.User) { u.Expiry = now.Unix() + 3600 })
	if err := w.Event(addon.Event{ID: 31, Event: "user.expired"}, renewed); err != nil {
		t.Fatal(err)
	}
	if got := queued(gdb, "expired"); len(got) != 1 || got[0].UserID != 1 {
		t.Fatalf("expired: %+v", got)
	}
}
