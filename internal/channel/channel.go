// Package channel is the ways Notif reaches a user: each kind (a bot, an
// SMS provider, a mail server, the generic HTTP channel) turns one rendered
// notice into one send, and says how it went in one of three ways the
// outbox understands — sent, refused for this user (try the next channel),
// or worth retrying later.
package channel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

// Recipient is the user a notice goes to: the panel account and its
// contact card, where every messenger link is kept (P36 (iii)).
type Recipient struct {
	UserID  uint
	Name    string
	Contact map[string]string
	Lang    string
}

// Message is one notice rendered for one channel and language.
type Message struct {
	// Key is the delivery's once-only key, for a provider that takes an
	// idempotency id.
	Key   string
	Kind  string
	Title string
	Text  string
	Vars  map[string]string
}

// ErrNoAddress: the user has no address on this channel (no Telegram id,
// no phone). The next channel is tried at once.
var ErrNoAddress = errors.New("the user has no address on this channel")

// Refused is the channel turning this user down for good — a blocked bot,
// a number that does not exist. The next channel is tried at once. Unlink
// names the contact key that no longer reaches the user (a bot the user
// blocked), for the outbox to remove from the panel.
type Refused struct {
	Reason string
	Unlink string
}

func (e *Refused) Error() string { return e.Reason }

// Retry is a failure worth trying again: the provider was down, timed out
// or asked to slow down. After is its own wait, when it named one.
type Retry struct {
	Reason string
	After  time.Duration
}

func (e *Retry) Error() string { return e.Reason }

// Sender sends through one configured channel.
type Sender interface {
	Send(ctx context.Context, to Recipient, m Message) error
}

// Field is one setting of a kind, as the admin web shows it.
type Field struct {
	Key      string `json:"key"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Multiline asks for a text area (headers, a body template).
	Multiline bool   `json:"multiline,omitempty"`
	Default   string `json:"default,omitempty"`
	// Choices, when set, are the only values the field takes.
	Choices []string `json:"choices,omitempty"`
}

// Kind is one kind of channel.
type Kind struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// Contact is the contact key a bot kind links (telegram_id…), "" for a
	// kind that is not a bot.
	Contact string `json:"contact,omitempty"`
	// PerMinute is the send rate when the admin sets none.
	PerMinute int `json:"perMinute"`
	// New makes a sender from a channel's settings, refusing ones that
	// cannot work.
	New func(cfg map[string]string) (Sender, error) `json:"-"`
}

var kinds = map[string]Kind{}

// Register adds a kind; the kinds register themselves in init.
func Register(k Kind) { kinds[k.Name] = k }

// Lookup finds a kind.
func Lookup(name string) (Kind, bool) {
	k, ok := kinds[name]
	return k, ok
}

// Kinds is every kind, by name.
func Kinds() []Kind {
	out := make([]Kind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Check fills a channel's settings with the kind's defaults and refuses one
// that is missing a required field or cannot make a sender. A default fills
// only a field that was not sent: one sent empty stays empty, which is how
// the admin says "none" (the generic channel's address, say).
func Check(kind string, cfg map[string]string) (map[string]string, error) {
	k, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("unknown channel kind %q", kind)
	}
	out := map[string]string{}
	for _, f := range k.Fields {
		v, sent := cfg[f.Key]
		if !sent {
			v = f.Default
		}
		if f.Required && v == "" {
			return nil, fmt.Errorf("%s is required", f.Key)
		}
		if len(f.Choices) > 0 && v != "" && !slices.Contains(f.Choices, v) {
			return nil, fmt.Errorf("%s must be one of %v", f.Key, f.Choices)
		}
		out[f.Key] = v
	}
	if _, err := k.New(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Mask stands for a secret that is set, in what the browser sees.
const Mask = "\u2022\u2022\u2022\u2022"

// Redact is a channel's settings as the browser may see them: secrets
// replaced by the mask when they are set.
func Redact(kind string, cfg map[string]string) map[string]string {
	k, _ := Lookup(kind)
	out := map[string]string{}
	for _, f := range k.Fields {
		v, ok := cfg[f.Key]
		if !ok {
			continue
		}
		if f.Secret && v != "" {
			v = Mask
		}
		out[f.Key] = v
	}
	return out
}

// Merge applies an edit: a field not sent keeps its value, and so does a
// secret sent empty or as the mask.
func Merge(kind string, old, edit map[string]string) map[string]string {
	k, _ := Lookup(kind)
	out := map[string]string{}
	for _, f := range k.Fields {
		v, sent := edit[f.Key]
		if !sent || (f.Secret && (v == "" || v == Mask)) {
			v, sent = old[f.Key]
		}
		if sent {
			out[f.Key] = v
		}
	}
	return out
}
