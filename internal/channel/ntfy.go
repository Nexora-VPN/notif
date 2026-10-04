package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ntfy (P39 (a)): a push app the user installs, subscribed to a topic of
// their own. The topic is the secret — anyone who knows it reads it — so
// Notif draws it from its own secret and the account (links.NtfyTopic) and
// keeps it, like every link, in the account's contact card as "ntfy": the
// admin turns it on for a user and hands them the subscribe address. A
// publish succeeds whether or not anyone listens, so only an account with
// the key is sent to; without it ntfy is no address and the next channel
// is tried.

func init() {
	Register(Kind{
		Name: "ntfy", PerMinute: 120,
		Fields: []Field{
			{Key: "server", Default: "https://ntfy.sh"},
			{Key: "token", Secret: true},
			{Key: "priority", Default: "3", Choices: []string{"1", "2", "3", "4", "5"}},
		},
		New: newNtfy,
	})
}

type ntfy struct {
	server, token string
	priority      int
	client        *http.Client
}

func newNtfy(cfg map[string]string) (Sender, error) {
	server, err := checkBase(cfg["server"])
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	p, _ := strconv.Atoi(cfg["priority"])
	if p < 1 || p > 5 {
		p = 3
	}
	return &ntfy{server: server, token: strings.TrimSpace(cfg["token"]), priority: p, client: &http.Client{Timeout: 20 * time.Second}}, nil
}

// NtfyServer is a channel's server, for the subscribe address the admin
// hands a user.
func NtfyServer(cfg map[string]string) string {
	s, err := checkBase(cfg["server"])
	if err != nil {
		return "https://ntfy.sh"
	}
	return s
}

func (n *ntfy) Send(ctx context.Context, to Recipient, m Message) error {
	topic := strings.TrimSpace(to.Contact["ntfy"])
	if topic == "" {
		return ErrNoAddress
	}
	// JSON publishing, so a title in any script travels as it is.
	body, _ := json.Marshal(map[string]any{"topic": topic, "title": m.Title, "message": m.Text, "priority": n.priority})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.server, bytes.NewReader(body))
	if err != nil {
		return &Refused{Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return &Retry{Reason: "ntfy: " + err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	reason := fmt.Sprintf("ntfy HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return &Retry{Reason: reason, After: retryAfter(resp.Header.Get("Retry-After"))}
	default:
		return &Refused{Reason: reason}
	}
}
