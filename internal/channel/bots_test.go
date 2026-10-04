package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI is a Bot API that records sendMessage and answers by chat id:
// 403 blocks, 429 floods, anything else is sent.
func fakeAPI(t *testing.T) (*httptest.Server, *[]map[string]any, *[]string) {
	var sent []map[string]any
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		switch p["chat_id"] {
		case float64(403):
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
		case float64(429):
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":9}}`))
		default:
			sent = append(sent, p)
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &sent, &paths
}

// TestTheBots: Telegram and Soroush Plus write HTML, Bale its own Markdown
// with no parse_mode and, switched on, through the business API; a
// @username is not a chat; a blocked bot is refused and its link taken
// away; flood control waits.
func TestTheBots(t *testing.T) {
	srv, sent, paths := fakeAPI(t)
	m := Message{Title: "Expiry <soon>", Text: "3 days & counting"}
	for _, kind := range []string{"telegram", "soroush", "bale"} {
		cfg, err := Check(kind, map[string]string{"token": "1:abc", "apiBase": srv.URL})
		if err != nil {
			t.Fatal(kind, err)
		}
		k, _ := Lookup(kind)
		s, _ := k.New(cfg)
		to := Recipient{Contact: map[string]string{k.Contact: "42"}}
		if err := s.Send(context.Background(), to, m); err != nil {
			t.Fatal(kind, err)
		}
		got := (*sent)[len(*sent)-1]
		if kind == "bale" {
			if got["parse_mode"] != nil || got["text"] != "*Expiry <soon>*\n\n3 days & counting" {
				t.Fatalf("bale sent %v", got)
			}
		} else if got["parse_mode"] != "HTML" || got["text"] != "<b>Expiry &lt;soon&gt;</b>\n\n3 days &amp; counting" {
			t.Fatalf("%s sent %v", kind, got)
		}
		if err := s.Send(context.Background(), Recipient{Contact: map[string]string{}}, m); !errors.Is(err, ErrNoAddress) {
			t.Fatalf("%s with no link: %v", kind, err)
		}
	}
	k, _ := Lookup("bale")
	cfg, _ := Check("bale", map[string]string{"token": "1:abc", "apiBase": srv.URL, "business": "on"})
	s, _ := k.New(cfg)
	_ = s.Send(context.Background(), Recipient{Contact: map[string]string{"bale_id": "42"}}, m)
	if last := (*paths)[len(*paths)-1]; last != "/business/bot1:abc/sendMessage" {
		t.Fatalf("business switch sent to %s", last)
	}
	if _, err := Check("bale", map[string]string{"token": "1:abc", "business": "maybe"}); err == nil {
		t.Fatal("a choice outside the list was taken")
	}

	k, _ = Lookup("telegram")
	tg, _ := k.New(map[string]string{"token": "1:abc", "apiBase": srv.URL})
	var refused *Refused
	if err := tg.Send(context.Background(), Recipient{Contact: map[string]string{"telegram_id": "@someone"}}, m); !errors.As(err, &refused) || refused.Unlink != "" {
		t.Fatalf("a @username: %v", err)
	}
	if err := tg.Send(context.Background(), Recipient{Contact: map[string]string{"telegram_id": "403"}}, m); !errors.As(err, &refused) || refused.Unlink != "telegram_id" {
		t.Fatalf("blocked: %v", err)
	}
	var retry *Retry
	if err := tg.Send(context.Background(), Recipient{Contact: map[string]string{"telegram_id": "429"}}, m); !errors.As(err, &retry) || retry.After.Seconds() != 9 {
		t.Fatalf("flood: %v", err)
	}
	if _, err := Check("telegram", map[string]string{"token": "no-colon"}); err == nil || !strings.Contains(err.Error(), "id:secret") {
		t.Fatalf("a bad token: %v", err)
	}
}
