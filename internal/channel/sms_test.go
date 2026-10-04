package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestKavenegar: the lookup goes to /{key}/verify/lookup.json with the
// receptor as 09…, the template and its tokens filled from the notice — a
// space where tokens take none becomes a dash — and return.status decides.
func TestKavenegar(t *testing.T) {
	var path string
	var form url.Values
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"return":{"status":` + itoa(status) + `,"message":"m"},"entries":[]}`))
	}))
	defer srv.Close()
	cfg, err := Check("kavenegar", map[string]string{
		"apiKey": "KEY", "template": "expiry", "apiBase": srv.URL + "/v1",
		"variables": "token={name}\ntoken2={days}\ntoken10={title}",
	})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("kavenegar")
	s, _ := k.New(cfg)
	to := Recipient{Contact: map[string]string{"phone": "+98 912 000 0001"}}
	m := Message{Title: "حساب شما به زودی تمام می‌شود", Vars: map[string]string{"name": "ana user", "days": "3"}}
	if err := s.Send(context.Background(), to, m); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/KEY/verify/lookup.json" || form.Get("receptor") != "09120000001" || form.Get("template") != "expiry" ||
		form.Get("token") != "ana-user" || form.Get("token2") != "3" || form.Get("token10") != m.Title {
		t.Fatalf("%s %v", path, form)
	}
	_ = s.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "+7 912 345 67 89"}}, m)
	if form.Get("receptor") != "0079123456789" {
		t.Fatalf("a foreign number: %v", form.Get("receptor"))
	}
	status = 424
	var refused *Refused
	if err := s.Send(context.Background(), to, m); !errors.As(err, &refused) {
		t.Fatalf("424: %v", err)
	}
	status = 409
	var retry *Retry
	if err := s.Send(context.Background(), to, m); !errors.As(err, &retry) {
		t.Fatalf("409: %v", err)
	}
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{}}, m); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no phone: %v", err)
	}
	if _, err := Check("kavenegar", map[string]string{"apiKey": "K", "template": "t", "variables": "code={name}"}); err == nil {
		t.Fatal("a variable Kavenegar does not have was taken")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// TestFaraz: the pattern goes with the Api-Key header, the recipient as
// 09…, the attributes cut to their declared lengths so Faraz never holds
// the message for a person; a foreign number is no address; "error" with
// 422 is refused and 429 retried.
func TestFaraz(t *testing.T) {
	var got map[string]any
	var key string
	code, body := 200, `{"status":"success","data":123}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("Api-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	cfg, err := Check("faraz", map[string]string{
		"apiKey": "FK", "code": "abc123", "lineNumber": "90008361", "apiBase": srv.URL,
		"variables": "name={name}\ndays={days}", "maxLengths": "name=5",
	})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("faraz")
	s, _ := k.New(cfg)
	m := Message{Vars: map[string]string{"name": "anastasia", "days": "3"}}
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "9120000001"}}, m); err != nil {
		t.Fatal(err)
	}
	attrs := got["attributes"].(map[string]any)
	if key != "FK" || got["recipient"] != "09120000001" || got["code"] != "abc123" || got["line_number"] != "90008361" ||
		attrs["name"] != "anast" || attrs["days"] != "3" {
		t.Fatalf("sent %v with %q", got, key)
	}
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "+79123456789"}}, m); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("a Russian number: %v", err)
	}
	code, body = 422, `{"status":"error","messages":{"code":["invalid"]}}`
	var refused *Refused
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "09120000001"}}, m); !errors.As(err, &refused) {
		t.Fatalf("422: %v", err)
	}
	code = 429
	var retry *Retry
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "09120000001"}}, m); !errors.As(err, &retry) {
		t.Fatalf("429: %v", err)
	}
}

// TestNtfy: only an account with an ntfy topic is sent to, as JSON to the
// server's root with the token; without one ntfy is no address.
func TestNtfy(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	cfg, _ := Check("ntfy", map[string]string{"server": srv.URL, "token": "tk", "priority": "4"})
	k, _ := Lookup("ntfy")
	s, _ := k.New(cfg)
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"ntfy": "notif-abc"}}, Message{Title: "عنوان", Text: "متن"}); err != nil {
		t.Fatal(err)
	}
	if got["topic"] != "notif-abc" || got["title"] != "عنوان" || got["priority"] != float64(4) || auth != "Bearer tk" {
		t.Fatalf("sent %v %q", got, auth)
	}
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{}}, Message{}); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no topic: %v", err)
	}
}

// TestEmail: a plain-text message with the title as an encoded subject
// reaches an SMTP server; a 550 is final, and no address is said as such.
func TestEmail(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rcptCode := "250"
	data := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
				say("220 test")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						say("250-test")
						say("250 AUTH PLAIN")
					case strings.HasPrefix(cmd, "RCPT"):
						say(rcptCode + " rcpt")
					case cmd == "DATA":
						say("354 go")
						var b strings.Builder
						for {
							l, _ := r.ReadString('\n')
							if l == ".\r\n" {
								break
							}
							b.WriteString(l)
						}
						data <- b.String()
						say("250 queued")
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(c)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	cfg, err := Check("smtp", map[string]string{"host": host, "port": port, "security": "none", "from": "notices@example.com", "fromName": "Nexora"})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("smtp")
	s, _ := k.New(cfg)
	to := Recipient{Contact: map[string]string{"email": "ana@example.org"}}
	if err := s.Send(context.Background(), to, Message{Title: "تمدید شد", Text: "حساب شما تمدید شد."}); err != nil {
		t.Fatal(err)
	}
	msg := <-data
	if !strings.Contains(msg, "Subject: =?utf-8?q?") || !strings.Contains(msg, "To: ana@example.org") ||
		!strings.Contains(msg, "Content-Type: text/plain; charset=utf-8") || !strings.Contains(msg, "@example.com>") {
		t.Fatalf("message:\n%s", msg)
	}
	rcptCode = "550"
	var refused *Refused
	if err := s.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &refused) {
		t.Fatalf("550: %v", err)
	}
	if err := s.Send(context.Background(), Recipient{Contact: map[string]string{"email": "not an address"}}, Message{}); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("a bad address: %v", err)
	}
}
