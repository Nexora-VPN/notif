// Package users keeps Notif's copy of the panel's accounts: everything a
// notice needs (name, contact card, expiry, traffic, owner, group), read
// with the addon's token. The panel stays the record (G-D1): the copy is
// refreshed by a full read every ten minutes — the only read that sees
// traffic used, since usage does not touch updated_at — by
// `updated_since` every minute for edits, and at once for an account an
// event names. Beside it, the chats each card names (model.Chat), so a chat
// finds its accounts by an index.
package users

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
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
	// Changed hears an account the copy already had, as a read found it —
	// an edit nobody sent an event for (P36 (i)), or one an event's own
	// read sees; the event then cancels the edit notices it tells itself.
	Changed func(old, new model.User)

	// mu holds a whole read pass (Full, Edits), so two never interleave:
	// each reads the copy as the last one left it, and lastEdit, which only
	// moves forward, is read and written under it.
	mu       sync.Mutex
	lastEdit int64

	// kick wakes Run for a rebase owed (MarkRebase).
	kickOnce sync.Once
	kickCh   chan struct{}
}

// pageSize is the panel's largest page.
const pageSize = 1000

// account is the panel's user object, the fields the copy keeps.
type account struct {
	ID             uint              `json:"id"`
	Name           string            `json:"name"`
	Contact        map[string]string `json:"contact"`
	Enable         bool              `json:"enable"`
	DisabledReason string            `json:"disabledReason"`
	TotalUp        int64             `json:"totalUp"`
	TotalDown      int64             `json:"totalDown"`
	Duration       int64             `json:"duration"`
	ActivatedAt    int64             `json:"activatedAt"`
	Expiry         int64             `json:"expiry"`
	Volume         int64             `json:"volume"`
	Up             int64             `json:"up"`
	Down           int64             `json:"down"`
	Group          string            `json:"group"`
	AdminID        uint              `json:"adminId"`
	SubID          string            `json:"subId"`
	SubToken       string            `json:"subToken"`
	UpdatedAt      int64             `json:"updatedAt"`
}

// SubHash is what the copy keeps of a subscription id or token: enough to
// recognise one a user sends, nothing to rebuild it from.
func SubHash(v string) string {
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("notif-sub:" + v))
	return hex.EncodeToString(sum[:])
}

func (s *Sync) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// save writes what a read found. An account the copy already has in a
// later state — an event's read of one account (One) that landed while a
// page of a longer pass was on its way — keeps it: a read never writes an
// older state over a newer one, nor tells its change again. With force, as
// after a restore of the panel, every account is written as read: the
// panel's own times went back with it.
func (s *Sync) save(list []account, seen int64, watch, force bool) error {
	if len(list) == 0 {
		return nil
	}
	var written []model.User
	old := map[uint]model.User{}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		ids := make([]uint, 0, len(list))
		for _, a := range list {
			ids = append(ids, a.ID)
		}
		var stored []model.User
		if err := tx.Find(&stored, ids).Error; err != nil {
			return err
		}
		for _, r := range stored {
			old[r.ID] = r
		}
		written = s.rows(list, old, seen, force)
		if len(written) == 0 {
			return nil
		}
		// The same condition in the database, for a writer between the
		// read above and this one (PostgreSQL's transactions do not hold
		// the rows read).
		upsert := clause.OnConflict{UpdateAll: true}
		if !force {
			upsert.Where = clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "users.updated_at <= excluded.updated_at"}}}
		}
		res := tx.Clauses(upsert).CreateInBatches(written, 200)
		if res.Error != nil {
			return res.Error
		}
		return saveChats(tx, written)
	})
	if err != nil {
		return err
	}
	if watch && s.Changed != nil {
		for _, r := range written {
			if o, ok := old[r.ID]; ok && o.GoneAt == 0 {
				s.Changed(o, r)
			}
		}
	}
	return nil
}

