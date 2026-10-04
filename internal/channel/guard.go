package channel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nexora-vpn/addon-kit/telegram"
)

// Every channel reaches an address the admin chose — the generic HTTP
// channel's, an SMS provider's or a bot's API address, the ntfy server,
// the mail server — and by default only on the public internet. An
// address of this server (loopback, and every address its network
// interfaces carry, its public one among them), on a private network,
// link-local (the cloud's metadata service among them) or otherwise not
// routable is refused when it is dialled: the check runs on the address the
// name resolved to, for every connection, so neither a name that resolves
// differently the second time nor a redirect gets past it. The admin web is
// no way to read the server's own neighbourhood.
//
// In a container "this server" is the container: the host's addresses are
// not on its interfaces, so the host is reached as any other address is —
// at the bridge's gateway (172.17.0.1, say) when "private" is on, and at
// its public address always.
//
// What widens it:
//   - "private" on a channel, the admin's word that its address is on their
//     own network (a relay over the LAN, WireGuard or Tailscale, ntfy in the
//     same compose stack): the private ranges and 100.64.0.0/10 are reached
//     then. This server's own addresses, link-local and the metadata
//     services stay refused even so.
//   - A bot channel's own proxy is an address the admin typed, and is
//     judged as one: public, or private with "private" on — or one of the
//     proxies the operator named at install (the local_proxies option,
//     SetLocalProxies), host and port both, wherever it is: this server, the
//     docker host's gateway, the LAN. A proxy on this server that Notif
//     0.1.0 used is kept as one named (KeptProxy, CarryOver).
//   - The environment's proxy — HTTPS_PROXY / HTTP_PROXY with NO_PROXY, for
//     the channels other than the bots — is the operator's, and is dialled
//     wherever it is, this server included.
//
// No proxy, whoever named it, is dialled on a link-local address or a
// metadata service.
//
// Through a proxy, a target written as an address is judged as a direct
// one is; a target written as a name is resolved by the proxy, where Notif
// cannot see the address — only this host's names, the .internal names
// (the clouds' metadata among them), the metadata services' other names,
// the well-known names that answer with whatever address they spell
// (nip.io and the like) and, unless "private" is on, a bare name with no
// dot are refused here.
// Resolving every name here instead would judge the censoring resolvers'
// answers, which put blocked names on private or sinkhole addresses — the
// very reason the proxy is there. So a proxy the operator named reaches
// what that proxy reaches: the operator's choice, at install.

// allowLocal lifts the check; only tests turn it on (AllowLocal).
var allowLocal atomic.Bool

// AllowLocal lets the channels reach this host and private networks. Only
// tests call it, for a provider stood in on 127.0.0.1; an install never
// does.
func AllowLocal(on bool) { allowLocal.Store(on) }

// ErrLocalAddress is a channel's address that it may not reach.
var ErrLocalAddress = errors.New("the address is not public: a channel reaches public addresses, and a private network only when its settings say the address is on your own network; this server's own addresses and the cloud's metadata service never (a bot's proxy elsewhere only when the install names it)")

// privateField is the admin's word that a channel's address is on their
// own network, off unless they say so.
var privateField = Field{Key: "private", Default: "off", Choices: []string{"off", "on"}}

// ownNetwork reads privateField from a channel's settings.
func ownNetwork(cfg map[string]string) bool { return cfg["private"] == "on" }

