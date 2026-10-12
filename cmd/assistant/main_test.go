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

// This exercises the real CLI, HTTP clients, SQLite synchronization and agent;
// only external model responses are provided by the local HTTP fixture.
func TestCLIEndToEndAndFollowup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "cache.md"), []byte("---\ntitle: Cache Lab\n---\n矩阵转置减少缓存未命中。"), 0600)
	steps := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&p)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embeddings" {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 0}}}, "usage": map[string]int{"prompt_tokens": 4}})
			return
		}
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		steps++
		var msgs []struct{ Role, Content string }
		json.Unmarshal(p["messages"], &msgs)
		action := ""
		phase := (steps - 1) % 3
		if phase == 0 {
			if steps == 4 {
				found := false
				for _, m := range msgs {
					if strings.Contains(m.Content, "这篇记录矩阵转置") {
						found = true
					}
				}
				if !found {
					t.Error("followup lost conversational context")
				}
			}
			action = `{"action":"search","query":"矩阵转置"}`
		} else {
			last := msgs[len(msgs)-1].Content
			pos := strings.Index(last, `"id":"`)
			if pos < 0 {
				t.Error("no candidate/evidence ID")
				http.Error(w, "invalid", 500)
				return
			}
			id := strings.Split(last[pos+6:], `"`)[0]
			if phase == 1 {
				action = fmt.Sprintf(`{"action":"read","ids":[%q]}`, id)
			} else {
				action = fmt.Sprintf(`{"action":"final","answer":"这篇记录矩阵转置 [%s]","citations":[%q]}`, id, id)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": action}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}})
	}))
	defer s.Close()
	config := filepath.Join(t.TempDir(), "config.json")
	data := fmt.Sprintf(`{"articles_dir":%q,"index_db":%q,"chat":{"base_url":%q,"model":"chat","api_key":"test"},"embedding":{"base_url":%q,"model":"embed","api_key":"test"}}`, root, filepath.Join(t.TempDir(), "i.db"), s.URL, s.URL)
	os.WriteFile(config, []byte(data), 0600)
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
func TestCLIOfflineScanWithoutCredentials(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("# test"), 0600)
	var out, log bytes.Buffer
	if err := run(context.Background(), []string{"-scan", root}, strings.NewReader(""), &out, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "a.md") {
		t.Fatal(out.String())
	}
}
