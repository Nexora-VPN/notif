package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexora-vpn/addon-kit/web"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/model"
	"github.com/nexora-vpn/notif/internal/settings"
)

// TestEverythingLivesUnderTheBasePath (GN-S7, P43 (b)): with a base path
// the admin web, its API, the panel's routes and the address check answer
// only under it, and the root — Notif has no other page — answers like
// nothing is there. The admin's cookie keeps to the base path's tree.
func TestEverythingLivesUnderTheBasePath(t *testing.T) {
	_, _, h := newServerWith(t, func(c *config.Config) { c.BasePath = "/q7w2" })
	for _, p := range []string{"/", "/api/me", "/api/setup", "/health", "/.well-known/nexora-addon.json", "/nexora/setup", whoAmIPath, "/channels", "/q7w"} {
		if w := call(t, h, "GET", p, ""); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "q7w2") {
			t.Errorf("%s at the root = %d %q", p, w.Code, w.Body)
		}
	}
	if w := call(t, h, "GET", "/q7w2", ""); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/q7w2/" {
		t.Errorf("the bare base = %d %q", w.Code, w.Header().Get("Location"))
	}
	for p, want := range map[string]int{"/q7w2/health": http.StatusOK, "/q7w2/.well-known/nexora-addon.json": http.StatusOK, "/q7w2/api/me": http.StatusUnauthorized, "/q7w2" + whoAmIPath: http.StatusOK} {
		if w := call(t, h, "GET", p, ""); w.Code != want {
			t.Errorf("%s = %d, want %d", p, w.Code, want)
		}
	}
	w := call(t, h, "POST", "/q7w2/api/login", `{"username":"admin","password":"correct horse"}`)
	if c := session(t, w); c.Path != "/q7w2/" {
		t.Errorf("the session cookie's path = %q", c.Path)
	}
}

// TestTheSetUpChecklist (GN-S7, P44 (b)): each step done or not from what
// Notif holds — the address, a password of the admin's own rather than the
// install's, the second factor, a channel on, the panel's registration —
// and the address check finds this Notif under its base path, over a
// self-signed certificate too, and not another program. What the panel's
// install did is listed only while it is wrong (the panel's P11): an
// address over HTTPS and a password of the admin's own drop out.
func TestTheSetUpChecklist(t *testing.T) {
	s, gdb, h := newServerWith(t, func(c *config.Config) {
		c.BasePath, c.AdminPassword, c.PublicURL = "/q7w2", "correct horse", "https://203.0.113.9:8443"
	})
	cookie := session(t, call(t, h, "POST", "/q7w2/api/login", `{"username":"admin","password":"correct horse"}`))
	read := func() setupView {
		t.Helper()
		w := call(t, h, "GET", "/q7w2/api/setup", "", cookie)
		var v setupView
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("setup = %d %s", w.Code, w.Body)
		}
		return v
	}
	steps := func(v setupView) map[string]setupStep {
		out := map[string]setupStep{}
		for _, st := range v.Steps {
			out[st.Key] = st
		}
		return out
	}
	v := read()
	st := steps(v)
	if _, listed := st["address"]; listed || v.Done || v.AdminURL != "https://203.0.113.9:8443/q7w2/" || st["password"].Done || st["twofactor"].Done || st["channel"].Done || st["panel"].Done || st["panel"].Detail != "TEST-CLAIM" {
		t.Fatalf("a fresh install's checklist: %+v", v)
	}

	// The admin's own password, a second factor, a channel on.
	if w := call(t, h, "PUT", "/q7w2/api/me/password", `{"current":"correct horse","new":"a better horse 42"}`, cookie); w.Code >= 300 {
		t.Fatalf("password = %d %s", w.Code, w.Body)
	}
	cookie = session(t, call(t, h, "POST", "/q7w2/api/login", `{"username":"admin","password":"a better horse 42"}`))
	gdb.Model(&model.Admin{}).Where("username = ?", "admin").Update("totp_secret", "JBSWY3DPEHPK3PXP")
	gdb.Create(&model.Channel{Kind: "ntfy", Name: "ntfy", Enabled: true})
	st = steps(read())
	if _, listed := st["password"]; listed || !st["twofactor"].Done || !st["channel"].Done || st["channel"].Detail != "1" {
		t.Fatalf("after the admin's steps: %+v", st)
	}

	// An address without HTTPS is listed: it is wrong.
	s.cfg.HTTPS = "off"
	if err := settings.SaveAddress(gdb, "http://203.0.113.9:8097"); err != nil {
		t.Fatal(err)
	}
	if a := steps(read())["address"]; a.Key == "" || a.Done {
		t.Fatalf("an address over plain http with https off: %+v", a)
	}

	// The address check, from outside: this Notif under its base path, over
	// a self-signed certificate; another program refused.
	ours := httptest.NewTLSServer(s.Handler())
	defer ours.Close()
	s.cfg.HTTPS = web.HTTPSSelfSigned
	s.tls = &web.TLS{Mode: web.HTTPSSelfSigned, Dir: t.TempDir()}
	w := call(t, h, "PUT", "/q7w2/api/setup/address", `{"publicUrl":"`+ours.URL+`","check":true}`, cookie)
	var res struct {
		Reachable bool   `json:"reachable"`
		Error     string `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &res); !res.Reachable {
		t.Fatalf("this Notif over a self-signed certificate: %d %s", w.Code, w.Body)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("nginx")) }))
	defer other.Close()
	w = call(t, h, "PUT", "/q7w2/api/setup/address", `{"publicUrl":"`+other.URL+`","check":true}`, cookie)
	if json.Unmarshal(w.Body.Bytes(), &res); res.Reachable || !strings.Contains(res.Error, "not with this program") {
		t.Fatalf("another program: %s", w.Body)
	}
	if w := call(t, h, "PUT", "/q7w2/api/setup/address", `{"publicUrl":"https://n.example.com/admin"}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("an address with a path = %d", w.Code)
	}

	// The install's answer, changed, wins over the page's edit; unchanged,
	// it does not.
	if a, _ := settings.LoadAddress(gdb); a.PublicURL != other.URL {
		t.Fatalf("the page's edit was not kept: %+v", a)
	}
	if changed, _ := settings.ApplyInstallAddress(gdb, "https://203.0.113.9:8443"); changed {
		t.Fatal("an unchanged install answer overrode the page's edit")
	}
	if changed, _ := settings.ApplyInstallAddress(gdb, "https://notif.example.com"); !changed {
		t.Fatal("a changed install answer was not taken")
	}
}
