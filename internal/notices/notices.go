// Package notices turns a delivery into the words a user reads, in their
// language, for the channel it goes through. GN-S1 knows two notices — a
// channel test and the admin's own message; GN-S4 adds the account's
// notices with the admin's own texts.
package notices

import (
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

// The notices GN-S1 sends.
const (
	KindTest   = "test"
	KindCustom = "custom"
)

// Language is the user's: their contact card's "lang" when it is one Notif
// writes in, else the admin's default.
func Language(u model.User, fallback string) string {
	l := strings.ToLower(strings.TrimSpace(u.Contact.V["lang"]))
	for _, x := range settings.Languages {
		if x == l {
			return l
		}
	}
	return fallback
}

// Book is what rendering needs from the database: the admin's settings
// per notice, the calendar and the zone dates are written in.
type Book struct {
	notices  map[string]model.Notice
	calendar string
	loc      *time.Location
}

// Load reads it.
func Load(gdb *gorm.DB) Book {
	b := Book{notices: map[string]model.Notice{}, calendar: settings.LoadSchedule(gdb).Calendar}
	cfg, _ := settings.LoadDelivery(gdb)
	b.loc = cfg.Location()
	var rows []model.Notice
	gdb.Find(&rows)
	for _, n := range rows {
		b.notices[n.Kind] = n
	}
	return b
}

// Setting is a kind's setting: the admin's row, or the catalog's default.
func (b Book) Setting(kind string) model.Notice {
	if n, ok := b.notices[kind]; ok {
		return n
	}
	d, _ := Lookup(kind)
	return model.Notice{Kind: kind, Enabled: d.On}
}

// Text is a kind's words for a language and a channel kind: the admin's
// for that channel kind, else theirs for the language, else the default —
// field by field, so a text the admin wrote with no title keeps the
// default title.
func (b Book) Text(kind, lang, channelKind string) model.Text {
	out := Default(kind, lang)
	n, ok := b.notices[kind]
	if !ok {
		return out
	}
	layer := func(t model.Text) {
		if t.Title != "" {
			out.Title = t.Title
		}
		if t.Body != "" {
			out.Body = t.Body
		}
	}
	layer(n.Texts.V[lang][""])
	if channelKind != "" {
		layer(n.Texts.V[lang][channelKind])
	}
	return out
}

// Default is the built-in words.
func Default(kind, lang string) model.Text {
	t, ok := builtin[kind][lang]
	if !ok {
		t = builtin[kind]["en"]
	}
	return model.Text{Title: t.title, Body: t.body}
}

var unlimited = map[string][2]string{
	"en": {"unlimited", "no expiry"},
	"fa": {"نامحدود", "بدون انقضا"},
	"ru": {"безлимит", "бессрочно"},
	"zh": {"不限", "永不过期"},
}

// Vars are a notice's variables, written for a language: the figures the
// delivery recorded when it was raised, the account's own for the rest.
func (b Book) Vars(d model.Delivery, u model.User, lang string) map[string]string {
	raw := d.Vars.V
	num := func(k string, fallback int64) int64 {
		if v, ok := raw[k]; ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
		return fallback
	}
	un, ok := unlimited[lang]
	if !ok {
		un = unlimited["en"]
	}
	expiry, volume, used := num("expiry", u.Expiry), num("volume", u.Volume), num("used", u.Used)
	vars := map[string]string{"name": u.Name, "group": u.Group, "sub_url": u.SubURL}
	if expiry > 0 {
		vars["expiry"] = Date(expiry, lang, b.calendar, b.loc)
		days := (expiry - time.Now().Unix() + 86399) / 86400
		vars["days"] = Digits(lang, strconv.FormatInt(max(num("days", days), 0), 10))
	} else {
		vars["expiry"], vars["days"] = un[1], un[1]
	}
	vars["traffic_used"] = Traffic(used, lang)
	if volume > 0 {
		vars["traffic_total"] = Traffic(volume, lang)
		vars["traffic_left"] = Traffic(volume-used, lang)
		vars["percent"] = Digits(lang, strconv.FormatInt(num("percent", used*100/volume), 10))
	} else {
		vars["traffic_total"], vars["traffic_left"], vars["percent"] = un[0], un[0], Digits(lang, "0")
	}
	if a := num("added", 0); a > 0 {
		vars["added"] = Traffic(a, lang)
	}
	for k, v := range raw {
		if _, set := vars[k]; !set {
			vars[k] = v
		}
	}
	return vars
}

// Render is a delivery's message for one channel.
func (b Book) Render(d model.Delivery, u model.User, ch model.Channel, lang string) channel.Message {
	vars := b.Vars(d, u, lang)
	vars["channel"] = ch.Name
	m := channel.Message{Key: d.Key, Kind: d.Kind, Vars: vars}
	if d.Kind == KindCustom {
		m.Title, m.Text = fill(d.Title, vars), fill(d.Body, vars)
		return m
	}
	t := b.Text(d.Kind, lang, ch.Kind)
	m.Title, m.Text = fill(t.Title, vars), fill(t.Body, vars)
	return m
}

func fill(s string, vars map[string]string) string { return channel.Fill(s, vars) }
