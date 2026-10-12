package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeArticle(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func openTestIndex(t *testing.T, root, dbPath, model string, e Embedder) *Index {
	t.Helper()
	k, err := Open(root, dbPath, model, e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k.Close(); err != nil {
			t.Error(err)
		}
	})
	return k
}

func syncIndex(t *testing.T, k *Index) SyncStats {
	t.Helper()
	stats, err := k.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func totalChanges(t *testing.T, k *Index) int {
	t.Helper()
	var changes int
	if err := k.db.QueryRow("SELECT total_changes()").Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func TestUnchangedSyncDoesNotWrite(t *testing.T) {
	root, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "index.db")
	writeArticle(t, filepath.Join(root, "a.md"), "矩阵")
	e := &encoder{}
	k := openTestIndex(t, root, dbPath, "v1", e)
	syncIndex(t, k)
	before, calls := totalChanges(t, k), e.calls
	stats := syncIndex(t, k)
	if totalChanges(t, k) != before || e.calls != calls || stats.Chunks != 1 || stats.Encoded != 0 {
		t.Fatalf("unchanged snapshot was rebuilt: %+v", stats)
	}
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
	k = openTestIndex(t, root, dbPath, "v1", e)
	before = totalChanges(t, k)
	syncIndex(t, k)
	if totalChanges(t, k) != before {
		t.Fatal("reopened unchanged snapshot was rewritten")
	}
}

func TestIndexFilePermissions(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			root, parent := t.TempDir(), t.TempDir()
			if err := os.Chmod(parent, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "index.db")
			if existing {
				writeArticle(t, path, "")
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			openTestIndex(t, root, path, "v1", &encoder{})
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("database permissions: %v %v", info, err)
			}
		})
	}
}

func TestIndexRejectsSymlinkDatabase(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "other.db")
	writeArticle(t, outside, "")
	path := filepath.Join(t.TempDir(), "index.db")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	k, err := Open(t.TempDir(), path, "v1", &encoder{})
	if err == nil {
		k.Close()
		t.Fatal("symlink database accepted")
	}
}

func TestIndexAuxiliaryFilePermissions(t *testing.T) {
	root, parent := t.TempDir(), t.TempDir()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "index.db")
	k := openTestIndex(t, root, path, "v1", &encoder{})
	if _, err := k.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	syncIndex(t, k)
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s permissions: %v %v", suffix, info, err)
		}
	}
}

func TestEmptySnapshotAndCanceledSync(t *testing.T) {
	k := openTestIndex(t, t.TempDir(), filepath.Join(t.TempDir(), "i.db"), "v1", &encoder{})
	stats := syncIndex(t, k)
	if stats.Documents != 0 || stats.Chunks != 0 {
		t.Fatalf("empty snapshot: %+v", stats)
	}
	before := totalChanges(t, k)
	syncIndex(t, k)
	if totalChanges(t, k) != before {
		t.Fatal("empty snapshot was rewritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := k.Sync(ctx); err == nil {
		t.Fatal("canceled empty sync succeeded")
	}
	if _, err := k.Search(context.Background(), "矩阵", 1); err == nil {
		t.Fatal("failed sync left index searchable")
	}
	syncIndex(t, k)
	hits, err := k.Search(context.Background(), "矩阵", 1)
	if err != nil || len(hits) != 0 {
		t.Fatalf("recovery: %v %v", hits, err)
	}
}

type flakyEncoder struct {
	calls  int
	failAt int
}

func (e *flakyEncoder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	e.calls++
	if e.calls == e.failAt {
		return nil, fmt.Errorf("temporary provider failure")
	}
	return limitedEncoder{}.Embed(ctx, texts)
}

func TestSyncRetryReusesSuccessfulBatches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 23; i++ {
		writeArticle(t, filepath.Join(root, fmt.Sprintf("%02d.md", i)), fmt.Sprintf("文章 %d", i))
	}
	e := &flakyEncoder{failAt: 2}
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	stats, err := k.Sync(context.Background())
	if err == nil || stats.Encoded != 10 {
		t.Fatalf("first synchronization: %+v %v", stats, err)
	}
	if _, err := k.Search(context.Background(), "文章", 1); err == nil {
		t.Fatal("failed snapshot was searchable")
	}
	stats = syncIndex(t, k)
	if stats.Encoded != 13 || stats.Chunks != 23 {
		t.Fatalf("retry did not reuse successful batch: %+v", stats)
	}
}

type embeddingFunc func(context.Context, []string) ([][]float64, error)

func (f embeddingFunc) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	return f(ctx, texts)
}

func TestSyncRejectsChangesDuringEncoding(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.md")
	writeArticle(t, path, "原始正文")
	e := embeddingFunc(func(ctx context.Context, texts []string) ([][]float64, error) {
		writeArticle(t, path, "同步中已变化")
		return limitedEncoder{}.Embed(ctx, texts)
	})
	k := openTestIndex(t, root, filepath.Join(t.TempDir(), "i.db"), "v1", e)
	if _, err := k.Sync(context.Background()); err == nil {
		t.Fatal("changed snapshot published")
	}
	var count int
	if err := k.db.QueryRow("SELECT count(*) FROM chunks").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial snapshot survived")
	}
}
