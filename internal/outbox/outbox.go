// Package outbox sends the deliveries: one notice to one user, handed to
// the user's channels in the admin's order until one delivers it (P32).
//
// The promises it keeps:
//   - Once. A delivery's key is unique, so the same occasion is queued once;
//     an attempt is written as started before the channel is called, and a
//     delivery found mid-send after a restart is marked unknown rather than
//     sent again — a restart sends nobody anything twice.
//   - Fall-back. No address and a refusal pass to the next channel at once;
//     a failure worth retrying is tried three times on the same channel
//     (after 30 s, 2 min and 10 min, or the wait the provider named) before
//     the next one is.
//   - Quiet hours. A notice that is not urgent waits for them to end; it is
//     held, never dropped.
//   - Rate. Each channel sends at most its per-minute rate; a delivery that
//     would exceed it waits for the next token without counting a try.
//   - Blocks. A bot the user blocked, stopped or never started is not
//     tried again at that chat (model.Block); the chat is taken off the
//     account's card only when Notif itself wrote it there.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Tries is how often one channel is tried for one delivery before the next
// is, and Backoff the waits between them.
var (
	Tries   = 3
	Backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}
)

// Outbox sends.
type Outbox struct {
	DB *gorm.DB
	// Now is the clock (tests move it).
	Now func() time.Time
	// Workers is how many deliveries are sent at once.
	Workers int
	// Unlink hears that a channel can no longer reach a user at value, the
	// account's contact key: it takes the key off the card when Notif wrote
	// it there and the card still holds value (links.Release). The outbox
	// has already blocked the channel for that address.
	Unlink func(userID uint, key, value string)
	// SubURL reads an account's subscription link from the panel, for a
	// notice that names it: the copy never keeps the link's secret.
	SubURL func(ctx context.Context, userID uint) (string, error)

	kick chan struct{}
	mu   sync.Mutex
	// senders are built once per channel and its settings.
	senders map[uint]cachedSender
	buckets map[uint]*bucket

	// cur is the pass the workers send under, read again when it is older
	// than PassAge or Refresh says it changed.
	passMu sync.Mutex
	cur    *pass
	curAt  time.Time
}

// PassAge bounds how old the settings, the words and the channels a
// delivery is sent under may be: a channel switched off or deleted stops
// sending within it, and at once when the admin web says so (Refresh).
const PassAge = 30 * time.Second

// Refresh has the next delivery read the settings, the notices' words and
// the channels again: the admin changed them.
func (o *Outbox) Refresh() {
	o.passMu.Lock()
	o.cur = nil
	o.passMu.Unlock()
}

// current is the pass to send under now.
func (o *Outbox) current() *pass {
	o.passMu.Lock()
	defer o.passMu.Unlock()
	if o.cur == nil || time.Since(o.curAt) > PassAge {
		o.cur, o.curAt = o.snapshot(), time.Now()
	}
	return o.cur
}

type cachedSender struct {
	version string
	sender  channel.Sender
}

// New is an outbox on a database.
func New(gdb *gorm.DB) *Outbox {
	return &Outbox{
		DB: gdb, Now: time.Now, Workers: 4, kick: make(chan struct{}, 1),
		senders: map[uint]cachedSender{}, buckets: map[uint]*bucket{},
	}
}

// Enqueue queues a delivery unless its key was raised before — while its
// delivery is in the log, and a lasting key (a schedule line's) in
// model.Once long after — and reports whether it queued.
func (o *Outbox) Enqueue(d model.Delivery) (bool, error) {
	if d.Key == "" || d.UserID == 0 || d.Kind == "" {
		return false, errors.New("a delivery needs a key, a user and a kind")
	}
	d.ID = 0
	d.Status = model.DeliveryQueued
	// A delivery may ask to wait (an edit's notice, in case the event that
	// tells the same change is on its way); otherwise it is due now.
	d.NextAt = max(d.NextAt, o.Now().Unix())
	d.CreatedAt = o.Now().Unix()
	queued := false
	err := o.DB.Transaction(func(tx *gorm.DB) error {
		if d.Lasting {
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Once{Key: d.Key, At: d.CreatedAt})
			if res.Error != nil || res.RowsAffected == 0 {
				return res.Error
			}
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&d)
		queued = res.RowsAffected == 1
		return res.Error
	})
	if err != nil {
		return false, err
	}
	if queued {
		o.Kick()
	}
	return queued, nil
}

