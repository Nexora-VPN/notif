package outbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

// stub is a channel whose answer per user the test sets: "" sends, "none"
// has no address, "refuse" refuses, "fail" fails worth a retry.
type stub struct {
	mu      sync.Mutex
	answers map[string]string
	sent    map[string][]string // channel name → user names that got it
}

var stubs = &stub{answers: map[string]string{}, sent: map[string][]string{}}

type stubSender struct{ name string }

func (s stubSender) Send(_ context.Context, to channel.Recipient, m channel.Message) error {
	stubs.mu.Lock()
	defer stubs.mu.Unlock()
	switch stubs.answers[s.name+"/"+to.Name] {
	case "none":
		return channel.ErrNoAddress
	case "refuse":
		return &channel.Refused{Reason: "blocked"}
	case "fail":
		return &channel.Retry{Reason: "down"}
	case "blocked":
		return &channel.Refused{Reason: "blocked", Unlink: "telegram_id"}
	case "unknown":
		return &channel.Unknown{Reason: "timed out after the request"}
	}
	stubs.sent[s.name] = append(stubs.sent[s.name], to.Name+":"+m.Text)
	return nil
}

// linkSender records the link a channel's own template would use, as an
// SMS provider's variable map does.
type linkSender struct{ name string }

func (s linkSender) Send(_ context.Context, to channel.Recipient, m channel.Message) error {
	stubs.mu.Lock()
	defer stubs.mu.Unlock()
	stubs.sent[s.name] = append(stubs.sent[s.name], to.Name+":"+m.Vars["sub_url"])
	return nil
}

func init() {
	newStub := func(cfg map[string]string) (channel.Sender, error) { return stubSender{name: cfg["name"]}, nil }
	channel.Register(channel.Kind{Name: "stub", Fields: []channel.Field{{Key: "name"}}, New: newStub})
	// stubbot reaches a user by telegram_id, as a bot does.
	channel.Register(channel.Kind{Name: "stubbot", Contact: "telegram_id", Fields: []channel.Field{{Key: "name"}}, New: newStub})
	// stublink's settings may name the link, as an SMS template's do.
	channel.Register(channel.Kind{
		Name: "stublink", Fields: []channel.Field{{Key: "name"}, {Key: "variables"}},
		New: func(cfg map[string]string) (channel.Sender, error) { return linkSender{name: cfg["name"]}, nil },
	})
}

func reset() {
	stubs.mu.Lock()
	stubs.answers = map[string]string{}
	stubs.sent = map[string][]string{}
	stubs.mu.Unlock()
}

func sent(name string) []string {
	stubs.mu.Lock()
	defer stubs.mu.Unlock()
	return append([]string(nil), stubs.sent[name]...)
}

func open(t *testing.T) *gorm.DB {
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
	reset()
	return gdb
}

