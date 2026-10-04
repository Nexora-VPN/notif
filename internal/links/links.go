// Package links ties a user's messenger chats to their panel account. The
// link itself is a key of the account's contact card on the panel —
// telegram_id, bale_id, soroush_id, rubika_id (P36 (iii), G-D1) — so the
// panel stays the one record and every addon reads it. A user proves which
// account is theirs by sending a bot their subscription link, or a link
// code the admin handed them.
package links

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/users"
	"gorm.io/gorm"
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

// Set writes one key of a user's contact card on the panel — value "" takes
// it away — then refreshes the copy. The write carries the version it read,
// so another addon's edit in between is never overwritten: a conflict reads
// again, three times at most.
func (l *Links) Set(ctx context.Context, userID uint, key, value string) error {
	return l.Update(ctx, userID, func(c map[string]string) {
		if value == "" {
			delete(c, key)
		} else {
			c[key] = value
		}
	})
}

// Update changes a user's contact card on the panel with edit, under the
// same version check as Set; an edit that changes nothing writes nothing.
func (l *Links) Update(ctx context.Context, userID uint, edit func(map[string]string)) error {
	p := l.Panel()
	if p == nil {
		return ErrNotRegistered
	}
	path := "/users/" + strconv.FormatUint(uint64(userID), 10)
	for range 3 {
		var acc struct {
			Contact   map[string]string `json:"contact"`
			UpdatedAt int64             `json:"updatedAt"`
		}
		if err := p.Do(ctx, panel.Request{Method: http.MethodGet, Path: path}, &acc); err != nil {
			return err
		}
		if acc.Contact == nil {
			acc.Contact = map[string]string{}
		}
		before, _ := json.Marshal(acc.Contact)
		edit(acc.Contact)
		if after, _ := json.Marshal(acc.Contact); string(after) == string(before) {
			break
		}
		err := p.Do(ctx, panel.Request{Method: http.MethodPatch, Path: path, Body: map[string]any{
			"contact": acc.Contact, "version": acc.UpdatedAt,
		}}, nil)
		if panel.IsStatus(err, http.StatusConflict) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	_, err := l.Users.One(ctx, userID)
	return err
}

// Linked are the accounts in the copy whose contact key holds value.
func (l *Links) Linked(key, value string) []model.User {
	if value == "" {
		return nil
	}
	// A coarse match in SQL (the value appears in the card), then the exact
	// one here — the card's JSON is read the same on both databases.
	needle, _ := json.Marshal(value)
	var cands []model.User
	l.DB.Where("gone_at = 0 AND contact LIKE ?", "%"+string(needle)+"%").Find(&cands)
	var out []model.User
	for _, u := range cands {
		if u.Contact.V[key] == value {
			out = append(out, u)
		}
	}
	return out
}
