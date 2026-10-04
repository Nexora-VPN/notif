// Package watch decides when a user hears something (GN-S4): from the
// panel's events, from edits a read of the account finds, and from the
// admin's own schedule of expiry and traffic notices. It only decides — it
// queues deliveries, each once by its key, and the outbox sends them.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/outbox"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

// EditDelay is how long an edit's notice waits: the panel raises an event
// for a renewal, and a read may see the renewal first — the event's notice
// then takes the edit's place instead of joining it.
const EditDelay = 90 * time.Second

// Watch raises notices.
type Watch struct {
	DB     *gorm.DB
	Outbox *outbox.Outbox
	Now    func() time.Time
}

func (w *Watch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// state is what raising a notice reads: the admin's notices and the
// channels that are on. A pass over many accounts reads it once.
type state struct {
	book notices.Book
	on   []model.Channel
}

func (w *Watch) state() (state, error) {
	st := state{book: notices.Load(w.DB)}
	return st, w.DB.Where("enabled = ?", true).Find(&st.on).Error
}

// reachable reports whether any channel that is on could reach the user —
// a notice to an account with no address anywhere is not queued, or every
// account without a linked chat would fill the log with failures.
func (st state) reachable(u model.User) bool {
	for _, c := range st.on {
		key := AddressKey(c)
		if key == "" || strings.TrimSpace(u.Contact.V[key]) != "" {
			return true
		}
	}
	return false
}

// AddressKey is the contact key a channel reaches a user by, "" for a
// channel that needs none.
func AddressKey(c model.Channel) string { return channel.AddressKey(c.Kind, c.Config.V) }

// wants reports whether a notice of kind is on and could reach u.
func (st state) wants(kind string, u model.User) (model.Notice, bool) {
	set := st.book.Setting(kind)
	return set, set.Enabled && st.reachable(u)
}

// raise queues one notice if its kind is on and the user can be reached.
func (w *Watch) raise(kind string, u model.User, key string, vars map[string]string, delay time.Duration) (bool, error) {
	st, err := w.state()
	if err != nil {
		return false, err
	}
	return w.raiseIn(st, kind, u, key, vars, delay)
}

func (w *Watch) raiseIn(st state, kind string, u model.User, key string, vars map[string]string, delay time.Duration) (bool, error) {
	return w.enqueue(st, model.Delivery{Key: key, UserID: u.ID, Kind: kind, NextAt: w.now().Add(delay).Unix()}, u, vars)
}

// raiseLasting is raiseIn for a schedule line, whose key outlives the log.
func (w *Watch) raiseLasting(st state, kind string, u model.User, key string, vars map[string]string) (bool, error) {
	return w.enqueue(st, model.Delivery{Key: key, UserID: u.ID, Kind: kind, NextAt: w.now().Unix(), Lasting: true}, u, vars)
}

// enqueue queues d if its kind is on and the user can be reached.
func (w *Watch) enqueue(st state, d model.Delivery, u model.User, vars map[string]string) (bool, error) {
	set, ok := st.wants(d.Kind, u)
	if !ok {
		return false, nil
	}
	d.Urgent = set.Urgent
	d.Vars = model.JSON[map[string]string]{V: vars}
	queued, err := w.Outbox.Enqueue(d)
	if err != nil {
		return false, fmt.Errorf("%s for %d: %w", d.Kind, u.ID, err)
	}
	return queued, nil
}

// raiseLogged is raise for a caller with nobody to hand an error to.
func (w *Watch) raiseLogged(kind string, u model.User, key string, vars map[string]string, delay time.Duration) {
	if _, err := w.raise(kind, u, key, vars, delay); err != nil {
		log.Printf("watch: %v", err)
	}
}

func facts(u model.User) map[string]string {
	return map[string]string{
		"expiry": strconv.FormatInt(u.Expiry, 10), "volume": strconv.FormatInt(u.Volume, 10), "used": strconv.FormatInt(u.Used, 10),
	}
}

// supersedes are the edit notices each event's own notice tells in their
// place: a renewal tells the new date and allowance (and a new cycle's
// reset), activation the date, switching an account on what a person did.
// An event outside the list cancels nothing — a delayed user.expired says
// nothing about traffic someone added.
var supersedes = map[string][]string{
	"renewed":   {"expiry_changed", "traffic_added", "usage_reset"},
	"activated": {"expiry_changed"},
	"restored":  {"admin_enabled"},
}

// supersede cancels an account's edit notices of kinds still waiting from
// the last few minutes: an event has just told the same change.
func (w *Watch) supersede(userID uint, kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	return w.DB.Model(&model.Delivery{}).
		Where("user_id = ? AND kind IN ? AND status = ? AND created_at >= ?", userID, kinds, model.DeliveryQueued, w.now().Add(-3*time.Minute).Unix()).
		Updates(map[string]any{"status": model.DeliveryCancelled, "error": "an event of the panel's told this change"}).Error
}

// eventKinds are the single-account events and the notice each raises.
var eventKinds = map[string]string{
	"user.created": "created", "user.activated": "activated", "user.first_fetch": "first_fetch",
	"user.renewed": "renewed", "user.expired": "expired", "user.quota_reached": "quota_reached",
	"user.disabled": "disabled", "user.enabled": "restored", "user.device_limit_reached": "device_limit",
	"user.deleted": "deleted",
}

// ClockMargin is how far ahead of Notif's clock an expiry may lie and
// still be the one a user.expired told: the host's clock and the panel's
// may differ by that much.
const ClockMargin = 10 * time.Minute

// outdated reports whether the account as it is now contradicts what the
// event says happened — a user.expired that arrives after a renewal, a
// user.enabled after the account was switched off again — so its notice
// would tell something no longer true.
func outdated(kind string, u model.User, now int64) bool {
	switch kind {
	case "expired":
		// Notif's clock may run behind the panel's: only an expiry well
		// ahead of it says the account was renewed since.
		return u.Expiry <= 0 || u.Expiry > now+int64(ClockMargin/time.Second)
	case "quota_reached":
		return u.Volume <= 0 || u.Used < u.Volume
	case "disabled":
		return u.Enable
	case "restored":
		return !u.Enable
	case "activated":
		return u.ActivatedAt == 0
	}
	return false
}

// Event raises the notice a panel event calls for, for the account as the
// copy has it after the event (read before the event marked it gone, for a
// deletion). Its error is worth the panel delivering the event again.
func (w *Watch) Event(e addon.Event, u model.User) error {
	kind, ok := eventKinds[e.Event]
	if !ok || u.ID == 0 {
		return nil
	}
	var data struct {
		Reason string `json:"reason"`
		Expiry int64  `json:"expiry"`
		Volume int64  `json:"volume"`
	}
	_ = json.Unmarshal(e.Data, &data)
	// The enforcer's disabling for the account's own expiry or volume is
	// user.expired / user.quota_reached as well; only a reseller's
	// exhausted allowance is told as "disabled".
	if kind == "disabled" && !strings.HasPrefix(data.Reason, "resale") {
		return nil
	}
	if outdated(kind, u, w.now().Unix()) {
		return nil
	}
	vars := facts(u)
	if data.Expiry > 0 {
		vars["expiry"] = strconv.FormatInt(data.Expiry, 10)
	}
	if data.Volume > 0 {
		vars["volume"] = strconv.FormatInt(data.Volume, 10)
	}
	st, err := w.state()
	if err != nil {
		return err
	}
	// Only a notice that goes out takes the edits' place: with it off, or
	// no way to reach the user, the edits still tell what happened.
	if _, ok := st.wants(kind, u); !ok {
		return nil
	}
	if err := w.supersede(u.ID, supersedes[kind]); err != nil {
		return err
	}
	_, err = w.raiseIn(st, kind, u, eventKey(kind, u.ID, e), vars, 0)
	return err
}

// eventKey is an event's notice's once-only key: the event's id and its
// time, so the same delivery retried is one notice while a panel restored
// to an earlier state, or another panel, counting its ids again from where
// they once were, raises new ones.
func eventKey(kind string, userID uint, e addon.Event) string {
	return fmt.Sprintf("%s:%d:%d:%d", kind, userID, e.ID, e.Time)
}

// Deleted raises the deletion notice for an account a bulk operation
// removed.
func (w *Watch) Deleted(u model.User, e addon.Event) error {
	_, err := w.raise("deleted", u, eventKey("deleted", u.ID, e), facts(u), 0)
	return err
}

// pending is a plan counted from the first connection that has not had
// one: its expiry is provisional.
func pending(u model.User) bool { return u.Duration > 0 && u.ActivatedAt == 0 }

// Changed raises the notices for what an admin changed on an account
// without an event: traffic added, the date moved, usage reset, switched
// off or on by hand. Each waits EditDelay, in case an event tells it.
func (w *Watch) Changed(old, nu model.User) {
	at := strconv.FormatInt(nu.UpdatedAt, 10)
	if nu.Volume > old.Volume && old.Volume > 0 {
		vars := facts(nu)
		vars["added"] = strconv.FormatInt(nu.Volume-old.Volume, 10)
		w.raiseLogged("traffic_added", nu, fmt.Sprintf("traffic_added:%d:%s:%d", nu.ID, at, nu.Volume), vars, EditDelay)
	}
	if nu.Expiry != old.Expiry && nu.Expiry > 0 && !pending(nu) && !(pending(old) && nu.ActivatedAt > 0) {
		w.raiseLogged("expiry_changed", nu, fmt.Sprintf("expiry_changed:%d:%d", nu.ID, nu.Expiry), facts(nu), EditDelay)
	}
	if old.Used > 0 && nu.Used < old.Used/2 && nu.TotalUsed >= old.TotalUsed {
		w.raiseLogged("usage_reset", nu, fmt.Sprintf("usage_reset:%d:%d", nu.ID, nu.TotalUsed-nu.Used), facts(nu), EditDelay)
	}
	if old.Enable && !nu.Enable && nu.DisabledReason == "manual" {
		w.raiseLogged("admin_disabled", nu, fmt.Sprintf("admin_disabled:%d:%s", nu.ID, at), facts(nu), EditDelay)
	}
	if !old.Enable && nu.Enable && old.DisabledReason == "manual" {
		w.raiseLogged("admin_enabled", nu, fmt.Sprintf("admin_enabled:%d:%s", nu.ID, at), facts(nu), EditDelay)
	}
}

// Tick is one pass of the admin's schedule over every account: the nearest
// expiry line crossed, and the highest traffic line crossed, each told once
// — the key carries the expiry, or the allowance and the cycle, so a
// renewal, a top-up or a new cycle re-arms it. The notices, the channels
// and the keys already raised are read once for the pass, not per account.
func (w *Watch) Tick() {
	sched := settings.LoadSchedule(w.DB)
	if len(sched.ExpiryDays) == 0 && len(sched.TrafficPercents) == 0 {
		return
	}
	st, err := w.state()
	if err != nil {
		log.Printf("watch: the schedule: %v", err)
		return
	}
	type due struct {
		kind, key string
		u         model.User
		vars      map[string]string
	}
	var dues []due
	now := w.now().Unix()
	var us []model.User
	w.DB.Where("gone_at = 0 AND enable = ?", true).Find(&us)
	for _, u := range us {
		if u.Expiry > now && !pending(u) {
			left := u.Expiry - now
			line := 0
			for _, d := range sched.ExpiryDays { // most distant first
				if left <= int64(d)*86400 {
					line = d
				}
			}
			if line > 0 {
				vars := facts(u)
				vars["days"] = strconv.FormatInt((left+86399)/86400, 10)
				dues = append(dues, due{"expiring", fmt.Sprintf("expiring:%d:%d:%d", u.ID, u.Expiry, line), u, vars})
			}
		}
		if u.Volume > 0 && u.Used < u.Volume {
			pct := u.Used * 100 / u.Volume
			line := 0
			for _, p := range sched.TrafficPercents { // lowest first
				if pct >= int64(p) {
					line = p
				}
			}
			if line > 0 {
				vars := facts(u)
				vars["percent"] = strconv.FormatInt(pct, 10)
				dues = append(dues, due{"traffic_warning", fmt.Sprintf("traffic:%d:%d:%d:%d", u.ID, u.Volume, u.TotalUsed-u.Used, line), u, vars})
			}
		}
	}
	keys := make([]string, 0, len(dues))
	for _, d := range dues {
		keys = append(keys, d.key)
	}
	raised, err := w.Outbox.Raised(keys)
	if err != nil {
		log.Printf("watch: the schedule: %v", err)
		return
	}
	for _, d := range dues {
		if raised[d.key] {
			continue
		}
		if _, err := w.raiseLasting(st, d.kind, d.u, d.key, d.vars); err != nil {
			log.Printf("watch: %v", err)
		}
	}
}

// Run passes the schedule every five minutes until ctx ends.
func (w *Watch) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	for {
		w.Tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