// notPublic are the ranges the netip predicates do not cover: the address
// space carriers' NAT and some clouds' metadata services use, this-network,
// the protocol assignments, benchmarking, the reserved block, and NAT64's
// local-use prefix.
var notPublic = []netip.Prefix{
	sharedSpace,
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

var (
	// sharedSpace is the carriers' NAT space, where Tailscale puts its
	// network too.
	sharedSpace = netip.MustParsePrefix("100.64.0.0/10")
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
	// metadata are the clouds' metadata and host services outside
	// link-local: AWS's IPv6 one, Google Cloud's, Oracle Cloud's, Alibaba
	// Cloud's, and Azure's host address (WireServer), which is in public
	// space. The rest (169.254.169.254, Tencent's 169.254.0.23) are
	// link-local.
	metadata = []netip.Addr{
		netip.MustParseAddr("fd00:ec2::254"), netip.MustParseAddr("fd20:ce::254"), netip.MustParseAddr("fd00:c1::a9fe:a9fe"),
		netip.MustParseAddr("100.100.100.200"), netip.MustParseAddr("168.63.129.16"),
	}
	// metadataNames are the metadata services' names, refused even where
	// a proxy resolves the name: the names under .internal (Google's
	// metadata.google.internal, AWS's instance-data.ec2.internal) are
	// refused as a whole, being private-use by definition.
	metadataNames = []string{"metadata", "metadata.goog", "instance-data", "metadata.tencentyun.com"}
	// rebinding are names that answer with the address they spell
	// (127.0.0.1.nip.io, 10-0-0-1.sslip.io, anything.localtest.me): a way
	// to write this host or a private address as a name.
	rebinding = []string{"nip.io", "sslip.io", "xip.io", "nip.direct", "localtest.me", "lvh.me", "localhost.direct", "vcap.me", "lacolhost.com"}
)

// embedded is the IPv4 address NAT64's well-known prefix carries in its
// last four bytes: that address is the one judged.
func embedded(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() || !nat64.Contains(ip) {
		return ip, false
	}
	b := ip.As16()
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
}

// Public reports whether ip is on the public internet.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4, ok := embedded(ip); ok {
		return Public(v4)
	}
	for _, p := range notPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Reachable reports whether a channel may dial ip: a public address, or,
// when private is on, one on a private network — never this server's own,
// link-local or a metadata service.
func Reachable(ip netip.Addr, private bool) bool {
	ip = ip.Unmap()
	if v4, ok := embedded(ip); ok {
		return Reachable(v4, private)
	}
	if slices.Contains(metadata, ip) || own(ip) {
		return false
	}
	if Public(ip) {
		return true
	}
	return private && (ip.IsPrivate() || sharedSpace.Contains(ip))
}

// interfaceAddrs are this server's addresses (tests replace it).
var interfaceAddrs = net.InterfaceAddrs

// owned is this server's addresses as last read, and when.
var owned struct {
	sync.Mutex
	at    time.Time
	addrs []netip.Addr
}

// own reports whether ip is one of this server's addresses, as its network
// interfaces carry them now: read again when a minute old, so an address
// the server gains later is refused too.
func own(ip netip.Addr) bool {
	owned.Lock()
	defer owned.Unlock()
	if time.Since(owned.at) > time.Minute {
		if list, err := interfaceAddrs(); err == nil {
			addrs := make([]netip.Addr, 0, len(list))
			for _, a := range list {
				var raw net.IP
				switch v := a.(type) {
				case *net.IPNet:
					raw = v.IP
				case *net.IPAddr:
					raw = v.IP
				}
				if x, ok := netip.AddrFromSlice(raw); ok {
					addrs = append(addrs, x.Unmap())
				}
			}
			owned.addrs, owned.at = addrs, time.Now()
		}
	}
	return slices.Contains(owned.addrs, ip.Unmap().WithZone(""))
}

// neverProxy are the addresses no proxy is dialled at, whoever named it:
// link-local (the cloud's metadata service among them), the metadata
// services, multicast and no address at all.
func neverProxy(ip netip.Addr) bool {
	ip = ip.Unmap().WithZone("")
	if v4, ok := embedded(ip); ok {
		ip = v4
	}
	return !ip.IsValid() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || slices.Contains(metadata, ip)
}

// envProxyReachable reports whether the environment's proxy may be dialled
// at ip: anywhere but link-local and the metadata services — the operator
// set it, on this server or elsewhere.
func envProxyReachable(ip netip.Addr) bool { return !neverProxy(ip) }

// namedProxy is one proxy the operator named (local_proxies), or a kept
// one (KeptProxy): a host — an address or a name — and a port.
type namedProxy struct {
	host string
	addr netip.Addr // the host when it is an address
	port uint16
}

// parseNamedProxy reads host:port.
func parseNamedProxy(hostport string) (namedProxy, error) {
	host, port, err := net.SplitHostPort(hostport)
	n, perr := strconv.ParseUint(port, 10, 16)
	if err != nil || perr != nil || n == 0 || host == "" {
		return namedProxy{}, fmt.Errorf("%q is not host:port", hostport)
	}
	p := namedProxy{host: host, port: uint16(n)}
	if ip, err := netip.ParseAddr(host); err == nil {
		if neverProxy(ip) {
			return namedProxy{}, fmt.Errorf("%q is link-local or a metadata service, never a proxy", hostport)
		}
		p.addr = ip.Unmap().WithZone("")
	}
	return p, nil
}

