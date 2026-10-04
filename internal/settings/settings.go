// Package settings keeps Notif's settings: named JSON documents in the
// settings table, each read with its defaults filled in.
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/notif/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Delivery is how notices go out.
type Delivery struct {
	// TimeZone is where the quiet hours are counted, an IANA name; empty is
	// the server's own zone.
	TimeZone string `json:"timeZone"`
	// Quiet hours: between From and To ("22:00", "08:00", across midnight
	// when From is later) a notice that is not urgent waits for To.
	QuietEnabled bool   `json:"quietEnabled"`
	QuietFrom    string `json:"quietFrom"`
	QuietTo      string `json:"quietTo"`
	// Language is the users' language when nothing says otherwise.
	Language string `json:"language"`
	// RetentionDays is how long the log keeps a finished delivery.
	RetentionDays int `json:"retentionDays"`
}

// Languages are the ones Notif writes in.
var Languages = []string{"fa", "en", "ru", "zh"}

func (d *Delivery) defaults() {
	if d.QuietFrom == "" {
		d.QuietFrom = "22:00"
	}
	if d.QuietTo == "" {
		d.QuietTo = "08:00"
	}
	if d.Language == "" {
		d.Language = "en"
	}
	if d.RetentionDays <= 0 {
		d.RetentionDays = 90
	}
}

// Check refuses what cannot work.
func (d Delivery) Check() error {
	if d.TimeZone != "" {
		if _, err := time.LoadLocation(d.TimeZone); err != nil {
			return fmt.Errorf("unknown time zone %q", d.TimeZone)
		}
	}
	for _, v := range []string{d.QuietFrom, d.QuietTo} {
		if _, err := clock(v); err != nil {
			return err
		}
	}
	if d.QuietEnabled && d.QuietFrom == d.QuietTo {
		return errors.New("the quiet hours start and end at the same time")
	}
	ok := false
	for _, l := range Languages {
		ok = ok || l == d.Language
	}
	if !ok {
		return fmt.Errorf("unknown language %q", d.Language)
	}
	if d.RetentionDays < 1 || d.RetentionDays > 3650 {
		return errors.New("keep the log for 1 to 3650 days")
	}
	return nil
}

// Location is the zone the quiet hours are counted in.
func (d Delivery) Location() *time.Location {
	if d.TimeZone != "" {
		if loc, err := time.LoadLocation(d.TimeZone); err == nil {
			return loc
		}
	}
	return time.Local
}

// clock reads "HH:MM" as minutes after midnight.
func clock(v string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q is not a time of day (HH:MM)", v)
	}
	return hh*60 + mm, nil
}

// QuietUntil is when the quiet hours around t end, or the zero time when t
// is not inside them (or they are off).
func (d Delivery) QuietUntil(t time.Time) time.Time {
	if !d.QuietEnabled {
		return time.Time{}
	}
	from, err1 := clock(d.QuietFrom)
	to, err2 := clock(d.QuietTo)
	if err1 != nil || err2 != nil || from == to {
		return time.Time{}
	}
	local := t.In(d.Location())
	now := local.Hour()*60 + local.Minute()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	end := func(dayOffset int) time.Time {
		return midnight.AddDate(0, 0, dayOffset).Add(time.Duration(to) * time.Minute)
	}
	if from < to { // within one day, 01:00–06:00
		if now >= from && now < to {
			return end(0)
		}
		return time.Time{}
	}
	// Across midnight, 22:00–08:00.
	if now >= from {
		return end(1)
	}
	if now < to {
		return end(0)
	}
	return time.Time{}
}

const keyDelivery = "delivery"

// LoadDelivery reads the delivery settings with their defaults.
func LoadDelivery(gdb *gorm.DB) (Delivery, error) {
	var d Delivery
	err := load(gdb, keyDelivery, &d)
	d.defaults()
	return d, err
}

// SaveDelivery checks and writes them.
func SaveDelivery(gdb *gorm.DB, d Delivery) error {
	d.defaults()
	if err := d.Check(); err != nil {
		return err
	}
	return save(gdb, keyDelivery, d)
}

func load(gdb *gorm.DB, key string, v any) error {
	var row model.Setting
	err := gdb.Where("key = ?", key).Limit(1).Find(&row).Error
	if err != nil || row.Value == "" {
		return err
	}
	return json.Unmarshal([]byte(row.Value), v)
}

func save(gdb *gorm.DB, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return gdb.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Setting{Key: key, Value: string(b)}).Error
}

const keySecret = "secret"

// Secret is Notif's own secret — link codes and ntfy topics are derived from
// it — drawn on first use and kept.
func Secret(gdb *gorm.DB) ([]byte, error) {
	var hexed string
	if err := load(gdb, keySecret, &hexed); err != nil {
		return nil, err
	}
	if b, err := hex.DecodeString(hexed); err == nil && len(b) == 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	// Two first starts must not draw two secrets: the row is created only
	// when absent, and whatever is there afterwards is the secret.
	raw, _ := json.Marshal(hex.EncodeToString(b))
	if err := gdb.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Setting{Key: keySecret, Value: string(raw)}).Error; err != nil {
		return nil, err
	}
	if err := load(gdb, keySecret, &hexed); err != nil {
		return nil, err
	}
	return hex.DecodeString(hexed)
}

