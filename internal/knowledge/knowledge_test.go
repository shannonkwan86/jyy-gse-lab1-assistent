package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type encoder struct {
	calls int
	fail  bool
}

func (e *encoder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	e.calls += len(texts)
	if e.fail {
		return nil, errors.New("offline")
	}
	v := make([][]float64, len(texts))
	for i, s := range texts {
		if strings.Contains(s, "矩阵") {
			v[i] = []float64{1, 0}
		} else {
			v[i] = []float64{0, 1}
		}
	}
	return v, nil
}

func TestSyncCacheUpdateDeleteAndFailure(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	writeArticle(t, p, "---\ntitle: 缓存实验\n---\n\n## 转置\n矩阵转置减少缓存未命中。\n")
	e := &encoder{}
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "index.db"), "test-model", e)
	syncIndex(t, k)
	n := e.calls
	syncIndex(t, k)
	if e.calls != n {
		t.Fatal("unchanged content re-encoded")
	}
	hits := searchHits(t, k, "矩阵")
	c, err := k.Read(hits[0].ID)
	if err != nil || c.Start != 5 || !strings.Contains(c.Text, "矩阵") {
		t.Fatalf("source %+v %v", c, err)
	}
	writeArticle(t, p, "---\ntitle: 新标题\n---\n新的内容\n")
	e.fail = true
	if _, err := k.Sync(context.Background()); err == nil {
		t.Fatal("expected failed sync")
	}
	if _, err := k.Read(c.ID); err == nil {
		t.Fatal("stale evidence accepted")
	}
	e.fail = false
	syncIndex(t, k)
	if _, err := k.Read(c.ID); err == nil {
		t.Fatal("removed chunk accepted")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	syncIndex(t, k)
	hits, err = k.Search(context.Background(), "矩阵", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted source returned: %v %v", hits, err)
	}
}

func searchHits(t *testing.T, k *Index, query string) []Hit {
	t.Helper()
	hits, err := k.Search(context.Background(), query, 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search: %v %v", hits, err)
	}
	return hits
}

func TestScanSkipsDraftEncryptedAndSymlinks(t *testing.T) {
	root := t.TempDir()
	for n, s := range map[string]string{"ok.md": "---\ntitle: 正文\n---\nhello", "draft.md": "---\ndraft: true\n---\nsecret", "private.md": "---\nencrypted: true\n---\nsecret"} {
		writeArticle(t, filepath.Join(root, n), s)
	}
	if err := os.Symlink(filepath.Join(root, "ok.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	docs, err := Scan(root)
	if err != nil || len(docs) != 1 || docs[0].Title != "正文" {
		t.Fatalf("%+v %v", docs, err)
	}
}

func TestSyncFailureDoesNotPublishPartialIndex(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	writeArticle(t, p, "旧内容")
	e := &encoder{}
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	syncIndex(t, k)
	writeArticle(t, p, "新内容")
	e.fail = true
	if _, err := k.Sync(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	var text string
	if err := k.db.QueryRow("SELECT text FROM chunks LIMIT 1").Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "旧内容" {
		t.Fatalf("partial publish: %q", text)
	}
}

func TestReadRejectsReplacementSymlink(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	writeArticle(t, p, "矩阵")
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "test", &encoder{})
	syncIndex(t, k)
	hits := searchHits(t, k, "矩阵")
	external := filepath.Join(t.TempDir(), "external.md")
	writeArticle(t, external, "矩阵")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, p); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Read(hits[0].ID); err == nil {
		t.Fatal("replacement symlink escaped corpus")
	}
}

func TestSemanticRetrievalWithoutSharedKeywords(t *testing.T) {
	root := t.TempDir()
	writeArticle(t, filepath.Join(root, "a.md"), "矩阵转置")
	writeArticle(t, filepath.Join(root, "b.md"), "旅行记录")
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "test", semanticEncoder{})
	syncIndex(t, k)
	hits, err := k.Search(context.Background(), "改善内存局部性", 1)
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Text, "矩阵") {
		t.Fatalf("%+v %v", hits, err)
	}
}

type semanticEncoder struct{}

func (semanticEncoder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	v := make([][]float64, len(texts))
	for i, s := range texts {
		if strings.Contains(s, "矩阵") || s == "改善内存局部性" {
			v[i] = []float64{1, 0}
		} else {
			v[i] = []float64{0, 1}
		}
	}
	return v, nil
}

func TestReopenReusesCacheAndModelChangeReencodes(t *testing.T) {
	root := t.TempDir()
	writeArticle(t, filepath.Join(root, "a.md"), "矩阵")
	db := filepath.Join(t.TempDir(), "i.db")
	for i, model := range []string{"v1", "v1", "v2"} {
		e := &encoder{}
		k := openTestIndex(t, root, db, model, e)
		syncIndex(t, k)
		want := 1
		if i == 1 {
			want = 0
		}
		if e.calls != want {
			t.Fatalf("iteration %d: encoded %d, want %d", i, e.calls, want)
		}
		if err := k.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFailedSyncDisablesSearchAndCancellationStopsSync(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	writeArticle(t, p, "矩阵")
	e := &encoder{}
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	syncIndex(t, k)
	writeArticle(t, p, "changed")
	e.fail = true
	if _, err := k.Sync(context.Background()); err == nil {
		t.Fatal("sync should fail")
	}
	if _, err := k.Search(context.Background(), "矩阵", 1); err == nil {
		t.Fatal("search used stale snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := k.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled synchronization: %v", err)
	}
}

type limitedEncoder struct{}

func (limitedEncoder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 10 {
		return nil, errors.New("provider allows at most 10 inputs")
	}
	v := make([][]float64, len(texts))
	for i := range v {
		v[i] = []float64{1, 0}
	}
	return v, nil
}

func TestSyncHonorsProviderTenInputLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 23; i++ {
		writeArticle(t, filepath.Join(root, fmt.Sprintf("%d.md", i)), fmt.Sprintf("文章 %d", i))
	}
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "bailian", limitedEncoder{})
	stats := syncIndex(t, k)
	if stats.Chunks != 23 || stats.Encoded != 23 {
		t.Fatalf("incomplete index: %+v", stats)
	}
}