// Raised is which of the lasting keys were raised before, read a few
// hundred keys at a time — for a pass over every account, which would
// otherwise ask once per account.
func (o *Outbox) Raised(keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(keys); start += 500 {
		var got []string
		if err := o.DB.Model(&model.Once{}).Where("key IN ?", keys[start:min(start+500, len(keys))]).Pluck("key", &got).Error; err != nil {
			return nil, err
		}
		for _, k := range got {
			out[k] = true
		}
	}
	return out, nil
}

// Kick wakes the workers.
func (o *Outbox) Kick() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// Recover runs once at start: a delivery left in a worker's hands either
// had not reached its channel (queued again) or may have (unknown, and not
// sent again).
func (o *Outbox) Recover() error {
	var stuck []model.Delivery
	if err := o.DB.Where("status = ?", model.DeliverySending).Find(&stuck).Error; err != nil {
		return err
	}
	for _, d := range stuck {
		var started int64
		o.DB.Model(&model.Attempt{}).Where("delivery_id = ? AND outcome = ?", d.ID, model.AttemptStarted).Count(&started)
		if started > 0 {
			o.DB.Model(&model.Attempt{}).Where("delivery_id = ? AND outcome = ?", d.ID, model.AttemptStarted).
				Updates(map[string]any{"outcome": model.AttemptError, "detail": "Notif stopped during this attempt; it may have been delivered"})
			o.DB.Model(&d).Updates(map[string]any{"status": model.DeliveryUnknown, "error": "Notif stopped while sending; not sent again so nobody gets it twice"})
			continue
		}
		o.DB.Model(&d).Update("status", model.DeliveryQueued)
	}
	return nil
}

