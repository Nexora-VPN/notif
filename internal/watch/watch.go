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

// reachable reports whether any channel that is on could reach the user —
// a notice to an account with no address anywhere is not queued, or every
// account without a linked chat would fill the log with failures.
func (w *Watch) reachable(u model.User) bool {
	var chs []model.Channel
	w.DB.Where("enabled = ?", true).Find(&chs)
	for _, c := range chs {
		key := AddressKey(c)
		if key == "" || strings.TrimSpace(u.Contact.V[key]) != "" {
			return true
		}
	}
	return false
}

// AddressKey is the contact key a channel reaches a user by, "" for a
// channel that needs none.
func AddressKey(c model.Channel) string {
	switch c.Kind {
	case "telegram":
		return "telegram_id"
	case "bale":
		return "bale_id"
	case "soroush":
		return "soroush_id"
	case "rubika":
		return "rubika_id"
	case "kavenegar", "faraz":
		return "phone"
	case "smtp":
		return "email"
	case "ntfy":
		return "ntfy"
	case "http":
		return strings.TrimSpace(c.Config.V["address"])
	}
	return ""
}

// raise queues one notice if its kind is on and the user can be reached.
func (w *Watch) raise(kind string, u model.User, key string, vars map[string]string, delay time.Duration) bool {
	book := notices.Load(w.DB)
	set := book.Setting(kind)
	if !set.Enabled || !w.reachable(u) {
		return false
	}
	d := model.Delivery{
		Key: key, UserID: u.ID, Kind: kind, Urgent: set.Urgent,
		Vars: model.JSON[map[string]string]{V: vars}, NextAt: w.now().Add(delay).Unix(),
	}
	queued, err := w.Outbox.Enqueue(d)
	if err != nil {
		log.Printf("watch: %s for %d: %v", kind, u.ID, err)
	}
	return queued
}

func facts(u model.User) map[string]string {
	return map[string]string{
		"expiry": strconv.FormatInt(u.Expiry, 10), "volume": strconv.FormatInt(u.Volume, 10), "used": strconv.FormatInt(u.Used, 10),
	}
}

var editKinds = []string{"traffic_added", "expiry_changed", "usage_reset", "admin_disabled", "admin_enabled"}

// supersede cancels an account's edit notices still waiting from the last
// few minutes: an event has just told the same change.
func (w *Watch) supersede(userID uint) {
	w.DB.Model(&model.Delivery{}).
		Where("user_id = ? AND kind IN ? AND status = ? AND created_at >= ?", userID, editKinds, model.DeliveryQueued, w.now().Add(-3*time.Minute).Unix()).
		Updates(map[string]any{"status": model.DeliveryCancelled, "error": "an event of the panel's told this change"})
}

// eventKinds are the single-account events and the notice each raises.
var eventKinds = map[string]string{
	"user.created": "created", "user.activated": "activated", "user.first_fetch": "first_fetch",
	"user.renewed": "renewed", "user.expired": "expired", "user.quota_reached": "quota_reached",
	"user.disabled": "disabled", "user.enabled": "restored", "user.device_limit_reached": "device_limit",
	"user.deleted": "deleted",
}

// Event raises the notice a panel event calls for, for the account as the
// copy has it after the event (read before the event marked it gone, for a
// deletion).
func (w *Watch) Event(e addon.Event, u model.User) {
	kind, ok := eventKinds[e.Event]
	if !ok || u.ID == 0 {
		return
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
		return
	}
	vars := facts(u)
	if data.Expiry > 0 {
		vars["expiry"] = strconv.FormatInt(data.Expiry, 10)
	}
	if data.Volume > 0 {
		vars["volume"] = strconv.FormatInt(data.Volume, 10)
	}
	w.supersede(u.ID)
	w.raise(kind, u, fmt.Sprintf("%s:%d:%d", kind, u.ID, e.ID), vars, 0)
}

// Deleted raises the deletion notice for an account a bulk operation
// removed.
func (w *Watch) Deleted(u model.User, eventID uint64) {
	w.raise("deleted", u, fmt.Sprintf("deleted:%d:%d", u.ID, eventID), facts(u), 0)
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
		w.raise("traffic_added", nu, fmt.Sprintf("traffic_added:%d:%s:%d", nu.ID, at, nu.Volume), vars, EditDelay)
	}
	if nu.Expiry != old.Expiry && nu.Expiry > 0 && !pending(nu) && !(pending(old) && nu.ActivatedAt > 0) {
		w.raise("expiry_changed", nu, fmt.Sprintf("expiry_changed:%d:%d", nu.ID, nu.Expiry), facts(nu), EditDelay)
	}
	if old.Used > 0 && nu.Used < old.Used/2 && nu.TotalUsed >= old.TotalUsed {
		w.raise("usage_reset", nu, fmt.Sprintf("usage_reset:%d:%d", nu.ID, nu.TotalUsed-nu.Used), facts(nu), EditDelay)
	}
	if old.Enable && !nu.Enable && nu.DisabledReason == "manual" {
		w.raise("admin_disabled", nu, fmt.Sprintf("admin_disabled:%d:%s", nu.ID, at), facts(nu), EditDelay)
	}
	if !old.Enable && nu.Enable && old.DisabledReason == "manual" {
		w.raise("admin_enabled", nu, fmt.Sprintf("admin_enabled:%d:%s", nu.ID, at), facts(nu), EditDelay)
	}
}

// Tick is one pass of the admin's schedule over every account: the nearest
// expiry line crossed, and the highest traffic line crossed, each told once
// — the key carries the expiry, or the allowance and the cycle, so a
// renewal, a top-up or a new cycle re-arms it.
func (w *Watch) Tick() {
	sched := settings.LoadSchedule(w.DB)
	if len(sched.ExpiryDays) == 0 && len(sched.TrafficPercents) == 0 {
		return
	}
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
				w.raise("expiring", u, fmt.Sprintf("expiring:%d:%d:%d", u.ID, u.Expiry, line), vars, 0)
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
				w.raise("traffic_warning", u, fmt.Sprintf("traffic:%d:%d:%d:%d", u.ID, u.Volume, u.TotalUsed-u.Used, line), vars, 0)
			}
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
