package channel

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

// guarded runs a test with the check on, as an install has it.
func guarded(t *testing.T) {
	AllowLocal(false)
	t.Cleanup(func() { AllowLocal(true) })
}

func newSender(t *testing.T, kind string, cfg map[string]string) Sender {
	t.Helper()
	cfg, err := Check(kind, cfg)
	if err != nil {
		t.Fatal(kind, err)
	}
	k, _ := Lookup(kind)
	s, err := k.New(cfg)
	if err != nil {
		t.Fatal(kind, err)
	}
	return s
}

// TestEveryChannelIsGuarded: the bots' API address, Rubika's, an SMS
// provider's and the mail server are refused on this host as the generic
// channel's address is — and the mail server's banner never reaches the
// log.
func TestEveryChannelIsGuarded(t *testing.T) {
	code, answer := 200, `{"ok":true,"result":{}}`
	srv, _ := standIn(t, &code, &answer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("220 internal-mail.corp ESMTP secret-banner\r\n"))
			_, _ = bufio.NewReader(c).ReadString('\n')
			c.Close()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	guarded(t)
	cases := []struct {
		kind string
		cfg  map[string]string
		to   map[string]string
	}{
		{"telegram", map[string]string{"token": "1:abc", "apiBase": srv.URL}, map[string]string{"telegram_id": "42"}},
		{"bale", map[string]string{"token": "1:abc", "apiBase": srv.URL, "private": "on"}, map[string]string{"bale_id": "42"}},
		{"rubika", map[string]string{"token": "tok", "apiBase": srv.URL}, map[string]string{"rubika_id": "b0"}},
		{"kavenegar", map[string]string{"apiKey": "k", "template": "t", "apiBase": srv.URL}, map[string]string{"phone": "09121234567"}},
		{"smtp", map[string]string{"host": host, "port": port, "security": "none", "from": "a@example.com"}, map[string]string{"email": "b@example.org"}},
		{"smtp", map[string]string{"host": host, "port": port, "security": "none", "from": "a@example.com", "private": "on"}, map[string]string{"email": "b@example.org"}},
	}
	for _, c := range cases {
		err := newSender(t, c.kind, c.cfg).Send(context.Background(), Recipient{Contact: c.to}, Message{Text: "x"})
		var refused *Refused
		if !errors.As(err, &refused) || !strings.Contains(err.Error(), "not public") || strings.Contains(err.Error(), "secret-banner") {
			t.Fatalf("%s %v: %v", c.kind, c.cfg, err)
		}
	}
}

// TestTheOwnNetwork: with the admin's word a private network is reached;
// this host, link-local and the metadata services never are.
func TestTheOwnNetwork(t *testing.T) {
	for _, ip := range []string{"10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.7", "100.101.102.103", "fd12::1", "::ffff:10.0.0.1"} {
		a := netip.MustParseAddr(ip)
		if Reachable(a, false) || !Reachable(a, true) {
			t.Fatalf("%s: off %v, on %v", ip, Reachable(a, false), Reachable(a, true))
		}
	}
	for _, ip := range []string{
		"127.0.0.1", "::1", "169.254.169.254", "fe80::1", "fd00:ec2::254", "fd20:ce::254", "fd00:c1::a9fe:a9fe", "100.100.100.200",
		"168.63.129.16", "64:ff9b::a83f:8110", "0.0.0.0", "224.0.0.1", "::ffff:127.0.0.1", "64:ff9b::7f00:1",
	} {
		if Reachable(netip.MustParseAddr(ip), true) {
			t.Fatalf("%s is reached with the own network on", ip)
		}
	}
	if !Reachable(netip.MustParseAddr("8.8.8.8"), false) {
		t.Fatal("a public address is refused")
	}
	if !envProxyReachable(netip.MustParseAddr("127.0.0.1")) || envProxyReachable(netip.MustParseAddr("169.254.169.254")) {
		t.Fatal("the environment's proxy on this host is dialled; one on the metadata address is not")
	}
	// The field is the kinds' own, off unless the admin turns it on.
	for _, kind := range []string{"http", "kavenegar", "faraz", "ntfy", "smtp", "telegram", "bale", "soroush", "rubika"} {
		cfg, err := Check(kind, map[string]string{"url": "https://x.io", "token": "1:a", "apiKey": "k", "template": "t", "code": "c", "lineNumber": "1", "host": "h", "from": "a@b.c"})
		if err != nil || cfg["private"] != "off" {
			t.Fatalf("%s: %v %v", kind, cfg["private"], err)
		}
	}
}

