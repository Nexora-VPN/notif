package bots

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexora-vpn/addon-kit/panel"
	"github.com/nexora-vpn/addon-kit/telegram"
	"github.com/nexora-vpn/notif/internal/channel"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"github.com/nexora-vpn/notif/internal/links"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/users"
	"gorm.io/gorm"
)

// fakePanel keeps accounts' contact cards and versions, as GET and PATCH
// /api/v1/users/{id} do.
type fakePanel struct {
	mu       sync.Mutex
	contacts map[string]map[string]string
	version  map[string]int64
	// race, when set, changes the card under the next PATCH once.
	race bool
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
	c, ok := f.contacts[id]
	if !ok {
		w.WriteHeader(404)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Contact map[string]string `json:"contact"`
			Version int64             `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.race {
			f.race = false
			c["email"] = "changed@elsewhere.io"
			f.version[id]++
		}
		if body.Version != f.version[id] {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"changed","version":1}`))
			return
		}
		f.contacts[id] = body.Contact
		f.version[id]++
		c = body.Contact
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": json.Number(id), "name": "user" + id, "contact": c, "updatedAt": f.version[id], "subId": "subid-ana-123", "subToken": "0123456789abcdef"})
}

func open(t *testing.T) *gorm.DB {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{DataDir: dir, Driver: config.DriverSQLite, DSN: dir + "/notif.db"}
	if dsn := os.Getenv("NEXORA_TEST_POSTGRES_DSN"); dsn != "" {
		cfg.Driver, cfg.DSN = config.DriverPostgres, dsn
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver == config.DriverPostgres {
		gdb.Exec("TRUNCATE settings, users, channels, deliveries, attempts, sends RESTART IDENTITY")
	}
	return gdb
}

// replies is a Bot API that records what the bot says.
type replies struct {
	mu   sync.Mutex
	said []string
}

func (b *replies) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.said) == 0 {
		return ""
	}
	return b.said[len(b.said)-1]
}

func setup(t *testing.T) (*Manager, *fakePanel, *channel.Bot, *replies) {
	gdb := open(t)
	fp := &fakePanel{contacts: map[string]map[string]string{"7": {"phone": "0912"}}, version: map[string]int64{"7": 1}}
	psrv := httptest.NewServer(fp)
	t.Cleanup(psrv.Close)
	rep := &replies{}
	bsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		rep.mu.Lock()
		rep.said = append(rep.said, p["text"].(string))
		rep.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`))
	}))
	t.Cleanup(bsrv.Close)
	gdb.Create(&model.User{
		ID: 7, Name: "ana", SubIDHash: users.SubHash("subid-ana-123"), SubTokenHash: users.SubHash("0123456789abcdef"),
		Contact: model.JSON[map[string]string]{V: map[string]string{"phone": "0912"}},
	})
	client := &panel.Client{Base: psrv.URL, Token: "t"}
	sync := &users.Sync{DB: gdb, Panel: func() users.Getter { return client }}
	l := &links.Links{DB: gdb, Users: sync, Secret: []byte("secret"), Panel: func() links.Panel { return client }}
	bot, _ := channel.NewBot("telegram", map[string]string{"token": "1:abc", "apiBase": bsrv.URL})
	return New(gdb, l), fp, bot, rep
}

func msg(chat int64, text, lang string) telegram.Message {
	return telegram.Message{Chat: telegram.Chat{ID: chat, Type: "private"}, From: &telegram.User{ID: chat, LanguageCode: lang}, Text: text}
}

// TestLinkingAChat: /start greets in the user's language; the subscription
// link writes the chat into the account's card on the panel, keeping the
// rest of the card and surviving another writer in between; /stop takes it
// away; a link code links too; a wrong link is refused, and five wrong ones
// lock the chat out.
func TestLinkingAChat(t *testing.T) {
	m, fp, bot, rep := setup(t)
	ctx := context.Background()
	m.Handle(ctx, bot, msg(500, "/start", "fa"))
	if !strings.HasPrefix(rep.last(), "سلام!") {
		t.Fatalf("welcome: %q", rep.last())
	}
	fp.race = true
	m.Handle(ctx, bot, msg(500, "here it is: https://sub.example.com/sub/subid-ana-123#ana", "en"))
	if !strings.Contains(rep.last(), "Connected to the account ana") {
		t.Fatalf("link: %q", rep.last())
	}
	if c := fp.contacts["7"]; c["telegram_id"] != "500" || c["phone"] != "0912" || c["email"] != "changed@elsewhere.io" || c["lang"] != "en" {
		t.Fatalf("the card on the panel: %v", c)
	}
	var u model.User
	m.DB.First(&u, 7)
	if u.Contact.V["telegram_id"] != "500" {
		t.Fatalf("the copy: %v", u.Contact.V)
	}
	m.Handle(ctx, bot, msg(500, "/stop", "en"))
	if _, ok := fp.contacts["7"]["telegram_id"]; ok || !strings.HasPrefix(rep.last(), "Disconnected") {
		t.Fatalf("stop: %v %q", fp.contacts["7"], rep.last())
	}
	m.Handle(ctx, bot, msg(500, "/stop", "en"))
	if !strings.Contains(rep.last(), "not connected") {
		t.Fatalf("stop twice: %q", rep.last())
	}
	m.Handle(ctx, bot, msg(600, "/start "+m.Links.Code(7), "ru"))
	if fp.contacts["7"]["telegram_id"] != "600" || !strings.HasPrefix(rep.last(), "Подключено") {
		t.Fatalf("code: %v %q", fp.contacts["7"], rep.last())
	}
	m.Handle(ctx, bot, msg(600, "0123456789abcdef", "en"))
	if !strings.Contains(rep.last(), "Connected") {
		t.Fatalf("a bare token: %q", rep.last())
	}
	for range 5 {
		m.Handle(ctx, bot, msg(700, "7-aaaaaaaaaa", "en"))
	}
	m.Handle(ctx, bot, msg(700, "subid-ana-123", "en"))
	if !strings.HasPrefix(rep.last(), "Too many") || fp.contacts["7"]["telegram_id"] != "600" {
		t.Fatalf("lock-out: %q %v", rep.last(), fp.contacts["7"])
	}
	m.Handle(ctx, bot, telegram.Message{Chat: telegram.Chat{ID: -1, Type: "group"}, Text: "/start"})
	if strings.Contains(rep.last(), "Hello") {
		t.Fatal("a group was answered")
	}
}

// TestAReaderPerBot: an enabled bot channel gets a reader that names the
// bot, reads updates from where it stopped, and is stopped with its channel.
func TestAReaderPerBot(t *testing.T) {
	gdb := open(t)
	var mu sync.Mutex
	offsets := []float64{}
	served := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"notif_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			mu.Lock()
			offsets = append(offsets, p["offset"].(float64))
			first := !served
			served = true
			mu.Unlock()
			if first {
				_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":41,"message":{"message_id":1,"chat":{"id":5,"type":"private"},"text":"/start"}}]}`))
				return
			}
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":5}}}`))
		}
	}))
	defer srv.Close()
	gdb.Create(&model.Channel{
		Kind: "telegram", Name: "tg", Enabled: true, Position: 1,
		Config: model.JSON[map[string]string]{V: map[string]string{"token": "1:abc", "apiBase": srv.URL}},
	})
	m := New(gdb, &links.Links{DB: gdb, Secret: []byte("s"), Panel: func() links.Panel { return nil }})
	m.Wait = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	m.Reconcile(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var c model.Channel
		gdb.First(&c, 1)
		mu.Lock()
		n := len(offsets)
		mu.Unlock()
		if c.State.V["username"] == "notif_bot" && n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %v, %d polls", c.State.V, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if offsets[0] != 0 || offsets[1] != 42 {
		t.Fatalf("offsets %v", offsets)
	}
	mu.Unlock()
	gdb.Model(&model.Channel{}).Where("id = 1").Update("enabled", false)
	m.Reconcile(ctx)
	m.mu.Lock()
	n := len(m.running)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d readers after the channel was turned off", n)
	}
	cancel()
}

