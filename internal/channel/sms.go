package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The named SMS providers (P37 (a)): Kavenegar and Faraz SMS, both by an
// approved template ("lookup" / "pattern") on a service line, which reaches
// numbers that block advertising SMS. The admin maps the template's
// variables to Notif's — one line each, token={name} — and the text of the
// message is the provider's approved template, not Notif's: the notice's
// variables fill it. Read from their documentation on 2026-10-04.

func init() {
	Register(Kind{
		Name: "kavenegar", Contact: "", PerMinute: 120,
		Fields: []Field{
			{Key: "apiKey", Secret: true, Required: true},
			{Key: "template", Required: true},
			{Key: "variables", Multiline: true, Default: "token={name}"},
			{Key: "apiBase", Default: "https://api.kavenegar.com/v1"},
		},
		New: newKavenegar,
	})
	Register(Kind{
		Name: "faraz", PerMinute: 60,
		Fields: []Field{
			{Key: "apiKey", Secret: true, Required: true},
			{Key: "code", Required: true},
			{Key: "lineNumber", Required: true},
			{Key: "variables", Multiline: true, Default: "name={name}"},
			{Key: "maxLengths", Multiline: true},
			{Key: "apiBase", Default: "https://api.iranpayamak.com"},
		},
		New: newFaraz,
	})
}

// smsVars renders a template's variable map for one message.
func smsVars(assign [][2]string, m Message) map[string]string {
	vars := map[string]string{"title": m.Title, "text": m.Text}
	for k, v := range m.Vars {
		vars[k] = v
	}
	out := map[string]string{}
	for _, a := range assign {
		out[a[0]] = strings.TrimSpace(Fill(a[1], vars))
	}
	return out
}

func checkBase(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		return "", errors.New("apiBase must be an http:// or https:// address")
	}
	return base, nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ── Kavenegar ────────────────────────────────────────────────────────────
//
// POST {base}/{key}/verify/lookup.json, form fields receptor, template and
// token…token3 (no spaces), token10 (up to 5 spaces), token20 (up to 8);
// the answer's return.status is the HTTP status, 200 for sent.

type kavenegar struct {
	key, template, base string
	assign              [][2]string
	client              *http.Client
}

var kavenegarTokens = map[string]int{"token": 0, "token2": 0, "token3": 0, "token10": 5, "token20": 8}

func newKavenegar(cfg map[string]string) (Sender, error) {
	base, err := checkBase(cfg["apiBase"])
	if err != nil {
		return nil, err
	}
	assign, err := Assignments(cfg["variables"])
	if err != nil {
		return nil, err
	}
	for _, a := range assign {
		if _, ok := kavenegarTokens[a[0]]; !ok {
			return nil, fmt.Errorf("Kavenegar's variables are token, token2, token3, token10 and token20, not %q", a[0])
		}
	}
	return &kavenegar{
		key: strings.TrimSpace(cfg["apiKey"]), template: strings.TrimSpace(cfg["template"]), base: base,
		assign: assign, client: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// kavenegarToken fits a value to a token's rules: no more spaces than it
// allows (the rest become dashes) and 100 characters at most.
func kavenegarToken(v string, spaces int) string {
	v = strings.Join(strings.Fields(v), " ")
	n := 0
	v = strings.Map(func(r rune) rune {
		if r == ' ' {
			n++
			if n > spaces {
				return '-'
			}
		}
		return r
	}, v)
	return clip(v, 100)
}

func (k *kavenegar) Send(ctx context.Context, to Recipient, m Message) error {
	receptor := IranLocal(to.Contact["phone"])
	if receptor == "" {
		e := E164(to.Contact["phone"])
		if e == "" {
			return ErrNoAddress
		}
		receptor = "00" + e[1:] // Kavenegar takes a foreign number with 00
	}
	form := url.Values{"receptor": {receptor}, "template": {k.template}}
	for name, v := range smsVars(k.assign, m) {
		form.Set(name, kavenegarToken(v, kavenegarTokens[name]))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.base+"/"+url.PathEscape(k.key)+"/verify/lookup.json", strings.NewReader(form.Encode()))
	if err != nil {
		return &Refused{Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := k.client.Do(req)
	if err != nil {
		return &Retry{Reason: "kavenegar: " + strings.ReplaceAll(err.Error(), k.key, "<key>")}
	}
	defer resp.Body.Close()
	var ans struct {
		Return struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"return"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ans)
	status := ans.Return.Status
	if status == 0 {
		status = resp.StatusCode
	}
	reason := fmt.Sprintf("kavenegar %d: %s", status, ans.Return.Message)
	switch {
	case status == 200:
		return nil
	case status == 409 || status >= 500 || status == 0:
		return &Retry{Reason: reason}
	case status == 418:
		return &Refused{Reason: reason + " (the account has no credit)"}
	default:
		return &Refused{Reason: reason}
	}
}

// ── Faraz SMS ────────────────────────────────────────────────────────────
//
// POST {base}/ws/v1/sms/pattern with the header Api-Key and {code,
// recipient (09…), line_number, number_format, attributes}; the answer's
// status is "success" or "error". A value longer than the pattern declares
// is not refused by Faraz but held for a person to approve while the call
// still says success — so every value is cut to its declared length here.

type faraz struct {
	key, code, line, base string
	assign                [][2]string
	max                   map[string]int
	client                *http.Client
}

func newFaraz(cfg map[string]string) (Sender, error) {
	base, err := checkBase(cfg["apiBase"])
	if err != nil {
		return nil, err
	}
	assign, err := Assignments(cfg["variables"])
	if err != nil {
		return nil, err
	}
	lens, err := Assignments(cfg["maxLengths"])
	if err != nil {
		return nil, err
	}
	max := map[string]int{}
	for _, l := range lens {
		n, err := strconv.Atoi(l[1])
		if err != nil || n < 1 {
			return nil, fmt.Errorf("maxLengths: %s=%s is not a length", l[0], l[1])
		}
		max[l[0]] = n
	}
	return &faraz{
		key: strings.TrimSpace(cfg["apiKey"]), code: strings.TrimSpace(cfg["code"]), line: strings.TrimSpace(cfg["lineNumber"]),
		base: base, assign: assign, max: max, client: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

var farazRecipient = regexp.MustCompile(`^09\d{9}$`)

func (f *faraz) Send(ctx context.Context, to Recipient, m Message) error {
	recipient := IranLocal(to.Contact["phone"])
	if !farazRecipient.MatchString(recipient) {
		// Faraz sends to Iranian mobiles only: any other number is no
		// address on this channel.
		return ErrNoAddress
	}
	attrs := smsVars(f.assign, m)
	for k, v := range attrs {
		if n, ok := f.max[k]; ok {
			attrs[k] = clip(v, n)
		}
	}
	body, _ := json.Marshal(map[string]any{
		"code": f.code, "recipient": recipient, "line_number": f.line, "number_format": "english", "attributes": attrs,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+"/ws/v1/sms/pattern", bytes.NewReader(body))
	if err != nil {
		return &Refused{Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", f.key)
	resp, err := f.client.Do(req)
	if err != nil {
		return &Retry{Reason: "faraz: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var ans struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &ans)
	reason := fmt.Sprintf("faraz HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	switch {
	case resp.StatusCode/100 == 2 && ans.Status == "success":
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return &Retry{Reason: reason, After: time.Minute}
	case resp.StatusCode >= 500:
		return &Retry{Reason: reason}
	default:
		return &Refused{Reason: clip(reason, 500)}
	}
}