// fixture: two users and two stub channels, first then second, each at
// 600 a minute.
func fixture(t *testing.T) (*Outbox, *gorm.DB, *time.Time) {
	gdb := open(t)
	for _, u := range []model.User{{ID: 1, Name: "ana"}, {ID: 2, Name: "bob"}} {
		u.Contact = model.JSON[map[string]string]{V: map[string]string{}}
		if err := gdb.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, n := range []string{"first", "second"} {
		gdb.Create(&model.Channel{
			Kind: "stub", Name: n, Enabled: true, Position: i + 1, PerMinute: 600,
			Config: model.JSON[map[string]string]{V: map[string]string{"name": n}},
		})
	}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	o := New(gdb)
	o.Now = func() time.Time { return now }
	return o, gdb, &now
}

func status(t *testing.T, gdb *gorm.DB, key string) model.Delivery {
	t.Helper()
	var d model.Delivery
	if err := gdb.Where("key = ?", key).First(&d).Error; err != nil {
		t.Fatal(err)
	}
	return d
}

func custom(key string, user uint) model.Delivery {
	return model.Delivery{Key: key, UserID: user, Kind: "custom", Body: "hi {name}"}
}

// TestANoticeGoesOnceAcrossARestart: the same key queues once; a delivery
// caught mid-send by a restart is not sent again; one caught before its
// channel was called is.
func TestANoticeGoesOnceAcrossARestart(t *testing.T) {
	o, gdb, _ := fixture(t)
	ctx := context.Background()
	if ok, err := o.Enqueue(custom("k1", 1)); !ok || err != nil {
		t.Fatalf("enqueue: %v %v", ok, err)
	}
	if ok, _ := o.Enqueue(custom("k1", 1)); ok {
		t.Fatal("the same key queued twice")
	}
	o.ProcessDue(ctx)
	o.ProcessDue(ctx)
	if got := sent("first"); len(got) != 1 || got[0] != "ana:hi ana" {
		t.Fatalf("first got %v", got)
	}
	if d := status(t, gdb, "k1"); d.Status != model.DeliverySent || d.SentAt == 0 {
		t.Fatalf("k1 %+v", d)
	}

	// A restart: k2 was being sent (its attempt had started), k3 had been
	// claimed but no channel called yet.
	o.Enqueue(custom("k2", 2))
	o.Enqueue(custom("k3", 2))
	k2, k3 := status(t, gdb, "k2"), status(t, gdb, "k3")
	gdb.Model(&k2).Update("status", model.DeliverySending)
	gdb.Model(&k3).Update("status", model.DeliverySending)
	gdb.Create(&model.Attempt{DeliveryID: k2.ID, ChannelID: 1, At: 1, Outcome: model.AttemptStarted})
	o2 := New(gdb)
	o2.Now = o.Now
	if err := o2.Recover(); err != nil {
		t.Fatal(err)
	}
	o2.ProcessDue(ctx)
	if d := status(t, gdb, "k2"); d.Status != model.DeliveryUnknown {
		t.Fatalf("k2 after the restart: %s", d.Status)
	}
	if d := status(t, gdb, "k3"); d.Status != model.DeliverySent {
		t.Fatalf("k3 after the restart: %s", d.Status)
	}
	if got := sent("first"); len(got) != 2 {
		t.Fatalf("after the restart first got %v", got)
	}
}

// TestFallingToTheNextChannel: no address and a refusal pass on at once; a
// failure is tried three times, with waits, before the next channel is.
func TestFallingToTheNextChannel(t *testing.T) {
	o, gdb, now := fixture(t)
	ctx := context.Background()
	stubs.answers["first/ana"] = "refuse"
	stubs.answers["first/bob"] = "none"
	o.Enqueue(custom("a", 1))
	o.Enqueue(custom("b", 2))
	o.ProcessDue(ctx)
	if got := sent("second"); len(got) != 2 {
		t.Fatalf("second got %v", got)
	}
	if d := status(t, gdb, "a"); d.Status != model.DeliverySent || d.ChannelID != 2 {
		t.Fatalf("a %+v", d)
	}
	var as []model.Attempt
	gdb.Where("delivery_id = ?", status(t, gdb, "a").ID).Order("id").Find(&as)
	if len(as) != 2 || as[0].Outcome != model.AttemptRefused || as[1].Outcome != model.AttemptSent {
		t.Fatalf("a's attempts %+v", as)
	}

	reset()
	stubs.answers["first/ana"] = "fail"
	o.Enqueue(custom("c", 1))
	for i, wait := range Backoff[:Tries-1] {
		o.ProcessDue(ctx)
		d := status(t, gdb, "c")
		if d.Status != model.DeliveryQueued || d.NextAt != now.Add(wait).Unix() {
			t.Fatalf("after failure %d: %+v", i+1, d)
		}
		*now = now.Add(wait)
	}
	o.ProcessDue(ctx)
	if d := status(t, gdb, "c"); d.Status != model.DeliverySent || d.ChannelID != 2 {
		t.Fatalf("c after three failures: %+v", d)
	}

	reset()
	stubs.answers["first/bob"] = "refuse"
	stubs.answers["second/bob"] = "none"
	o.Enqueue(custom("d", 2))
	o.ProcessDue(ctx)
	if d := status(t, gdb, "d"); d.Status != model.DeliveryFailed || d.Error != "no channel reached the user" {
		t.Fatalf("d %+v", d)
	}
}

// TestQuietHoursHoldANotice until they end; an urgent one goes at once.
func TestQuietHoursHoldANotice(t *testing.T) {
	o, gdb, now := fixture(t)
	ctx := context.Background()
	if err := settings.SaveDelivery(gdb, settings.Delivery{TimeZone: "UTC", QuietEnabled: true, QuietFrom: "22:00", QuietTo: "08:00"}); err != nil {
		t.Fatal(err)
	}
	*now = time.Date(2026, 10, 4, 23, 30, 0, 0, time.UTC)
	o.Enqueue(custom("night", 1))
	urgent := custom("urgent", 2)
	urgent.Urgent = true
	o.Enqueue(urgent)
	o.ProcessDue(ctx)
	morning := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC).Unix()
	if d := status(t, gdb, "night"); d.Status != model.DeliveryHeld || d.NextAt != morning {
		t.Fatalf("night %+v", d)
	}
	if d := status(t, gdb, "urgent"); d.Status != model.DeliverySent {
		t.Fatalf("urgent %+v", d)
	}
	*now = time.Unix(morning, 0)
	o.ProcessDue(ctx)
	if d := status(t, gdb, "night"); d.Status != model.DeliverySent {
		t.Fatalf("night in the morning %+v", d)
	}
}

