// Package admins keeps Notif's admin accounts and their sessions, on the
// kit's passwords, tokens and TOTP (addon-kit/auth).
package admins

import (
	"errors"
	"strings"
	"time"

	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/notif/internal/model"
	"gorm.io/gorm"
)

// SessionTTL is how long a sign-in lasts.
const SessionTTL = 7 * 24 * time.Hour

// EnsureFirst makes the first admin from the install's answers when there
// is none, and reports whether it did. Without a password it makes nobody:
// `notif admin reset-password` is then the way in.
func EnsureFirst(gdb *gorm.DB, username, password string) (bool, error) {
	var n int64
	if err := gdb.Model(&model.Admin{}).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 || password == "" {
		return false, nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, err
	}
	return true, gdb.Create(&model.Admin{Username: username, PasswordHash: hash}).Error
}

// ResetPassword sets an admin's password, making the account when it is not
// there, and turns its second factor off and ends its sessions: a lost
// password and a lost phone are one recovery, as on the panel.
func ResetPassword(gdb *gorm.DB, username, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return gdb.Transaction(func(tx *gorm.DB) error {
		var a model.Admin
		err := tx.Where("username = ?", username).First(&a).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&model.Admin{Username: username, PasswordHash: hash}).Error
		}
		if err != nil {
			return err
		}
		if err := tx.Model(&a).Updates(map[string]any{"password_hash": hash, "totp_secret": "", "totp_last_used": 0}).Error; err != nil {
			return err
		}
		return tx.Where("admin_id = ?", a.ID).Delete(&model.Session{}).Error
	})
}

// Start records a session and returns its token.
func Start(gdb *gorm.DB, adminID uint) (string, error) {
	tok := auth.RandomToken()
	err := gdb.Create(&model.Session{
		TokenHash: auth.HashToken(tok), AdminID: adminID, ExpiresAt: time.Now().Add(SessionTTL).Unix(),
	}).Error
	return tok, err
}

// ErrSignedOut is a missing, unknown or expired session.
var ErrSignedOut = errors.New("not signed in")

// Of is the admin a session token signs in.
func Of(gdb *gorm.DB, token string) (model.Admin, error) {
	var s model.Session
	if token == "" || gdb.Where("token_hash = ?", auth.HashToken(token)).First(&s).Error != nil {
		return model.Admin{}, ErrSignedOut
	}
	if time.Now().Unix() >= s.ExpiresAt {
		gdb.Delete(&s)
		return model.Admin{}, ErrSignedOut
	}
	var a model.Admin
	if err := gdb.First(&a, s.AdminID).Error; err != nil {
		return model.Admin{}, ErrSignedOut
	}
	return a, nil
}

// End deletes a session.
func End(gdb *gorm.DB, token string) error {
	return gdb.Where("token_hash = ?", auth.HashToken(token)).Delete(&model.Session{}).Error
}

// EndOthers ends every session of an admin but the one with keep's token.
func EndOthers(gdb *gorm.DB, adminID uint, keep string) error {
	return gdb.Where("admin_id = ? AND token_hash <> ?", adminID, auth.HashToken(keep)).Delete(&model.Session{}).Error
}
