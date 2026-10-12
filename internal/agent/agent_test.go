package agent

import (
	"assistant/internal/knowledge"
	"assistant/internal/llm"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type embed struct{}

func (embed) Embed(context.Context, []string) ([][]float64, error) { return [][]float64{{1, 0}}, nil }

type scripted struct {
	step int
	bad  bool
	loop bool
}

func (s *scripted) Chat(ctx context.Context, m []llm.Message) (string, error) {
	s.step++
	if s.loop {
		return `{"action":"search","query":"缓存"}`, nil
	}
	switch s.step {
	case 1:
		return `{"action":"search","query":"缓存"}`, nil
	case 2:
		var id string
		for _, v := range m {
			if strings.Contains(v.Content, `"results"`) {
				pos := strings.Index(v.Content, `"id":"`)
				if pos >= 0 {
					id = strings.Split(v.Content[pos+6:], `"`)[0]
				}
			}
		}
		return fmt.Sprintf(`{"action":"read","ids":[%q]}`, id), nil
	default:
		var id string
		for _, v := range m {
			if strings.Contains(v.Content, `"evidence"`) {
				pos := strings.Index(v.Content, `"id":"`)
				if pos >= 0 {
					id = strings.Split(v.Content[pos+6:], `"`)[0]
				}
			}
		}
		if s.bad {
			id = "invented"
		}
		return fmt.Sprintf(`{"action":"final","answer":"这篇介绍缓存。","citations":[%q]}`, id), nil
	}
}
func index(t *testing.T) *knowledge.Index {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("# 缓存\n缓存实验记录。"), 0600)
	k, err := knowledge.Open(root, filepath.Join(t.TempDir(), "i.db"), "test", embed{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.Close() })
	if _, err = k.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return k
}
func TestAgentSearchReadAndCitedAnswer(t *testing.T) {
	a := New(&scripted{}, index(t), 8, nil)
	r, err := a.Query(context.Background(), "找缓存文章")
	if err != nil || len(r.Sources) != 1 || !strings.Contains(r.Sources[0].Text, "实验") {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestAgentRejectsInventedCitation(t *testing.T) {
	a := New(&scripted{bad: true}, index(t), 8, nil)
	if _, err := a.Query(context.Background(), "找缓存"); err == nil {
		t.Fatal("invented citation accepted")
	}
}
func TestAgentStopsAtBudget(t *testing.T) {
	a := New(&scripted{loop: true}, index(t), 2, nil)
	if _, err := a.Query(context.Background(), "找缓存"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("%v", err)
	}
}

type responseFunc func(context.Context, []llm.Message) (string, error)

func (f responseFunc) Chat(c context.Context, m []llm.Message) (string, error) { return f(c, m) }
func TestAgentCannotReadUnsearchedID(t *testing.T) {
	k := index(t)
	hits, _ := k.Search(context.Background(), "缓存", 1)
	a := New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		return fmt.Sprintf(`{"action":"read","ids":[%q]}`, hits[0].ID), nil
	}), k, 8, nil)
	if _, err := a.Query(context.Background(), "读一下"); err == nil {
		t.Fatal("unsearched ID accepted")
	}
}
func TestAgentRejectsChangedSourceBeforeFinal(t *testing.T) {
	k := index(t)
	hits, _ := k.Search(context.Background(), "缓存", 1)
	id := hits[0].ID
	step := 0
	a := New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		step++
		switch step {
		case 1:
			return `{"action":"search","query":"缓存"}`, nil
		case 2:
			return fmt.Sprintf(`{"action":"read","ids":[%q]}`, id), nil
		default:
			os.WriteFile(hits[0].Path, []byte("changed"), 0600)
			return fmt.Sprintf(`{"action":"final","answer":"缓存实验","citations":[%q]}`, id), nil
		}
	}), k, 8, nil)
	if _, err := a.Query(context.Background(), "缓存"); err == nil {
		t.Fatal("changed source was published")
	}
}

func TestAgentRejectsAnswerWithoutCitation(t *testing.T) {
	step := 0
	a := New(responseFunc(func(context.Context, []llm.Message) (string, error) {
		step++
		if step == 1 {
			return `{"action":"search","query":"缓存"}`, nil
		}
		return `{"action":"final","answer":"确定的事实","citations":[]}`, nil
	}), index(t), 8, nil)
	if _, err := a.Query(context.Background(), "缓存"); err == nil {
		t.Fatal("uncited supported answer accepted")
	}
}