// rows are the accounts of list to write: each one the copy has not, or
// has in a state no later than the read's — or, with force, every one.
func (s *Sync) rows(list []account, stored map[uint]model.User, seen int64, force bool) []model.User {
	rows := make([]model.User, 0, len(list))
	for _, a := range list {
		if o, ok := stored[a.ID]; ok && !force && o.UpdatedAt > a.UpdatedAt {
			continue
		}
		if a.Contact == nil {
			a.Contact = map[string]string{}
		}
		rows = append(rows, model.User{
			ID: a.ID, Name: a.Name, Contact: model.JSON[map[string]string]{V: a.Contact}, Enable: a.Enable,
			DisabledReason: a.DisabledReason, TotalUsed: a.TotalUp + a.TotalDown, Duration: a.Duration, ActivatedAt: a.ActivatedAt,
			Expiry: a.Expiry, Volume: a.Volume, Used: a.Up + a.Down, Group: a.Group, AdminID: a.AdminID,
			SubIDHash: SubHash(a.SubID), SubTokenHash: SubHash(a.SubToken),
			UpdatedAt: a.UpdatedAt, SeenAt: seen,
		})
	}
	return rows
}

// Full reads every account; one the panel no longer lists is marked gone
// once a read of it confirms it — an account deleted while the pages were
// read shifts the later ones, and the account that slid back across a page
// is missed by the walk but still there.
func (s *Sync) Full(ctx context.Context) error { return s.full(ctx, false) }

// Rebase reads every account after the panel was restored from a backup:
// its accounts, and their times, went back to the backup's, so each is
// written as read even where the copy holds a later time, and a change it
// finds is not told — nobody edited the account; the admin restored the
// panel.
func (s *Sync) Rebase(ctx context.Context) error { return s.full(ctx, true) }

// MarkRebase records that the panel was restored: a Rebase is owed until
// one completes. Run makes it at once, again a minute after one that
// failed, and at every start while it is owed — a stop or a panel that did
// not answer midway leaves it owed, never forgotten.
func (s *Sync) MarkRebase() error {
	if err := settings.MarkRebase(s.DB); err != nil {
		return err
	}
	select {
	case s.kick() <- struct{}{}:
	default:
	}
	return nil
}

func (s *Sync) kick() chan struct{} {
	s.kickOnce.Do(func() { s.kickCh = make(chan struct{}, 1) })
	return s.kickCh
}

// owed makes the rebase owed, if one is, and reports whether one was: it
// stands in for the read it replaces, since a full read or an edits read
// would keep the copy's later times over the restored ones and tell the
// difference as edits nobody made. The mark is cleared only when the pass
// has completed.
func (s *Sync) owed(ctx context.Context) bool {
	mark, err := settings.RebaseOwed(s.DB)
	if err != nil {
		log.Printf("users: is a read after the panel's restore owed: %v", err)
		return false
	}
	if mark == "" {
		return false
	}
	if s.Panel() == nil {
		return true
	}
	if err := s.Rebase(ctx); err != nil {
		log.Printf("users: the read after the panel's restore (tried again in a minute): %v", err)
		return true
	}
	if err := settings.RebaseDone(s.DB, mark); err != nil {
		log.Printf("users: the read after the panel's restore is done, but not recorded: %v", err)
	}
	return true
}

func (s *Sync) full(ctx context.Context, force bool) error {
	p := s.Panel()
	if p == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	start := s.now().Unix()
	for offset := 0; ; offset += pageSize {
		var page struct {
			Items []account `json:"items"`
			Total int       `json:"total"`
		}
		if err := p.Get(ctx, fmt.Sprintf("/users?sort=id&limit=%d&offset=%d", pageSize, offset), &page); err != nil {
			return err
		}
		if err := s.save(page.Items, start, !force, force); err != nil {
			return err
		}
		if len(page.Items) < pageSize {
			break
		}
	}
	s.lastEdit = max(s.lastEdit, start)
	var missed []uint
	if err := s.DB.Model(&model.User{}).Where("seen_at < ? AND gone_at = 0", start).Pluck("id", &missed).Error; err != nil {
		return err
	}
	for _, id := range missed {
		if _, err := s.read(ctx, id, !force, force); err != nil && !panel.IsStatus(err, 404) {
			return err
		}
	}
	return nil
}

// Edits reads the accounts edited since the last read.
func (s *Sync) Edits(ctx context.Context) error {
	p := s.Panel()
	if p == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastEdit == 0 {
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
		if err := s.save(page.Items, start, true, false); err != nil {
			return err
		}
		if len(page.Items) < pageSize {
			break
		}
	}
	s.lastEdit = max(s.lastEdit, start)
	return nil
}

