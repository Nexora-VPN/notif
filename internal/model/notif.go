package model

// The notifier's own records: its copy of the panel's accounts, the
// channels in the admin's order, and the outbox of deliveries with every
// attempt each one made.

// User is Notif's copy of one panel account — only what a notice needs.
// The panel stays the record: the copy is refreshed from it (internal/users)
// and every messenger link is read from Contact (P36 (iii)), never kept here.
type User struct {
	ID      uint                    `json:"id" gorm:"primaryKey;autoIncrement:false"`
	Name    string                  `json:"name" gorm:"index;not null"`
	Contact JSON[map[string]string] `json:"contact"`
	Enable  bool                    `json:"enable" gorm:"not null"`
	// DisabledReason is the panel's: "manual" for a person, or the
	// enforcer's own (expiry, volume, resale-…).
	DisabledReason string `json:"disabledReason" gorm:"not null;default:''"`
	Expiry         int64  `json:"expiry" gorm:"not null;default:0"`
	Volume         int64  `json:"volume" gorm:"not null;default:0"`
	Used           int64  `json:"used" gorm:"not null;default:0"`
	// TotalUsed is all the traffic ever used; TotalUsed - Used grows at each
	// periodic reset, which is how a traffic warning re-arms for a new cycle.
	TotalUsed int64 `json:"totalUsed" gorm:"not null;default:0"`
	// Duration and ActivatedAt: a plan counted from the first connection
	// has a provisional expiry until ActivatedAt is set.
	Duration    int64  `json:"duration" gorm:"not null;default:0"`
	ActivatedAt int64  `json:"activatedAt" gorm:"not null;default:0"`
	Group       string `json:"group" gorm:"not null;default:''"`
	AdminID     uint   `json:"adminId" gorm:"index;not null;default:0"`
	// SubURL is the subscription link, never stored: it carries the
	// subscription's secret, so it is read from the panel when a notice
	// that names it is sent (internal/outbox) and lives only that long.
	SubURL string `json:"-" gorm:"-"`
	// SubIDHash and SubTokenHash are hashes of the subscription's id and
	// token, so a user who sends a bot their subscription link is found
	// without Notif keeping the link's secret part.
	SubIDHash    string `json:"-" gorm:"index;not null;default:''"`
	SubTokenHash string `json:"-" gorm:"index;not null;default:''"`
	// UpdatedAt is the panel's, as it sent it (never Notif's own clock: a
	// read compares it with the copy's to keep the later state); SeenAt is
	// when this copy was last refreshed; GoneAt is when the panel said the
	// account was deleted.
	UpdatedAt int64 `json:"updatedAt" gorm:"not null;default:0;autoUpdateTime:false"`
	SeenAt    int64 `json:"seenAt" gorm:"not null;default:0"`
	GoneAt    int64 `json:"goneAt" gorm:"index;not null;default:0"`
}

// Channel is one configured way to reach users: a bot, an SMS provider, a
// mail server, the generic HTTP channel. Position is the admin's fall-back
// order, lowest first. Config holds the kind's own settings, secrets among
// them, and is never sent back to the browser whole (internal/channel).
type Channel struct {
	ID       uint                    `json:"id" gorm:"primaryKey"`
	Kind     string                  `json:"kind" gorm:"not null"`
	Name     string                  `json:"name" gorm:"not null"`
	Enabled  bool                    `json:"enabled" gorm:"not null"`
	Position int                     `json:"position" gorm:"index;not null;default:0"`
	Config   JSON[map[string]string] `json:"-"`
	// PerMinute is the most this channel sends in a minute; 0 is the kind's
	// default.
	PerMinute int `json:"perMinute" gorm:"not null;default:0"`
	// State is what the channel itself reports: a bot's username, why its
	// updates cannot be read.
	State     JSON[map[string]string] `json:"state"`
	CreatedAt int64                   `json:"createdAt" gorm:"autoCreateTime"`
}

// The states of a delivery.
const (
	// Queued waits for its NextAt; Held is a queued one waiting out the quiet
	// hours; Sending is in a worker's hands.
	DeliveryQueued  = "queued"
	DeliveryHeld    = "held"
	DeliverySending = "sending"
	// Sent went out through one channel. Failed reached nobody: every
	// channel was tried, or the user has none. Cancelled was stopped by the
	// admin. Unknown was being sent when Notif stopped: it may have gone,
	// so it is never sent again — a restart sends nobody anything twice.
	DeliverySent      = "sent"
	DeliveryFailed    = "failed"
	DeliveryCancelled = "cancelled"
	DeliveryUnknown   = "unknown"
)

