package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRubika: a notice goes as {"status":"OK"} JSON to /v3/{token}/sendMessage
// with the title marked Bold by its UTF-16 length; an error status is
// refused, a server failure retried, a missing link is no address.
func TestRubika(t *testing.T) {
	var got map[string]any
	status := "OK"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/tok123/sendMessage" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if status == "500" {
			w.WriteHeader(502)
			return
		}
		_, _ = w.Write([]byte(`{"status":"` + status + `","dev_message":"no such chat","data":{"message_id":"9"}}`))
	}))
	defer srv.Close()
	cfg, err := Check("rubika", map[string]string{"token": "tok123", "apiBase": srv.URL + "/v3"})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("rubika")
	s, _ := k.New(cfg)
	to := Recipient{Contact: map[string]string{"rubika_id": "b0abc"}}
	if err := s.Send(context.Background(), to, Message{Title: "انقضا 😀", Text: "۳ روز"}); err != nil {
		t.Fatal(err)
	}
	parts := got["metadata"].(map[string]any)["meta_data_parts"].([]any)
	part := parts[0].(map[string]any)
	// "انقضا 😀" is 7 runes but 8 UTF-16 units (the emoji is two).
	if got["chat_id"] != "b0abc" || got["text"] != "انقضا 😀\n\n۳ روز" || part["type"] != "Bold" || part["length"] != float64(8) {
		t.Fatalf("sent %v", got)
	}
	status = "INVALID_INPUT"
	var refused *Refused
	if err := s.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &refused) || !strings.Contains(err.Error(), "no such chat") {
		t.Fatalf("an error status: %v", err)
	}
	status = "500"
	var retry *Retry
	if err := s.Send(context.Background(), to, Message{Text: "x"}); !errors.As(err, &retry) {
		t.Fatalf("a server failure: %v", err)
	}
	if err := s.Send(context.Background(), Recipient{}, Message{Text: "x"}); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("no link: %v", err)
	}
	if _, err := Check("rubika", map[string]string{"token": "a b"}); err == nil {
		t.Fatal("a token with a space was taken")
	}
}
