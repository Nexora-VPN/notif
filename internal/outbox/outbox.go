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
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/notices"
	"github.com/nexora-vpn/notif/internal/settings"
	"gorm.io/gorm"
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

	kick chan struct{}
	mu   sync.Mutex
	// senders are built once per channel and its settings.
	senders map[uint]cachedSender
	buckets map[uint]*bucket
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

// Enqueue queues a delivery unless one with its key exists; it reports
// whether it queued.
func (o *Outbox) Enqueue(d model.Delivery) (bool, error) {
	if d.Key == "" || d.UserID == 0 || d.Kind == "" {
		return false, errors.New("a delivery needs a key, a user and a kind")
	}
	d.ID = 0
	d.Status = model.DeliveryQueued
	d.NextAt = o.Now().Unix()
	res := o.DB.Where(model.Delivery{Key: d.Key}).Attrs(d).FirstOrCreate(&d)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		o.Kick()
		return true, nil
	}
	return false, nil
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

// Run sends until ctx ends.
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
				o.process(ctx, id)
			}
		}()
	}
	defer func() { close(jobs); wg.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for _, id := range o.claim(50) {
			select {
			case jobs <- id:
			case <-ctx.Done():
				return
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
		for _, id := range ids {
			o.process(ctx, id)
		}
	}
}

func (o *Outbox) set(d *model.Delivery, fields map[string]any) {
	if err := o.DB.Model(d).Updates(fields).Error; err != nil {
		log.Printf("outbox: delivery %d: %v", d.ID, err)
	}
}

// process takes one claimed delivery one step: sent, passed on, retried
// later, held, or failed.
func (o *Outbox) process(ctx context.Context, id uint) {
	var d model.Delivery
	if o.DB.First(&d, id).Error != nil {
		return
	}
	now := o.Now()
	cfg, _ := settings.LoadDelivery(o.DB)
	if !d.Urgent {
		if until := cfg.QuietUntil(now); !until.IsZero() {
			o.set(&d, map[string]any{"status": model.DeliveryHeld, "next_at": until.Unix()})
			return
		}
	}
	var u model.User
	if o.DB.First(&u, d.UserID).Error != nil || u.GoneAt > 0 {
		o.set(&d, map[string]any{"status": model.DeliveryFailed, "error": "the account is no longer on the panel"})
		return
	}
	order, err := o.order(d)
	if err != nil {
		o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Add(time.Minute).Unix()})
		return
	}
	// Where the delivery stands: the channels already done with, and how
	// often the current one has failed.
	var attempts []model.Attempt
	o.DB.Where("delivery_id = ?", d.ID).Order("id").Find(&attempts)
	done := map[uint]bool{}
	errs := map[uint]int{}
	for _, a := range attempts {
		switch a.Outcome {
		case model.AttemptNoAddress, model.AttemptRefused:
			done[a.ChannelID] = true
		case model.AttemptError:
			errs[a.ChannelID]++
			if errs[a.ChannelID] >= Tries {
				done[a.ChannelID] = true
			}
		}
	}
	lang := notices.Language(u, cfg.Language)
	to := channel.Recipient{UserID: u.ID, Name: u.Name, Contact: u.Contact.V, Lang: lang}
	for _, ch := range order {
		if done[ch.ID] {
			continue
		}
		sender, err := o.sender(ch)
		if err != nil {
			o.attempt(d, ch, model.AttemptRefused, "the channel's settings do not work: "+err.Error())
			continue
		}
		if wait := o.take(ch); wait > 0 {
			o.set(&d, map[string]any{"status": model.DeliveryQueued, "next_at": now.Add(wait).Unix()})
			return
		}
		a := o.attempt(d, ch, model.AttemptStarted, "")
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = sender.Send(sctx, to, notices.Render(d, u, ch, lang))
		cancel()
		var refused *channel.Refused
		var retry *channel.Retry
		switch {
		case err == nil:
			o.finish(a, model.AttemptSent, "")
			o.set(&d, map[string]any{"status": model.DeliverySent, "channel_id": ch.ID, "sent_at": o.Now().Unix(), "error": ""})
			return
		case errors.Is(err, channel.ErrNoAddress):
			o.finish(a, model.AttemptNoAddress, err.Error())
		case errors.As(err, &refused):
			o.finish(a, model.AttemptRefused, refused.Reason)
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

// order is the channels to try: the admin's order of the enabled ones, or
// the one channel a test names.
func (o *Outbox) order(d model.Delivery) ([]model.Channel, error) {
	var chs []model.Channel
	q := o.DB.Where("enabled = ?", true)
	if d.Only != 0 {
		q = o.DB.Where("id = ?", d.Only)
	}
	if err := q.Find(&chs).Error; err != nil {
		return nil, err
	}
	sort.SliceStable(chs, func(i, j int) bool {
		if chs[i].Position != chs[j].Position {
			return chs[i].Position < chs[j].Position
		}
		return chs[i].ID < chs[j].ID
	})
	return chs, nil
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

// Prune deletes finished deliveries, and their attempts, older than the
// retention the admin set.
func (o *Outbox) Prune() {
	cfg, _ := settings.LoadDelivery(o.DB)
	cut := o.Now().AddDate(0, 0, -cfg.RetentionDays).Unix()
	finished := []string{model.DeliverySent, model.DeliveryFailed, model.DeliveryCancelled, model.DeliveryUnknown}
	o.DB.Where("delivery_id IN (?)", o.DB.Model(&model.Delivery{}).Select("id").Where("status IN ? AND created_at < ?", finished, cut)).
		Delete(&model.Attempt{})
	o.DB.Where("status IN ? AND created_at < ?", finished, cut).Delete(&model.Delivery{})
}
