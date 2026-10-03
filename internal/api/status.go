package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/model"
)

// statusView is the dashboard's readout: Notif's version and database, and
// where it stands with the panel — registered (the panel, the token's
// scopes, whether a read with it works) or waiting with its claim code.
type statusView struct {
	Version    string       `json:"version"`
	Database   string       `json:"database"`
	ClaimCode  string       `json:"claimCode,omitempty"`
	Panel      *model.Panel `json:"panel,omitempty"`
	Scopes     []string     `json:"scopes"`
	Accounts   *int         `json:"accounts,omitempty"`
	PanelError string       `json:"panelError,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	v := statusView{Version: s.version, Database: s.cfg.Driver, Scopes: []string{}}
	c := s.addon.Credentials()
	if c == nil {
		v.ClaimCode = s.addon.ClaimCode()
		writeJSON(w, http.StatusOK, v)
		return
	}
	var row model.Panel
	if s.db.First(&row, "id = ?", panelID(*c)).Error == nil {
		v.Panel = &row
	}
	if c.Scopes != nil {
		v.Scopes = c.Scopes
	}
	// One read with the token proves the registration end to end.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var list struct {
		Total int `json:"total"`
	}
	client := &panel.Client{Base: c.Panel.URL, Token: c.Token}
	if err := client.Get(ctx, "/users?limit=1", &list); err != nil {
		v.PanelError = err.Error()
	} else {
		v.Accounts = &list.Total
	}
	writeJSON(w, http.StatusOK, v)
}
