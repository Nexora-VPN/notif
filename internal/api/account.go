package api

import (
	"net/http"
	"strings"
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	addr := auth.ClientAddr(r)
	if s.logins.Locked(addr) {
		writeErr(w, http.StatusTooManyRequests, "too many failed sign-ins; try again in a few minutes")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var a model.Admin
	if s.db.Where("username = ?", strings.TrimSpace(req.Username)).First(&a).Error != nil || !auth.CheckPassword(a.PasswordHash, req.Password) {
		s.logins.Fail(addr)
		writeErr(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
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
	addr := auth.ClientAddr(r)
	if s.logins.Locked(addr) {
		writeErr(w, http.StatusTooManyRequests, "too many failed sign-ins; try again in a few minutes")
		return
	}
	var req struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	p, ok := s.pending[req.Token]
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		writeErr(w, http.StatusUnauthorized, "the sign-in has expired; enter the password again")
		return
	}
	var a model.Admin
	if s.db.First(&a, p.adminID).Error != nil {
		writeErr(w, http.StatusUnauthorized, "the sign-in has expired; enter the password again")
		return
	}
	step, ok := auth.VerifyTOTP(a.TOTPSecret, req.Code, auth.Now(), a.TOTPLastUsed)
	if !ok {
		s.logins.Fail(addr)
		writeErr(w, http.StatusUnauthorized, "wrong code")
		return
	}
	s.mu.Lock()
	delete(s.pending, req.Token)
	s.mu.Unlock()
	s.db.Model(&a).Update("totp_last_used", step)
	s.startSession(w, r, a)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, a model.Admin) {
	tok, err := admins.Start(s.db, a.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start the session")
		return
	}
	a.LastLoginAt = time.Now().Unix()
	s.db.Model(&a).Update("last_login_at", a.LastLoginAt)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil, MaxAge: int(admins.SessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, viewOf(a))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, _ := r.Cookie(sessionCookie); c != nil {
		admins.End(s.db, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request, a model.Admin) {
	writeJSON(w, http.StatusOK, viewOf(a))
}

// handlePassword changes the password and ends the admin's other sessions.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !auth.CheckPassword(a.PasswordHash, req.Current) {
		writeErr(w, http.StatusForbidden, "the current password is wrong")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Model(&a).Update("password_hash", hash)
	c, _ := r.Cookie(sessionCookie)
	admins.EndOthers(s.db, a.ID, c.Value)
	w.WriteHeader(http.StatusNoContent)
}

// handleTOTPEnrol draws a secret to scan; it takes effect when a code from
// it is confirmed.
func (s *Server) handleTOTPEnrol(w http.ResponseWriter, _ *http.Request, a model.Admin) {
	if a.TOTPEnabled() {
		writeErr(w, http.StatusConflict, "two-factor sign-in is already on")
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
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	secret := s.enrol[a.ID]
	s.mu.Unlock()
	if secret == "" {
		writeErr(w, http.StatusConflict, "start the enrolment first")
		return
	}
	step, ok := auth.VerifyTOTP(secret, req.Code, auth.Now(), 0)
	if !ok {
		writeErr(w, http.StatusBadRequest, "wrong code")
		return
	}
	s.mu.Lock()
	delete(s.enrol, a.ID)
	s.mu.Unlock()
	s.db.Model(&a).Updates(map[string]any{"totp_secret": secret, "totp_last_used": step})
	w.WriteHeader(http.StatusNoContent)
}

// handleTOTPDisable turns the second factor off with the password.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !auth.CheckPassword(a.PasswordHash, req.Password) {
		writeErr(w, http.StatusForbidden, "the password is wrong")
		return
	}
	s.db.Model(&a).Updates(map[string]any{"totp_secret": "", "totp_last_used": 0})
	w.WriteHeader(http.StatusNoContent)
}