// is reports whether a dial to ap is a dial to this proxy: its port, and
// its address or one its name resolves to now.
func (p namedProxy) is(ap netip.AddrPort) bool {
	if ap.Port() != p.port {
		return false
	}
	ip := ap.Addr().Unmap().WithZone("")
	if p.addr.IsValid() {
		return ip == p.addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", p.host)
	return err == nil && slices.ContainsFunc(addrs, func(a netip.Addr) bool { return a.Unmap().WithZone("") == ip })
}

// localProxies are the proxies the operator named at install.
var localProxies atomic.Pointer[[]namedProxy]

// SetLocalProxies reads the install's local_proxies option: the proxies a
// bot channel may name wherever they are — on this server, at the docker
// host's gateway, on the LAN — each host:port, separated by commas or
// spaces. The operator's answer is trusted as given, short of a link-local
// address or a metadata service.
func SetLocalProxies(list string) error {
	var out []namedProxy
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		p, err := parseNamedProxy(f)
		if err != nil {
			return fmt.Errorf("local_proxies: %w", err)
		}
		out = append(out, p)
	}
	localProxies.Store(&out)
	return nil
}

// botProxyReachable is whether a bot channel's own proxy may be dialled at
// ip and port: where the channel itself may reach, a proxy the operator
// named, or the channel's kept one.
func botProxyReachable(private bool, kept []namedProxy) func(netip.AddrPort) bool {
	return func(ap netip.AddrPort) bool {
		if neverProxy(ap.Addr()) {
			return false
		}
		if Reachable(ap.Addr(), private) {
			return true
		}
		is := func(p namedProxy) bool { return p.is(ap) }
		if l := localProxies.Load(); l != nil && slices.ContainsFunc(*l, is) {
			return true
		}
		return slices.ContainsFunc(kept, is)
	}
}

// control is a dialer's last word on a connection: the resolved address
// and port.
func control(ok func(netip.AddrPort) bool) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		if allowLocal.Load() {
			return nil
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !ok(ap) {
			return ErrLocalAddress
		}
		return nil
	}
}

// byAddr is a check of the address alone, whatever the port.
func byAddr(ok func(netip.Addr) bool) func(netip.AddrPort) bool {
	return func(ap netip.AddrPort) bool { return ok(ap.Addr()) }
}

// guardedDialer dials what a channel may reach: public addresses, and
// private ones when private is on (the mail server's connection).
func guardedDialer(private bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 15 * time.Second, KeepAlive: 30 * time.Second,
		Control: control(byAddr(func(ip netip.Addr) bool { return Reachable(ip, private) })),
	}
}