// forwardProxy is an HTTP proxy on this host that answers every request
// itself and records where it was asked to go.
func forwardProxy(t *testing.T, answer string) (*url.URL, *[]string) {
	var asked []string
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Host)
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(p.Close)
	u, _ := url.Parse(p.URL)
	return u, &asked
}

// TestAProxyIsDialledWhereItIs: a bot's proxy is the admin's and is held
// to the channel's rules — on this host only when the install named it,
// host and port; a target written as an address is still judged through
// it, a name is left to the proxy but for this host's, the metadata
// services' and the self-spelling ones. The other channels take the
// environment's proxy, which is the operator's and may be on this host.
func TestAProxyIsDialledWhereItIs(t *testing.T) {
	proxy, asked := forwardProxy(t, `{"ok":true,"result":{"message_id":1}}`)
	guarded(t)
	t.Cleanup(func() { _ = SetLocalProxies("") })
	to := Recipient{Contact: map[string]string{"telegram_id": "42"}}
	bot := newSender(t, "telegram", map[string]string{"token": "1:abc", "apiBase": "http://api.example.test", "proxy": proxy.String()})
	var refused *Refused
	if err := bot.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &refused) || len(*asked) != 0 {
		t.Fatalf("an admin's proxy on this host the install did not name: %v, asked %v", err, *asked)
	}
	_, port, _ := net.SplitHostPort(proxy.Host)
	if err := SetLocalProxies("localhost:1, 127.0.0.1:" + port); err != nil {
		t.Fatal(err)
	}
	if err := bot.Send(context.Background(), to, Message{Text: "x"}); err != nil || len(*asked) != 1 || (*asked)[0] != "api.example.test" {
		t.Fatalf("through the proxy: %v, asked %v", err, *asked)
	}
	// The same host on another port is another service of this host (a
	// new channel: the one above keeps its connection to the proxy).
	_ = SetLocalProxies("127.0.0.1:1")
	bot = newSender(t, "telegram", map[string]string{"token": "1:abc", "apiBase": "http://api.example.test", "proxy": proxy.String()})
	if err := bot.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &refused) || len(*asked) != 1 {
		t.Fatalf("a port the install did not name: %v, asked %v", err, *asked)
	}
	_ = SetLocalProxies("127.0.0.1:" + port)
	for _, base := range []string{
		"http://127.0.0.1:9", "http://169.254.169.254", "http://localhost:8080", "http://[fd20:ce::254]",
		"http://metadata.google.internal", "http://metadata", "http://127.0.0.1.nip.io:8080", "http://10-0-0-1.sslip.io",
	} {
		bot := newSender(t, "telegram", map[string]string{"token": "1:abc", "apiBase": base, "proxy": proxy.String()})
		var refused *Refused
		if err := bot.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &refused) {
			t.Fatalf("%s through the proxy: %v", base, err)
		}
	}
	if len(*asked) != 1 {
		t.Fatalf("the proxy was asked for %v", *asked)
	}
	*asked = nil

	// A proxy Notif 0.1.0 used on this server is kept while the channel's
	// proxy is the one carried over.
	k, _ := Lookup("telegram")
	for kept, works := range map[string]bool{proxy.String(): true, "socks5://127.0.0.1:1": false} {
		_ = SetLocalProxies("")
		cfg, _ := Check("telegram", map[string]string{"token": "1:abc", "apiBase": "http://api.example.test", "proxy": proxy.String()})
		cfg[KeptProxy] = kept
		bot, err := k.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := bot.Send(context.Background(), to, Message{Text: "x"}); (err == nil) != works {
			t.Fatalf("kept %s: %v", kept, err)
		}
	}
	_ = SetLocalProxies("")

	envProxy = func(*http.Request) (*url.URL, error) { return proxy, nil }
	t.Cleanup(func() { envProxy = http.ProxyFromEnvironment })
	s := newSender(t, "http", map[string]string{"url": "http://sms.example.test/send", "address": ""})
	if err := s.Send(context.Background(), Recipient{}, Message{Text: "x"}); err != nil || len(*asked) == 0 || (*asked)[len(*asked)-1] != "sms.example.test" {
		t.Fatalf("the environment's proxy: %v, asked %v", err, *asked)
	}
}

