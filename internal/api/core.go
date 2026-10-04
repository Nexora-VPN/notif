package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
)

// The admin's routes for the core (GN-S1): the channels in their order,
// the delivery log, the delivery settings, a test, and the dashboard's
// figures.

func (s *Server) mountCore(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/channel-kinds", s.signedIn(s.handleKinds))
	mux.HandleFunc("GET /api/channels", s.signedIn(s.handleChannels))
	mux.HandleFunc("POST /api/channels", s.signedIn(s.handleChannelCreate))
	mux.HandleFunc("PUT /api/channels/order", s.signedIn(s.handleChannelOrder))
	mux.HandleFunc("PUT /api/channels/{id}", s.signedIn(s.handleChannelUpdate))
	mux.HandleFunc("DELETE /api/channels/{id}", s.signedIn(s.handleChannelDelete))
	mux.HandleFunc("POST /api/test", s.signedIn(s.handleTest))
	mux.HandleFunc("GET /api/deliveries", s.signedIn(s.handleDeliveries))
	mux.HandleFunc("GET /api/deliveries/{id}", s.signedIn(s.handleDelivery))
	mux.HandleFunc("POST /api/deliveries/{id}/cancel", s.signedIn(s.handleDeliveryCancel))
	mux.HandleFunc("GET /api/settings/delivery", s.signedIn(s.handleDeliverySettings))
	mux.HandleFunc("PUT /api/settings/delivery", s.signedIn(s.handleDeliverySettingsSave))
	mux.HandleFunc("GET /api/summary", s.signedIn(s.handleSummary))
}

func pathID(r *http.Request) (uint, error) {
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || n == 0 {
		return 0, errors.New("bad id")
	}
	return uint(n), nil
}

func (s *Server) handleKinds(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	writeJSON(w, http.StatusOK, channel.Kinds())
}

// channelView is a channel as the browser sees it: secrets masked, and how
// much it sent today and this month — what an SMS provider bills.
type channelView struct {
	model.Channel
	Config    map[string]string `json:"config"`
	SentToday int64             `json:"sentToday"`
	SentMonth int64             `json:"sentMonth"`
}

func viewChannel(c model.Channel) channelView {
	return channelView{Channel: c, Config: channel.Redact(c.Kind, c.Config.V)}
}

// sentCounts is each channel's sent attempts since midnight and since the
// first of the month, in the delivery settings' zone.
func (s *Server) sentCounts() (today, month map[uint]int64) {
	cfg, _ := settings.LoadDelivery(s.db)
	now := time.Now().In(cfg.Location())
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	count := func(since int64) map[uint]int64 {
		var rows []struct {
			ChannelID uint
			N         int64
		}
		s.db.Model(&model.Attempt{}).Select("channel_id, COUNT(*) AS n").
			Where("outcome = ? AND at >= ?", model.AttemptSent, since).Group("channel_id").Scan(&rows)
		out := map[uint]int64{}
		for _, r := range rows {
			out[r.ChannelID] = r.N
		}
		return out
	}
	return count(day), count(first)
}

func (s *Server) channels() ([]model.Channel, error) {
	var chs []model.Channel
	err := s.db.Order("position, id").Find(&chs).Error
	return chs, err
}

func (s *Server) handleChannels(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	chs, err := s.channels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	today, month := s.sentCounts()
	out := make([]channelView, 0, len(chs))
	for _, c := range chs {
		v := viewChannel(c)
		v.SentToday, v.SentMonth = today[c.ID], month[c.ID]
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

type channelBody struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	PerMinute int               `json:"perMinute"`
	Config    map[string]string `json:"config"`
}

func (b *channelBody) check() error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || len(b.Name) > 64 {
		return errors.New("a channel needs a name of up to 64 characters")
	}
	if b.PerMinute < 0 || b.PerMinute > 100000 {
		return errors.New("the rate is 0 (the kind's default) to 100000 a minute")
	}
	return nil
}