// Run sends until ctx ends, then waits for the sends in hand: each ends
// within its own timeout, which ctx's end does not cut short.
func (o *Outbox) Run(ctx context.Context) {
	if err := o.Recover(); err != nil {
		log.Printf("outbox: recover: %v", err)
	}
	jobs := make(chan uint)
	var wg sync.WaitGroup
	for range max(o.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				// The settings, the words and the channels at most PassAge
				// old: one read for many deliveries, not one each.
				o.process(ctx, o.current(), id)
			}
		}()
	}
	defer func() { close(jobs); wg.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ids := o.claim(50); len(ids) > 0 {
			for i, id := range ids {
				select {
				case jobs <- id:
				case <-ctx.Done():
					// What was claimed and not handed out goes back.
					o.DB.Model(&model.Delivery{}).Where("id IN ? AND status = ?", ids[i:], model.DeliverySending).
						Update("status", model.DeliveryQueued)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-o.kick:
		}
	}
}

// claim takes up to n due deliveries into a worker's hands. The update's
// condition makes the claim exclusive between workers and processes.
func (o *Outbox) claim(n int) []uint {
	var due []model.Delivery
	o.DB.Select("id").Where("status IN ? AND next_at <= ?", []string{model.DeliveryQueued, model.DeliveryHeld}, o.Now().Unix()).
		Order("next_at, id").Limit(n).Find(&due)
	var out []uint
	for _, d := range due {
		res := o.DB.Model(&model.Delivery{}).Where("id = ? AND status IN ?", d.ID, []string{model.DeliveryQueued, model.DeliveryHeld}).
			Update("status", model.DeliverySending)
		if res.RowsAffected == 1 {
			out = append(out, d.ID)
		}
	}
	return out
}

// ProcessDue sends every due delivery now, in this goroutine (tests).
func (o *Outbox) ProcessDue(ctx context.Context) {
	for {
		ids := o.claim(50)
		if len(ids) == 0 {
			return
		}
		p := o.snapshot()
		for _, id := range ids {
			o.process(ctx, p, id)
		}
	}
}

func (o *Outbox) set(d *model.Delivery, fields map[string]any) {
	if err := o.DB.Model(d).Updates(fields).Error; err != nil {
		log.Printf("outbox: delivery %d: %v", d.ID, err)
	}
}

// pass is what deliveries are sent under, read once for many: the
// delivery settings, the notices' words and the channels.
type pass struct {
	cfg  settings.Delivery
	book notices.Book
	// channels is every channel in the admin's order; err is why they
	// could not be read.
	channels []model.Channel
	err      error
}

func (o *Outbox) snapshot() *pass {
	p := &pass{book: notices.Load(o.DB)}
	p.cfg, _ = settings.LoadDelivery(o.DB)
	if p.err = o.DB.Find(&p.channels).Error; p.err == nil {
		sort.SliceStable(p.channels, func(i, j int) bool {
			if p.channels[i].Position != p.channels[j].Position {
				return p.channels[i].Position < p.channels[j].Position
			}
			return p.channels[i].ID < p.channels[j].ID
		})
	}
	return p
}

// order is the channels to try: the admin's order of the enabled ones, or
// the one channel a test names.
func (p *pass) order(d model.Delivery) ([]model.Channel, error) {
	if p.err != nil {
		return nil, p.err
	}
	var out []model.Channel
	for _, c := range p.channels {
		if d.Only != 0 && c.ID == d.Only || d.Only == 0 && c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// process takes one claimed delivery one step: sent, passed on, retried
// later, held, or failed.
func (o *Outbox) process(ctx context.Context, p *pass, id uint) {
	var d model.Delivery
	if o.DB.First(&d, id).Error != nil {
		return
	}
	now := o.Now()
	var u model.User
	found := o.DB.First(&u, d.UserID).Error == nil
	// A delivery made before the copy was forgotten (users.Sync.Forget) was
	// meant for an account of the panel Notif has left; the account under
	// its id now is another panel's, another person's, whatever state the
	// delivery was in. Read after the account, so an account the new
	// panel's read wrote is never taken for the old one's.
	if last, err := settings.ForgottenThrough(o.DB); err != nil {
		o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Add(time.Minute).Unix()})
		return
	} else if d.ID <= last {
		o.set(&d, map[string]any{"status": model.DeliveryCancelled, "error": "Notif was registered with another panel"})
		return
	}
	cfg := p.cfg
	if !d.Urgent {
		if until := cfg.QuietUntil(now); !until.IsZero() {
			o.set(&d, map[string]any{"status": model.DeliveryHeld, "next_at": until.Unix()})
			return
		}
	}
	// A deleted account still hears that it was deleted, from the copy.
	if !found || u.GoneAt > 0 && d.Kind != "deleted" {
		o.set(&d, map[string]any{"status": model.DeliveryFailed, "error": "the account is no longer on the panel"})
		return
	}
	order, err := p.order(d)
	if err != nil {
		o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Add(time.Minute).Unix()})
		return
	}
	// Where the delivery stands: the channels already done with, and how
	// often the current one has failed. An attempt that sent ends it: Notif
	// stopped between writing the attempt and the delivery.
	var attempts []model.Attempt
	o.DB.Where("delivery_id = ?", d.ID).Order("id").Find(&attempts)
	done := map[uint]bool{}
	errs := map[uint]int{}
	for _, a := range attempts {
		switch a.Outcome {
		case model.AttemptSent:
			o.set(&d, map[string]any{"status": model.DeliverySent, "channel_id": a.ChannelID, "sent_at": a.At, "error": ""})
			return
		case model.AttemptNoAddress, model.AttemptRefused:
			done[a.ChannelID] = true
		case model.AttemptError:
			errs[a.ChannelID]++
			if errs[a.ChannelID] >= Tries {
				done[a.ChannelID] = true
			}
		}
	}
	var blocks []model.Block
	o.DB.Where("user_id = ?", u.ID).Find(&blocks)
	blocked := map[uint]string{}
	for _, b := range blocks {
		blocked[b.ChannelID] = b.Value
	}
	lang := notices.Language(u, cfg.Language)
	to := channel.Recipient{UserID: u.ID, Name: u.Name, Contact: u.Contact.V, Lang: lang}
	subRead := false
	for _, ch := range order {
		if done[ch.ID] {
			continue
		}
		if ctx.Err() != nil {
			// Notif is stopping: the next channel waits for the next start,
			// so a stop waits for one send at most.
			o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Unix()})
			return
		}
		sender, err := o.sender(ch)
		if err != nil {
			o.attempt(d, ch, model.AttemptRefused, "the channel's settings do not work: "+err.Error())
			continue
		}
		// No address, or one this channel is blocked from, passes on at
		// once, before a token of the channel's rate is spent on it.
		if key := channel.AddressKey(ch.Kind, ch.Config.V); key != "" {
			address := strings.TrimSpace(to.Contact[key])
			if address == "" {
				o.attempt(d, ch, model.AttemptNoAddress, channel.ErrNoAddress.Error())
				continue
			}
			if v, ok := blocked[ch.ID]; ok && v == address {
				o.attempt(d, ch, model.AttemptNoAddress, "the user stopped or blocked this bot at "+key+" "+address)
				continue
			}
		}
		msg := p.book.Render(d, u, ch, lang)
		if !subRead && p.book.NamesSubURL(d, ch, lang) {
			// The link is read when a notice names it, and never kept.
			subRead = true
			if u.SubURL, err = o.subURL(ctx, u.ID); err != nil {
				o.set(&d, map[string]any{
					"status": model.DeliveryQueued, "next_at": now.Add(time.Minute).Unix(),
					"error": "the subscription link could not be read from the panel: " + err.Error(),
				})
				return
			}
			msg = p.book.Render(d, u, ch, lang)
		}
		if wait := o.take(ch); wait > 0 {
			o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Add(wait).Unix()})
			return
		}
		a := o.attempt(d, ch, model.AttemptStarted, "")
		// A send in hand finishes within its own timeout even while Notif
		// stops: cut short, its outcome would be unknown.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SendWait)
		err = sender.Send(sctx, to, msg)
		cancel()
		var refused *channel.Refused
		var retry *channel.Retry
		var unknown *channel.Unknown
		switch {
		case err == nil:
			o.finish(a, model.AttemptSent, "")
			o.set(&d, map[string]any{"status": model.DeliverySent, "channel_id": ch.ID, "sent_at": o.Now().Unix(), "error": ""})
			return
		case errors.Is(err, channel.ErrNoAddress):
			o.finish(a, model.AttemptNoAddress, err.Error())
		case errors.As(err, &unknown):
			// The provider may have sent it: neither again nor elsewhere.
			o.finish(a, model.AttemptError, unknown.Reason)
			o.set(&d, map[string]any{"status": model.DeliveryUnknown, "channel_id": ch.ID, "error": unknown.Reason})
			return
		case errors.As(err, &refused):
			detail := refused.Reason
			if refused.Unlink != "" {
				// The panel's card is not touched here: the channel is blocked
				// for this address, and the key is taken off only when Notif
				// wrote it (Unlink).
				value := strings.TrimSpace(to.Contact[refused.Unlink])
				detail += " (not tried again at " + refused.Unlink + " " + value + ")"
				o.block(u.ID, ch.ID, value, refused.Reason)
				if o.Unlink != nil {
					o.Unlink(u.ID, refused.Unlink, value)
				}
			}
			o.finish(a, model.AttemptRefused, detail)
		default:
			reason, after := err.Error(), time.Duration(0)
			if errors.As(err, &retry) {
				reason, after = retry.Reason, retry.After
			}
			o.finish(a, model.AttemptError, reason)
			n := errs[ch.ID] + 1
			if n < Tries {
				if after == 0 {
					after = Backoff[min(n-1, len(Backoff)-1)]
				}
				o.set(&d, map[string]any{"status": model.DeliveryQueued, "channel_id": ch.ID, "next_at": o.Now().Add(after).Unix(), "error": reason})
				return
			}
		}
		d.ChannelID = ch.ID
	}
	msg := "no channel reached the user"
	if len(order) == 0 {
		msg = "no channel is turned on"
	}
	o.set(&d, map[string]any{"status": model.DeliveryFailed, "channel_id": d.ChannelID, "error": msg})
}