// TestARubikaReader: a started bot is greeted, a message with the
// subscription link links the chat on the panel, a stopped bot unlinks it
// with no reply, and the offset is kept.
func TestARubikaReader(t *testing.T) {
	m, fp, _, _ := setup(t)
	var mu sync.Mutex
	var said []string
	var offsets []any
	batches := [][]string{
		{`{"type":"StartedBot","chat_id":"b0ana"}`, `{"type":"NewMessage","chat_id":"b0ana","new_message":{"text":"https://x.io/sub/subid-ana-123","sender_type":"User"}}`},
		{`{"type":"StoppedBot","chat_id":"b0ana"}`},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"status":"OK","data":{"bot":{"username":"notif_rubika_bot"}}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			offsets = append(offsets, p["offset_id"])
			n := len(offsets)
			if n <= len(batches) {
				_, _ = w.Write([]byte(`{"status":"OK","data":{"updates":[` + strings.Join(batches[n-1], ",") + `],"next_offset_id":"o` + string(rune('0'+n)) + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"OK","data":{"updates":[],"next_offset_id":"o2"}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			said = append(said, p["text"].(string))
			_, _ = w.Write([]byte(`{"status":"OK","data":{"message_id":"1"}}`))
		}
	}))
	defer srv.Close()
	m.DB.Create(&model.Channel{
		Kind: "rubika", Name: "rb", Enabled: true, Position: 1,
		Config: model.JSON[map[string]string]{V: map[string]string{"token": "tok", "apiBase": srv.URL}},
	})
	m.Idle = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Reconcile(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := len(offsets) >= 3
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("offsets %v", offsets)
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.stopAll()
	mu.Lock()
	defer mu.Unlock()
	if len(said) != 2 || !strings.HasPrefix(said[0], "Hello!") || !strings.Contains(said[1], "Connected to the account ana") {
		t.Fatalf("said %q", said)
	}
	if offsets[0] != nil || offsets[1] != "o1" || offsets[2] != "o2" {
		t.Fatalf("offsets %v", offsets)
	}
	if _, linked := fp.contacts["7"]["rubika_id"]; linked {
		t.Fatalf("still linked after StoppedBot: %v", fp.contacts["7"])
	}
	var c model.Channel
	m.DB.First(&c)
	if c.State.V["username"] != "notif_rubika_bot" {
		t.Fatalf("state %v", c.State.V)
	}
}
