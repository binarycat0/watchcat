package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBotPost(t *testing.T) {
	var got map[string]any
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		if got["channel"] == "C-bad" {
			w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	b := &Bot{Token: "xoxb-test", BaseURL: ts.URL}
	if err := b.Post(context.Background(), "C1", "hi"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer xoxb-test" || got["text"] != "hi" {
		t.Fatalf("auth=%q body=%v", auth, got)
	}
	if err := b.Post(context.Background(), "C-bad", "hi"); err == nil || err.Error() != "slack: channel_not_found" {
		t.Fatalf("got %v", err)
	}
}
