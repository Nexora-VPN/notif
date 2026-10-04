package channel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Email (P33): an SMTP server of the operator's. In Russia's whitelist mode
// and in China with the VPN off it is the one channel that still reaches a
// customer (P40), so the message is plain and transactional: UTF-8 text,
// quoted-printable, a Message-ID on the sender's own domain.

func init() {
	Register(Kind{
		Name: "smtp", PerMinute: 60,
		Fields: []Field{
			{Key: "host", Required: true},
			{Key: "port", Default: "587"},
			{Key: "security", Default: "starttls", Choices: []string{"starttls", "tls", "none"}},
			{Key: "username"},
			{Key: "password", Secret: true},
			{Key: "from", Required: true},
			{Key: "fromName"},
		},
		New: newSMTP,
	})
}

type smtpChannel struct {
	host, port, security, user, pass string
	from                             mail.Address
}

func newSMTP(cfg map[string]string) (Sender, error) {
	c := &smtpChannel{
		host: strings.TrimSpace(cfg["host"]), port: strings.TrimSpace(cfg["port"]), security: cfg["security"],
		user: strings.TrimSpace(cfg["username"]), pass: cfg["password"],
	}
	if c.port == "" {
		c.port = "587"
	}
	if n, err := strconv.Atoi(c.port); err != nil || n < 1 || n > 65535 {
		return nil, errors.New("port must be a port number")
	}
	from, err := mail.ParseAddress(strings.TrimSpace(cfg["from"]))
	if err != nil {
		return nil, fmt.Errorf("from is not an address: %w", err)
	}
	from.Name = strings.TrimSpace(cfg["fromName"])
	c.from = *from
	return c, nil
}

func (c *smtpChannel) Send(ctx context.Context, to Recipient, m Message) error {
	addr, err := mail.ParseAddress(strings.TrimSpace(to.Contact["email"]))
	if err != nil {
		return ErrNoAddress
	}
	msg, err := c.compose(addr.Address, m)
	if err != nil {
		return &Refused{Reason: err.Error()}
	}
	return classifySMTP(c.deliver(ctx, addr.Address, msg))
}

func (c *smtpChannel) compose(to string, m Message) ([]byte, error) {
	var body bytes.Buffer
	qp := quotedprintable.NewWriter(&body)
	text := m.Text
	if m.Title != "" {
		text = m.Title + "\n\n" + m.Text
	}
	if _, err := qp.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	_ = qp.Close()
	subject := m.Title
	if subject == "" {
		subject = clip(m.Text, 60)
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := c.from.Address[strings.LastIndex(c.from.Address, "@")+1:]
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", c.from.String())
	h("To", to)
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

func (c *smtpChannel) deliver(ctx context.Context, to string, msg []byte) error {
	address := net.JoinHostPort(c.host, c.port)
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: c.host, MinVersion: tls.VersionTLS12}
	if c.security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cl, err := smtp.NewClient(conn, c.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if c.security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return &Refused{Reason: "the mail server does not offer STARTTLS; choose tls or none"}
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if c.user != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.user, c.pass, c.host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(c.from.Address); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

// classifySMTP: a 5xx answer is final (an address that does not exist, a
// refused sender), a 4xx or a broken connection is worth trying again.
func classifySMTP(err error) error {
	if err == nil {
		return nil
	}
	var refused *Refused
	if errors.As(err, &refused) {
		return err
	}
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 500 {
		return &Refused{Reason: "smtp: " + err.Error()}
	}
	return &Retry{Reason: "smtp: " + err.Error()}
}
