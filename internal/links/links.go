// Package links ties a user's messenger chats to their panel account. The
// link itself is a key of the account's contact card on the panel —
// telegram_id, bale_id, soroush_id, rubika_id (P36 (iii), G-D1) — so the
// panel stays the one record and every addon reads it. A user proves which
// account is theirs by sending a bot their subscription link, or a link
// code the admin handed them. Notif remembers which keys it wrote
// (model.Link): those, and only those, it takes away again.
package links

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/users"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Panel is the panel as far as links need it (a *panel.Client).
type Panel interface {
	Do(ctx context.Context, req panel.Request, out any) error
}

// Links reads and writes the links.
type Links struct {
	DB     *gorm.DB
	Panel  func() Panel
	Users  *users.Sync
	Secret []byte
}

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Code is a user's link code: their id and a MAC of it, short enough to type.
func (l *Links) Code(userID uint) string {
	return strconv.FormatUint(uint64(userID), 10) + "-" + l.mac(userID)
}

func (l *Links) mac(userID uint) string {
	m := hmac.New(sha256.New, l.Secret)
	fmt.Fprintf(m, "link:%d", userID)
	return strings.ToLower(codeEncoding.EncodeToString(m.Sum(nil))[:10])
}

// NtfyTopic is a user's ntfy topic: long enough that nobody guesses it,
// drawn from Notif's secret so it is the same every time it is asked for.
func (l *Links) NtfyTopic(userID uint) string {
	m := hmac.New(sha256.New, l.Secret)
	fmt.Fprintf(m, "ntfy:%d", userID)
	return "notif-" + strings.ToLower(codeEncoding.EncodeToString(m.Sum(nil))[:20])
}

var codePattern = regexp.MustCompile(`^(\d{1,12})-([a-z2-7]{10})$`)

// ByCode is the account a link code names, when the code is genuine.
func (l *Links) ByCode(code string) (uint, bool) {
	m := codePattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(code)))
	if m == nil {
		return 0, false
	}
	id, _ := strconv.ParseUint(m[1], 10, 64)
	if !hmac.Equal([]byte(l.mac(uint(id))), []byte(m[2])) {
		return 0, false
	}
	return uint(id), true
}