// Offset is where a bot's updates were read up to.
func Offset(gdb *gorm.DB, channelID uint) int64 {
	var n int64
	_ = load(gdb, fmt.Sprintf("bot_offset:%d", channelID), &n)
	return n
}

// SetOffset records it.
func SetOffset(gdb *gorm.DB, channelID uint, n int64) error {
	return save(gdb, fmt.Sprintf("bot_offset:%d", channelID), n)
}

// OffsetText is where an API with string offsets (Rubika's) was read up to.
func OffsetText(gdb *gorm.DB, channelID uint) string {
	var v string
	_ = load(gdb, fmt.Sprintf("bot_offset_text:%d", channelID), &v)
	return v
}

// SetOffsetText records it.
func SetOffsetText(gdb *gorm.DB, channelID uint, v string) error {
	return save(gdb, fmt.Sprintf("bot_offset_text:%d", channelID), v)
}

// Schedule is the admin's own timing for the notices nobody's event
// raises (GN-S4): how many days before the expiry, and at what share of
// the traffic, a user hears. Each line is said once and re-arms itself: a
// renewal moves the expiry, a top-up or a new cycle moves the traffic.
type Schedule struct {
	ExpiryDays      []int `json:"expiryDays"`
	TrafficPercents []int `json:"trafficPercents"`
	// Calendar is how dates are written: "auto" (the Persian calendar in
	// Persian, the Gregorian elsewhere), "jalali" or "gregorian".
	Calendar string `json:"calendar"`
}

const keySchedule = "schedule"

func (s *Schedule) defaults(stored bool) {
	if !stored {
		s.ExpiryDays = []int{3, 1}
		s.TrafficPercents = []int{80, 95}
	}
	if s.Calendar == "" {
		s.Calendar = "auto"
	}
	s.ExpiryDays = tidy(s.ExpiryDays, true)
	s.TrafficPercents = tidy(s.TrafficPercents, false)
}

// tidy sorts and drops repeats: days most distant first, percents lowest
// first.
func tidy(v []int, desc bool) []int {
	out := []int{}
	seen := map[int]bool{}
	for _, n := range v {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	slices.Sort(out)
	if desc {
		slices.Reverse(out)
	}
	return out
}

// Check refuses a schedule that cannot work.
func (s Schedule) Check() error {
	if len(s.ExpiryDays) > 10 || len(s.TrafficPercents) > 10 {
		return errors.New("ten lines at most of each")
	}
	for _, d := range s.ExpiryDays {
		if d < 1 || d > 365 {
			return fmt.Errorf("%d is not 1 to 365 days", d)
		}
	}
	for _, p := range s.TrafficPercents {
		if p < 1 || p > 99 {
			return fmt.Errorf("%d is not 1 to 99 percent", p)
		}
	}
	switch s.Calendar {
	case "auto", "jalali", "gregorian":
	default:
		return errors.New("calendar is auto, jalali or gregorian")
	}
	return nil
}

// LoadSchedule reads the schedule with its defaults.
func LoadSchedule(gdb *gorm.DB) Schedule {
	var row model.Setting
	var s Schedule
	stored := gdb.Where("key = ?", keySchedule).Limit(1).Find(&row).Error == nil && row.Value != ""
	if stored {
		_ = json.Unmarshal([]byte(row.Value), &s)
	}
	s.defaults(stored)
	return s
}

// SaveSchedule checks and writes it; an empty list switches that family off.
func SaveSchedule(gdb *gorm.DB, s Schedule) error {
	if s.ExpiryDays == nil {
		s.ExpiryDays = []int{}
	}
	if s.TrafficPercents == nil {
		s.TrafficPercents = []int{}
	}
	s.defaults(true)
	if err := s.Check(); err != nil {
		return err
	}
	return save(gdb, keySchedule, s)
}

const keyRebase = "rebase_pending"

// MarkRebase records that the panel was restored and a read that writes
// every account as it is now (users.Sync.Rebase) is owed; the mark is a
// fresh one each time, so a pass that began before a second restore does
// not clear the second's.
func MarkRebase(gdb *gorm.DB) error {
	return save(gdb, keyRebase, strconv.FormatInt(time.Now().UnixNano(), 10))
}

// RebaseOwed is the mark of the rebase owed, "" for none.
func RebaseOwed(gdb *gorm.DB) (string, error) {
	var mark string
	return mark, load(gdb, keyRebase, &mark)
}

// RebaseDone clears the mark when it is still mark: the pass that read it
// has completed.
func RebaseDone(gdb *gorm.DB, mark string) error {
	raw, _ := json.Marshal(mark)
	return gdb.Where("key = ? AND value = ?", keyRebase, string(raw)).Delete(&model.Setting{}).Error
}

// RebaseDropped clears any mark: the copy it was owed to is forgotten.
func RebaseDropped(gdb *gorm.DB) error {
	return gdb.Where("key = ?", keyRebase).Delete(&model.Setting{}).Error
}

const keyForgotten = "forgotten_through"

// SetForgottenThrough records the last delivery made before the copy of the
// accounts was forgotten (users.Sync.Forget): it and every one before it
// were meant for the accounts of a panel Notif has left.
func SetForgottenThrough(gdb *gorm.DB, id uint) error {
	return save(gdb, keyForgotten, id)
}

// ForgottenThrough is that delivery's id, 0 when the copy was never
// forgotten.
func ForgottenThrough(gdb *gorm.DB) (uint, error) {
	var id uint
	return id, load(gdb, keyForgotten, &id)
}
