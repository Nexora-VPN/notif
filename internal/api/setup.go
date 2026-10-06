package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/addon-kit/web"
	"github.com/nexora-vpn/notif/internal/backup"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
)

// Set-up (docs/phase-g.md GN-S7, P44 (b)): the checklist the admin web
// shows until Notif is ready — its address and certificate, checked from
// outside; the admin's own password and second factor; the first channel;
// the panel's registration; the backups — each with what to do next.

// whoAmIPath is where Notif answers its instance name, under the base path
// like everything else: the address check asks the public address for it.
const whoAmIPath = "/nexora-notif/whoami"

func (s *Server) mountSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET "+whoAmIPath, func(w http.ResponseWriter, r *http.Request) {
		id, err := settings.Instance(s.db)
		if err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		web.WhoAmI(id).ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /api/setup", s.signedIn(s.handleSetup))
	mux.HandleFunc("PUT /api/setup/address", s.signedIn(s.handleSetupAddress))
}

// setupStep is one line of the checklist.
type setupStep struct {
	Key    string `json:"key"`
	Done   bool   `json:"done"`
	Detail string `json:"detail,omitempty"`
	// Optional steps do not hold the checklist open.
	Optional bool `json:"optional,omitempty"`
}

type setupView struct {
	Steps []setupStep `json:"steps"`
	Done  bool        `json:"done"`
	// HTTPS is the install's https answer; Fingerprint the self-signed
	// certificate's SHA-256, for the admin to compare with the browser's.
	HTTPS       string `json:"https"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicURL   string `json:"publicUrl"`
	// BasePath is the path Notif is under ("" at the root); AdminURL the
	// address with it, where the admins open Notif.
	BasePath string `json:"basePath"`
	AdminURL string `json:"adminUrl,omitempty"`
	Database string `json:"database"`
}

// handleSetup is the checklist, worked out from what Notif holds now.
func (s *Server) handleSetup(w http.ResponseWriter, _ *http.Request, a model.Admin) {
	addr, err := settings.LoadAddress(s.db)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	var channels int64
	if err := s.db.Model(&model.Channel{}).Where("enabled = ?", true).Count(&channels).Error; err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	v := setupView{
		HTTPS: s.cfg.HTTPS, PublicURL: addr.PublicURL, BasePath: s.cfg.BasePath, Database: s.cfg.Driver,
	}
	if addr.PublicURL != "" {
		v.AdminURL = addr.PublicURL + s.cfg.BasePath + "/"
	}
	if s.selfSigned() {
		v.Fingerprint = s.tls.Fingerprint()
	}
	// The install's password stays in its .env in the clear: the step is
	// done once the admin has a password of their own.
	ownPassword := s.cfg.AdminPassword == "" || !auth.CheckPassword(a.PasswordHash, s.cfg.AdminPassword)
	panelStep := setupStep{Key: "panel"}
	if c := s.addon.Credentials(); c != nil {
		panelStep.Done, panelStep.Detail = true, c.Panel.URL
	} else {
		panelStep.Detail = s.addon.ClaimCode()
	}
	backups := setupStep{Key: "backups", Optional: true}
	if s.cfg.Driver == config.DriverSQLite {
		if at := backup.Latest(s.cfg); !at.IsZero() {
			backups.Done, backups.Detail = true, strconv.FormatInt(at.Unix(), 10)
		}
	}
	// The address is done once there is one served over HTTPS — Notif's
	// own, or a proxy's in front of an install with https off.
	addressDone := addr.PublicURL != "" && (s.cfg.HTTPS != "off" || strings.HasPrefix(addr.PublicURL, "https://"))
	steps := []setupStep{
		{Key: "address", Done: addressDone, Detail: addr.PublicURL},
		{Key: "password", Done: ownPassword},
		{Key: "twofactor", Done: a.TOTPEnabled()},
		{Key: "channel", Done: channels > 0, Detail: strconv.FormatInt(channels, 10)},
		panelStep,
		backups,
	}
	// What the panel's install did — the address and its HTTPS, the
	// registration, the install's password — is listed only while it is
	// wrong (docs/phase-h.md P11 in the panel's repository): the list is
	// Notif's own work.
	v.Steps = []setupStep{}
	for _, st := range steps {
		if st.Done && (st.Key == "address" || st.Key == "panel" || st.Key == "password") {
			continue
		}
		v.Steps = append(v.Steps, st)
	}
	v.Done = true
	for _, st := range v.Steps {
		if !st.Done && !st.Optional {
			v.Done = false
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSetupAddress saves the public address and asks it, from outside,
// whether it reaches this Notif. A failed check still saves: DNS may be
// on its way.
func (s *Server) handleSetupAddress(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var req struct {
		PublicURL string `json:"publicUrl"`
		Check     bool   `json:"check"`
	}
	if err := decode(r, &req); err != nil {
		badBody(w)
		return
	}
	if req.PublicURL != "" || !req.Check {
		if err := settings.SaveAddress(s.db, req.PublicURL); err != nil {
			writeCode(w, http.StatusBadRequest, "address_invalid", err.Error())
			return
		}
	}
	addr, err := settings.LoadAddress(s.db)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string]any{"publicUrl": addr.PublicURL, "reachable": false}
	if addr.PublicURL == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	id, err := settings.Instance(s.db)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	target := addr.PublicURL + s.cfg.BasePath + whoAmIPath
	if err := web.CheckAddress(r.Context(), target, id, s.selfSigned()); err != nil {
		out["error"] = err.Error()
	} else {
		out["reachable"] = true
		out["checkedAt"] = time.Now().Unix()
	}
	writeJSON(w, http.StatusOK, out)
}
