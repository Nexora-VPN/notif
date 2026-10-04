// Package model is Notif's own database. Nothing here is the panel's: the
// accounts are read from the panel with the addon's token, and only what
// Notif itself decides — its admins, the panel it is registered with, and
// (from GN-S1 on) its channels, notices and deliveries — is kept.
package model

// Admin is someone who signs in to Notif's admin web. P35 (a): the main
// admin only, for now; every send will name an owner so resellers can be
// added without a migration.
type Admin struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	Username     string `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string `json:"-" gorm:"not null"`
	// TOTPSecret is empty while the second factor is off; TOTPLastUsed is
	// the last time step accepted, so a code is never taken twice.
	TOTPSecret   string `json:"-" gorm:"not null;default:''"`
	TOTPLastUsed int64  `json:"-" gorm:"not null;default:0"`
	LastLoginAt  int64  `json:"lastLoginAt" gorm:"not null;default:0"`
	CreatedAt    int64  `json:"createdAt" gorm:"autoCreateTime"`
}

// TOTPEnabled reports whether the admin signs in with a second factor.
func (a Admin) TOTPEnabled() bool { return a.TOTPSecret != "" }

// Session is a signed-in browser, kept as the hash of its token.
type Session struct {
	ID        uint   `gorm:"primaryKey"`
	TokenHash string `gorm:"uniqueIndex;not null"`
	AdminID   uint   `gorm:"index;not null"`
	ExpiresAt int64  `gorm:"not null"`
}

// Panel is the panel Notif was registered with, by the id the setup body
// carries; RemovedAt is set when the panel says goodbye.
type Panel struct {
	ID           string `json:"id" gorm:"primaryKey"`
	URL          string `json:"url" gorm:"not null"`
	Version      string `json:"version" gorm:"not null;default:''"`
	RegisteredAt int64  `json:"registeredAt" gorm:"not null"`
	RemovedAt    int64  `json:"removedAt" gorm:"not null;default:0"`
}

// All is every table, for the migration.
func All() []any {
	return []any{
		&Admin{}, &Session{}, &Panel{}, &Setting{},
		&User{}, &Channel{}, &Delivery{}, &Attempt{}, &Send{}, &Notice{},
	}
}

// Setting is one named JSON document (internal/settings).
type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"type:text;not null"`
}
