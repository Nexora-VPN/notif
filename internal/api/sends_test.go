package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nexora-vpn/notif/internal/model"
)

// TestAMessageToAGroup: the count before sending says where each account's
// message would go and how many are SMS; the send queues exactly those
// accounts, once each; the report matches the count; a second send is
// cancelled before it goes; an account's history lists what it was sent.
func TestAMessageToAGroup(t *testing.T) {
	s, gdb, h := newServer(t)
	var mu sync.Mutex
	got := map[string]int{}
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		got[r.URL.Path+" "+b["to"]+" "+b["text"]]++
		mu.Unlock()
	}))
	defer stand.Close()
	c := session(t, call(t, h, "POST", "/api/login", `{"username":"admin","password":"correct horse"}`))
	call(t, h, "POST", "/api/channels", `{"kind":"http","name":"bot","enabled":true,"config":{"url":"`+stand.URL+`/bot","address":"telegram_id"}}`, c)
	call(t, h, "POST", "/api/channels", `{"kind":"http","name":"sms","enabled":true,"config":{"url":"`+stand.URL+`/sms","address":"phone"}}`, c)
	for i, contact := range []map[string]string{
		{"telegram_id": "11"}, {"telegram_id": "12", "phone": "0912"}, {"phone": "0913"}, {"email": "x@y.z"},
	} {
		gdb.Create(&model.User{ID: uint(i + 1), Name: "u" + string(rune('a'+i)), Expiry: 2000000000, Contact: model.JSON[map[string]string]{V: contact}})
	}
	body := `{"userIds":[1,2,3,4],"title":"Maintenance","body":"Hello {name}, the service pauses tonight."}`
	var p struct {
		Total, Unreachable, SMS int
		ByChannel               []struct {
			Name  string
			Count int
		}
		Preview *model.Text
	}
	_ = json.Unmarshal(call(t, h, "POST", "/api/sends/preview", body, c).Body.Bytes(), &p)
	if p.Total != 4 || p.Unreachable != 1 || p.SMS != 1 || len(p.ByChannel) != 2 || p.ByChannel[0].Count != 2 || p.ByChannel[1].Count != 1 ||
		p.Preview == nil || p.Preview.Body != "Hello ua, the service pauses tonight." {
		t.Fatalf("preview %+v %+v", p, p.Preview)
	}
	w := call(t, h, "POST", "/api/sends", body, c)
	if w.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	var made struct{ ID uint }
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	s.outbox.ProcessDue(t.Context())
	s.outbox.ProcessDue(t.Context())
	var rep struct {
		Total, Unreachable, Sent, Failed, Queued, Cancelled int
		ByChannel                                           []struct {
			Name  string
			Count int
			SMS   bool
		}
	}
	_ = json.Unmarshal(call(t, h, "GET", "/api/sends/1", "", c).Body.Bytes(), &rep)
	if rep.Total != 4 || rep.Unreachable != 1 || rep.Sent != 3 || rep.Failed != 0 || rep.Queued != 0 || len(rep.ByChannel) != 2 {
		t.Fatalf("report %+v", rep)
	}
	sms := 0
	for _, b := range rep.ByChannel {
		if b.SMS {
			sms += b.Count
		}
	}
	if sms != p.SMS {
		t.Fatalf("SMS counted %d, sent %d", p.SMS, sms)
	}
	mu.Lock()
	if len(got) != 3 || got["/bot 12 Hello ub, the service pauses tonight."] != 1 || got["/sms 0913 Hello uc, the service pauses tonight."] != 1 {
		t.Fatalf("received %v", got)
	}
	mu.Unlock()

	call(t, h, "POST", "/api/sends", body, c)
	_ = json.Unmarshal(call(t, h, "POST", "/api/sends/2/cancel", "", c).Body.Bytes(), &rep)
	if rep.Cancelled != 3 {
		t.Fatalf("cancelled %+v", rep)
	}
	var hist []struct{ Kind, Status string }
	_ = json.Unmarshal(call(t, h, "GET", "/api/users/2/history", "", c).Body.Bytes(), &hist)
	if len(hist) != 2 || hist[0].Status != "cancelled" || hist[1].Status != "sent" {
		t.Fatalf("history %+v", hist)
	}
	for _, bad := range []string{`{"body":"x"}`, `{"userIds":[1],"filter":{"group":"g"},"body":"x"}`, `{"filter":{"password":"x"},"body":"x"}`, `{"userIds":[1],"body":" "}`} {
		if w := call(t, h, "POST", "/api/sends", bad, c); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "error") {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
}