// checkTarget judges a request's host before any proxy is asked: an
// address as a direct dial would be, and the names of this host always.
// A name sent through a proxy is resolved there, where Notif cannot see
// the answer: through one, the private-use .internal names, the metadata
// services' and the names that spell their own address are refused too,
// and a bare name (a host on a private network's) unless private is on.
func checkTarget(host string, private, proxied bool) error {
	if allowLocal.Load() {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Reachable(ip, private) {
			return ErrLocalAddress
		}
		return nil
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	under := func(zone string) bool { return host == zone || strings.HasSuffix(host, "."+zone) }
	switch {
	case under("localhost"):
		return ErrLocalAddress
	case !proxied:
		// Resolved here, and the address judged at the dial.
		return nil
	case under("internal"), slices.ContainsFunc(metadataNames, under), slices.ContainsFunc(rebinding, under),
		!private && !strings.Contains(host, "."):
		return ErrLocalAddress
	}
	return nil
}

// envProxy is the environment's proxy for a request (tests replace it).
var envProxy = http.ProxyFromEnvironment

// proxyAddr is the address a transport dials for a proxy, its port filled
// in by the scheme as the transport fills it.
func proxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// BotProxy reads a bot channel's proxy setting: nil for none.
func BotProxy(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if err := telegram.ValidProxy(s); err != nil {
		return nil, err
	}
	u, _ := url.Parse(s)
	if u.Scheme == "socks5h" {
		// Go's SOCKS5 dialer always hands the proxy the host name, which is
		// what socks5h means elsewhere; accept the spelling people copy.
		u.Scheme = "socks5"
	}
	return u, nil
}

// reach is what one channel's HTTP client may reach.
type reach struct {
	// private is the channel's privateField.
	private bool
	// proxy is the channel's own proxy (a bot's), nil for none.
	proxy *url.URL
	// env takes the proxy from the environment when there is no own one.
	env bool
	// kept is the channel's kept proxy (KeptProxy), dialled as one the
	// operator named.
	kept []namedProxy
}

// client is an HTTP client that reaches what r allows. A redirect is
// followed only to an address the same check allows (the dial refuses the
// rest), and at most three times.
func client(tlsCfg *tls.Config, r reach, timeout time.Duration) *http.Client {
	// The proxies this client was told to use: a dial to one of them is a
	// dial to the proxy, judged as one.
	var proxies sync.Map
	pick := func(req *http.Request) (*url.URL, error) {
		switch {
		case r.proxy != nil:
			return r.proxy, nil
		case r.env:
			return envProxy(req)
		}
		return nil, nil
	}
	proxyFor := func(req *http.Request) (*url.URL, error) {
		u, err := pick(req)
		if err != nil {
			return nil, err
		}
		if err := checkTarget(req.URL.Hostname(), r.private, u != nil); err != nil {
			return nil, err
		}
		if u != nil {
			proxies.Store(proxyAddr(u), true)
		}
		return u, nil
	}
	direct := guardedDialer(r.private)
	// The environment's proxy is the operator's; a bot's own is the
	// admin's, judged as the channel's address is.
	proxyOK := byAddr(envProxyReachable)
	if r.proxy != nil {
		proxyOK = botProxyReachable(r.private, r.kept)
	}
	viaProxy := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: control(proxyOK)}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if _, ok := proxies.Load(addr); ok {
			return viaProxy.DialContext(ctx, network, addr)
		}
		return direct.DialContext(ctx, network, addr)
	}
	transport := &http.Transport{
		Proxy:                 proxyFor,
		DialContext:           dial,
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// providerClient is the client of a channel that calls a provider's HTTP
// API (the generic channel, the SMS providers, ntfy): the environment's
// proxy, and private addresses as its settings say.
func providerClient(tlsCfg *tls.Config, cfg map[string]string) *http.Client {
	return client(tlsCfg, reach{private: ownNetwork(cfg), env: true}, 20*time.Second)
}

// botClient is a bot channel's client: its own proxy or none — a bot's
// proxy is set on the channel, never taken from the environment — with
// time for a long poll of updates.
func botClient(cfg map[string]string) (*http.Client, error) {
	proxy, err := BotProxy(cfg["proxy"])
	if err != nil {
		return nil, err
	}
	r := reach{private: ownNetwork(cfg), proxy: proxy}
	if k := cfg[KeptProxy]; proxy != nil && k != "" && k == cfg["proxy"] {
		if p, err := parseNamedProxy(proxyAddr(proxy)); err == nil {
			r.kept = []namedProxy{p}
		}
	}
	return client(nil, r, 90*time.Second), nil
}

// KeptProxy is a bot channel's record that its proxy, on this server, was
// carried over from Notif 0.1.0, which dialled any address (CarryOver):
// while the proxy field still holds the value recorded, that proxy is
// dialled as one the install named. It is Notif's, never the admin web's:
// not a field, so never shown or taken from a request, and an edit that
// changes the proxy drops it.
const KeptProxy = "keptProxy"

// CarryOver takes a channel's settings as Notif 0.1.0 stored them — with
// no "private" field: that version dialled every address — and carries
// over what worked: "private" on when its address or a bot's proxy is on a
// private network (an address there, a name that resolves there, or a name
// with no dot, as a container's beside it is), and a bot's proxy on this
// server — loopback or any of its interfaces' addresses — kept (KeptProxy). An address on this server itself is not carried
// over: only a proxy is. lookup resolves a name; one it cannot resolve is
// left as public. It reports false for settings a later version stored,
// which are left as they are.
func CarryOver(cfg map[string]string, lookup func(host string) []netip.Addr) (map[string]string, bool) {
	if _, stored := cfg["private"]; stored {
		return cfg, false
	}
	out := maps.Clone(cfg)
	if out == nil {
		out = map[string]string{}
	}
	addrs := func(host string) []netip.Addr {
		if ip, err := netip.ParseAddr(host); err == nil {
			return []netip.Addr{ip.Unmap().WithZone("")}
		}
		if strings.EqualFold(host, "localhost") {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
		}
		return lookup(host)
	}
	onPrivate := func(host string) bool {
		if host == "" {
			return false
		}
		if _, err := netip.ParseAddr(host); err != nil && !strings.Contains(host, ".") && !strings.EqualFold(host, "localhost") {
			return true
		}
		return slices.ContainsFunc(addrs(host), func(ip netip.Addr) bool {
			if v4, ok := embedded(ip); ok {
				ip = v4
			}
			return ip.IsPrivate() || sharedSpace.Contains(ip)
		})
	}
	private := false
	for _, key := range []string{"url", "apiBase", "server", "host"} {
		v := strings.TrimSpace(cfg[key])
		if v == "" {
			continue
		}
		host := v
		if strings.Contains(v, "://") {
			u, err := url.Parse(v)
			if err != nil {
				continue
			}
			host = u.Hostname()
		}
		private = private || onPrivate(host)
	}
	if proxy, err := BotProxy(cfg["proxy"]); err == nil && proxy != nil {
		host := proxy.Hostname()
		// On this server: loopback, or any address its interfaces carry —
		// which Reachable refuses, as it refuses this server's every one.
		if slices.ContainsFunc(addrs(host), func(ip netip.Addr) bool { return ip.Unmap().IsLoopback() || own(ip) }) {
			out[KeptProxy] = cfg["proxy"]
		} else {
			private = private || onPrivate(host)
		}
	}
	out["private"] = "off"
	if private {
		out["private"] = "on"
	}
	return out, true
}

// sendWatch follows one send's request: whether the whole of it was
// written, and whether an answer began to come back.
type sendWatch struct {
	wrote, answered atomic.Bool
}

// watchSend is ctx carrying a sendWatch for the requests made under it.
func watchSend(ctx context.Context) (context.Context, *sendWatch) {
	w := &sendWatch{}
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				w.wrote.Store(true)
			}
		},
		GotFirstResponseByte: func() { w.answered.Store(true) },
	}), w
}

