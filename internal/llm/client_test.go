package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientChatAndEmbeddingProtocol(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing auth")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			if body["model"] != "chat" {
				t.Error("wrong model")
			}
			w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"action\":\"search\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
		case "/embeddings":
			w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(Config{BaseURL: s.URL, APIKey: "test", Model: "chat"})
	v, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(v) != 2 || v[0][0] != 1 {
		t.Fatalf("%v %v", v, err)
	}
	answer, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}})
	if err != nil || answer != `{"action":"search"}` {
		t.Fatalf("%q %v", answer, err)
	}
	u := c.Usage()
	if u.Calls != 2 || u.PromptTokens != 16 || u.CompletionTokens != 3 {
		t.Fatalf("usage %+v", u)
	}
}
func TestClientRejectsInvalidEmbeddingAndHidesErrorBody(t *testing.T) {
	for _, body := range []string{`{"data":[{"index":0,"embedding":[0,0]}]}`, `{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		c := New(Config{BaseURL: s.URL})
		if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
			t.Fatal("bad embedding accepted")
		}
		s.Close()
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); w.Write([]byte("secret credential")) }))
	defer s.Close()
	c := New(Config{BaseURL: s.URL})
	_, err := c.Chat(context.Background(), nil)
	if err == nil || err.Error() != "model service returned HTTP 401" {
		t.Fatalf("%v", err)
	}
}
