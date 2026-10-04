// Package notices turns a delivery into the words a user reads, in their
// language, for the channel it goes through. GN-S1 knows two notices — a
// channel test and the admin's own message; GN-S4 adds the account's
// notices with the admin's own texts.
package notices

import (
	"strings"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
)

// The notices GN-S1 sends.
const (
	KindTest   = "test"
	KindCustom = "custom"
)

type text struct{ title, body string }

// builtin are the defaults, per notice and language. They are strictly
// transactional — no price, offer or the word VPN (P40).
var builtin = map[string]map[string]text{
	KindTest: {
		"en": {"Test", "Hello {name}, this is a test from {channel}. Notices about your account will arrive here."},
		"fa": {"آزمایش", "سلام {name}، این پیام آزمایشی از {channel} است. خبرهای حساب شما از همین راه می‌رسد."},
		"ru": {"Проверка", "Здравствуйте, {name}! Это проверка канала {channel}. Уведомления о вашем аккаунте будут приходить сюда."},
		"zh": {"测试", "{name}，您好，这是来自 {channel} 的测试消息。有关您账户的通知将通过这里发送。"},
	},
}

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

// Render is a delivery's message for one channel.
func Render(d model.Delivery, u model.User, ch model.Channel, lang string) channel.Message {
	vars := map[string]string{"name": u.Name, "channel": ch.Name}
	for k, v := range d.Vars.V {
		vars[k] = v
	}
	m := channel.Message{Key: d.Key, Kind: d.Kind, Vars: vars}
	if d.Kind == KindCustom {
		m.Title, m.Text = fill(d.Title, vars), fill(d.Body, vars)
		return m
	}
	t, ok := builtin[d.Kind][lang]
	if !ok {
		t = builtin[d.Kind]["en"]
	}
	m.Title, m.Text = fill(t.title, vars), fill(t.body, vars)
	return m
}

func fill(s string, vars map[string]string) string { return channel.Fill(s, vars) }
