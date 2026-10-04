package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/watch"
	"gorm.io/gorm"
)

// The admin's own messages (GN-S5): to one account or to a group the
// panel's own filters pick, counted before they go — per channel, with
// what SMS will carry — then queued once per account, sent at the
// channels' rates, cancellable, and reported.

func (s *Server) mountSends(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sends/preview", s.signedIn(s.handleSendPreview))
	mux.HandleFunc("POST /api/sends", s.signedIn(s.handleSendCreate))
	mux.HandleFunc("GET /api/sends", s.signedIn(s.handleSends))
	mux.HandleFunc("GET /api/sends/{id}", s.signedIn(s.handleSend))
	mux.HandleFunc("POST /api/sends/{id}/cancel", s.signedIn(s.handleSendCancel))
	mux.HandleFunc("GET /api/users/{id}/history", s.signedIn(s.handleHistory))
}

// filterKeys are the panel's list filters a group may use, by their
// names on GET /api/v1/users.
var filterKeys = []string{
	"q", "group", "status", "admin_id", "template_id", "node_id", "protocol",
	"expires_after", "expires_before", "expiry", "used_min", "used_max", "online", "sub_fetched",
}

type sendBody struct {
	// UserIDs names accounts one by one; Filter picks a group on the panel.
	// One of the two.
	UserIDs []uint            `json:"userIds"`
	Filter  map[string]string `json:"filter"`
	Title   string            `json:"title"`
	Body    string            `json:"body"`
	Urgent  bool              `json:"urgent"`
}

func (b *sendBody) check(needText bool) error {
	b.Title, b.Body = strings.TrimSpace(b.Title), strings.TrimSpace(b.Body)
	if needText && b.Body == "" {
		return codedErr("send_empty", "write the message")
	}
	if utf8.RuneCountInString(b.Title) > 200 || utf8.RuneCountInString(b.Body) > 4000 {
		return codedErr("text_too_long", "a title is 200 characters at most and a message 4000")
	}
	if len(b.UserIDs) == 0 && len(b.Filter) == 0 {
		return codedErr("send_no_target", "name the accounts, or a group by the panel's filters")
	}
	if len(b.UserIDs) > 0 && len(b.Filter) > 0 {
		return codedErr("send_both", "name the accounts or a group, not both")
	}
	for k, v := range b.Filter {
		known := false
		for _, f := range filterKeys {
			known = known || f == k
		}
		if !known {
			return codedErr("filter_unknown", fmt.Sprintf("%q is not one of the panel's filters", k), "filter", k)
		}
		if strings.TrimSpace(v) == "" {
			delete(b.Filter, k)
		}
	}
	return nil
}

