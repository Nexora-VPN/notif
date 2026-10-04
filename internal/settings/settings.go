// Package settings keeps Notif's settings: named JSON documents in the
// settings table, each read with its defaults filled in.
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
