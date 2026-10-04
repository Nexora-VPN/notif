// Package api is Notif's HTTP side: the addon contract the kit mounts (the
// manifest, the setup, the panel's events, health), the admin's own API
// under /api, and the admin web.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/admins"
	"github.com/nexora-vpn/notif/internal/bots"
	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/links"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/outbox"
	"github.com/nexora-vpn/notif/internal/settings"
	"github.com/nexora-vpn/notif/internal/users"
	"github.com/nexora-vpn/notif/internal/watch"
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
	// watch decides when a user hears something.
	watch *watch.Watch

	mu      sync.Mutex
	pending map[string]pendingLogin // sign-in token → the admin waiting for a code
	enrol   map[uint]string         // admin → the TOTP secret being enrolled
}

// pendingLogin is a right password waiting for its code.
type pendingLogin struct {
	adminID uint
	expires time.Time
}

// New wires the server; spa is the built admin web, nil for none. It
// refuses to start without Notif's secret: the link codes and the ntfy
// topics are drawn from it, and without it they would be anyone's to make.
func New(gdb *gorm.DB, a *addon.Addon, cfg config.Config, version string, spa fs.FS) (*Server, error) {
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
		return nil, fmt.Errorf("Notif's secret: %w", err)
	}
	if len(secret) < 32 {
		return nil, errors.New("Notif's secret is too short; the settings row \"secret\" is damaged")
	}
	s.links = &links.Links{DB: gdb, Users: s.users, Secret: secret, Panel: func() links.Panel {
		if c := s.panelClient(); c != nil {
			return c
		}
		return nil
	}}
	s.bots = bots.New(gdb, s.links)
	s.watch = &watch.Watch{DB: gdb, Outbox: s.outbox}
	s.users.Changed = s.watch.Changed
	// A bot that can no longer reach a chat: the outbox has blocked it
	// there; the chat leaves the card only when Notif wrote it.
	s.outbox.Unlink = func(userID uint, key, value string) {
		go func() {
			if _, err := s.links.Release(context.Background(), userID, key, value); err != nil {
				log.Printf("links: release %s of %d: %v", key, userID, err)
			}
		}()
	}
	s.outbox.SubURL = s.subURL
	a.OnSetup(s.registered)
	a.OnEventErr(s.event)
	return s, nil
}

// subURL reads an account's subscription link from the panel.
func (s *Server) subURL(ctx context.Context, userID uint) (string, error) {
	p := s.panelClient()
	if p == nil {
		return "", errors.New("not registered with a panel")
	}
	var acc struct {
		SubURL string `json:"subUrl"`
	}
	if err := p.Get(ctx, "/users/"+strconv.FormatUint(uint64(userID), 10), &acc); err != nil {
		return "", err
	}
	return acc.SubURL, nil
}

// panelID is the registration's panel: its id, or its address for a panel
// too old to send one.
func panelID(c addon.Credentials) string {
	if c.Panel.ID != "" {
		return c.Panel.ID
	}
	return c.Panel.URL
}

