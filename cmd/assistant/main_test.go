package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real CLI, HTTP clients, SQLite and agent; only model responses are simulated.
func TestCLIEndToEndAndFollowup(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, filepath.Join(root, "cache.md"), "---\ntitle: Cache Lab\n---\n矩阵转置减少缓存未命中。")
	fixture := &modelFixture{t: t}
	s := httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	defer s.Close()
	config := filepath.Join(t.TempDir(), "config.json")
	data := fmt.Sprintf(`{"articles_dir":%q,"index_db":%q,"chat":{"base_url":%q,"model":"chat","api_key":"test"},"embedding":{"base_url":%q,"model":"embed","api_key":"test"}}`, root, filepath.Join(t.TempDir(), "i.db"), s.URL, s.URL)
	writeCLIFile(t, config, data)
	var out, log bytes.Buffer
	err := run(context.Background(), []string{"-config", config}, strings.NewReader("找缓存文章\n展开看看\n/exit\n"), &out, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "cache.md:4-4") || strings.Count(out.String(), "这篇记录矩阵转置") != 2 {
		t.Fatalf("output %s", out.String())
	}
	if !strings.Contains(log.String(), "encoded 0 changed chunks") {
		t.Fatalf("cache not reused: %s", log.String())
	}
}

type modelFixture struct {
	t     *testing.T
	steps int
}

func (f *modelFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Error(err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var response any
	switch r.URL.Path {
	case "/embeddings":
		response = map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 0}}}, "usage": map[string]int{"prompt_tokens": 4}}
	case "/chat/completions":
		f.steps++
		var messages []string
		for _, m := range payload.Messages {
			messages = append(messages, m.Content)
		}
		action := f.action(messages)
		response = map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": action}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}}
	default:
		http.NotFound(w, r)
		return
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		f.t.Error(err)
	}
}

func (f *modelFixture) action(messages []string) string {
	phase := (f.steps - 1) % 2
	if phase == 0 {
		if f.steps == 3 && !strings.Contains(strings.Join(messages, "\n"), "这篇记录矩阵转置") {
			f.t.Error("followup lost conversational context")
		}
		return `{"action":"search","query":"矩阵转置"}`
	}
	if len(messages) == 0 {
		f.t.Error("no messages")
		return "{}"
	}
	last := messages[len(messages)-1]
	pos := strings.Index(last, `"id":"`)
	if pos < 0 {
		f.t.Error("no candidate/evidence ID")
		return "{}"
	}
	id := strings.Split(last[pos+6:], `"`)[0]
	return fmt.Sprintf(`{"action":"final","answer":"这篇记录矩阵转置 [%s]","citations":[%q]}`, id, id)
}

func writeCLIFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIOfflineScanWithoutCredentials(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, filepath.Join(root, "a.md"), "# test")
	var out, log bytes.Buffer
	if err := run(context.Background(), []string{"-scan", root}, strings.NewReader(""), &out, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "a.md") {
		t.Fatal(out.String())
	}
}