// LinkWait and SendWait bound one delivery's work in hand: reading the
// link from the panel, then one channel's send. A stop waits for both
// (main's shutdownWait is longer).
const (
	LinkWait = 10 * time.Second
	SendWait = 30 * time.Second
)

// subURL is an account's subscription link, read from the panel.
func (o *Outbox) subURL(ctx context.Context, userID uint) (string, error) {
	if o.SubURL == nil {
		return "", errors.New("not registered with a panel")
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), LinkWait)
	defer cancel()
	return o.SubURL(rctx, userID)
}

// block records that a channel no longer reaches a user at value.
func (o *Outbox) block(userID, channelID uint, value, reason string) {
	err := o.DB.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Block{
		UserID: userID, ChannelID: channelID, Value: value, Reason: reason, At: o.Now().Unix(),
	}).Error
	if err != nil {
		log.Printf("outbox: block channel %d for %d: %v", channelID, userID, err)
	}
}

func (o *Outbox) attempt(d model.Delivery, ch model.Channel, outcome, detail string) model.Attempt {
	a := model.Attempt{DeliveryID: d.ID, ChannelID: ch.ID, At: o.Now().Unix(), Outcome: outcome, Detail: detail}
	if err := o.DB.Create(&a).Error; err != nil {
		log.Printf("outbox: attempt of delivery %d: %v", d.ID, err)
	}
	return a
}