// TestAChannelKeepsItsRate: at one a minute the second notice waits a
// minute and is not counted as a failure.
func TestAChannelKeepsItsRate(t *testing.T) {
	o, gdb, now := fixture(t)
	ctx := context.Background()
	gdb.Model(&model.Channel{}).Where("id = 1").Update("per_minute", 1)
	o.Enqueue(custom("r1", 1))
	o.Enqueue(custom("r2", 2))
	o.ProcessDue(ctx)
	d := status(t, gdb, "r2")
	if d.Status != model.DeliveryQueued || d.NextAt != now.Add(time.Minute).Unix() {
		t.Fatalf("r2 %+v", d)
	}
	var n int64
	gdb.Model(&model.Attempt{}).Where("delivery_id = ?", d.ID).Count(&n)
	if n != 0 {
		t.Fatalf("a rate wait counted %d attempts", n)
	}
	*now = now.Add(time.Minute)
	o.ProcessDue(ctx)
	if got := sent("first"); len(got) != 2 {
		t.Fatalf("first got %v", got)
	}
}

// TestATestNamesOneChannelAndAGoneUserFails.
func TestATestNamesOneChannelAndAGoneUserFails(t *testing.T) {
	o, gdb, _ := fixture(t)
	ctx := context.Background()
	d := custom("only", 1)
	d.Only = 2
	o.Enqueue(d)
	stubs.answers["second/ana"] = "refuse"
	o.ProcessDue(ctx)
	if d := status(t, gdb, "only"); d.Status != model.DeliveryFailed || len(sent("first")) != 0 {
		t.Fatalf("only %+v, first got %v", d, sent("first"))
	}
	gdb.Model(&model.User{}).Where("id = 2").Update("gone_at", 1)
	o.Enqueue(custom("gone", 2))
	o.ProcessDue(ctx)
	if d := status(t, gdb, "gone"); d.Status != model.DeliveryFailed || d.Error != "the account is no longer on the panel" {
		t.Fatalf("gone %+v", d)
	}
	gdb.Model(&model.Channel{}).Where("1 = 1").Update("enabled", false)
	o.Enqueue(custom("none", 1))
	o.ProcessDue(ctx)
	if d := status(t, gdb, "none"); d.Error != "no channel is turned on" {
		t.Fatalf("none %+v", d)
	}
}

// lasting is a delivery whose key outlives the log, as a schedule line's.
func lasting(key string, user uint) model.Delivery {
	d := custom(key, user)
	d.Lasting = true
	return d
}

// TestPruneKeepsWhatIsYoung, and the lasting keys of what it deletes: a
// schedule line pruned from the log is not raised again until its key is
// older than any line; any other key holds only while its delivery is in
// the log, and keeps no row of its own.
func TestPruneKeepsWhatIsYoung(t *testing.T) {
	o, gdb, now := fixture(t)
	o.Enqueue(lasting("old", 1))
	o.Enqueue(custom("send:1:1", 1))
	o.ProcessDue(context.Background())
	gdb.Model(&model.Delivery{}).Where("key IN ?", []string{"old", "send:1:1"}).Update("created_at", now.AddDate(0, 0, -91).Unix())
	gdb.Model(&model.Once{}).Where("key = ?", "old").Update("at", now.AddDate(0, 0, -91).Unix())
	var rows int64
	gdb.Model(&model.Once{}).Where("key = ?", "send:1:1").Count(&rows)
	if rows != 0 {
		t.Fatal("a key that is not lasting was kept apart from the log")
	}
	if ok, _ := o.Enqueue(custom("send:1:1", 1)); ok {
		t.Fatal("a key raised again while its delivery is in the log")
	}
	o.Enqueue(lasting("young", 1))
	o.Prune()
	if ok, _ := o.Enqueue(custom("send:1:1", 1)); !ok {
		t.Fatal("a key that is not lasting still holds after the log forgot it")
	}
	gdb.Where("key = ?", "send:1:1").Delete(&model.Delivery{})
	var n int64
	gdb.Model(&model.Delivery{}).Count(&n)
	var a int64
	gdb.Model(&model.Attempt{}).Count(&a)
	if n != 1 || a != 0 {
		t.Fatalf("after prune: %d deliveries, %d attempts", n, a)
	}
	if ok, _ := o.Enqueue(lasting("old", 1)); ok {
		t.Fatal("a pruned notice was raised again")
	}
	if raised, _ := o.Raised([]string{"old", "young", "new"}); !raised["old"] || !raised["young"] || raised["new"] {
		t.Fatalf("raised %v", raised)
	}
	gdb.Model(&model.Once{}).Where("key = ?", "old").Update("at", now.AddDate(0, 0, -OnceDays-1).Unix())
	o.Prune()
	if ok, _ := o.Enqueue(lasting("old", 1)); !ok {
		t.Fatal("a key older than any schedule line still holds")
	}
}