// hasRead reports whether a full read has been made, so edits can be read
// since it.
func (s *Sync) hasRead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEdit != 0
}

// One reads one account now — an event named it — and tells Changed what
// moved, as an edit's read does: the event cancels the edit notices that
// tell what it tells itself (internal/watch), and the rest of what changed
// is still told. A 404 marks it gone.
func (s *Sync) One(ctx context.Context, id uint) (model.User, error) {
	return s.read(ctx, id, true, false)
}

func (s *Sync) read(ctx context.Context, id uint, watch, force bool) (model.User, error) {
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
	if err := s.save([]account{a}, s.now().Unix(), watch, force); err != nil {
		return model.User{}, err
	}
	var u model.User
	return u, s.DB.First(&u, id).Error
}

// Forget drops the copy when Notif is registered with another panel: an
// account there is another person under the same id. With it go what was
// tied to the old panel's accounts — the chats their cards named, the keys
// Notif wrote to them, the channels blocked for them — and the deliveries
// still waiting to reach them; one already in a worker's hands is dropped
// by the outbox (settings.ForgottenThrough). The next full read starts
// over, and a rebase owed to the old panel's restore is owed no more.
func (s *Sync) Forget() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEdit = 0
	return s.DB.Transaction(func(tx *gorm.DB) error {
		all := tx.Session(&gorm.Session{AllowGlobalUpdate: true})
		for _, m := range []any{&model.User{}, &model.Chat{}, &model.Link{}, &model.Block{}} {
			if err := all.Delete(m).Error; err != nil {
				return err
			}
		}
		var last uint
		if err := tx.Model(&model.Delivery{}).Select("COALESCE(MAX(id), 0)").Scan(&last).Error; err != nil {
			return err
		}
		if err := settings.SetForgottenThrough(tx, last); err != nil {
			return err
		}
		if err := settings.RebaseDropped(tx); err != nil {
			return err
		}
		return tx.Model(&model.Delivery{}).
			Where("status IN ?", []string{model.DeliveryQueued, model.DeliveryHeld}).
			Updates(map[string]any{"status": model.DeliveryCancelled, "error": "Notif was registered with another panel"}).Error
	})
}

// Gone marks an account deleted.
func (s *Sync) Gone(id uint) {
	s.DB.Model(&model.User{}).Where("id = ? AND gone_at = 0", id).Update("gone_at", s.now().Unix())
}

// Run keeps the copy fresh until ctx ends, and makes the rebase a restore
// of the panel left owed (MarkRebase) first.
func (s *Sync) Run(ctx context.Context) {
	step := func(n int) {
		if s.owed(ctx) {
			return
		}
		if n%10 == 0 || !s.hasRead() {
			if err := s.Full(ctx); err != nil {
				log.Printf("users: full read: %v", err)
			}
			return
		}
		if err := s.Edits(ctx); err != nil {
			log.Printf("users: edits: %v", err)
		}
	}
	step(0)
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	for n := 1; ; {
		select {
		case <-ctx.Done():
			return
		case <-s.kick():
			s.owed(ctx)
			continue
		case <-minute.C:
		}
		step(n)
		n++
	}
}

// chatKeys are the contact keys a bot links: telegram_id, bale_id,
// soroush_id, rubika_id.
func chatKeys() []string {
	var out []string
	for _, kind := range channel.BotKinds {
		if k, ok := channel.Lookup(kind); ok && k.Contact != "" {
			out = append(out, k.Contact)
		}
	}
	return out
}

// saveChats replaces the saved accounts' rows of model.Chat with the chats
// their cards name now.
func saveChats(tx *gorm.DB, rows []model.User) error {
	keys := chatKeys()
	ids := make([]uint, 0, len(rows))
	var chats []model.Chat
	for _, r := range rows {
		ids = append(ids, r.ID)
		for _, k := range keys {
			if v := strings.TrimSpace(r.Contact.V[k]); v != "" {
				chats = append(chats, model.Chat{UserID: r.ID, Key: k, Value: v})
			}
		}
	}
	for start := 0; start < len(ids); start += 500 {
		if err := tx.Where("user_id IN ?", ids[start:min(start+500, len(ids))]).Delete(&model.Chat{}).Error; err != nil {
			return err
		}
	}
	if len(chats) == 0 {
		return nil
	}
	return tx.CreateInBatches(chats, 200).Error
}
