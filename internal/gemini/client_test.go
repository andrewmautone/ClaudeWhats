package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeServer(t *testing.T, reply string, capture *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "models/test-model:generateContent") || r.URL.Query().Get("key") != "k" {
			t.Errorf("bad request %s %s", r.URL.Path, r.URL.RawQuery)
		}
		json.NewDecoder(r.Body).Decode(capture)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"` + reply + `"}]}}]}`))
	}))
}

func TestTranscribeSendsInlineAudio(t *testing.T) {
	var got map[string]any
	srv := fakeServer(t, "olá mundo", &got)
	defer srv.Close()
	c := New("k", "test-model")
	c.BaseURL = srv.URL
	out, err := c.Transcribe(context.Background(), []byte("abc"), "audio/ogg")
	if err != nil || out != "olá mundo" {
		t.Fatal(out, err)
	}
	parts := got["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected text+inline parts: %v", parts)
	}
	inline := parts[1].(map[string]any)["inline_data"].(map[string]any)
	if inline["mime_type"] != "audio/ogg" || inline["data"] != "YWJj" {
		t.Fatalf("%v", inline)
	}
}

func TestSummarizeAndErrors(t *testing.T) {
	var got map[string]any
	srv := fakeServer(t, "resumo", &got)
	defer srv.Close()
	c := New("k", "test-model")
	c.BaseURL = srv.URL
	out, err := c.Summarize(context.Background(), "resuma", "texto longo")
	if err != nil || out != "resumo" {
		t.Fatal(out, err)
	}
	if _, err := New("", "m").Summarize(context.Background(), "a", "b"); err != ErrNoKey {
		t.Fatal("missing key must be ErrNoKey")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer bad.Close()
	c.BaseURL = bad.URL
	if _, err := c.Describe(context.Background(), []byte("x"), "image/jpeg", ""); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected http error, got %v", err)
	}
}