func (s *Server) handleChannelCreate(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var b channelBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := b.check(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := channel.Check(b.Kind, b.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var last model.Channel
	s.db.Order("position desc").Limit(1).Find(&last)
	c := model.Channel{
		Kind: b.Kind, Name: b.Name, Enabled: b.Enabled, PerMinute: b.PerMinute,
		Position: last.Position + 1, Config: model.JSON[map[string]string]{V: cfg},
	}
	if err := s.db.Create(&c).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, viewChannel(c))
}

func (s *Server) handleChannelUpdate(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var c model.Channel
	if s.db.First(&c, id).Error != nil {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	var b channelBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := b.check(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Kind != "" && b.Kind != c.Kind {
		writeErr(w, http.StatusBadRequest, "a channel's kind cannot change; add a new channel")
		return
	}
	cfg, err := channel.Check(c.Kind, channel.Merge(c.Kind, c.Config.V, b.Config))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.Name, c.Enabled, c.PerMinute, c.Config = b.Name, b.Enabled, b.PerMinute, model.JSON[map[string]string]{V: cfg}
	if err := s.db.Select("name", "enabled", "per_minute", "config").Updates(&c).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewChannel(c))
}

// handleChannelOrder sets the fall-back order: every channel's id, first
// tried first.
func (s *Server) handleChannelOrder(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var b struct {
		IDs []uint `json:"ids"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	chs, err := s.channels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	known := map[uint]bool{}
	for _, c := range chs {
		known[c.ID] = true
	}
	seen := map[uint]bool{}
	for _, id := range b.IDs {
		if !known[id] || seen[id] {
			writeErr(w, http.StatusBadRequest, "the order must name every channel once")
			return
		}
		seen[id] = true
	}
	if len(seen) != len(known) {
		writeErr(w, http.StatusBadRequest, "the order must name every channel once")
		return
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range b.IDs {
			if err := tx.Model(&model.Channel{}).Where("id = ?", id).Update("position", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChannelDelete(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res := s.db.Delete(&model.Channel{}, id); res.RowsAffected == 0 {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTest queues a test notice to one account, by name: through one
// channel when it names one, else through the fall-back order. A test is
// urgent unless it asks to wait out the quiet hours like a notice.
func (s *Server) handleTest(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var b struct {
		User    string `json:"user"`
		Channel uint   `json:"channel"`
		Quiet   bool   `json:"quiet"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.userByName(r, strings.TrimSpace(b.User))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if b.Channel != 0 && s.db.First(&model.Channel{}, b.Channel).Error != nil {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	d := model.Delivery{
		Key:    fmt.Sprintf("test:%d:%d:%d", u.ID, b.Channel, time.Now().UnixNano()),
		UserID: u.ID, Kind: notices.KindTest, Urgent: !b.Quiet, Only: b.Channel,
	}
	if _, err := s.outbox.Enqueue(d); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var queued model.Delivery
	s.db.Where("key = ?", d.Key).First(&queued)
	writeJSON(w, http.StatusAccepted, queued)
}

// userByName is an account from the copy, or read from the panel when the
// copy does not have it yet.
func (s *Server) userByName(r *http.Request, name string) (model.User, error) {
	var u model.User
	if name == "" {
		return u, errors.New("name an account")
	}
	if s.db.Where("name = ? AND gone_at = 0", name).First(&u).Error == nil {
		return u, nil
	}
	if p := s.panelClient(); p != nil {
		var page struct {
			Items []struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if p.Get(r.Context(), "/users?limit=20&q="+urlQuery(name), &page) == nil {
			for _, it := range page.Items {
				if it.Name == name {
					return s.users.One(r.Context(), it.ID)
				}
			}
		}
	}
	return u, fmt.Errorf("no account named %q on the panel", name)
}

// deliveryView is a delivery with its attempts and names.
type deliveryView struct {
	model.Delivery
	UserName    string        `json:"userName"`
	ChannelName string        `json:"channelName"`
	Attempts    []attemptView `json:"attempts,omitempty"`
}

type attemptView struct {
	model.Attempt
	ChannelName string `json:"channelName"`
}

func (s *Server) channelNames() map[uint]string {
	chs, _ := s.channels()
	out := map[uint]string{}
	for _, c := range chs {
		out[c.ID] = c.Name
	}
	return out
}

func (s *Server) handleDeliveries(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	q := s.db.Model(&model.Delivery{})
	if st := r.URL.Query().Get("status"); st != "" {
		q = q.Where("status IN ?", strings.Split(st, ","))
	}
	if k := r.URL.Query().Get("kind"); k != "" {
		q = q.Where("kind = ?", k)
	}
	if name := strings.TrimSpace(r.URL.Query().Get("user")); name != "" {
		q = q.Where("user_id IN (?)", s.db.Model(&model.User{}).Select("id").Where("name LIKE ?", "%"+name+"%"))
	}
	var total int64
	q.Count(&total)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	var ds []model.Delivery
	if err := q.Order("id desc").Limit(limit).Offset(max(offset, 0)).Find(&ds).Error; err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.views(ds, false), "total": total})
}

func (s *Server) views(ds []model.Delivery, attempts bool) []deliveryView {
	names := s.channelNames()
	ids := make([]uint, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.UserID)
	}
	var us []model.User
	s.db.Select("id", "name").Find(&us, ids)
	users := map[uint]string{}
	for _, u := range us {
		users[u.ID] = u.Name
	}
	out := make([]deliveryView, 0, len(ds))
	for _, d := range ds {
		v := deliveryView{Delivery: d, UserName: users[d.UserID], ChannelName: names[d.ChannelID]}
		if attempts {
			var as []model.Attempt
			s.db.Where("delivery_id = ?", d.ID).Order("id").Find(&as)
			for _, a := range as {
				v.Attempts = append(v.Attempts, attemptView{Attempt: a, ChannelName: names[a.ChannelID]})
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handleDelivery(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var d model.Delivery
	if s.db.First(&d, id).Error != nil {
		writeErr(w, http.StatusNotFound, "no such delivery")
		return
	}
	writeJSON(w, http.StatusOK, s.views([]model.Delivery{d}, true)[0])
}

// handleDeliveryCancel stops a delivery that has not gone out.
func (s *Server) handleDeliveryCancel(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res := s.db.Model(&model.Delivery{}).Where("id = ? AND status IN ?", id, []string{model.DeliveryQueued, model.DeliveryHeld}).
		Update("status", model.DeliveryCancelled)
	if res.RowsAffected == 0 {
		writeErr(w, http.StatusConflict, "only a delivery that is waiting can be cancelled")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeliverySettings(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	d, err := settings.LoadDelivery(s.db)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": d, "serverZone": serverZone(time.Now())})
}

func (s *Server) handleDeliverySettingsSave(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var d settings.Delivery
	if err := decode(r, &d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := settings.SaveDelivery(s.db, d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.outbox.Kick()
	w.WriteHeader(http.StatusNoContent)
}

// summary is the dashboard's figures: today's deliveries by state and by
// channel, what waits, and the size of the copy.
type summary struct {
	Today     map[string]int64 `json:"today"`
	ByChannel []struct {
		ChannelID uint   `json:"channelId"`
		Name      string `json:"name"`
		Sent      int64  `json:"sent"`
	} `json:"byChannel"`
	Waiting  int64 `json:"waiting"`
	Held     int64 `json:"held"`
	Users    int64 `json:"users"`
	Channels int64 `json:"channels"`
	Quiet    bool  `json:"quiet"`
}

func (s *Server) handleSummary(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	cfg, _ := settings.LoadDelivery(s.db)
	now := time.Now().In(cfg.Location())
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	out := summary{Today: map[string]int64{}, Quiet: !cfg.QuietUntil(time.Now()).IsZero()}
	var rows []struct {
		Status string
		N      int64
	}
	s.db.Model(&model.Delivery{}).Select("status, COUNT(*) AS n").Where("created_at >= ?", midnight).Group("status").Scan(&rows)
	for _, r := range rows {
		out.Today[r.Status] = r.N
	}
	s.db.Model(&model.Delivery{}).Where("status IN ?", []string{model.DeliveryQueued, model.DeliverySending}).Count(&out.Waiting)
	s.db.Model(&model.Delivery{}).Where("status = ?", model.DeliveryHeld).Count(&out.Held)
	s.db.Model(&model.User{}).Where("gone_at = 0").Count(&out.Users)
	s.db.Model(&model.Channel{}).Where("enabled = ?", true).Count(&out.Channels)
	names := s.channelNames()
	var per []struct {
		ChannelID uint
		N         int64
	}
	s.db.Model(&model.Delivery{}).Select("channel_id, COUNT(*) AS n").
		Where("status = ? AND sent_at >= ?", model.DeliverySent, midnight).Group("channel_id").Scan(&per)
	for _, p := range per {
		out.ByChannel = append(out.ByChannel, struct {
			ChannelID uint   `json:"channelId"`
			Name      string `json:"name"`
			Sent      int64  `json:"sent"`
		}{p.ChannelID, names[p.ChannelID], p.N})
	}
	writeJSON(w, http.StatusOK, out)
}

// serverZone names the server's own zone for the admin: its IANA name when
// TZ gives one, else its abbreviation and offset ("CEST, UTC+02:00").
func serverZone(now time.Time) string {
	if name := time.Local.String(); name != "Local" {
		return name
	}
	abbr, off := now.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("%s, UTC%s%02d:%02d", abbr, sign, off/3600, off%3600/60)
}

func (s *Server) deliverySettings() settings.Delivery {
	d, _ := settings.LoadDelivery(s.db)
	return d
}
