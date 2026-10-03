package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/addon-kit/auth"
	"github.com/nexora-vpn/notif/internal/admins"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
	"gorm.io/gorm"
)

// newServer is a Notif on a fresh database — SQLite in a temporary
// directory, or the PostgreSQL that NEXORA_TEST_POSTGRES_DSN names (its
// tables emptied first) — with one admin, admin / correct horse.
func newServer(t *testing.T) (*Server, *gorm.DB, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{Port: "0", DataDir: dir, Driver: config.DriverSQLite, DSN: dir + "/notif.db"}
	if dsn := os.Getenv("NEXORA_TEST_POSTGRES_DSN"); dsn != "" {
		cfg.Driver, cfg.DSN = config.DriverPostgres, dsn
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver == config.DriverPostgres {
		gdb.Exec("TRUNCATE admins, sessions, panels, settings, users, channels, deliveries, attempts, sends RESTART IDENTITY")
	}
	if _, err := admins.EnsureFirst(gdb, "admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../nexora-addon.json")
	if err != nil {
		t.Fatal(err)
	}
	a, err := addon.New(addon.Config{Manifest: raw, DataDir: dir, ClaimCode: "TEST-CLAIM", Healthy: Healthy(gdb)})
	if err != nil {
		t.Fatal(err)
	}
	s := New(gdb, a, cfg, "test", nil)
	return s, gdb, s.Handler()
}

func call(t *testing.T, h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.7:4000"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func session(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("the session cookie is not HttpOnly and SameSite=Strict: %+v", c)
			}
			return c
		}
	}
	t.Fatalf("no session cookie: %d %s", w.Code, w.Body)
	return nil
}

// TestSigningInWithAndWithoutASecondFactor: a password signs in; once a
// second factor is on, the password asks for a code, a code is taken once,
// and the second factor goes off only with the password.
func TestSigningInWithAndWithoutASecondFactor(t *testing.T) {
	_, _, h := newServer(t)
	if w := call(t, h, "GET", "/api/me", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/login", `{"username":"admin","password":"wrong one!!"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", w.Code)
	}
	c := session(t, call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`))
	if w := call(t, h, "GET", "/api/me", "", c); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"username":"admin"`) {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}

	w := call(t, h, "POST", "/api/me/2fa/totp", "", c)
	var enrol struct{ Secret, URI string }
	_ = json.Unmarshal(w.Body.Bytes(), &enrol)
	if w.Code != http.StatusOK || !strings.HasPrefix(enrol.URI, "otpauth://totp/Nexora%20Notif:admin") {
		t.Fatalf("enrol: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "POST", "/api/me/2fa/totp/confirm", `{"code":"`+auth.TOTPCode(enrol.Secret, time.Now())+`"}`, c); w.Code != http.StatusNoContent {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}

	w = call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`)
	var pending struct {
		MFA   bool
		Token string
	}
	_ = json.Unmarshal(w.Body.Bytes(), &pending)
	if !pending.MFA || pending.Token == "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("the password alone signed in: %s", w.Body)
	}
	// The code the enrolment used is spent: the next step's is needed.
	auth.ClockOffset = 30 * time.Second
	defer func() { auth.ClockOffset = 0 }()
	code := auth.TOTPCode(enrol.Secret, auth.Now())
	c2 := session(t, call(t, h, "POST", "/api/login/mfa", `{"token":"`+pending.Token+`","code":"`+code+`"}`))
	w = call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`)
	_ = json.Unmarshal(w.Body.Bytes(), &pending)
	if w := call(t, h, "POST", "/api/login/mfa", `{"token":"`+pending.Token+`","code":"`+code+`"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("a code was taken twice: %d", w.Code)
	}
	if w := call(t, h, "DELETE", "/api/me/2fa", `{"password":"wrong one!!"}`, c2); w.Code != http.StatusForbidden {
		t.Fatalf("the second factor went off without the password: %d", w.Code)
	}
	if w := call(t, h, "DELETE", "/api/me/2fa", `{"password":"correct horse"}`, c2); w.Code != http.StatusNoContent {
		t.Fatalf("off: %d", w.Code)
	}
}

// TestFailedSignInsLockTheAddressOut: five failures, and even the right
// password waits.
func TestFailedSignInsLockTheAddressOut(t *testing.T) {
	_, _, h := newServer(t)
	for range 5 {
		call(t, h, "POST", "/api/login", `{"username":"admin","password":"wrong one!!"}`)
	}
	if w := call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after five failures: %d", w.Code)
	}
}

// TestAChangedPasswordEndsTheOtherSessions keeps the one that changed it.
func TestAChangedPasswordEndsTheOtherSessions(t *testing.T) {
	_, _, h := newServer(t)
	login := `{"username":"admin","password":"correct horse"}`
	a := session(t, call(t, h, "POST", "/api/login", login))
	b := session(t, call(t, h, "POST", "/api/login", login))
	if w := call(t, h, "PUT", "/api/me/password", `{"current":"correct horse","new":"short"}`, a); w.Code != http.StatusBadRequest {
		t.Fatalf("a short password: %d", w.Code)
	}
	if w := call(t, h, "PUT", "/api/me/password", `{"current":"correct horse","new":"battery staple"}`, a); w.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	if call(t, h, "GET", "/api/me", "", a).Code != http.StatusOK || call(t, h, "GET", "/api/me", "", b).Code != http.StatusUnauthorized {
		t.Fatal("the sessions after a password change")
	}
}

// TestUnregisteredItShowsItsClaimCodeAndIsHealthy: the dashboard's status
// before a panel registered it, the manifest, and the health path.
func TestUnregisteredItShowsItsClaimCodeAndIsHealthy(t *testing.T) {
	_, _, h := newServer(t)
	c := session(t, call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`))
	w := call(t, h, "GET", "/api/status", "", c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"claimCode":"TEST-CLAIM"`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/.well-known/nexora-addon.json", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug": "notif"`) && !strings.Contains(w.Body.String(), `"slug":"notif"`) {
		t.Fatalf("manifest: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "GET", "/api/nothing", "", c); w.Code != http.StatusNotFound {
		t.Fatalf("an unknown route: %d", w.Code)
	}
}
