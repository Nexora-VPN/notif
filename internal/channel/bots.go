package channel

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/addon-kit/telegram"
)

// The bots (P33, P38): Telegram, Bale and Soroush Plus speak one Bot API,
// so one adapter serves the three at their own addresses. A bot writes only
// to a chat that has started it; the chat id is the user's link, kept on
// the panel in the contact key the kind names (P36 (iii)).
//
// Bale renders every text as its own Markdown and takes no parse_mode; it
// also limits how fast a bot may write to users who are not talking to it,
// and offers a paid business API (the same methods under /business) for
// exactly that, which the "business" switch selects for sending.

// The bot addresses.
const (
	SoroushAPI = "https://api.splus.ir"
)

// BotKinds are the kinds that are bots.
var BotKinds = []string{"telegram", "bale", "soroush"}

func init() {
	common := func(base string) []Field {
		return []Field{
			{Key: "token", Secret: true, Required: true},
			{Key: "apiBase", Default: base},
			{Key: "proxy"},
		}
	}
	Register(Kind{Name: "telegram", Contact: "telegram_id", PerMinute: 1200, Fields: common(telegram.TelegramAPI), New: newBot("telegram")})
	Register(Kind{
		Name: "bale", Contact: "bale_id", PerMinute: 300,
		Fields: append(common(telegram.BaleAPI), Field{Key: "business", Default: "off", Choices: []string{"off", "on"}}),
		New:    newBot("bale"),
	})
	Register(Kind{Name: "soroush", Contact: "soroush_id", PerMinute: 600, Fields: common(SoroushAPI), New: newBot("soroush")})
}

// Bot is a bot channel's sender, and the reader of its updates
// (internal/bots).
type Bot struct {
	Kind    string
	Contact string
	// API is the bot for reading and replying; Out is the one notices go
	// out through (Bale's business API when it is switched on).
	API telegram.Bot
	Out telegram.Bot
}

func newBot(kind string) func(cfg map[string]string) (Sender, error) {
	return func(cfg map[string]string) (Sender, error) {
		b, err := NewBot(kind, cfg)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

// NewBot builds a bot from a channel's settings.
func NewBot(kind string, cfg map[string]string) (*Bot, error) {
	k, ok := Lookup(kind)
	if !ok || k.Contact == "" {
		return nil, fmt.Errorf("%q is not a bot", kind)
	}
	token := strings.TrimSpace(cfg["token"])
	if id, _, ok := strings.Cut(token, ":"); !ok || id == "" {
		return nil, errors.New("a bot token is id:secret, as the BotFather gives it")
	}
	client, err := telegram.HTTPClient(strings.TrimSpace(cfg["proxy"]))
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(cfg["apiBase"]), "/")
	if base != "" && !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return nil, errors.New("apiBase must be an http:// or https:// address")
	}
	b := &Bot{Kind: kind, Contact: k.Contact, API: telegram.Bot{Token: token, APIBase: base, HTTP: client}}
	b.Out = b.API
	if kind == "bale" && cfg["business"] == "on" {
		if base == "" {
			base = telegram.BaleAPI
		}
		b.Out.APIBase = base + "/business"
	}
	return b, nil
}

// Format is a notice as the bot writes it: the title in bold over the text,
// as HTML for Telegram and Soroush Plus and as Bale's Markdown for Bale.
func (b *Bot) Format(title, text string) (string, map[string]any) {
	if b.Kind == "bale" {
		if title == "" {
			return text, nil
		}
		return "*" + strings.ReplaceAll(title, "*", "") + "*\n\n" + text, nil
	}
	body := html.EscapeString(text)
	if title != "" {
		body = "<b>" + html.EscapeString(title) + "</b>\n\n" + body
	}
	return body, map[string]any{"parse_mode": "HTML"}
}

// SendText writes to a chat, formatted for the kind.
func (b *Bot) SendText(ctx context.Context, via telegram.Bot, chat int64, title, text string) error {
	body, extra := b.Format(title, text)
	params := map[string]any{"chat_id": chat, "text": body, "link_preview_options": map[string]bool{"is_disabled": true}}
	for k, v := range extra {
		params[k] = v
	}
	return via.Call(ctx, "sendMessage", params, nil)
}

// Send delivers a notice to the user's chat.
func (b *Bot) Send(ctx context.Context, to Recipient, m Message) error {
	raw := strings.TrimSpace(to.Contact[b.Contact])
	if raw == "" {
		return ErrNoAddress
	}
	chat, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return &Refused{Reason: b.Contact + " is " + raw + ", not a chat id: the user must start the bot to be linked"}
	}
	err = b.SendText(ctx, b.Out, chat, m.Title, m.Text)
	return Classify(err, b.Contact)
}

// Classify turns a Bot API error into the outbox's terms: a chat that
// blocked or never started the bot is refused and unlinked; flood control
// and the server's own failures are retried; any other refusal is final.
func Classify(err error, contact string) error {
	if err == nil {
		return nil
	}
	if telegram.Blocked(err) {
		return &Refused{Reason: err.Error(), Unlink: contact}
	}
	var e *telegram.Error
	if errors.As(err, &e) {
		switch {
		case e.Code == http.StatusTooManyRequests:
			return &Retry{Reason: err.Error(), After: time.Duration(e.RetryAfter) * time.Second}
		case e.Code >= 500:
			return &Retry{Reason: err.Error()}
		default:
			return &Refused{Reason: err.Error()}
		}
	}
	return &Retry{Reason: err.Error()}
}