// TestABlockedBotIsBlockedNotErased: a refusal that names a link blocks
// the channel for that chat and hands the chat to the unlink hook — which
// takes it off the card only when Notif wrote it — and the notice falls to
// the next channel; the next notice does not try the blocked chat, and a
// new chat on the card is tried again.
func TestABlockedBotIsBlockedNotErased(t *testing.T) {
	o, gdb, _ := fixture(t)
	gdb.Model(&model.User{}).Where("id = 1").Update("contact", model.JSON[map[string]string]{V: map[string]string{"telegram_id": "500"}})
	gdb.Model(&model.Channel{}).Where("id = 1").Update("kind", "stubbot")
	var got []string
	o.Unlink = func(id uint, key, value string) { got = append(got, fmt.Sprint(id, key, value)) }
	stubs.answers["first/ana"] = "blocked"
	o.Enqueue(custom("b", 1))
	o.ProcessDue(context.Background())
	if len(got) != 1 || got[0] != "1telegram_id500" || status(t, gdb, "b").ChannelID != 2 {
		t.Fatalf("unlinked %v, delivery %+v", got, status(t, gdb, "b"))
	}
	var b model.Block
	if gdb.First(&b).Error != nil || b.UserID != 1 || b.ChannelID != 1 || b.Value != "500" {
		t.Fatalf("block %+v", b)
	}
	var u model.User
	gdb.First(&u, 1)
	if u.Contact.V["telegram_id"] != "500" {
		t.Fatalf("the copy's card lost its chat: %v", u.Contact.V)
	}
	reset()
	o.Enqueue(custom("b2", 1))
	o.ProcessDue(context.Background())
	if len(sent("first")) != 0 || len(sent("second")) != 1 {
		t.Fatalf("the blocked chat was tried: first %v, second %v", sent("first"), sent("second"))
	}
	gdb.Model(&model.User{}).Where("id = 1").Update("contact", model.JSON[map[string]string]{V: map[string]string{"telegram_id": "501"}})
	o.Enqueue(custom("b3", 1))
	o.ProcessDue(context.Background())
	if len(sent("first")) != 1 {
		t.Fatalf("a new chat was not tried: %v", sent("first"))
	}
}

// TestNoAddressSpendsNoToken: an account with no address on a channel
// passes on without taking from the channel's rate, so the next account's
// notice is not held back by it.
func TestNoAddressSpendsNoToken(t *testing.T) {
	o, gdb, _ := fixture(t)
	gdb.Model(&model.Channel{}).Where("id = 1").Updates(map[string]any{"kind": "stubbot", "per_minute": 1})
	gdb.Model(&model.User{}).Where("id = 2").Update("contact", model.JSON[map[string]string]{V: map[string]string{"telegram_id": "7"}})
	o.Enqueue(custom("n1", 1))
	o.Enqueue(custom("n2", 2))
	o.ProcessDue(context.Background())
	if d := status(t, gdb, "n2"); d.Status != model.DeliverySent || d.ChannelID != 1 {
		t.Fatalf("n2 %+v", d)
	}
	if d := status(t, gdb, "n1"); d.Status != model.DeliverySent || d.ChannelID != 2 {
		t.Fatalf("n1 %+v", d)
	}
}

// TestASentAttemptIsSent: a stop between the attempt's "sent" and the
// delivery's leaves the delivery to be finished as sent, not sent again.
func TestASentAttemptIsSent(t *testing.T) {
	o, gdb, _ := fixture(t)
	o.Enqueue(custom("s", 1))
	d := status(t, gdb, "s")
	gdb.Create(&model.Attempt{DeliveryID: d.ID, ChannelID: 1, At: 5, Outcome: model.AttemptSent})
	o.ProcessDue(context.Background())
	if d := status(t, gdb, "s"); d.Status != model.DeliverySent || d.ChannelID != 1 || len(sent("first")) != 0 {
		t.Fatalf("s %+v, sent %v", d, sent("first"))
	}
}

