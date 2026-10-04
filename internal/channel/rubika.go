package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/nexora-vpn/addon-kit/telegram"
)

// Rubika (P38 (b), GN-S2b): its own Bot API v3, not Telegram's —
// https://botapi.rubika.ir/v3/{token}/{method}, every call a JSON POST
// answered as {"status": "OK", "data": {...}}. Formatting is not markup
// but metadata: parts of the text marked Bold, Italic… by UTF-16 offset.
// A chat is addressed by an opaque string id, learnt when the user starts
// the bot or writes to it; the link is the account's contact key
// rubika_id. Its API refused connections from outside Iran when this was
// written (2026-10-04): a panel abroad needs the proxy setting, or a relay
// inside Iran as the API address.

// RubikaAPI is the Bot API's address.
const RubikaAPI = "https://botapi.rubika.ir/v3"

func init() {
	Register(Kind{
		Name: "rubika", Contact: "rubika_id", PerMinute: 60,
		Fields: []Field{
			{Key: "token", Secret: true, Required: true},
			{Key: "apiBase", Default: RubikaAPI},
			{Key: "proxy"},
		},
		New: func(cfg map[string]string) (Sender, error) { return NewRubika(cfg) },
	})
}

// Rubika is a Rubika bot.
type Rubika struct {
	Token string
	Base  string
	HTTP  *http.Client
}

// NewRubika builds one from a channel's settings.
func NewRubika(cfg map[string]string) (*Rubika, error) {
	token := strings.TrimSpace(cfg["token"])
	if token == "" || strings.ContainsAny(token, "/ ?#") {
		return nil, errors.New("the bot token is the one Rubika's BotFather gave, with no spaces or slashes")
	}
	client, err := telegram.HTTPClient(strings.TrimSpace(cfg["proxy"]))
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(cfg["apiBase"]), "/")
	if base == "" {
		base = RubikaAPI
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return nil, errors.New("apiBase must be an http:// or https:// address")
	}
	return &Rubika{Token: token, Base: base, HTTP: client}, nil
}

// RubikaError is the API answering anything but OK.
type RubikaError struct {
	HTTP    int
	Status  string
	Message string
}

func (e *RubikaError) Error() string {
	if e.Status == "" {
		return fmt.Sprintf("rubika: HTTP %d", e.HTTP)
	}
	return "rubika: " + e.Status + " " + e.Message
}

// Call is one method; out receives "data".
func (r *Rubika) Call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Base+"/"+r.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		// The URL holds the token: never let it into an error message.
		return fmt.Errorf("rubika: %s", strings.ReplaceAll(err.Error(), r.Token, "<token>"))
	}
	defer resp.Body.Close()
	var env struct {
		Status     string          `json:"status"`
		DevMessage string          `json:"dev_message"`
		Data       json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&env); err != nil || (env.Status == "" && resp.StatusCode != http.StatusOK) {
		return &RubikaError{HTTP: resp.StatusCode}
	}
	if env.Status != "OK" {
		return &RubikaError{HTTP: resp.StatusCode, Status: env.Status, Message: env.DevMessage}
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// Me is the bot's username.
func (r *Rubika) Me(ctx context.Context) (string, error) {
	var d struct {
		Bot struct {
			Username string `json:"username"`
		} `json:"bot"`
	}
	err := r.Call(ctx, "getMe", map[string]any{}, &d)
	return d.Bot.Username, err
}

// RubikaUpdate is the part of an update Notif reads.
type RubikaUpdate struct {
	Type       string `json:"type"`
	ChatID     string `json:"chat_id"`
	NewMessage *struct {
		Text       string `json:"text"`
		SenderType string `json:"sender_type"`
	} `json:"new_message"`
}

// Updates reads the updates after offset; next is where the following
// read starts ("" when the API names none).
func (r *Rubika) Updates(ctx context.Context, offset string, limit int) ([]RubikaUpdate, string, error) {
	params := map[string]any{"limit": limit}
	if offset != "" {
		params["offset_id"] = offset
	}
	var d struct {
		Updates      []RubikaUpdate `json:"updates"`
		NextOffsetID string         `json:"next_offset_id"`
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	err := r.Call(ctx, "getUpdates", params, &d)
	return d.Updates, d.NextOffsetID, err
}

// SendText writes to a chat, the title in bold over the text.
func (r *Rubika) SendText(ctx context.Context, chat, title, text string) error {
	params := map[string]any{"chat_id": chat, "text": text}
	if title != "" {
		params["text"] = title + "\n\n" + text
		params["metadata"] = map[string]any{"meta_data_parts": []map[string]any{
			{"type": "Bold", "from_index": 0, "length": len(utf16.Encode([]rune(title)))},
		}}
	}
	return r.Call(ctx, "sendMessage", params, nil)
}

// Send delivers a notice.
func (r *Rubika) Send(ctx context.Context, to Recipient, m Message) error {
	chat := strings.TrimSpace(to.Contact["rubika_id"])
	if chat == "" {
		return ErrNoAddress
	}
	err := r.SendText(ctx, chat, m.Title, m.Text)
	if err == nil {
		return nil
	}
	var e *RubikaError
	if !errors.As(err, &e) {
		return &Retry{Reason: err.Error()}
	}
	switch {
	case e.HTTP == http.StatusTooManyRequests || strings.Contains(e.Status, "TOO"):
		return &Retry{Reason: err.Error(), After: time.Minute}
	case e.HTTP >= 500 || e.Status == "":
		return &Retry{Reason: err.Error()}
	default:
		// The documentation names no status for a chat that stopped the
		// bot; a StoppedBot update unlinks it instead (internal/bots).
		return &Refused{Reason: err.Error()}
	}
}
