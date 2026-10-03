package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTheGenericChannel: the body template gets the address and the text,
// escaped; a header is sent; 2xx sends, 4xx refuses, 5xx and 429 retry;
// no address is said as such; a secret stays masked.
func TestTheGenericChannel(t *testing.T) {
	var got map[string]string
	var auth string
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		auth = r.Header.Get("Authorization")
		if code == 429 {
			w.Header().Set("Retry-After", "7")
		}
		w.WriteHeader(code)
	}))
	defer srv.Close()
	cfg, err := Check("http", map[string]string{"url": srv.URL + "/send", "headers": "Authorization: Bearer k"})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("http")
	s, _ := k.New(cfg)
	to := Recipient{Name: "ana", Contact: map[string]string{"phone": "+989120000000"}}
	m := Message{Title: "T", Text: `say "hi" \ now`}
	if err := s.Send(context.Background(), to, m); err != nil {
		t.Fatal(err)
	}
	if got["to"] != "+989120000000" || got["text"] != `say "hi" \ now` || auth != "Bearer k" {
		t.Fatalf("sent %v with %q", got, auth)
	}
	code = 400
	var refused *Refused
	if err := s.Send(context.Background(), to, m); !errors.As(err, &refused) {
		t.Fatalf("400: %v", err)
	}
	code = 429
	var retry *Retry
	if err := s.Send(context.Background(), to, m); !errors.As(err, &retry) || retry.After.Seconds() != 7 {
		t.Fatalf("429: %v", err)
	}
	if err := s.Send(context.Background(), Recipient{Name: "bob"}, m); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no phone: %v", err)
	}
	if r := Redact("http", cfg); r["headers"] != "••••" {
		t.Fatalf("redacted %v", r)
	}
	if m := Merge("http", cfg, map[string]string{"url": srv.URL, "headers": "••••"}); m["headers"] != "Authorization: Bearer k" {
		t.Fatalf("merged %v", m)
	}
	// An address sent empty is "none" and survives an edit that leaves it out.
	none, _ := Check("http", map[string]string{"url": srv.URL, "address": ""})
	none, _ = Check("http", Merge("http", none, map[string]string{"url": srv.URL}))
	if none["address"] != "" {
		t.Fatalf("an empty address became %q", none["address"])
	}
	code = 200
	k2, _ := Lookup("http")
	s2, _ := k2.New(none)
	if err := s2.Send(context.Background(), Recipient{Name: "bob"}, m); err != nil {
		t.Fatalf("no address needed: %v", err)
	}
	if _, err := Check("http", map[string]string{"url": "ftp://x"}); err == nil {
		t.Fatal("an ftp url was taken")
	}
	if _, err := Check("http", map[string]string{"url": "https://x", "body": "{{"}); err == nil {
		t.Fatal("a broken template was taken")
	}
}
