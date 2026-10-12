package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"assistant/internal/knowledge"
	"assistant/internal/llm"
)

func actionJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func finalAgent(t *testing.T, k *knowledge.Index, final func() map[string]any) *Agent {
	t.Helper()
	step := 0
	return New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		step++
		switch step {
		case 1:
			return `{"action":"search","query":"缓存"}`, nil
		default:
			return actionJSON(t, final()), nil
		}
	}), k, 8, nil)
}

func TestAgentChecksStructuredCitations(t *testing.T) {
	for _, mode := range []string{"invented", "unlisted"} {
		t.Run(mode, func(t *testing.T) {
			k := index(t)
			id := searchTestHits(t, k)[0].ID
			a := finalAgent(t, k, func() map[string]any {
				citations := []string{id}
				if mode == "unlisted" || mode == "invented" {
					citations = []string{strings.Repeat("f", 64)}
				}
				return map[string]any{"action": "final", "sections": []map[string]any{{"text": "[CSAPP] 缓存实验", "citations": citations}}}
			})
			if _, err := a.Query(context.Background(), "缓存"); err == nil {
				t.Fatal("invalid structured citation accepted")
			}
		})
	}
}

func TestAgentAcceptsConsistentAndInsufficientAnswers(t *testing.T) {
	for _, insufficient := range []bool{false, true} {
		k := index(t)
		hits, err := k.Search(context.Background(), "缓存", 1)
		if err != nil || len(hits) != 1 {
			t.Fatalf("search: %v %v", hits, err)
		}
		id := hits[0].ID
		a := finalAgent(t, k, func() map[string]any {
			if insufficient {
				return map[string]any{"action": "final", "answer": "资料不足", "insufficient": true}
			}
			return map[string]any{"action": "final", "answer": "缓存 [" + id + "] [" + id + "]", "citations": []string{id, id}}
		})
		r, err := a.Query(context.Background(), "缓存")
		if err != nil || (!insufficient && len(r.Sources) != 1) {
			t.Fatalf("result: %+v %v", r, err)
		}
	}
}

type batchEmbed struct{}

func (batchEmbed) Embed(_ context.Context, texts []string) ([][]float64, error) {
	v := make([][]float64, len(texts))
	for i := range v {
		v[i] = []float64{1, 0}
	}
	return v, nil
}

func TestAgentRejectsChangedUncitedEvidence(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("缓存 "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	k, err := knowledge.Open(root, filepath.Join(t.TempDir(), "i.db"), "test", batchEmbed{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	if _, err := k.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := k.Search(context.Background(), "缓存", 5)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %v %v", hits, err)
	}
	a := finalAgent(t, k, func() map[string]any {
		if err := os.WriteFile(hits[0].Path, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		id := hits[1].ID
		return map[string]any{"action": "final", "answer": "缓存 [" + id + "]", "citations": []string{id}}
	})
	if _, err := a.Query(context.Background(), "缓存"); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("changed uncited evidence: %v", err)
	}
}

func TestAgentAllowsUncitedRefusal(t *testing.T) {
	step := 0
	a := New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		step++
		if step == 1 {
			return `{"action":"search","query":"无关主题"}`, nil
		}
		return `{"action":"final","answer":"未找到足够证据","insufficient":true}`, nil
	}), index(t), 8, nil)
	if _, err := a.Query(context.Background(), "无关主题"); err != nil {
		t.Fatalf("uncited refusal: %v", err)
	}
}

func TestCitationChecksAllowMarkdownLinksAndCode(t *testing.T) {
	k := index(t)
	id := searchTestHits(t, k)[0].ID
	a := finalAgent(t, k, func() map[string]any {
		answer := "[说明](https://example.com) `a[i]`\n```go\na[j]\n```\n缓存 [" + id + "]"
		return map[string]any{"action": "final", "answer": answer, "citations": []string{id}}
	})
	if _, err := a.Query(context.Background(), "缓存"); err != nil {
		t.Fatalf("ordinary Markdown was treated as a citation: %v", err)
	}
}

func TestStructuredSectionsAllowBracketedArticleTitles(t *testing.T) {
	k := index(t)
	id := searchTestHits(t, k)[0].ID
	a := finalAgent(t, k, func() map[string]any {
		return map[string]any{
			"action": "final",
			"sections": []map[string]any{{
				"text":      "[CSAPP] Cache Lab 的测试记录。",
				"citations": []string{id},
			}},
		}
	})
	r, err := a.Query(context.Background(), "缓存")
	if err != nil || r.Answer != "[CSAPP] Cache Lab 的测试记录。" || len(r.Sections) != 1 || len(r.Sources) != 1 {
		t.Fatalf("structured citation result: %+v %v", r, err)
	}
}

func TestAgentRejectsStaleSearchBeforeReturningEvidence(t *testing.T) {
	k := index(t)
	hit := searchTestHits(t, k)[0]
	calls := 0
	a := New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		calls++
		writeTestArticle(t, hit.Path, "changed after indexing")
		return `{"action":"search","query":"缓存"}`, nil
	}), k, 8, nil)
	_, err := a.Query(context.Background(), "关于ICPC 南昌站 邀请赛的文章")
	if err == nil || !strings.Contains(err.Error(), "source changed") || calls != 1 {
		t.Fatalf("stale evidence reached model: calls=%d err=%v", calls, err)
	}
}

func TestAgentICPCQueryCanCiteSearchDirectly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "icpc.md")
	writeTestArticle(t, path, "# ICPC 南昌站邀请赛\n南昌站邀请赛的合成比赛记录。")
	k, err := knowledge.Open(root, filepath.Join(t.TempDir(), "i.db"), "test", embed{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	if _, err := k.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a := New(responseFunc(func(_ context.Context, messages []llm.Message) (string, error) {
		calls++
		if calls == 1 {
			return `{"action":"search","query":"ICPC 南昌站 邀请赛"}`, nil
		}
		last := messages[len(messages)-1].Content
		var data struct {
			Results []knowledge.Hit `json:"results"`
		}
		if err := json.Unmarshal([]byte(last[strings.Index(last, "{"):]), &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Results) != 1 {
			t.Fatalf("unexpected results: %d", len(data.Results))
		}
		id := data.Results[0].ID
		return actionJSON(t, map[string]any{"action": "final", "answer": "南昌站邀请赛文章 [" + id + "]", "citations": []string{id}}), nil
	}), k, 8, nil)
	r, err := a.Query(context.Background(), "关于ICPC 南昌站 邀请赛的文章")
	if err != nil || calls != 2 || len(r.Sources) != 1 || filepath.Base(r.Sources[0].Path) != "icpc.md" {
		t.Fatalf("search/final result: %+v calls=%d err=%v", r, calls, err)
	}
}
