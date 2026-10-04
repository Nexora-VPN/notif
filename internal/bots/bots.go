// Package bots reads what users write to Notif's bots — one long poll per
// bot channel, the only reader of its token — and links a chat to an
// account: a subscription link or a link code connects it, /stop
// disconnects it. The link is written to the account's contact card on the
// panel (internal/links).
package bots

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/addon-kit/telegram"
	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/links"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

// Manager runs one reader per enabled bot channel, restarting it when the
// channel's settings change.
type Manager struct {
	DB    *gorm.DB
	Links *links.Links
	// Wait is how long one poll waits for updates.
	Wait time.Duration

	mu      sync.Mutex
	running map[uint]reader
	// tries limits how often one chat may try to link: a subscription token
	// is a secret, and a bot must not be a way to guess one.
	tries *auth.Limiter
}

type reader struct {
	version string
	stop    context.CancelFunc
	done    chan struct{}
}

// New is a manager.
func New(gdb *gorm.DB, l *links.Links) *Manager {
	return &Manager{DB: gdb, Links: l, Wait: 25 * time.Second, running: map[uint]reader{}, tries: auth.NewLimiter(5, 10*time.Minute)}
}

// Run keeps the readers in step with the channels until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		m.Reconcile(ctx)
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-t.C:
		}
	}
}

func isBot(kind string) bool {
	for _, k := range channel.BotKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Reconcile starts, restarts and stops readers to match the channels.
func (m *Manager) Reconcile(ctx context.Context) {
	var chs []model.Channel
	m.DB.Where("enabled = ?", true).Find(&chs)
	want := map[uint]model.Channel{}
	for _, c := range chs {
		if isBot(c.Kind) {
			want[c.ID] = c
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.running {
		c, ok := want[id]
		if !ok || version(c) != r.version {
			r.stop()
			<-r.done
			delete(m.running, id)
		}
	}
	for id, c := range want {
		if _, ok := m.running[id]; ok {
			continue
		}
		bot, err := channel.NewBot(c.Kind, c.Config.V)
		if err != nil {
			m.state(c.ID, map[string]string{"error": err.Error()})
			continue
		}
		rctx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		m.running[id] = reader{version: version(c), stop: stop, done: done}
		go func() {
			defer close(done)
			m.read(rctx, c, bot)
		}()
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.running {
		r.stop()
		<-r.done
		delete(m.running, id)
	}
}

func version(c model.Channel) string {
	v, _ := c.Config.Value()
	return fmt.Sprint(v)
}

// state records what a channel reports about itself.
func (m *Manager) state(id uint, st map[string]string) {
	m.DB.Model(&model.Channel{}).Where("id = ?", id).Update("state", model.JSON[map[string]string]{V: st})
}

// read polls one bot until ctx ends.
func (m *Manager) read(ctx context.Context, c model.Channel, bot *channel.Bot) {
	st := map[string]string{}
	if me, err := bot.API.Me(ctx); err != nil {
		st["error"] = err.Error()
	} else {
		st["username"] = me.Username
	}
	m.state(c.ID, st)
	offset := settings.Offset(m.DB, c.ID)
	for ctx.Err() == nil {
		ups, err := bot.API.Updates(ctx, offset, m.Wait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var e *telegram.Error
			wait := 5 * time.Second
			msg := err.Error()
			if errors.As(err, &e) && e.Code == 409 {
				msg = "another program reads this bot's updates (a webhook, or the same token in Shop): give Notif a bot of its own"
				wait = 30 * time.Second
			}
			st["error"] = msg
			m.state(c.ID, st)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		if st["error"] != "" {
			delete(st, "error")
			m.state(c.ID, st)
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			if u.Message != nil {
				m.Handle(ctx, bot, *u.Message)
			}
		}
		if len(ups) > 0 {
			if err := settings.SetOffset(m.DB, c.ID, offset); err != nil {
				log.Printf("bots: offset of channel %d: %v", c.ID, err)
			}
		}
	}
}

// Handle answers one message.
func (m *Manager) Handle(ctx context.Context, bot *channel.Bot, msg telegram.Message) {
	if msg.Chat.Type != "" && msg.Chat.Type != "private" {
		return
	}
	chat := msg.Chat.ID
	chatID := strconv.FormatInt(chat, 10)
	lang := m.language(msg.From)
	reply := func(key string, vars map[string]string) {
		if err := bot.SendText(ctx, bot.API, chat, "", notices.Bot(key, lang, vars)); err != nil {
			log.Printf("bots: reply to %d: %v", chat, err)
		}
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	cmd, arg, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@") // /stop@my_bot in a client that adds it
	switch strings.ToLower(cmd) {
	case "/stop":
		linked := m.Links.Linked(bot.Contact, chatID)
		if len(linked) == 0 {
			reply("notLinked", nil)
			return
		}
		for _, u := range linked {
			if err := m.Links.Set(ctx, u.ID, bot.Contact, ""); err != nil {
				log.Printf("bots: unlink %d: %v", u.ID, err)
				reply("failed", nil)
				return
			}
		}
		reply("stopped", nil)
		return
	case "/start":
		text = strings.TrimSpace(arg)
		if text == "" {
			reply("welcome", nil)
			return
		}
	}
	key := bot.Kind + ":" + chatID
	if m.tries.Locked(key) {
		reply("tooMany", nil)
		return
	}
	u, ok := m.account(text)
	if !ok {
		m.tries.Fail(key)
		reply("notFound", nil)
		return
	}
	// The chat is the link; the messenger's language, when the card has
	// none, is the language the user's notices are written in.
	err := m.Links.Update(ctx, u.ID, func(c map[string]string) {
		c[bot.Contact] = chatID
		if c["lang"] == "" && m.known(msg.From) {
			c["lang"] = lang
		}
	})
	if err != nil {
		log.Printf("bots: link %d to %s %s: %v", u.ID, bot.Kind, chatID, err)
		reply("failed", nil)
		return
	}
	reply("linked", map[string]string{"name": u.Name})
}

// account is the one a message names: a link code, else a subscription
// link or token.
func (m *Manager) account(text string) (model.User, bool) {
	var u model.User
	if id, ok := m.Links.ByCode(text); ok {
		return u, m.DB.Where("gone_at = 0").First(&u, id).Error == nil
	}
	return m.Links.BySub(text)
}

// known reports whether the messenger said a language Notif writes in.
func (m *Manager) known(from *telegram.User) bool {
	if from == nil || len(from.LanguageCode) < 2 {
		return false
	}
	return slices.Contains(settings.Languages, strings.ToLower(from.LanguageCode[:2]))
}

// language is the user's messenger language when Notif writes in it, else
// the admin's default.
func (m *Manager) language(from *telegram.User) string {
	cfg, _ := settings.LoadDelivery(m.DB)
	if from != nil {
		code := strings.ToLower(from.LanguageCode)
		if len(code) > 2 {
			code = code[:2]
		}
		for _, l := range settings.Languages {
			if l == code {
				return l
			}
		}
	}
	return cfg.Language
}
