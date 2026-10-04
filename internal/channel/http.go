package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// The generic HTTP channel (P32, P39, P40): any provider with an HTTP API,
// set up as an address, a method, headers and a body, each a template over
// the notice — the way an SMS provider of the operator's choosing, a
// WhatsApp intermediary, LINE, Matrix, Pushover or VK are reached without a
// release. The body is JSON, a form (one name=value line each) or none;
// authentication is a header, HTTP Basic, or the {{.Secret}} placed where
// the provider wants it; a response pattern can say what success looks like
// when a provider answers 200 to a failure; a CA bundle trusts a private
// certificate. Presets fill all of this for the providers the docs name.

func init() {
	Register(Kind{
		Name: "http",
		Fields: []Field{
			{Key: "url", Required: true},
			{Key: "method", Default: "POST", Choices: []string{"GET", "POST", "PUT", "PATCH"}},
			{Key: "address", Default: "phone"},
			{Key: "secret", Secret: true},
			{Key: "headers", Multiline: true},
			{Key: "bodyType", Default: "json", Choices: []string{"json", "form", "none"}},
			{Key: "contentType", Default: "application/json"},
			{Key: "body", Multiline: true, Default: `{"to": {{json .Address}}, "title": {{json .Title}}, "text": {{json .Text}}}`},
			{Key: "basicUser"},
			{Key: "basicPassword", Secret: true},
			{Key: "successPattern"},
			{Key: "caBundle", Multiline: true},
		},
		PerMinute: 60,
		New:       newHTTP,
		Presets:   httpPresets,
	})
}

type httpChannel struct {
	method, address, bodyType, contentType string
	secret, basicUser, basicPass           string
	urlT, bodyT                            *template.Template
	formT                                  [][2]*template.Template
	headerT                                [][2]*template.Template
	success                                *regexp.Regexp
	client                                 *http.Client
}

var funcs = template.FuncMap{
	// json writes a value as a JSON literal, quotes and escapes included.
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
	// e164 is a phone number as +<country><number>, msisdn the same
	// without the plus, iran an Iranian mobile as 09….
	"e164":   E164,
	"msisdn": func(s string) string { return strings.TrimPrefix(E164(s), "+") },
	"iran":   IranLocal,
}

func parse(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(funcs).Option("missingkey=zero").Parse(text)
}