// recipients are the accounts a send names, as the copy has them: one by
// one, or every account the panel's filters match, read page by page.
func (s *Server) recipients(ctx context.Context, b sendBody) ([]model.User, error) {
	ids := b.UserIDs
	if len(b.Filter) > 0 {
		p := s.panelClient()
		if p == nil {
			return nil, codedErr("not_registered", "not registered with a panel")
		}
		q := url.Values{}
		for k, v := range b.Filter {
			q.Set(k, v)
		}
		q.Set("sort", "id")
		q.Set("limit", "1000")
		for offset := 0; ; offset += 1000 {
			q.Set("offset", strconv.Itoa(offset))
			var page struct {
				Items []struct {
					ID uint `json:"id"`
				} `json:"items"`
			}
			if err := p.Get(ctx, "/users?"+q.Encode(), &page); err != nil {
				return nil, err
			}
			for _, it := range page.Items {
				ids = append(ids, it.ID)
			}
			if len(page.Items) < 1000 {
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var us []model.User
	for start := 0; start < len(ids); start += 500 {
		var part []model.User
		if err := s.db.Where("gone_at = 0").Find(&part, ids[start:min(start+500, len(ids))]).Error; err != nil {
			return nil, err
		}
		us = append(us, part...)
	}
	sort.Slice(us, func(i, j int) bool { return us[i].ID < us[j].ID })
	return us, nil
}

// smsKinds carry an SMS, which is what an operator pays per message.
func smsChannel(c model.Channel) bool {
	return c.Kind == "kavenegar" || c.Kind == "faraz" || c.Kind == "http" && watch.AddressKey(c) == "phone"
}

// plan is where each account's message would go: the first channel in the
// admin's order that has an address for it. A prediction — a bot the user
// blocked falls further — but the one an operator decides on.
type plan struct {
	Total       int            `json:"total"`
	Unreachable int            `json:"unreachable"`
	SMS         int            `json:"sms"`
	ByChannel   []channelCount `json:"byChannel"`
	// Sample is a few of the accounts, to see the group is the one meant.
	Sample []string `json:"sample"`
	// Preview is the message as the first account would read it.
	Preview *model.Text `json:"preview,omitempty"`
}

type channelCount struct {
	ChannelID uint   `json:"channelId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Count     int    `json:"count"`
	SMS       bool   `json:"sms"`
}

func (s *Server) plan(us []model.User) (plan, map[uint]bool) {
	chs, _ := s.channels()
	var on []model.Channel
	for _, c := range chs {
		if c.Enabled {
			on = append(on, c)
		}
	}
	counts := make([]channelCount, len(on))
	for i, c := range on {
		counts[i] = channelCount{ChannelID: c.ID, Name: c.Name, Kind: c.Kind, SMS: smsChannel(c)}
	}
	p := plan{Total: len(us)}
	reach := map[uint]bool{}
	for _, u := range us {
		if len(p.Sample) < 8 {
			p.Sample = append(p.Sample, u.Name)
		}
		for i, c := range on {
			key := watch.AddressKey(c)
			if key == "" || strings.TrimSpace(u.Contact.V[key]) != "" {
				counts[i].Count++
				if counts[i].SMS {
					p.SMS++
				}
				reach[u.ID] = true
				break
			}
		}
		if !reach[u.ID] {
			p.Unreachable++
		}
	}
	for _, c := range counts {
		if c.Count > 0 {
			p.ByChannel = append(p.ByChannel, c)
		}
	}
	return p, reach
}

func (s *Server) handleSendPreview(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	var b sendBody
	if err := decode(r, &b); err != nil {
		badBody(w)
		return
	}
	if err := b.check(false); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	us, err := s.recipients(r.Context(), b)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	p, _ := s.plan(us)
	if len(us) > 0 && b.Body != "" {
		cfg := s.deliverySettings()
		u := us[0]
		lang := notices.Language(u, cfg.Language)
		d := model.Delivery{Kind: notices.KindCustom, Title: b.Title, Body: b.Body}
		book := notices.Load(s.db)
		if book.NamesSubURL(d, model.Channel{}, lang) {
			// The copy keeps no link; the preview reads it as the send will.
			u.SubURL, _ = s.subURL(r.Context(), u.ID)
		}
		m := book.Render(d, u, model.Channel{}, lang)
		p.Preview = &model.Text{Title: m.Title, Body: m.Text}
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleSendCreate(w http.ResponseWriter, r *http.Request, a model.Admin) {
	var b sendBody
	if err := decode(r, &b); err != nil {
		badBody(w)
		return
	}
	if err := b.check(true); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	us, err := s.recipients(r.Context(), b)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if len(us) == 0 {
		writeCode(w, http.StatusBadRequest, "no_match", "no account matches")
		return
	}
	p, reach := s.plan(us)
	filter := b.Filter
	if len(b.UserIDs) > 0 {
		names := make([]string, 0, len(us))
		for _, u := range us {
			names = append(names, u.Name)
		}
		filter = map[string]string{"accounts": strings.Join(names, ", ")}
	}
	send := model.Send{
		Title: b.Title, Body: b.Body, Urgent: b.Urgent, Filter: model.JSON[map[string]string]{V: filter},
		Total: p.Total, Unreachable: p.Unreachable,
	}
	// The send and every delivery in one transaction: a message is queued
	// to the whole group or to none of it.
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&send).Error; err != nil {
			return err
		}
		now := time.Now().Unix()
		var batch []model.Delivery
		for _, u := range us {
			if !reach[u.ID] {
				continue
			}
			batch = append(batch, model.Delivery{
				Key: fmt.Sprintf("send:%d:%d", send.ID, u.ID), UserID: u.ID, Kind: notices.KindCustom,
				Title: b.Title, Body: b.Body, Urgent: b.Urgent, SendID: send.ID,
				Status: model.DeliveryQueued, NextAt: now, CreatedAt: now,
				Vars: model.JSON[map[string]string]{V: map[string]string{}},
			})
		}
		return tx.CreateInBatches(batch, 200).Error
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.outbox.Kick()
	writeJSON(w, http.StatusCreated, s.report(send))
}

// sendReport is a send with how its deliveries stand.
type sendReport struct {
	model.Send
	Queued    int64          `json:"queued"`
	Sent      int64          `json:"sent"`
	Failed    int64          `json:"failed"`
	Cancelled int64          `json:"cancelled"`
	ByChannel []channelCount `json:"byChannel"`
}

func (s *Server) report(send model.Send) sendReport {
	rep := sendReport{Send: send}
	var rows []struct {
		Status string
		N      int64
	}
	s.db.Model(&model.Delivery{}).Select("status, COUNT(*) AS n").Where("send_id = ?", send.ID).Group("status").Scan(&rows)
	for _, r := range rows {
		switch r.Status {
		case model.DeliverySent:
			rep.Sent += r.N
		case model.DeliveryFailed, model.DeliveryUnknown:
			rep.Failed += r.N
		case model.DeliveryCancelled:
			rep.Cancelled += r.N
		default:
			rep.Queued += r.N
		}
	}
	var per []struct {
		ChannelID uint
		N         int64
	}
	s.db.Model(&model.Delivery{}).Select("channel_id, COUNT(*) AS n").
		Where("send_id = ? AND status = ?", send.ID, model.DeliverySent).Group("channel_id").Scan(&per)
	chs, _ := s.channels()
	byID := map[uint]model.Channel{}
	for _, c := range chs {
		byID[c.ID] = c
	}
	for _, p := range per {
		c := byID[p.ChannelID]
		rep.ByChannel = append(rep.ByChannel, channelCount{ChannelID: p.ChannelID, Name: c.Name, Kind: c.Kind, Count: int(p.N), SMS: smsChannel(c)})
	}
	return rep
}

func (s *Server) handleSends(w http.ResponseWriter, _ *http.Request, _ model.Admin) {
	var sends []model.Send
	s.db.Order("id desc").Limit(50).Find(&sends)
	out := make([]sendReport, 0, len(sends))
	for _, sd := range sends {
		out = append(out, s.report(sd))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var sd model.Send
	if s.db.First(&sd, id).Error != nil {
		notFound(w, "no such message")
		return
	}
	writeJSON(w, http.StatusOK, s.report(sd))
}

// handleSendCancel stops what of a send has not gone out.
func (s *Server) handleSendCancel(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var sd model.Send
	if s.db.First(&sd, id).Error != nil {
		notFound(w, "no such message")
		return
	}
	s.db.Model(&model.Delivery{}).Where("send_id = ? AND status IN ?", id, []string{model.DeliveryQueued, model.DeliveryHeld}).
		Updates(map[string]any{"status": model.DeliveryCancelled, "error": "the admin cancelled the message"})
	s.db.Model(&sd).Update("cancelled_at", time.Now().Unix())
	writeJSON(w, http.StatusOK, s.report(sd))
}

// handleHistory is what an account has been sent, newest first.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, _ model.Admin) {
	id, err := pathID(r)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var ds []model.Delivery
	s.db.Where("user_id = ?", id).Order("id desc").Limit(50).Find(&ds)
	writeJSON(w, http.StatusOK, s.views(ds, false))
}