// registered keeps the panel Notif was registered with and reads its
// accounts. A panel other than the last one has other accounts under the
// same ids: the copy of the last one's is forgotten first, so no notice
// meant for one of them reaches the new panel's account of that id.
func (s *Server) registered(c addon.Credentials) {
	var last model.Panel
	s.db.Order("registered_at DESC").Limit(1).Find(&last)
	if last.ID != "" && last.ID != panelID(c) {
		if err := s.users.Forget(); err != nil {
			log.Printf("users: forget the accounts of panel %s: %v", last.ID, err)
		}
	}
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
// once a day, until ctx ends; it returns once they have all stopped — the
// outbox after the sends it had in hand.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){s.outbox.Run, s.users.Run, s.bots.Run, s.watch.Run} {
		wg.Go(func() { run(ctx) })
	}
	defer wg.Wait()
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
// account event refreshes the copy of that account and raises its notice;
// a restore of the panel reads every account again as it is now; the
// goodbye forgets the credentials so Notif can be registered again. An
// error — the panel or the database not answering — has the panel deliver
// the event again; what was done already is not done twice (the notices'
// keys carry the event's id).
func (s *Server) event(e addon.Event) error {
	if strings.HasPrefix(e.Event, "user.") {
		return s.userEvent(e)
	}
	if e.Event == "panel.restore_applied" {
		// The accounts are as they were at the backup, their times too: a
		// read that writes each as it finds it is owed, recorded before the
		// event is acknowledged and made by the copy's own loop until one
		// completes (users.Sync.MarkRebase) — not by the panel's delivery,
		// which would wait for it.
		return s.users.MarkRebase()
	}
	if e.Event != "panel.addon_removed" {
		return nil
	}
	if c := s.addon.Credentials(); c != nil {
		s.db.Model(&model.Panel{}).Where("id = ?", panelID(*c)).Update("removed_at", time.Now().Unix())
	}
	// The panel has let go: it delivers nothing again, whatever is answered.
	if err := s.addon.Forget(); err != nil {
		log.Printf("addon: forget the registration: %v", err)
	}
	return nil
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
	s.mountNotices(mux)
	s.mountSends(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusNotFound, "not_found", "no such API route")
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
			writeCode(w, http.StatusUnauthorized, "signed_out", err.Error())
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

// writeErr is an error the admin web shows as the server words it.
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// writeCode is an error with a code the admin web translates
// (frontend/src/i18n.ts, errors.<code>), and the values its words name as
// name, value pairs; msg is the English, for a client that does not know
// the code.
func writeCode(w http.ResponseWriter, status int, code, msg string, params ...string) {
	body := map[string]any{"error": msg, "code": code}
	if len(params) > 1 {
		p := map[string]string{}
		for i := 0; i+1 < len(params); i += 2 {
			p[params[i]] = params[i+1]
		}
		body["params"] = p
	}
	writeJSON(w, status, body)
}

// coded is an error that carries its code for the admin web.
type coded struct {
	code, msg string
	params    []string
}

func (e *coded) Error() string { return e.msg }

// codedErr is an error with a code and the values its words name.
func codedErr(code, msg string, params ...string) error {
	return &coded{code: code, msg: msg, params: params}
}

// fail writes err with status: with its code when it has one — a coded
// error, a channel's settings refused, a link before registration or one
// the card's other writers kept beating — else as the server words it.
func fail(w http.ResponseWriter, status int, err error) {
	var c *coded
	var se *channel.SettingsError
	switch {
	case errors.As(err, &c):
		writeCode(w, status, c.code, c.msg, c.params...)
	case errors.As(err, &se):
		switch se.Code {
		case "required", "choice":
			// The field as a key and its kind: the admin web words it as
			// the channel form labels it.
			writeCode(w, status, "field_"+se.Code, se.Reason, "field", se.Field, "kind", se.Kind)
		case "kind":
			writeCode(w, status, "channel_kind", se.Reason)
		default:
			writeCode(w, status, "channel_settings", se.Reason, "detail", se.Reason)
		}
	case errors.Is(err, links.ErrNotRegistered):
		writeCode(w, status, "not_registered", err.Error())
	case errors.Is(err, links.ErrConflict):
		writeCode(w, http.StatusConflict, "card_conflict", err.Error())
	case status == http.StatusBadGateway:
		writeCode(w, status, "panel_failed", err.Error(), "detail", err.Error())
	default:
		writeErr(w, status, err.Error())
	}
}

// badBody is a body that could not be read.
func badBody(w http.ResponseWriter) {
	writeCode(w, http.StatusBadRequest, "bad_request", "invalid body")
}

// notFound is a route's object that is not there.
func notFound(w http.ResponseWriter, msg string) { writeCode(w, http.StatusNotFound, "not_found", msg) }

// decode reads a JSON body; its error is said as badBody.
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

// eventReadWait bounds an event's read of its account.
const eventReadWait = 6 * time.Second

// userEvent keeps the copy in step with what an event says, and raises
// the notice it calls for (internal/watch).
func (s *Server) userEvent(e addon.Event) error {
	var data struct {
		UserID uint   `json:"userId"`
		Op     string `json:"op"`
		IDs    []uint `json:"ids"`
	}
	_ = json.Unmarshal(e.Data, &data)
	copyOf := func(id uint) (model.User, error) {
		var u model.User
		err := s.db.Limit(1).Find(&u, id).Error
		return u, err
	}
	switch {
	case e.Event == "user.deleted" && data.UserID != 0:
		// Told from the copy, which still has the account.
		u, err := copyOf(data.UserID)
		if err != nil {
			return err
		}
		if err := s.watch.Event(e, u); err != nil {
			return err
		}
		s.users.Gone(data.UserID)
	case e.Event == "user.bulk" && data.Op == "delete":
		for _, id := range data.IDs {
			u, err := copyOf(id)
			if err != nil {
				return err
			}
			if u.ID != 0 && u.GoneAt == 0 {
				if err := s.watch.Deleted(u, e); err != nil {
					return err
				}
			}
			s.users.Gone(id)
		}
	case e.Event == "user.bulk" || e.Event == "user.generated":
		// One event for many accounts says neither what changed on each nor
		// how: a read of the edits finds that out, and tells it. It may be
		// long, so it is not the panel's delivery that waits for it.
		go func() {
			if err := s.users.Edits(context.Background()); err != nil {
				log.Printf("users: after %s: %v", e.Event, err)
			}
		}()
	case data.UserID != 0:
		// The account as it is now: the read tells what an edit changed
		// besides, and the event's notice takes the place of the edits it
		// tells itself. A panel that does not answer has the event
		// delivered again; an account it no longer has is told from the copy.
		// The read ends well within the panel's own wait for the delivery
		// (10 seconds), so a slow one is a failure the panel retries, not
		// a delivery it gave up on while Notif still acted on it.
		ctx, cancel := context.WithTimeout(context.Background(), eventReadWait)
		defer cancel()
		u, err := s.users.One(ctx, data.UserID)
		if err != nil {
			if !panel.IsStatus(err, http.StatusNotFound) {
				return fmt.Errorf("users: after %s: %w", e.Event, err)
			}
			if u, err = copyOf(data.UserID); err != nil {
				return err
			}
		}
		return s.watch.Event(e, u)
	}
	return nil
}
