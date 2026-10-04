package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
)

// The admin's view of the accounts as Notif knows them (GN-S2): who is
// reachable where, the link code a user sends a bot, and taking a link away.

func (s *Server) mountUsers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users", s.signedIn(s.handleUsers))
	mux.HandleFunc("GET /api/users/{id}", s.signedIn(s.handleUser))
	mux.HandleFunc("POST /api/users/{id}/unlink", s.signedIn(s.handleUnlink))
	mux.HandleFunc("POST /api/users/{id}/ntfy", s.signedIn(s.handleNtfy))
}

// reachKeys are the contact keys a channel reaches a user by.
var reachKeys = []string{"telegram_id", "bale_id", "soroush_id", "rubika_id", "phone", "email", "ntfy"}

// linkKeys are the ones Notif writes and may take away.
var linkKeys = []string{"telegram_id", "bale_id", "soroush_id", "rubika_id", "ntfy"}

type userView struct {
	ID      uint              `json:"id"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	Enable  bool              `json:"enable"`
	Expiry  int64             `json:"expiry"`
	Volume  int64             `json:"volume"`
	Used    int64             `json:"used"`
	Reach   map[string]string `json:"reach"`
	Contact map[string]string `json:"contact,omitempty"`
}

func viewUser(u model.User, full bool) userView {
	v := userView{ID: u.ID, Name: u.Name, Group: u.Group, Enable: u.Enable, Expiry: u.Expiry, Volume: u.Volume, Used: u.Used, Reach: map[string]string{}}
	for _, k := range reachKeys {
		if x := u.Contact.V[k]; x != "" {
			v.Reach[k] = x
		}
	}
	if full {
		v.Contact = u.Contact.V
	}
	return v
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	q := s.db.Model(&model.User{}).Where("gone_at = 0")
	if term := strings.TrimSpace(r.URL.Query().Get("q")); term != "" {
		like := "%" + strings.ToLower(term) + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(contact) LIKE ?", like, like)
	}
	var total int64
	q.Count(&total)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	var us []model.User
	if err := q.Order("name").Limit(limit).Offset(max(offset, 0)).Find(&us).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userView, 0, len(us))
	for _, u := range us {
		out = append(out, viewUser(u, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
}

// botLink is one bot a user can link: where to find it and what to send.
type botLink struct {
	ChannelID uint   `json:"channelId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Username  string `json:"username"`
	// URL opens the bot; for Telegram it carries the code, so one tap links.
	URL    string `json:"url,omitempty"`
	Linked string `json:"linked,omitempty"`
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var u model.User
	if s.db.First(&u, id).Error != nil || u.GoneAt > 0 {
		writeErr(w, http.StatusNotFound, "no such account")
		return
	}
	code := s.links.Code(u.ID)
	chs, _ := s.channels()
	var bots []botLink
	for _, c := range chs {
		k, _ := channel.Lookup(c.Kind)
		if k.Contact == "" {
			continue
		}
		b := botLink{ChannelID: c.ID, Name: c.Name, Kind: c.Kind, Username: c.State.V["username"], Linked: u.Contact.V[k.Contact]}
		if b.Username != "" {
			switch c.Kind {
			case "telegram":
				b.URL = "https://t.me/" + b.Username + "?start=" + url.QueryEscape(code)
			case "bale":
				b.URL = "https://ble.ir/" + b.Username
			}
		}
		bots = append(bots, b)
	}
	// The ntfy channels: the address a user subscribes to, once the admin
	// has turned ntfy on for them.
	var feeds []map[string]any
	for _, c := range chs {
		if c.Kind == "ntfy" {
			f := map[string]any{"channelId": c.ID, "name": c.Name, "server": channel.NtfyServer(c.Config.V)}
			if topic := u.Contact.V["ntfy"]; topic != "" {
				f["topic"] = topic
				f["url"] = f["server"].(string) + "/" + topic
			}
			feeds = append(feeds, f)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": viewUser(u, true), "code": code, "bots": bots, "ntfy": feeds})
}

// handleNtfy turns ntfy on for a user: their topic, written to their card.
func (s *Server) handleNtfy(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	topic := s.links.NtfyTopic(id)
	if err := s.links.Set(r.Context(), id, "ntfy", topic); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"topic": topic})
}

func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var b struct {
		Key string `json:"key"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ok := false
	for _, k := range linkKeys {
		ok = ok || k == b.Key
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "only a messenger link can be taken away here")
		return
	}
	if err := s.links.Set(r.Context(), id, b.Key, ""); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