func newHTTP(cfg map[string]string) (Sender, error) {
	c := &httpChannel{
		method:    strings.ToUpper(strings.TrimSpace(cfg["method"])),
		address:   strings.TrimSpace(cfg["address"]),
		bodyType:  cfg["bodyType"],
		secret:    cfg["secret"],
		basicUser: strings.TrimSpace(cfg["basicUser"]), basicPass: cfg["basicPassword"],
		contentType: strings.TrimSpace(cfg["contentType"]),
	}
	if c.method == "" {
		c.method = http.MethodPost
	}
	if c.bodyType == "" {
		c.bodyType = "json"
	}
	var err error
	if c.urlT, err = parse("url", cfg["url"]); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	if u, err := url.Parse(strings.SplitN(cfg["url"], "{{", 2)[0]); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("url must start with http:// or https:// and a host")
	}
	switch c.bodyType {
	case "form":
		lines, err := Assignments(cfg["body"])
		if err != nil {
			return nil, fmt.Errorf("body: %w", err)
		}
		for _, l := range lines {
			t, err := parse(l[0], l[1])
			if err != nil {
				return nil, fmt.Errorf("body %s: %w", l[0], err)
			}
			n, _ := parse("name", l[0])
			c.formT = append(c.formT, [2]*template.Template{n, t})
		}
	case "json":
		if c.bodyT, err = parse("body", cfg["body"]); err != nil {
			return nil, fmt.Errorf("body: %w", err)
		}
	}
	for _, line := range strings.Split(cfg["headers"], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("a header is Name: value, one a line (%q)", line)
		}
		kt, _ := parse("hname", strings.TrimSpace(k))
		vt, err := parse("header", strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", k, err)
		}
		c.headerT = append(c.headerT, [2]*template.Template{kt, vt})
	}
	if p := strings.TrimSpace(cfg["successPattern"]); p != "" {
		if c.success, err = regexp.Compile(p); err != nil {
			return nil, fmt.Errorf("successPattern: %w", err)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if pem := strings.TrimSpace(cfg["caBundle"]); pem != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(pem)) {
			return nil, errors.New("caBundle holds no PEM certificate")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	c.client = &http.Client{Timeout: 20 * time.Second, Transport: transport}
	return c, nil
}

// view is what the templates see.
type view struct {
	Address, Name, Title, Text, Kind, Key, Lang, Secret string
	// ID is a number fixed by the delivery, the same on every retry: an
	// idempotency id for a provider that takes a numeric one (VK's
	// random_id), Key the same as text (Matrix's transaction id).
	ID            int64
	Vars, Contact map[string]string
}

func stableID(key string) int64 {
	sum := sha256.Sum256([]byte(key))
	return int64(binary.BigEndian.Uint32(sum[:4]) & 0x7fffffff)
}

func render(t *template.Template, v view) (string, error) {
	var b bytes.Buffer
	err := t.Execute(&b, v)
	return b.String(), err
}

func (c *httpChannel) Send(ctx context.Context, to Recipient, m Message) error {
	v := view{
		Name: to.Name, Title: m.Title, Text: m.Text, Kind: m.Kind, Key: m.Key, Lang: to.Lang,
		Secret: c.secret, ID: stableID(m.Key), Vars: m.Vars, Contact: to.Contact,
	}
	if c.address != "" {
		v.Address = strings.TrimSpace(to.Contact[c.address])
		if v.Address == "" {
			return ErrNoAddress
		}
	}
	u, err := render(c.urlT, v)
	if err != nil {
		return &Refused{Reason: "url: " + err.Error()}
	}
	var body io.Reader
	contentType := c.contentType
	switch c.bodyType {
	case "json":
		s, err := render(c.bodyT, v)
		if err != nil {
			return &Refused{Reason: "body: " + err.Error()}
		}
		body = strings.NewReader(s)
	case "form":
		form := url.Values{}
		for _, f := range c.formT {
			name, _ := render(f[0], v)
			val, err := render(f[1], v)
			if err != nil {
				return &Refused{Reason: "body " + name + ": " + err.Error()}
			}
			form.Add(name, val)
		}
		if c.method == http.MethodGet {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += sep + form.Encode()
		} else {
			body = strings.NewReader(form.Encode())
			contentType = "application/x-www-form-urlencoded"
		}
	}
	if c.method == http.MethodGet {
		body = nil
	}
	req, err := http.NewRequestWithContext(ctx, c.method, u, body)
	if err != nil {
		return &Refused{Reason: strings.ReplaceAll(err.Error(), c.secret, "<secret>")}
	}
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range c.headerT {
		k, _ := render(h[0], v)
		val, err := render(h[1], v)
		if err != nil {
			return &Refused{Reason: "header " + k + ": " + err.Error()}
		}
		req.Header.Set(k, val)
	}
	if c.basicUser != "" {
		req.SetBasicAuth(c.basicUser, c.basicPass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return &Retry{Reason: c.hide(err.Error())}
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	reason := c.hide(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, clip(strings.TrimSpace(string(answer)), 500)))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if c.success != nil && !c.success.Match(answer) {
			return &Refused{Reason: "the answer does not say sent: " + reason}
		}
		return nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooEarly, resp.StatusCode >= 500:
		return &Retry{Reason: reason, After: retryAfter(resp.Header.Get("Retry-After"))}
	default:
		return &Refused{Reason: reason}
	}
}

// hide keeps the secret out of the log, where a provider that echoes its
// key (or a URL carrying it) would otherwise put it.
func (c *httpChannel) hide(s string) string {
	if c.secret == "" {
		return s
	}
	return strings.ReplaceAll(s, c.secret, "<secret>")
}

func retryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
