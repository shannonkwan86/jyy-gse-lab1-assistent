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
	ctx := context.Background()
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	p := filepath.Join(root, "a.md")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("---\ntitle: 缓存实验\n---\n\n## 转置\n矩阵转置减少缓存未命中。\n")
	e := &encoder{}
	k, err := Open(root, db, "test-model", e)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	n := e.calls
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if e.calls != n {
		t.Fatal("unchanged content re-encoded")
	}
	hits, err := k.Search(ctx, "矩阵", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search %v %v", hits, err)
	}
	c, err := k.Read(hits[0].ID)
	if err != nil || c.Start != 5 || !strings.Contains(c.Text, "矩阵") {
		t.Fatalf("source %+v %v", c, err)
	}
	old := c.ID
	write("---\ntitle: 新标题\n---\n新的内容\n")
	e.fail = true
	if _, err = k.Sync(ctx); err == nil {
		t.Fatal("expected failed sync")
	}
	if _, err = k.Read(old); err == nil {
		t.Fatal("stale evidence accepted")
	}
	e.fail = false
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = k.Read(old); err == nil {
		t.Fatal("removed chunk accepted")
	}
	os.Remove(p)
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	hits, err = k.Search(ctx, "矩阵", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted source returned: %v %v", hits, err)
	}
}
func TestScanSkipsDraftEncryptedAndSymlinks(t *testing.T) {
	root := t.TempDir()
	for n, s := range map[string]string{"ok.md": "---\ntitle: 正文\n---\nhello", "draft.md": "---\ndraft: true\n---\nsecret", "private.md": "---\nencrypted: true\n---\nsecret"} {
		os.WriteFile(filepath.Join(root, n), []byte(s), 0600)
	}
	os.Symlink(filepath.Join(root, "ok.md"), filepath.Join(root, "link.md"))
	docs, err := Scan(root)
	if err != nil || len(docs) != 1 || docs[0].Title != "正文" {
		t.Fatalf("%+v %v", docs, err)
	}
}
func TestSyncFailureDoesNotPublishPartialIndex(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	os.WriteFile(p, []byte("旧内容"), 0600)
	e := &encoder{}
	k, err := Open(root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	k.Sync(context.Background())
	var before int
	k.db.QueryRow("SELECT count(*) FROM chunks").Scan(&before)
	os.WriteFile(p, []byte("新内容"), 0600)
	e.fail = true
	k.Sync(context.Background())
	var text string
	k.db.QueryRow("SELECT text FROM chunks LIMIT 1").Scan(&text)
	if text != "旧内容" || before != 1 {
		t.Fatalf("partial publish: %q", text)
	}
}

func TestReadRejectsReplacementSymlink(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	os.WriteFile(p, []byte("矩阵"), 0600)
	k, err := Open(root, filepath.Join(t.TempDir(), "i.db"), "test", &encoder{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	k.Sync(context.Background())
	hits, _ := k.Search(context.Background(), "矩阵", 1)
	external := filepath.Join(t.TempDir(), "external.md")
	os.WriteFile(external, []byte("矩阵"), 0600)
	os.Remove(p)
	os.Symlink(external, p)
	if _, err = k.Read(hits[0].ID); err == nil {
		t.Fatal("replacement symlink escaped corpus")
	}
}
func TestSemanticRetrievalWithoutSharedKeywords(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("矩阵转置"), 0600)
	os.WriteFile(filepath.Join(root, "b.md"), []byte("旅行记录"), 0600)
	k, err := Open(root, filepath.Join(t.TempDir(), "i.db"), "test", semanticEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	k.Sync(context.Background())
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
	os.WriteFile(filepath.Join(root, "a.md"), []byte("矩阵"), 0600)
	db := filepath.Join(t.TempDir(), "i.db")
	ctx := context.Background()
	e := &encoder{}
	k, err := Open(root, db, "v1", e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	k.Close()
	e = &encoder{}
	k, err = Open(root, db, "v1", e)
	if err != nil {
		t.Fatal(err)
	}
	k.Sync(ctx)
	if e.calls != 0 {
		t.Fatal("cache not persisted")
	}
	k.Close()
	e = &encoder{}
	k, err = Open(root, db, "v2", e)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if _, err = k.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if e.calls != 1 {
		t.Fatal("changed model reused old embedding")
	}
}
func TestFailedSyncDisablesSearchAndCancellationStopsSync(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.md")
	os.WriteFile(p, []byte("矩阵"), 0600)
	e := &encoder{}
	k, err := Open(root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	k.Sync(context.Background())
	os.WriteFile(p, []byte("changed"), 0600)
	e.fail = true
	if _, err = k.Sync(context.Background()); err == nil {
		t.Fatal("sync should fail")
	}
	if _, err = k.Search(context.Background(), "矩阵", 1); err == nil {
		t.Fatal("search used stale snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = k.Sync(ctx); err == nil {
		t.Fatal("canceled synchronization continued")
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
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.md", i)), []byte(fmt.Sprintf("文章 %d", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	k, err := Open(root, filepath.Join(t.TempDir(), "i.db"), "bailian", limitedEncoder{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	stats, err := k.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chunks != 23 || stats.Encoded != 23 {
		t.Fatalf("incomplete index: %+v", stats)
	}
}