// failure classifies a request that got no answer: a refused address is
// refused for good; after the whole request was written the provider may
// have acted on it, and the outcome is unknown (not sent again); before,
// it cannot have, and it is worth a retry. reason is already free of
// secrets. It is nil for a failure that came with an answer, which the
// caller judges by the answer.
func (w *sendWatch) failure(err error, name, reason string) error {
	switch {
	case errors.Is(err, ErrLocalAddress) || strings.Contains(err.Error(), ErrLocalAddress.Error()):
		// A client that keeps only an error's words (the Bot API's) still
		// says which it was.
		return &Refused{Reason: name + ": " + ErrLocalAddress.Error()}
	case w.answered.Load():
		return nil
	case w.wrote.Load():
		return &Unknown{Reason: reason + " (the request reached the provider; it may have been sent, so it is not sent again)"}
	default:
		return &Retry{Reason: reason}
	}
}

// do sends one request to a provider and classifies a failure to get an
// answer (sendWatch.failure). hide takes secrets out of the words.
func do(c *http.Client, req *http.Request, name string, hide func(string) string) (*http.Response, error) {
	ctx, w := watchSend(req.Context())
	resp, err := c.Do(req.WithContext(ctx))
	if err == nil {
		return resp, nil
	}
	reason := name + ": " + hide(err.Error())
	if f := w.failure(err, name, reason); f != nil {
		return nil, f
	}
	return nil, &Retry{Reason: reason}
}

// statusLine is what is kept of a provider's answer: its status, never its
// body, which only the provider should be trusted to fill.
func statusLine(name string, resp *http.Response) string {
	text := http.StatusText(resp.StatusCode)
	if text == "" {
		return fmt.Sprintf("%s HTTP %d", name, resp.StatusCode)
	}
	return fmt.Sprintf("%s HTTP %d %s", name, resp.StatusCode, text)
}

// hider takes each secret, as written and as a URL or a form encodes it,
// out of a text: an error of the HTTP client names the URL it called, and a
// secret placed in it would otherwise reach the log.
func hider(secrets ...string) func(string) string {
	var pairs []string
	for _, s := range secrets {
		if s == "" {
			continue
		}
		// As written, as a query or a form encodes it, as a path segment or
		// a whole path does, and as %q quotes it inside a *url.Error.
		forms := []string{s, url.QueryEscape(s), url.PathEscape(s), (&url.URL{Path: s}).EscapedPath()}
		if q := strconv.Quote(s); len(q) >= 2 {
			forms = append(forms, q[1:len(q)-1])
		}
		seen := map[string]bool{}
		for _, f := range forms {
			if f != "" && !seen[f] {
				seen[f] = true
				pairs = append(pairs, f, "<secret>")
			}
		}
	}
	if len(pairs) == 0 {
		return func(s string) string { return s }
	}
	r := strings.NewReplacer(pairs...)
	return r.Replace
}
