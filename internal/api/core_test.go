package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexora-vpn/notif/internal/model"
)

// TestChannelsTheirOrderAndATest: a generic channel is added with a secret
// header the browser never sees again, put second in the order, tested
// against one account, and the log shows the delivery with its attempt.
func TestChannelsTheirOrderAndATest(t *testing.T) {
	s, gdb, h := newServer(t)
	var hits int
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("X-Key") != "s3cret" {
			w.WriteHeader(401)
		}
	}))
	defer stand.Close()
	c := session(t, call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`))
	gdb.Create(&model.User{ID: 7, Name: "ana", Contact: model.JSON[map[string]string]{V: map[string]string{"phone": "0912"}}})

	w := call(t, h, "POST", "/api/channels", `{"kind":"http","name":"sms","enabled":true,"config":{"url":"`+stand.URL+`","headers":"X-Key: s3cret"}}`, c)
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), "s3cret") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	call(t, h, "POST", "/api/channels", `{"kind":"http","name":"mail","enabled":true,"config":{"url":"`+stand.URL+`","address":"email"}}`, c)
	if w := call(t, h, "POST", "/api/channels", `{"kind":"nope","name":"x","config":{}}`, c); w.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind: %d", w.Code)
	}
	if w := call(t, h, "PUT", "/api/channels/order", `{"ids":[2]}`, c); w.Code != http.StatusBadRequest {
		t.Fatalf("an order missing a channel: %d", w.Code)
	}
	if w := call(t, h, "PUT", "/api/channels/order", `{"ids":[2,1]}`, c); w.Code != http.StatusNoContent {
		t.Fatalf("order: %d %s", w.Code, w.Body)
	}
	// An edit that leaves the secret masked keeps it.
	if w := call(t, h, "PUT", "/api/channels/1", `{"name":"sms","enabled":true,"perMinute":30,"config":{"url":"`+stand.URL+`","headers":"••••"}}`, c); w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	var list []map[string]any
	_ = json.Unmarshal(call(t, h, "GET", "/api/channels", "", c).Body.Bytes(), &list)
	if len(list) != 2 || list[0]["name"] != "mail" || list[1]["perMinute"] != float64(30) {
		t.Fatalf("channels %v", list)
	}

	if w := call(t, h, "POST", "/api/test", `{"user":"nobody"}`, c); w.Code != http.StatusNotFound {
		t.Fatalf("a test to nobody: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/test", `{"user":"ana"}`, c); w.Code != http.StatusAccepted {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
	s.outbox.ProcessDue(t.Context())
	if hits != 1 {
		t.Fatalf("the stand-in was called %d times", hits)
	}
	var log struct {
		Items []struct {
			ID          uint
			Status      string
			UserName    string
			ChannelName string
		}
		Total int
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/deliveries", "", c).Body.Bytes(), &log)
	if log.Total != 1 || log.Items[0].Status != "sent" || log.Items[0].UserName != "ana" || log.Items[0].ChannelName != "sms" {
		t.Fatalf("log %+v", log)
	}
	var one struct {
		Attempts []struct{ Outcome, ChannelName string }
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/deliveries/1", "", c).Body.Bytes(), &one)
	// ana has no email: mail (first now) had no address, sms sent.
	if len(one.Attempts) != 2 || one.Attempts[0].Outcome != "no_address" || one.Attempts[1].Outcome != "sent" {
		t.Fatalf("attempts %+v", one.Attempts)
	}
	var sum struct {
		Today    map[string]int
		Channels int
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/summary", "", c).Body.Bytes(), &sum)
	if sum.Today["sent"] != 1 || sum.Channels != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if w := call(t, h, "PUT", "/api/settings/delivery", `{"timeZone":"Asia/Tehran","quietEnabled":true,"quietFrom":"23:00","quietTo":"07:00","language":"fa","retentionDays":30}`, c); w.Code != http.StatusNoContent {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	if w := call(t, h, "PUT", "/api/settings/delivery", `{"timeZone":"Nowhere/Land"}`, c); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad zone: %d", w.Code)
	}
	if w := call(t, h, "DELETE", "/api/channels/2", "", c); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
}

// TestAnAccountsLinks: the list shows where an account is reachable, and its
// page gives the link code and the Telegram link that carries it.
func TestAnAccountsLinks(t *testing.T) {
	s, gdb, h := newServer(t)
	c := session(t, call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`))
	gdb.Create(&model.User{ID: 9, Name: "bob", Contact: model.JSON[map[string]string]{V: map[string]string{"bale_id": "77", "note": "x"}}})
	gdb.Create(&model.Channel{
		Kind: "telegram", Name: "tg", Enabled: true, Position: 1,
		Config: model.JSON[map[string]string]{V: map[string]string{"token": "1:a"}},
		State:  model.JSON[map[string]string]{V: map[string]string{"username": "notif_bot"}},
	})
	var list struct {
		Items []struct {
			Name  string
			Reach map[string]string
		}
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/users?q=bo", "", c).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Reach["bale_id"] != "77" || list.Items[0].Reach["note"] != "" {
		t.Fatalf("list %+v", list)
	}
	var one struct {
		Code string
		Bots []struct{ URL, Username string }
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/users/9", "", c).Body.Bytes(), &one)
	if one.Code != s.links.Code(9) || len(one.Bots) != 1 || one.Bots[0].URL != "https://t.me/notif_bot?start="+one.Code {
		t.Fatalf("account %+v", one)
	}
	if w := call(t, h, "POST", "/api/users/9/unlink", `{"key":"phone"}`, c); w.Code != http.StatusBadRequest {
		t.Fatalf("unlinking a phone: %d", w.Code)
	}
}
