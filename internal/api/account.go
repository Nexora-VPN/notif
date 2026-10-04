package api

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/notif/internal/admins"
	"github.com/nexora-vpn/notif/internal/model"
)

// Signing in, the second factor, and the admin's own security.

// pendingTTL is how long a right password waits for its code.
const pendingTTL = 5 * time.Minute

type meView struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	TOTPEnabled bool   `json:"totpEnabled"`
	LastLoginAt int64  `json:"lastLoginAt"`
}

func viewOf(a model.Admin) meView {
	return meView{ID: a.ID, Username: a.Username, TOTPEnabled: a.TOTPEnabled(), LastLoginAt: a.LastLoginAt}
}

// dummyHash is compared against when the username is unknown, so a wrong
// name takes as long to refuse as a wrong password and the time of the
// answer does not say which names exist.
var dummyHash = sync.OnceValue(func() string {
	h, _ := auth.HashPassword(auth.RandomToken())
	return h
})

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Try reserves the attempt: guesses sent at once count while they are
	// checked, so no more than the limit are checked at all.
	done, ok := s.logins.Try(auth.ClientAddr(r))
	if !ok {
		writeCode(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-ins; try again in a few minutes")
		return
	}
	failed := true
	defer func() { done(failed) }()
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	var a model.Admin
	found := s.db.Where("username = ?", strings.TrimSpace(req.Username)).First(&a).Error == nil
	hash := a.PasswordHash
	if !found {
		hash = dummyHash()
	}
	if !auth.CheckPassword(hash, req.Password) || !found {
		writeCode(w, http.StatusUnauthorized, "wrong_credentials", "wrong username or password")
		return
	}
	failed = false
	if a.TOTPEnabled() {
		tok := auth.RandomToken()
		s.mu.Lock()
		for k, p := range s.pending {
			if time.Now().After(p.expires) {
				delete(s.pending, k)
			}
		}
		s.pending[tok] = pendingLogin{adminID: a.ID, expires: time.Now().Add(pendingTTL)}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"mfa": true, "token": tok})
		return
	}
	s.startSession(w, r, a)
}

func (s *Server) handleLoginMFA(w http.ResponseWriter, r *http.Request) {
	done, ok := s.logins.Try(auth.ClientAddr(r))
	if !ok {
		writeCode(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-ins; try again in a few minutes")
		return
	}
	failed := false
	defer func() { done(failed) }()
	var req struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	s.mu.Lock()
	p, ok := s.pending[req.Token]
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		writeCode(w, http.StatusUnauthorized, "login_expired", "the sign-in has expired; enter the password again")
		return
	}
	var a model.Admin
	if s.db.First(&a, p.adminID).Error != nil {
		writeCode(w, http.StatusUnauthorized, "login_expired", "the sign-in has expired; enter the password again")
		return
	}
	step, ok := auth.VerifyTOTP(a.TOTPSecret, req.Code, auth.Now(), a.TOTPLastUsed)
	if !ok {
		failed = true
		writeCode(w, http.StatusUnauthorized, "wrong_code", "wrong code")
		return
	}
	// The step is recorded before the session starts, and only over an
	// earlier one: of two sign-ins with the same code at once, one takes
	// it and the other is refused.
	res := s.db.Model(&model.Admin{}).Where("id = ? AND totp_last_used < ?", a.ID, step).Update("totp_last_used", step)
	if res.Error != nil {
		writeErr(w, http.StatusInternalServerError, "could not record the code: "+res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		failed = true
		writeCode(w, http.StatusUnauthorized, "wrong_code", "wrong code")
		return
	}
	s.mu.Lock()
	delete(s.pending, req.Token)
	s.mu.Unlock()
	s.startSession(w, r, a)
}

// secure reports whether the browser reached Notif over HTTPS: directly,
// or through a proxy in front of it, which its own Origin (or Referer)
// says. Notif keeps no address of its own to compare with and trusts no
// proxy's headers; the browser's word decides only whether the cookie it
// is handed stays on HTTPS, which only the browser itself can make use of.
func secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	for _, h := range []string{"Origin", "Referer"} {
		if v := r.Header.Get(h); v != "" {
			return strings.HasPrefix(strings.ToLower(v), "https://")
		}
	}
	return false
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, a model.Admin) {
	tok, err := admins.Start(s.db, a.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start the session")
		return
	}
	a.LastLoginAt = time.Now().Unix()
	if err := s.db.Model(&a).Update("last_login_at", a.LastLoginAt).Error; err != nil {
		log.Printf("admins: the last sign-in of %d: %v", a.ID, err)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: secure(r), MaxAge: int(admins.SessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, viewOf(a))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, _ := r.Cookie(sessionCookie); c != nil {
		if err := admins.End(s.db, c.Value); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not end the session: "+err.Error())
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request, a model.Admin) {
	writeJSON(w, http.StatusOK, viewOf(a))
}

// passwordErr says why a new password was refused.
func passwordErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrShortPassword):
		writeCode(w, http.StatusBadRequest, "password_short", err.Error())
	case errors.Is(err, auth.ErrLongPassword):
		writeCode(w, http.StatusBadRequest, "password_long", err.Error())
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}

// handlePassword changes the password and ends the admin's other sessions.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	if !auth.CheckPassword(a.PasswordHash, req.Current) {
		writeCode(w, http.StatusForbidden, "wrong_password", "the current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		passwordErr(w, err)
		return
	}
	if err := s.db.Model(&a).Update("password_hash", hash).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the password: "+err.Error())
		return
	}
	c, _ := r.Cookie(sessionCookie)
	if err := admins.EndOthers(s.db, a.ID, c.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "the password is changed, but the other sessions could not be ended: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTOTPEnrol draws a secret to scan; it takes effect when a code from
// it is confirmed.
func (s *Server) handleTOTPEnrol(w http.ResponseWriter, _ *http.Request, a model.Admin) {
	if a.TOTPEnabled() {
		writeCode(w, http.StatusConflict, "totp_on", "two-factor sign-in is already on")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not draw a secret")
		return
	}
	s.mu.Lock()
	s.enrol[a.ID] = secret
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": auth.TOTPURI("Nexora Notif", a.Username, secret)})
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	s.mu.Lock()
	secret := s.enrol[a.ID]
	s.mu.Unlock()
	if secret == "" {
		writeCode(w, http.StatusConflict, "totp_not_started", "start the enrolment first")
		return
	}
	step, ok := auth.VerifyTOTP(secret, req.Code, auth.Now(), 0)
	if !ok {
		writeCode(w, http.StatusBadRequest, "wrong_code", "wrong code")
		return
	}
	if err := s.db.Model(&a).Updates(map[string]any{"totp_secret": secret, "totp_last_used": step}).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, "could not turn two-factor sign-in on: "+err.Error())
		return
	}
	s.mu.Lock()
	delete(s.enrol, a.ID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleTOTPDisable turns the second factor off with the password.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	if !auth.CheckPassword(a.PasswordHash, req.Password) {
		writeCode(w, http.StatusForbidden, "wrong_password", "the password is wrong")
		return
	}
	if err := s.db.Model(&a).Updates(map[string]any{"totp_secret": "", "totp_last_used": 0}).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, "could not turn two-factor sign-in off: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
