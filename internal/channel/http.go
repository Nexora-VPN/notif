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
	"strconv"
	"strings"
	"text/template"
	"time"
)

// The generic HTTP channel (P32): any provider with an HTTP API, set up by
// the admin as an address, a method, headers and a body, each a template
// over the notice. GN-S3 adds form bodies, Basic authentication, an id per
// send, a CA bundle and the presets.

func init() {
	Register(Kind{
		Name: "http",
		Fields: []Field{
			{Key: "url", Required: true},
			{Key: "method", Default: "POST"},
			{Key: "address", Default: "phone"},
			{Key: "headers", Secret: true, Multiline: true},
			{Key: "contentType", Default: "application/json"},
			{Key: "body", Multiline: true, Default: `{"to": {{json .Address}}, "title": {{json .Title}}, "text": {{json .Text}}}`},
		},
		PerMinute: 60,
		New:       newHTTP,
	})
}

type httpChannel struct {
	url, method, address, contentType string
	headers                           [][2]string
	urlT, bodyT                       *template.Template
	client                            *http.Client
}

var funcs = template.FuncMap{
	// json writes a value as a JSON literal, quotes and escapes included.
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
}

func newHTTP(cfg map[string]string) (Sender, error) {
	c := &httpChannel{
		method:      strings.ToUpper(strings.TrimSpace(cfg["method"])),
		address:     strings.TrimSpace(cfg["address"]),
		contentType: strings.TrimSpace(cfg["contentType"]),
		client:      &http.Client{Timeout: 20 * time.Second},
	}
	if c.method == "" {
		c.method = http.MethodPost
	}
	switch c.method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return nil, fmt.Errorf("method must be GET, POST, PUT or PATCH")
	}
	var err error
	if c.urlT, err = template.New("url").Funcs(funcs).Option("missingkey=zero").Parse(cfg["url"]); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	if u, err := url.Parse(strings.SplitN(cfg["url"], "{{", 2)[0]); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("url must start with http:// or https:// and a host")
	}
	if c.bodyT, err = template.New("body").Funcs(funcs).Option("missingkey=zero").Parse(cfg["body"]); err != nil {
		return nil, fmt.Errorf("body: %w", err)
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
		c.headers = append(c.headers, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	return c, nil
}

// view is what the templates see.
type view struct {
	Address, Name, Title, Text, Kind, Key, Lang string
	Vars, Contact                               map[string]string
}

func (c *httpChannel) Send(ctx context.Context, to Recipient, m Message) error {
	v := view{Name: to.Name, Title: m.Title, Text: m.Text, Kind: m.Kind, Key: m.Key, Lang: to.Lang, Vars: m.Vars, Contact: to.Contact}
	if c.address != "" {
		v.Address = strings.TrimSpace(to.Contact[c.address])
		if v.Address == "" {
			return ErrNoAddress
		}
	}
	var u, body bytes.Buffer
	if err := c.urlT.Execute(&u, v); err != nil {
		return &Refused{Reason: "url: " + err.Error()}
	}
	if err := c.bodyT.Execute(&body, v); err != nil {
		return &Refused{Reason: "body: " + err.Error()}
	}
	var rd io.Reader
	if c.method != http.MethodGet {
		rd = &body
	}
	req, err := http.NewRequestWithContext(ctx, c.method, u.String(), rd)
	if err != nil {
		return &Refused{Reason: err.Error()}
	}
	if rd != nil && c.contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}
	for _, h := range c.headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return &Retry{Reason: err.Error()}
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	reason := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(answer)))
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooEarly, resp.StatusCode >= 500:
		return &Retry{Reason: reason, After: retryAfter(resp.Header.Get("Retry-After"))}
	default:
		return &Refused{Reason: reason}
	}
}

func retryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
