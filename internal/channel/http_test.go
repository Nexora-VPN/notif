package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type seen struct {
	method, path, query, auth, user, pass, ctype string
	body                                         []byte
}

// TestMain lets the channels reach the stand-ins on 127.0.0.1; the test of
// the guard turns that off for itself.
func TestMain(m *testing.M) {
	AllowLocal(true)
	os.Exit(m.Run())
}

func standIn(t *testing.T, code *int, answer *string) (*httptest.Server, *seen) {
	var s seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s = seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization"), ctype: r.Header.Get("Content-Type")}
		s.user, s.pass, _ = r.BasicAuth()
		s.body, _ = io.ReadAll(r.Body)
		if *code == 429 {
			w.Header().Set("Retry-After", "7")
		}
		w.WriteHeader(*code)
		_, _ = w.Write([]byte(*answer))
	}))
	t.Cleanup(srv.Close)
	return srv, &s
}

func sender(t *testing.T, cfg map[string]string) Sender {
	t.Helper()
	cfg, err := Check("http", cfg)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("http")
	s, err := k.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestTheGenericChannel: a JSON body escaped, the secret in a header and
// masked to the browser, 2xx sent, 4xx refused, 429 and 5xx retried; no
// address is said as such, and "none" is a field sent empty.
func TestTheGenericChannel(t *testing.T) {
	code, answer := 200, ""
	srv, got := standIn(t, &code, &answer)
	cfg := map[string]string{"url": srv.URL + "/send", "secret": "k3y", "headers": "Authorization: Bearer {{.Secret}}"}
	s := sender(t, cfg)
	to := Recipient{Name: "ana", Contact: map[string]string{"phone": "+989120000000"}}
	m := Message{Key: "k1", Title: "T", Text: `say "hi" \ now`}
	if err := s.Send(context.Background(), to, m); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.Unmarshal(got.body, &body)
	if body["to"] != "+989120000000" || body["text"] != `say "hi" \ now` || got.auth != "Bearer k3y" {
		t.Fatalf("sent %s with %q", got.body, got.auth)
	}
	code = 400
	answer = `{"error":"bad key k3y"}`
	var refused *Refused
	if err := s.Send(context.Background(), to, m); !errors.As(err, &refused) || strings.Contains(err.Error(), "k3y") {
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
	full, _ := Check("http", cfg)
	if r := Redact("http", full); r["secret"] != Mask || r["headers"] != "Authorization: Bearer {{.Secret}}" {
		t.Fatalf("redacted %v", r)
	}
	if m := Merge("http", full, map[string]string{"url": srv.URL, "secret": Mask}, nil); m["secret"] != "k3y" {
		t.Fatalf("merged %v", m)
	}
	// A secret sent empty is kept; one named in clear is taken away.
	if m := Merge("http", full, map[string]string{"url": srv.URL, "secret": ""}, []string{"secret"}); m["secret"] != "" {
		t.Fatalf("cleared %v", m)
	}
	// An address sent empty is "none" and survives an edit that leaves it out.
	none, _ := Check("http", map[string]string{"url": srv.URL, "address": ""})
	none, _ = Check("http", Merge("http", none, map[string]string{"url": srv.URL}, nil))
	if none["address"] != "" {
		t.Fatalf("an empty address became %q", none["address"])
	}
	code = 200
	k, _ := Lookup("http")
	s2, _ := k.New(none)
	if err := s2.Send(context.Background(), Recipient{Name: "bob"}, m); err != nil {
		t.Fatalf("no address needed: %v", err)
	}
	for _, bad := range []map[string]string{
		{"url": "ftp://x"},
		{"url": "https://x", "body": "{{"},
		{"url": "https://x", "caBundle": "not a certificate"},
		{"url": "https://x", "successPattern": "("},
		{"url": "https://x", "bodyType": "form", "body": "no equals sign"},
	} {
		if _, err := Check("http", bad); err == nil {
			t.Fatalf("%v was taken", bad)
		}
	}
}

// TestFormsBasicAndSuccess: a form body with the phone in the provider's
// shape, Basic authentication, an id fixed per delivery, a 200 that does
// not say sent, and a GET that carries the form in its query.
func TestFormsBasicAndSuccess(t *testing.T) {
	code, answer := 200, `{"status":"OK","sms":{"79123456789":{"sms_id":"1"}}}`
	srv, got := standIn(t, &code, &answer)
	s := sender(t, map[string]string{
		"url": srv.URL + "/sms/send", "bodyType": "form", "secret": "key",
		"body":      "api_id={{.Secret}}\nto={{msisdn .Address}}\nmsg={{.Title}}: {{.Text}}\nrandom_id={{.ID}}",
		"basicUser": "me@example.com", "basicPassword": "pw", "successPattern": `"sms_id"`,
	})
	to := Recipient{Contact: map[string]string{"phone": "8 (912) 345-67-89"}}
	m := Message{Key: "renewed:7:1", Title: "Renewed", Text: "30 days & 50 GB"}
	if err := s.Send(context.Background(), to, m); err != nil {
		t.Fatal(err)
	}
	form, _ := url.ParseQuery(string(got.body))
	if got.ctype != "application/x-www-form-urlencoded" || form.Get("api_id") != "key" || form.Get("to") != "79123456789" ||
		form.Get("msg") != "Renewed: 30 days & 50 GB" || got.user != "me@example.com" || got.pass != "pw" {
		t.Fatalf("sent %s as %s, basic %s/%s", got.body, got.ctype, got.user, got.pass)
	}
	first := form.Get("random_id")
	_ = s.Send(context.Background(), to, m)
	form, _ = url.ParseQuery(string(got.body))
	if first == "" || form.Get("random_id") != first {
		t.Fatalf("the id changed between tries: %s, %s", first, form.Get("random_id"))
	}
	answer = `{"status":"ERROR","status_text":"no money"}`
	var refused *Refused
	if err := s.Send(context.Background(), to, m); !errors.As(err, &refused) || !strings.Contains(err.Error(), "does not say sent") {
		t.Fatalf("a 200 that failed: %v", err)
	}
	g := sender(t, map[string]string{"url": srv.URL + "/get?x=1", "method": "GET", "bodyType": "form", "body": "to={{iran .Address}}"})
	_ = g.Send(context.Background(), Recipient{Contact: map[string]string{"phone": "+98 912 000 0001"}}, m)
	if got.method != "GET" || got.query != "x=1&to=09120000001" || len(got.body) != 0 {
		t.Fatalf("GET %s ? %s body %q", got.path, got.query, got.body)
	}
}

// TestThePresetsAreValid: every preset is a channel the admin can save once
// the capitals are theirs.
func TestThePresetsAreValid(t *testing.T) {
	k, _ := Lookup("http")
	if len(k.Presets) < 8 {
		t.Fatalf("%d presets", len(k.Presets))
	}
	for _, p := range k.Presets {
		cfg := map[string]string{}
		for key, v := range p.Config {
			cfg[key] = strings.ReplaceAll(v, "HOMESERVER", "matrix.example.org")
		}
		if _, err := Check("http", cfg); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}

// TestOnlyPublicAddresses: a channel's address on this host, a private
// network or the metadata service is refused for good when it is dialled,
// and the log keeps no part of a provider's answer.
func TestOnlyPublicAddresses(t *testing.T) {
	code, answer := 200, "internal secret page"
	srv, _ := standIn(t, &code, &answer)
	AllowLocal(false)
	t.Cleanup(func() { AllowLocal(true) })
	to := Recipient{Contact: map[string]string{"phone": "0912"}}
	for _, u := range []string{srv.URL, "http://169.254.169.254/latest/meta-data", "http://10.0.0.1/", "http://[::1]:9/"} {
		var refused *Refused
		if err := sender(t, map[string]string{"url": u}).Send(context.Background(), to, Message{}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "public") {
			t.Fatalf("%s: %v", u, err)
		}
	}
	ntfy, _ := Check("ntfy", map[string]string{"server": srv.URL})
	k, _ := Lookup("ntfy")
	n, _ := k.New(ntfy)
	var refused *Refused
	if err := n.Send(context.Background(), Recipient{Contact: map[string]string{"ntfy": "t"}}, Message{}); !errors.As(err, &refused) {
		t.Fatalf("ntfy on this host: %v", err)
	}
	for _, ip := range []string{"8.8.8.8", "2606:4700::1111"} {
		if !Public(netip.MustParseAddr(ip)) {
			t.Fatalf("%s is public", ip)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "::1", "fe80::1", "fd00::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "0.0.0.0"} {
		if Public(netip.MustParseAddr(ip)) {
			t.Fatalf("%s is not public", ip)
		}
	}

	// A provider's answer stays out of the log; only its status is kept.
	AllowLocal(true)
	code, answer = 400, "internal secret page"
	if err := sender(t, map[string]string{"url": srv.URL}).Send(context.Background(), to, Message{}); err == nil || strings.Contains(err.Error(), "internal") || !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("400: %v", err)
	}
}

// TestASecretStaysOutOfTheLog: a secret placed in the address, as written
// or as the URL encodes it, is masked in a failure's words.
func TestASecretStaysOutOfTheLog(t *testing.T) {
	for _, secret := range []string{"k3y/with space&more", "plain-key"} {
		s := sender(t, map[string]string{"url": "http://127.0.0.1:1/send?key={{urlquery .Secret}}&k2={{.Secret}}", "secret": secret, "address": ""})
		err := s.Send(context.Background(), Recipient{}, Message{})
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), url.QueryEscape(secret)) {
			t.Fatalf("%q: %v", secret, err)
		}
	}
	if h := hider("")("nothing to hide"); h != "nothing to hide" {
		t.Fatalf("an empty secret: %q", h)
	}
}

// TestATimeoutAfterTheRequestIsUnknown: a provider that took the whole
// request and did not answer may have sent it — not tried again; one that
// could not be reached is worth a retry.
func TestATimeoutAfterTheRequestIsUnknown(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		time.Sleep(300 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)
	s := sender(t, map[string]string{"url": slow.URL, "address": ""})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var unknown *Unknown
	if err := s.Send(ctx, Recipient{}, Message{Text: "x"}); !errors.As(err, &unknown) {
		t.Fatalf("a timeout after the request: %v", err)
	}
	var retry *Retry
	if err := sender(t, map[string]string{"url": "http://127.0.0.1:1/", "address": ""}).Send(context.Background(), Recipient{}, Message{}); !errors.As(err, &retry) {
		t.Fatalf("a refused connection: %v", err)
	}
}
