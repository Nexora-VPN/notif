package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm/clause"
)

// The admin's notices (GN-S4): each kind on or off, urgent or not, its
// words per language and channel kind; the schedule; a preview.

func (s *Server) mountNotices(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notices", s.signedIn(s.handleNotices))
	mux.HandleFunc("PUT /api/notices/{kind}", s.signedIn(s.handleNoticeSave))
	mux.HandleFunc("POST /api/notices/preview", s.signedIn(s.handleNoticePreview))
	mux.HandleFunc("GET /api/settings/schedule", s.signedIn(s.handleSchedule))
	mux.HandleFunc("PUT /api/settings/schedule", s.signedIn(s.handleScheduleSave))
}

type noticeView struct {
	notices.Def
	Enabled  bool                             `json:"enabled"`
	Urgent   bool                             `json:"urgent"`
	Texts    map[string]map[string]model.Text `json:"texts"`
	Defaults map[string]model.Text            `json:"defaults"`
}

func (s *Server) handleNotices(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	book := notices.Load(s.db)
	out := make([]noticeView, 0, len(notices.Catalog))
	for _, d := range notices.Catalog {
		set := book.Setting(d.Kind)
		v := noticeView{Def: d, Enabled: set.Enabled, Urgent: set.Urgent, Texts: set.Texts.V, Defaults: map[string]model.Text{}}
		if v.Texts == nil {
			v.Texts = map[string]map[string]model.Text{}
		}
		for _, l := range settings.Languages {
			v.Defaults[l] = notices.Default(d.Kind, l)
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNoticeSave writes a kind's setting. A text left empty is the
// default's; a channel kind's override is kept only where it says
// something.
func (s *Server) handleNoticeSave(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	kind := r.PathValue("kind")
	if _, ok := notices.Lookup(kind); !ok {
		writeErr(w, http.StatusNotFound, "no such notice")
		return
	}
	var b struct {
		Enabled bool                             `json:"enabled"`
		Urgent  bool                             `json:"urgent"`
		Texts   map[string]map[string]model.Text `json:"texts"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	texts := map[string]map[string]model.Text{}
	for lang, per := range b.Texts {
		known := false
		for _, l := range settings.Languages {
			known = known || l == lang
		}
		if !known {
			writeErr(w, http.StatusBadRequest, "unknown language "+lang)
			return
		}
		for ck, t := range per {
			if _, ok := channel.Lookup(ck); ck != "" && !ok {
				writeErr(w, http.StatusBadRequest, "unknown channel kind "+ck)
				return
			}
			t.Title, t.Body = strings.TrimSpace(t.Title), strings.TrimSpace(t.Body)
			if len(t.Title) > 200 || len(t.Body) > 4000 {
				writeErr(w, http.StatusBadRequest, "a title is 200 characters at most and a text 4000")
				return
			}
			if t.Body == "" {
				continue
			}
			if texts[lang] == nil {
				texts[lang] = map[string]model.Text{}
			}
			texts[lang][ck] = t
		}
	}
	n := model.Notice{Kind: kind, Enabled: b.Enabled, Urgent: b.Urgent, Texts: model.JSON[map[string]map[string]model.Text]{V: texts}}
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&n).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNoticePreview renders a text as a sample account would read it.
func (s *Server) handleNoticePreview(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var b struct {
		Kind  string `json:"kind"`
		Lang  string `json:"lang"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().Unix()
	u := model.User{
		Name: "ana", Group: "gold", SubURL: "https://sub.example.com/sub/3f9a1c", Expiry: now + 3*86400 - 600,
		Volume: 50 << 30, Used: 42 << 30,
	}
	d := model.Delivery{Kind: notices.KindCustom, Title: b.Title, Body: b.Body, Vars: model.JSON[map[string]string]{V: map[string]string{
		"added": strconv.FormatInt(10<<30, 10),
	}}}
	m := notices.Load(s.db).Render(d, u, model.Channel{Name: "Telegram"}, b.Lang)
	writeJSON(w, http.StatusOK, map[string]string{"title": m.Title, "body": m.Text})
}

func (s *Server) handleSchedule(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	writeJSON(w, http.StatusOK, settings.LoadSchedule(s.db))
}

func (s *Server) handleScheduleSave(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var b settings.Schedule
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := settings.SaveSchedule(s.db, b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// A new line may already be crossed: pass the schedule now.
	go s.watch.Tick()
	w.WriteHeader(http.StatusNoContent)
}