// TestABotTimeoutAfterTheRequestIsUnknown: a Bot API (or Rubika) that took
// the whole request and did not answer may have sent it; one that could not
// be reached is worth a retry.
func TestABotTimeoutAfterTheRequestIsUnknown(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		time.Sleep(300 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)
	cases := []struct {
		kind string
		cfg  map[string]string
		to   map[string]string
	}{
		{"telegram", map[string]string{"token": "1:abc"}, map[string]string{"telegram_id": "42"}},
		{"rubika", map[string]string{"token": "tok"}, map[string]string{"rubika_id": "b0"}},
	}
	for _, c := range cases {
		c.cfg["apiBase"] = slow.URL
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var unknown *Unknown
		if err := newSender(t, c.kind, c.cfg).Send(ctx, Recipient{Contact: c.to}, Message{Text: "x"}); !errors.As(err, &unknown) {
			t.Fatalf("%s, a timeout after the request: %v", c.kind, err)
		}
		cancel()
		c.cfg["apiBase"] = "http://127.0.0.1:1"
		var retry *Retry
		if err := newSender(t, c.kind, c.cfg).Send(context.Background(), Recipient{Contact: c.to}, Message{Text: "x"}); !errors.As(err, &retry) {
			t.Fatalf("%s, a refused connection: %v", c.kind, err)
		}
	}
}

// TestTheOperatorsProxiesAnywhere: local_proxies is the operator's answer
// and is taken as given — this server, the docker host's gateway, the
// LAN, a name — port and all; never a link-local address or a metadata
// service.
func TestTheOperatorsProxiesAnywhere(t *testing.T) {
	t.Cleanup(func() { _ = SetLocalProxies("") })
	if err := SetLocalProxies("10.0.0.1:1080, 172.17.0.1:10808 localhost:10808"); err != nil {
		t.Fatal(err)
	}
	ok := botProxyReachable(false, nil)
	for ap, want := range map[string]bool{
		"10.0.0.1:1080": true, "10.0.0.1:1081": false, "172.17.0.1:10808": true, "127.0.0.1:10808": true, "10.0.0.2:1080": false,
	} {
		if ok(netip.MustParseAddrPort(ap)) != want {
			t.Errorf("%s: want %v", ap, want)
		}
	}
	for _, bad := range []string{"169.254.169.254:80", "[fe80::1]:1080", "[fd00:ec2::254]:80", "127.0.0.1", "127.0.0.1:0", ":1080"} {
		if SetLocalProxies(bad) == nil {
			t.Errorf("local_proxies took %q", bad)
		}
	}
}

