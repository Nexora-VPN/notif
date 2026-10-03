// Package users keeps Notif's copy of the panel's accounts: everything a
// notice needs (name, contact card, expiry, traffic, owner, group), read
// with the addon's token. The panel stays the record (G-D1): the copy is
// refreshed by a full read every ten minutes — the only read that sees
// traffic used, since usage does not touch updated_at — by
// `updated_since` every minute for edits, and at once for an account an
// event names.
package users

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Getter is the panel, as far as the copy needs it (a *panel.Client).
type Getter interface {
	Get(ctx context.Context, path string, out any) error
}

// Sync refreshes the copy.
type Sync struct {
	DB *gorm.DB
	// Panel is the registered panel's client, nil while Notif is not
	// registered.
	Panel func() Getter
	Now   func() time.Time

	lastEdit int64
}

// pageSize is the panel's largest page.
const pageSize = 1000

// account is the panel's user object, the fields the copy keeps.
type account struct {
	ID        uint              `json:"id"`
	Name      string            `json:"name"`
	Contact   map[string]string `json:"contact"`
	Enable    bool              `json:"enable"`
	Expiry    int64             `json:"expiry"`
	Volume    int64             `json:"volume"`
	Up        int64             `json:"up"`
	Down      int64             `json:"down"`
	Group     string            `json:"group"`
	AdminID   uint              `json:"adminId"`
	SubURL    string            `json:"subUrl"`
	UpdatedAt int64             `json:"updatedAt"`
}

func (s *Sync) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sync) save(list []account, seen int64) error {
	if len(list) == 0 {
		return nil
	}
	rows := make([]model.User, 0, len(list))
	for _, a := range list {
		if a.Contact == nil {
			a.Contact = map[string]string{}
		}
		rows = append(rows, model.User{
			ID: a.ID, Name: a.Name, Contact: model.JSON[map[string]string]{V: a.Contact}, Enable: a.Enable,
			Expiry: a.Expiry, Volume: a.Volume, Used: a.Up + a.Down, Group: a.Group, AdminID: a.AdminID,
			SubURL: a.SubURL, UpdatedAt: a.UpdatedAt, SeenAt: seen,
		})
	}
	return s.DB.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(rows, 200).Error
}

// Full reads every account; one the panel no longer lists is marked gone.
func (s *Sync) Full(ctx context.Context) error {
	p := s.Panel()
	if p == nil {
		return nil
	}
	start := s.now().Unix()
	for offset := 0; ; offset += pageSize {
		var page struct {
			Items []account `json:"items"`
			Total int       `json:"total"`
		}
		if err := p.Get(ctx, fmt.Sprintf("/users?sort=id&limit=%d&offset=%d", pageSize, offset), &page); err != nil {
			return err
		}
		if err := s.save(page.Items, start); err != nil {
			return err
		}
		if len(page.Items) < pageSize {
			break
		}
	}
	s.lastEdit = start
	return s.DB.Model(&model.User{}).Where("seen_at < ? AND gone_at = 0", start).Update("gone_at", start).Error
}

// Edits reads the accounts edited since the last read.
func (s *Sync) Edits(ctx context.Context) error {
	p := s.Panel()
	if p == nil || s.lastEdit == 0 {
		return nil
	}
	start := s.now().Unix()
	since := s.lastEdit - 2 // a second either side of a clock tick
	for offset := 0; ; offset += pageSize {
		var page struct {
			Items []account `json:"items"`
		}
		if err := p.Get(ctx, fmt.Sprintf("/users?sort=id&updated_since=%d&limit=%d&offset=%d", since, pageSize, offset), &page); err != nil {
			return err
		}
		if err := s.save(page.Items, start); err != nil {
			return err
		}
		if len(page.Items) < pageSize {
			break
		}
	}
	s.lastEdit = start
	return nil
}

// One reads one account now (an event named it); a 404 marks it gone.
func (s *Sync) One(ctx context.Context, id uint) (model.User, error) {
	p := s.Panel()
	if p == nil {
		return model.User{}, fmt.Errorf("not registered with a panel")
	}
	var a account
	if err := p.Get(ctx, "/users/"+strconv.FormatUint(uint64(id), 10), &a); err != nil {
		if panel.IsStatus(err, 404) {
			s.Gone(id)
		}
		return model.User{}, err
	}
	if err := s.save([]account{a}, s.now().Unix()); err != nil {
		return model.User{}, err
	}
	var u model.User
	return u, s.DB.First(&u, id).Error
}

// Gone marks an account deleted.
func (s *Sync) Gone(id uint) {
	s.DB.Model(&model.User{}).Where("id = ? AND gone_at = 0", id).Update("gone_at", s.now().Unix())
}

// Run keeps the copy fresh until ctx ends.
func (s *Sync) Run(ctx context.Context) {
	full := func() {
		if err := s.Full(ctx); err != nil {
			log.Printf("users: full read: %v", err)
		}
	}
	full()
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-minute.C:
		}
		if n%10 == 0 || s.lastEdit == 0 {
			full()
			continue
		}
		if err := s.Edits(ctx); err != nil {
			log.Printf("users: edits: %v", err)
		}
	}
}