var (
	subPattern   = regexp.MustCompile(`/sub/([A-Za-z0-9_\-]{6,128})`)
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_\-]{6,128}$`)
)

// BySub is the account whose subscription link or token the text holds.
func (l *Links) BySub(text string) (model.User, bool) {
	var candidates []string
	for _, m := range subPattern.FindAllStringSubmatch(text, -1) {
		candidates = append(candidates, m[1])
	}
	if t := strings.TrimSpace(text); tokenPattern.MatchString(t) {
		candidates = append(candidates, t)
	}
	for _, c := range candidates {
		h := users.SubHash(c)
		var u model.User
		if l.DB.Where("gone_at = 0 AND (sub_id_hash = ? OR sub_token_hash = ?)", h, h).First(&u).Error == nil {
			return u, true
		}
	}
	return model.User{}, false
}

// ErrNotRegistered is a link written before Notif is registered.
var ErrNotRegistered = errors.New("not registered with a panel")

// ErrConflict is a card that kept changing under the write: three reads,
// and each time another writer was quicker.
var ErrConflict = errors.New("the account's contact card kept changing while it was written; try again")

// Set writes one key of a user's contact card on the panel — value "" takes
// it away — then refreshes the copy, and records the key as Notif's own (a
// value Notif's write put there) or no longer (""). A card that held value
// already was written by someone else, so the key stays theirs. The write
// carries the version it read, so another addon's edit in between is never
// overwritten: a conflict reads again, three times at most.
func (l *Links) Set(ctx context.Context, userID uint, key, value string) error {
	before, err := l.Update(ctx, userID, func(c map[string]string) {
		if value == "" {
			delete(c, key)
		} else {
			c[key] = value
		}
	})
	if err != nil {
		return err
	}
	if value == "" {
		return l.DB.Where("user_id = ? AND key = ?", userID, key).Delete(&model.Link{}).Error
	}
	if before[key] == value {
		return nil
	}
	return l.Own(userID, key, value)
}

// Own records that Notif wrote value to key of a user's card, so it may
// take it away again (Release).
func (l *Links) Own(userID uint, key, value string) error {
	return l.DB.Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&model.Link{UserID: userID, Key: key, Value: value, At: time.Now().Unix()}).Error
}

// Release takes key off a user's card only when Notif wrote it and the card
// still holds value — the chat that failed, the one that wrote /stop — and
// reports whether it was Notif's to take. A key another addon wrote, or one
// written again since, is left as it is.
func (l *Links) Release(ctx context.Context, userID uint, key, value string) (bool, error) {
	var own model.Link
	if err := l.DB.Where("user_id = ? AND key = ?", userID, key).Limit(1).Find(&own).Error; err != nil {
		return false, err
	}
	if own.UserID == 0 || own.Value != value {
		return false, nil
	}
	_, err := l.Update(ctx, userID, func(c map[string]string) {
		if c[key] == value {
			delete(c, key)
		}
	})
	if err != nil {
		return false, err
	}
	return true, l.DB.Where("user_id = ? AND key = ? AND value = ?", userID, key, value).Delete(&model.Link{}).Error
}

// Block records that a channel no longer reaches a user at value — a bot
// the user stopped or blocked — so nothing is sent there while the card
// still says value. It is how Notif stops writing to a chat whose key it
// did not write and so may not take away.
func (l *Links) Block(userID, channelID uint, value, reason string) error {
	return l.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Block{
		UserID: userID, ChannelID: channelID, Value: value, Reason: reason, At: time.Now().Unix(),
	}).Error
}

// Unblock lifts a channel's block for a user: they linked it again.
func (l *Links) Unblock(userID, channelID uint) error {
	return l.DB.Where("user_id = ? AND channel_id = ?", userID, channelID).Delete(&model.Block{}).Error
}

// Update changes a user's contact card on the panel with edit, under the
// same version check as Set; an edit that changes nothing writes nothing.
// It returns the card as it was before the edit that took, so a caller can
// tell a key Notif's write set from one the card held already. A card that
// changes under every one of three writes is ErrConflict.
func (l *Links) Update(ctx context.Context, userID uint, edit func(map[string]string)) (map[string]string, error) {
	p := l.Panel()
	if p == nil {
		return nil, ErrNotRegistered
	}
	path := "/users/" + strconv.FormatUint(uint64(userID), 10)
	written := false
	var prior map[string]string
	for range 3 {
		var acc struct {
			Contact   map[string]string `json:"contact"`
			UpdatedAt int64             `json:"updatedAt"`
		}
		if err := p.Do(ctx, panel.Request{Method: http.MethodGet, Path: path}, &acc); err != nil {
			return nil, err
		}
		if acc.Contact == nil {
			acc.Contact = map[string]string{}
		}
		prior = maps.Clone(acc.Contact)
		before, _ := json.Marshal(acc.Contact)
		edit(acc.Contact)
		if after, _ := json.Marshal(acc.Contact); string(after) == string(before) {
			written = true
			break
		}
		err := p.Do(ctx, panel.Request{Method: http.MethodPatch, Path: path, Body: map[string]any{
			"contact": acc.Contact, "version": acc.UpdatedAt,
		}}, nil)
		if panel.IsStatus(err, http.StatusConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		written = true
		break
	}
	if !written {
		return nil, ErrConflict
	}
	_, err := l.Users.One(ctx, userID)
	return prior, err
}

// Linked are the accounts in the copy whose contact key holds value, found
// by the chats the copy keeps beside the cards (model.Chat).
func (l *Links) Linked(key, value string) []model.User {
	if value == "" {
		return nil
	}
	var out []model.User
	l.DB.Where("gone_at = 0 AND id IN (?)", l.DB.Model(&model.Chat{}).Select("user_id").Where("key = ? AND value = ?", key, value)).
		Order("id").Find(&out)
	return out
}
