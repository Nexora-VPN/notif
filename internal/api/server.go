// Package api is Notif's HTTP side: the addon contract the kit mounts (the
// manifest, the setup, the panel's events, health), the admin's own API
// under /api, and the admin web.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/admins"
	"github.com/nexora-vpn/notif/internal/bots"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/links"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/outbox"
	"github.com/nexora-vpn/notif/internal/settings"
	"github.com/nexora-vpn/notif/internal/users"
	"gorm.io/gorm"
)

const sessionCookie = "nexora_notif_session"

// Server is Notif's HTTP side.
type Server struct {
	db      *gorm.DB
	addon   *addon.Addon
	cfg     config.Config
	version string
	spa     fs.FS
	// logins locks out an address after five failed sign-ins in ten
	// minutes.
	logins *auth.Limiter
	// outbox sends the deliveries; users keeps the copy of the accounts.
	outbox *outbox.Outbox
	users  *users.Sync
	// links writes users' messenger chats to their contact cards; bots reads
	// what users write to the bots.
	links *links.Links
	bots  *bots.Manager

	mu      sync.Mutex
	pending map[string]pendingLogin // sign-in token → the admin waiting for a code
	enrol   map[uint]string         // admin → the TOTP secret being enrolled
}

// pendingLogin is a right password waiting for its code.
type pendingLogin struct {
	adminID uint
	expires time.Time
}

// New wires the server; spa is the built admin web, nil for none.
func New(gdb *gorm.DB, a *addon.Addon, cfg config.Config, version string, spa fs.FS) *Server {
	s := &Server{
		db: gdb, addon: a, cfg: cfg, version: version, spa: spa,
		logins:  auth.NewLimiter(5, 10*time.Minute),
		pending: map[string]pendingLogin{}, enrol: map[uint]string{},
		outbox: outbox.New(gdb),
	}
	s.users = &users.Sync{DB: gdb, Panel: func() users.Getter {
		if c := s.panelClient(); c != nil {
			return c
		}
		return nil
	}}
	secret, err := settings.Secret(gdb)
	if err != nil {
		log.Printf("settings: the secret: %v", err)
	}
	s.links = &links.Links{DB: gdb, Users: s.users, Secret: secret, Panel: func() links.Panel {
		if c := s.panelClient(); c != nil {
			return c
		}
		return nil
	}}
	s.bots = bots.New(gdb, s.links)
	s.outbox.Unlink = func(userID uint, key string) {
		go func() {
			if err := s.links.Set(context.Background(), userID, key, ""); err != nil {
				log.Printf("links: unlink %s of %d: %v", key, userID, err)
			}
		}()
	}
	a.OnSetup(s.registered)
	a.OnEvent(s.event)
	return s
}

// panelID is the registration's panel: its id, or its address for a panel
// too old to send one.
func panelID(c addon.Credentials) string {
	if c.Panel.ID != "" {
		return c.Panel.ID
	}
	return c.Panel.URL
}

func (s *Server) registered(c addon.Credentials) {
	s.db.Save(&model.Panel{ID: panelID(c), URL: c.Panel.URL, Version: c.Panel.Version, RegisteredAt: time.Now().Unix()})
	go func() {
		if err := s.users.Full(context.Background()); err != nil {
			log.Printf("users: the first read: %v", err)
		}
	}()
}

// panelClient is the registered panel's client, nil before registration.
func (s *Server) panelClient() *panel.Client {
	c := s.addon.Credentials()
	if c == nil {
		return nil
	}
	return &panel.Client{Base: c.Panel.URL, Token: c.Token}
}

func urlQuery(v string) string { return url.QueryEscape(v) }

// Run starts the outbox and the copy of the accounts, and prunes the log
// once a day, until ctx ends.
func (s *Server) Run(ctx context.Context) {
	go s.outbox.Run(ctx)
	go s.users.Run(ctx)
	go s.bots.Run(ctx)
	day := time.NewTicker(24 * time.Hour)
	defer day.Stop()
	s.outbox.Prune()
	for {
		select {
		case <-ctx.Done():
			return
		case <-day.C:
			s.outbox.Prune()
		}
	}
}

// event is the panel's events. A deleted account is marked gone; any other
// account event refreshes the copy of that account (GN-S4 turns them into
// notices); the goodbye forgets the credentials so Notif can be registered
// again.
func (s *Server) event(e addon.Event) {
	if strings.HasPrefix(e.Event, "user.") {
		s.userEvent(e)
		return
	}
	if e.Event != "panel.addon_removed" {
		return
	}
	if c := s.addon.Credentials(); c != nil {
		s.db.Model(&model.Panel{}).Where("id = ?", panelID(*c)).Update("removed_at", time.Now().Unix())
	}
	_ = s.addon.Forget()
}

// Handler is every route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.addon.Mount(mux)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/login/mfa", s.handleLoginMFA)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.signedIn(s.handleMe))
	mux.HandleFunc("PUT /api/me/password", s.signedIn(s.handlePassword))
	mux.HandleFunc("POST /api/me/2fa/totp", s.signedIn(s.handleTOTPEnrol))
	mux.HandleFunc("POST /api/me/2fa/totp/confirm", s.signedIn(s.handleTOTPConfirm))
	mux.HandleFunc("DELETE /api/me/2fa", s.signedIn(s.handleTOTPDisable))
	mux.HandleFunc("GET /api/status", s.signedIn(s.handleStatus))
	s.mountCore(mux)
	s.mountUsers(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "no such API route")
	})
	if s.spa != nil {
		mux.Handle("/", spaHandler(s.spa))
	}
	return securityHeaders(mux)
}

// Healthy is the health path's answer: the database answers.
func Healthy(gdb *gorm.DB) func() error {
	return func() error {
		sqlDB, err := gdb.DB()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return sqlDB.PingContext(ctx)
	}
}

// signedIn admits a request with a live session.
func (s *Server) signedIn(next func(http.ResponseWriter, *http.Request, model.Admin)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		if c, _ := r.Cookie(sessionCookie); c != nil {
			tok = c.Value
		}
		a, err := admins.Of(s.db, tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, a)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid body")
	}
	return nil
}

// securityHeaders keeps the admin web out of other sites' frames and tells
// browsers not to guess types.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

// spaHandler serves the built admin web, and index.html for any path that
// is not a file, so the web's own routes survive a reload.
func spaHandler(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				st, err := f.Stat()
				f.Close()
				if err == nil && !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// userEvent keeps the copy in step with what an event says.
func (s *Server) userEvent(e addon.Event) {
	var data struct {
		UserID uint   `json:"userId"`
		Op     string `json:"op"`
		IDs    []uint `json:"ids"`
	}
	_ = json.Unmarshal(e.Data, &data)
	switch {
	case e.Event == "user.deleted" && data.UserID != 0:
		s.users.Gone(data.UserID)
	case e.Event == "user.bulk" && data.Op == "delete":
		for _, id := range data.IDs {
			s.users.Gone(id)
		}
	case e.Event == "user.bulk" || e.Event == "user.generated":
		go func() {
			if err := s.users.Edits(context.Background()); err != nil {
				log.Printf("users: after %s: %v", e.Event, err)
			}
		}()
	case data.UserID != 0:
		go func() {
			if _, err := s.users.One(context.Background(), data.UserID); err != nil {
				log.Printf("users: after %s: %v", e.Event, err)
			}
		}()
	}
}