// TestAnUnknownOutcomeIsNotSentAgain: a send whose outcome is unknown is
// tried neither again nor on the next channel.
func TestAnUnknownOutcomeIsNotSentAgain(t *testing.T) {
	o, gdb, now := fixture(t)
	stubs.answers["first/ana"] = "unknown"
	o.Enqueue(custom("u", 1))
	o.ProcessDue(context.Background())
	*now = now.Add(time.Hour)
	o.ProcessDue(context.Background())
	if d := status(t, gdb, "u"); d.Status != model.DeliveryUnknown || len(sent("second")) != 0 {
		t.Fatalf("u %+v, second %v", d, sent("second"))
	}
}

// TestTheLinkIsReadWhenNamed: a notice that names {sub_url} reads it from
// the panel at send time; a panel that does not answer holds the notice a
// minute without counting a try.
func TestTheLinkIsReadWhenNamed(t *testing.T) {
	o, gdb, now := fixture(t)
	reads := 0
	var fail error
	o.SubURL = func(_ context.Context, id uint) (string, error) {
		reads++
		return fmt.Sprintf("https://x.io/sub/%d", id), fail
	}
	d := custom("plain", 1)
	o.Enqueue(d)
	o.ProcessDue(context.Background())
	if reads != 0 {
		t.Fatalf("a notice without the link read it %d times", reads)
	}
	fail = errors.New("down")
	d = model.Delivery{Key: "link", UserID: 2, Kind: "custom", Body: "{sub_url}"}
	o.Enqueue(d)
	o.ProcessDue(context.Background())
	if got := status(t, gdb, "link"); got.Status != model.DeliveryQueued || got.NextAt != now.Add(time.Minute).Unix() {
		t.Fatalf("with the panel down: %+v", got)
	}
	fail = nil
	*now = now.Add(time.Minute)
	o.ProcessDue(context.Background())
	if got := sent("first"); len(got) != 2 || got[1] != "bob:https://x.io/sub/2" {
		t.Fatalf("sent %v", got)
	}

	// A channel whose own settings use the link (token3={sub_url}) has it
	// read for a notice whose words do not name it.
	gdb.Model(&model.Channel{}).Where("1 = 1").Update("enabled", false)
	gdb.Create(&model.Channel{
		Kind: "stublink", Name: "sms", Enabled: true, Position: 3,
		Config: model.JSON[map[string]string]{V: map[string]string{"name": "sms", "variables": "token3={sub_url}"}},
	})
	reads = 0
	o.Enqueue(custom("sms", 1))
	o.ProcessDue(context.Background())
	if got := sent("sms"); reads != 1 || len(got) != 1 || got[0] != "ana:https://x.io/sub/1" {
		t.Fatalf("the channel's own template: %d reads, sent %v", reads, got)
	}
}

// TestAChannelSwitchedOffStopsSending: the workers' read of the channels
// is used again for a while, and read anew when the admin web says they
// changed.
func TestAChannelSwitchedOffStopsSending(t *testing.T) {
	o, gdb, _ := fixture(t)
	p := o.current()
	if len(p.channels) != 2 || o.current() != p {
		t.Fatalf("the pass: %d channels, read again %v", len(p.channels), o.current() != p)
	}
	gdb.Model(&model.Channel{}).Where("name = ?", "first").Update("enabled", false)
	o.Refresh()
	order, _ := o.current().order(model.Delivery{})
	if len(order) != 1 || order[0].Name != "second" {
		t.Fatalf("after the switch-off: %+v", order)
	}
}

// TestAForgottenDeliveryIsDropped: a delivery already in a worker's hands
// when the copy was forgotten for another panel is cancelled, not sent to
// the new panel's account under the same id; one made after is sent.
func TestAForgottenDeliveryIsDropped(t *testing.T) {
	o, gdb, _ := fixture(t)
	o.Enqueue(custom("old", 1))
	if ids := o.claim(10); len(ids) != 1 {
		t.Fatalf("claimed %v", ids)
	}
	// What users.Sync.Forget does, and the new panel's account 1.
	d := status(t, gdb, "old")
	if err := settings.SetForgottenThrough(gdb, d.ID); err != nil {
		t.Fatal(err)
	}
	gdb.Model(&model.User{}).Where("id = 1").Update("name", "zed")
	o.process(context.Background(), o.snapshot(), d.ID)
	if d := status(t, gdb, "old"); d.Status != model.DeliveryCancelled || len(sent("first")) != 0 {
		t.Fatalf("the old panel's delivery: %s, sent %v", d.Status, sent("first"))
	}
	o.Enqueue(custom("new", 1))
	o.ProcessDue(context.Background())
	if got := sent("first"); len(got) != 1 || got[0] != "zed:hi zed" {
		t.Fatalf("the new panel's delivery: %v", got)
	}
}