// Delivery is one notice to one user: written once, then handed to the
// user's channels in the admin's order until one delivers it.
type Delivery struct {
	ID uint `json:"id" gorm:"primaryKey"`
	// Key is what makes a notice once-only: the occasion and the user (an
	// event's id, an expiry date crossed, a send's id). A second notice with
	// the same key is not queued.
	Key    string `json:"key" gorm:"uniqueIndex;not null"`
	UserID uint   `json:"userId" gorm:"index;not null"`
	// Lasting keeps the key in model.Once long after the log forgets the
	// delivery — a schedule line's, which must not be crossed twice. Any
	// other key is once-only while its delivery is in the log. It is not
	// stored with the delivery.
	Lasting bool `json:"-" gorm:"-"`
	// Kind names the notice ("renewed", "expiring", "custom", "test"); its
	// text is rendered at send time for the channel and the language.
	Kind string                  `json:"kind" gorm:"index;not null"`
	Vars JSON[map[string]string] `json:"vars"`
	// Title and Body are the admin's own words for a custom message.
	Title string `json:"title" gorm:"not null;default:''"`
	Body  string `json:"body" gorm:"type:text;not null;default:''"`
	// Urgent goes out during the quiet hours too.
	Urgent bool `json:"urgent" gorm:"not null;default:false"`
	// OwnerID is the admin it is sent for — 0, the panel, for now (P35).
	OwnerID uint `json:"ownerId" gorm:"index;not null;default:0"`
	// SendID is the admin's message this delivery belongs to, 0 for none.
	SendID uint `json:"sendId" gorm:"index;not null;default:0"`
	// Only, when set, is the one channel to use — a test of that channel —
	// with no fall-back.
	Only   uint   `json:"only" gorm:"not null;default:0"`
	Status string `json:"status" gorm:"index;not null"`
	NextAt int64  `json:"nextAt" gorm:"index;not null;default:0"`
	// ChannelID is the channel that delivered it, or the last one tried.
	// Where it stands in the fall-back order is read from its attempts.
	ChannelID uint   `json:"channelId" gorm:"not null;default:0"`
	Error     string `json:"error" gorm:"type:text;not null;default:''"`
	CreatedAt int64  `json:"createdAt" gorm:"index;autoCreateTime"`
	SentAt    int64  `json:"sentAt" gorm:"not null;default:0"`
}

// The outcomes of one attempt.
const (
	AttemptStarted   = "started"
	AttemptSent      = "sent"
	AttemptNoAddress = "no_address"
	AttemptRefused   = "refused"
	AttemptError     = "error"
)

// Attempt is one channel's go at a delivery — the log the admin reads.
type Attempt struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	DeliveryID uint   `json:"deliveryId" gorm:"index;not null"`
	ChannelID  uint   `json:"channelId" gorm:"not null"`
	At         int64  `json:"at" gorm:"not null"`
	Outcome    string `json:"outcome" gorm:"not null"`
	Detail     string `json:"detail" gorm:"type:text;not null;default:''"`
}

// Send is an admin's own message to one user or a group (GN-S5): its
// deliveries carry its id.
type Send struct {
	ID      uint                    `json:"id" gorm:"primaryKey"`
	OwnerID uint                    `json:"ownerId" gorm:"index;not null;default:0"`
	Title   string                  `json:"title" gorm:"not null;default:''"`
	Body    string                  `json:"body" gorm:"type:text;not null"`
	Filter  JSON[map[string]string] `json:"filter"`
	Urgent  bool                    `json:"urgent" gorm:"not null;default:false"`
	// Total is how many accounts the recipients matched; Unreachable how
	// many of them no channel that was on could reach, which were not
	// queued.
	Total       int   `json:"total" gorm:"not null;default:0"`
	Unreachable int   `json:"unreachable" gorm:"not null;default:0"`
	CreatedAt   int64 `json:"createdAt" gorm:"autoCreateTime"`
	CancelledAt int64 `json:"cancelledAt" gorm:"not null;default:0"`
}

// Text is one notice's words in one language.
type Text struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Notice is the admin's setting for one kind of notice (internal/notices
// lists the kinds): on or off, urgent or not, and their own words —
// per language, and per channel kind where a channel needs other words
// (an SMS shorter than a bot's message): Texts[lang][""] is the language's
// text, Texts[lang]["kavenegar"] the override for that kind of channel.
type Notice struct {
	Kind    string                           `json:"kind" gorm:"primaryKey"`
	Enabled bool                             `json:"enabled" gorm:"not null"`
	Urgent  bool                             `json:"urgent" gorm:"not null;default:false"`
	Texts   JSON[map[string]map[string]Text] `json:"texts"`
}

// Once is a schedule line's once-only key (Delivery.Lasting), kept apart
// from the log: the log is pruned after the admin's retention, the keys
// long after any schedule line could be crossed again (outbox.Prune), so a
// line the log has forgotten is not crossed a second time.
type Once struct {
	Key string `gorm:"primaryKey"`
	At  int64  `gorm:"index;not null"`
}

// TableName keeps the table's name readable.
func (Once) TableName() string { return "once_keys" }

// Chat is one messenger chat an account's contact card names — its
// telegram_id, bale_id, soroush_id or rubika_id, whoever wrote it — kept
// beside the copy (internal/users keeps it in step) so a chat that writes
// /stop finds its accounts by an index.
type Chat struct {
	UserID uint   `gorm:"primaryKey;autoIncrement:false"`
	Key    string `gorm:"primaryKey;index:idx_chats_value,priority:1"`
	Value  string `gorm:"not null;index:idx_chats_value,priority:2"`
}

// Link is a contact key Notif itself wrote to an account's card — a chat
// the user linked through Notif's bot, the ntfy topic the admin turned on —
// with the value written. Only these are Notif's to take away: a key
// another addon wrote (Shop's telegram_id for its own bot) is never
// cleared by Notif.
type Link struct {
	UserID uint   `gorm:"primaryKey;autoIncrement:false"`
	Key    string `gorm:"primaryKey"`
	Value  string `gorm:"not null"`
	At     int64  `gorm:"not null"`
}

// Block is a channel that no longer reaches an account at an address: the
// user blocked or stopped the bot, or never started it. It holds while the
// account's address on that channel is still Value; linking again lifts it.
type Block struct {
	UserID    uint   `gorm:"primaryKey;autoIncrement:false"`
	ChannelID uint   `gorm:"primaryKey;autoIncrement:false"`
	Value     string `gorm:"not null"`
	Reason    string `gorm:"type:text;not null;default:''"`
	At        int64  `gorm:"not null"`
}