// TestThisServersOwnAddressesAreRefused: every address on this server's
// interfaces is this server — its public one, a bridge's — and refused
// with the switch on or off; the environment's proxy may still be there.
func TestThisServersOwnAddressesAreRefused(t *testing.T) {
	reread := func(f func() ([]net.Addr, error)) {
		interfaceAddrs = f
		owned.Lock()
		owned.at = time.Time{}
		owned.Unlock()
	}
	reread(func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("203.0.113.5"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("172.17.0.1"), Mask: net.CIDRMask(16, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)},
		}, nil
	})
	t.Cleanup(func() { reread(net.InterfaceAddrs) })
	for _, ip := range []string{"203.0.113.5", "172.17.0.1", "::ffff:203.0.113.5", "2001:db8::5"} {
		a := netip.MustParseAddr(ip)
		if Reachable(a, false) || Reachable(a, true) {
			t.Errorf("%s, this server's own, is reached", ip)
		}
		if !envProxyReachable(a) {
			t.Errorf("the environment's proxy on %s is refused", ip)
		}
	}
	if !Reachable(netip.MustParseAddr("203.0.113.6"), false) || !Reachable(netip.MustParseAddr("172.17.0.2"), true) {
		t.Fatal("a neighbour is refused as this server")
	}
	guarded(t)
	if err := checkTarget("203.0.113.5", true, true); !errors.Is(err, ErrLocalAddress) {
		t.Fatalf("this server's address through a proxy: %v", err)
	}
	// A 0.1.0 bot's proxy on one of these addresses — an Xray inbound on
	// the same box — is kept on upgrade like a loopback one, and dialled.
	for _, proxy := range []string{"socks5://203.0.113.5:1080", "socks5://172.17.0.1:10808"} {
		out, _ := CarryOver(map[string]string{"apiBase": "https://api.telegram.org", "proxy": proxy}, func(string) []netip.Addr { return nil })
		if out[KeptProxy] != proxy || out["private"] != "off" {
			t.Errorf("%s on this server is not kept: %v", proxy, out)
		}
		kept, err := parseNamedProxy(strings.TrimPrefix(proxy, "socks5://"))
		if err != nil {
			t.Fatal(err)
		}
		ap := netip.MustParseAddrPort(strings.TrimPrefix(proxy, "socks5://"))
		if !botProxyReachable(false, []namedProxy{kept})(ap) {
			t.Errorf("the kept proxy %s is refused", proxy)
		}
	}
}

// TestCarryOver: the settings 0.1.0 stored are carried over by where their
// addresses are; a name is looked up, one with no dot is a neighbour's.
func TestCarryOver(t *testing.T) {
	lookup := func(host string) []netip.Addr {
		switch host {
		case "relay.lan":
			return []netip.Addr{netip.MustParseAddr("192.168.1.9")}
		case "proxy.box":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.1")}
	}
	cases := []struct {
		kind          string
		cfg           map[string]string
		private, kept string
	}{
		{"http", map[string]string{"url": "https://relay.lan/send?to={{.Address}}"}, "on", ""},
		{"ntfy", map[string]string{"server": "http://ntfy:80"}, "on", ""},
		{"ntfy", map[string]string{"server": "https://ntfy.sh"}, "off", ""},
		{"telegram", map[string]string{"apiBase": "https://api.telegram.org", "proxy": "socks5h://proxy.box:1080"}, "off", "socks5h://proxy.box:1080"},
		{"telegram", map[string]string{"apiBase": "https://api.telegram.org", "proxy": "http://localhost:8118"}, "off", "http://localhost:8118"},
		{"rubika", map[string]string{"apiBase": "http://127.0.0.1:9000"}, "off", ""},
		{"smtp", map[string]string{"host": "100.64.1.2"}, "on", ""},
	}
	for _, c := range cases {
		out, carried := CarryOver(c.cfg, lookup)
		if !carried || out["private"] != c.private || out[KeptProxy] != c.kept {
			t.Errorf("%s %v: %v", c.kind, c.cfg, out)
		}
	}
	if _, carried := CarryOver(map[string]string{"url": "http://10.0.0.1", "private": "off"}, lookup); carried {
		t.Error("settings saved since were carried over again")
	}
}