func (o *Outbox) finish(a model.Attempt, outcome, detail string) {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	o.DB.Model(&a).Updates(map[string]any{"outcome": outcome, "detail": detail})
}

// sender is a channel's sender, built again when its settings change.
func (o *Outbox) sender(ch model.Channel) (channel.Sender, error) {
	k, ok := channel.Lookup(ch.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", ch.Kind)
	}
	version, _ := ch.Config.Value()
	v := fmt.Sprint(version)
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.senders[ch.ID]; ok && c.version == v {
		return c.sender, nil
	}
	s, err := k.New(ch.Config.V)
	if err != nil {
		return nil, err
	}
	o.senders[ch.ID] = cachedSender{version: v, sender: s}
	return s, nil
}

// take spends one of a channel's tokens, or says how long until there is
// one.
func (o *Outbox) take(ch model.Channel) time.Duration {
	rate := ch.PerMinute
	if rate <= 0 {
		k, _ := channel.Lookup(ch.Kind)
		rate = k.PerMinute
	}
	if rate <= 0 {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	b := o.buckets[ch.ID]
	if b == nil || b.rate != rate {
		b = &bucket{rate: rate, tokens: float64(rate), at: o.Now()}
		o.buckets[ch.ID] = b
	}
	return b.take(o.Now())
}

// bucket is a token bucket refilled at rate a minute, holding at most a
// minute's worth.
type bucket struct {
	rate   int
	tokens float64
	at     time.Time
}

func (b *bucket) take(now time.Time) time.Duration {
	per := time.Minute / time.Duration(b.rate)
	b.tokens = min(float64(b.rate), b.tokens+float64(now.Sub(b.at))/float64(per))
	b.at = now
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) * float64(per))
}

// OnceDays is how long a lasting key outlives its delivery: past the
// furthest a schedule line can lie before an expiry (365 days), so a line
// the log has forgotten is not crossed a second time.
const OnceDays = 400

// Prune deletes finished deliveries, and their attempts, older than the
// retention the admin set — and with them the once-only hold of their
// keys; the lasting keys are kept OnceDays, or the retention when it is
// longer.
func (o *Outbox) Prune() {
	cfg, _ := settings.LoadDelivery(o.DB)
	cut := o.Now().AddDate(0, 0, -cfg.RetentionDays).Unix()
	finished := []string{model.DeliverySent, model.DeliveryFailed, model.DeliveryCancelled, model.DeliveryUnknown}
	o.DB.Where("delivery_id IN (?)", o.DB.Model(&model.Delivery{}).Select("id").Where("status IN ? AND created_at < ?", finished, cut)).
		Delete(&model.Attempt{})
	o.DB.Where("status IN ? AND created_at < ?", finished, cut).Delete(&model.Delivery{})
	onceCut := o.Now().AddDate(0, 0, -max(cfg.RetentionDays, OnceDays)).Unix()
	o.DB.Where("at < ?", onceCut).Delete(&model.Once{})
}
