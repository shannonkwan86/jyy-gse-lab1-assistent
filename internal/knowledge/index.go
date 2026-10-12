package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}
type Index struct {
	db                *sql.DB
	root, fingerprint string
	embed             Embedder
	ready             bool
}
type SyncStats struct {
	Documents int `json:"documents"`
	Chunks    int `json:"chunks"`
	Encoded   int `json:"encoded"`
}

const schema = `PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS chunks(id TEXT PRIMARY KEY,path TEXT NOT NULL,title TEXT NOT NULL,heading TEXT NOT NULL,start INTEGER NOT NULL,end INTEGER NOT NULL,version TEXT NOT NULL,text TEXT NOT NULL,keywords TEXT NOT NULL,vector BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS embeddings(key TEXT PRIMARY KEY,vector BLOB NOT NULL);`

func Open(root, dbPath, fingerprint string, e Embedder) (*Index, error) {
	abs, err := articleRoot(root)
	if err != nil {
		return nil, err
	}
	if e == nil || fingerprint == "" {
		return nil, fmt.Errorf("embedding provider and identity required")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if err := prepareDatabase(dbPath); err != nil {
		return nil, err
	}
	// Encode the filesystem path so '?' and '#' cannot become SQLite options.
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Index{db: db, root: abs, fingerprint: abs + "|" + fingerprint + "|" + splitVersion, embed: e}, nil
}

func (k *Index) Close() error { return k.db.Close() }

func validVector(v []float64) error {
	if len(v) == 0 {
		return fmt.Errorf("empty embedding")
	}
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("nonfinite embedding")
		}
		norm += x * x
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return fmt.Errorf("invalid embedding norm")
	}
	return nil
}

func (k *Index) Sync(ctx context.Context) (SyncStats, error) {
	k.ready = false
	stats := SyncStats{}
	docs, err := ScanContext(ctx, k.root)
	if err != nil {
		return stats, err
	}
	stats.Documents = len(docs)
	for _, doc := range docs {
		stats.Chunks += len(doc.Chunks)
	}
	snapshot := k.snapshotID(docs)
	unchanged, err := k.hasSnapshot(ctx, snapshot)
	if err != nil {
		return stats, err
	}
	if unchanged {
		k.ready = true
		return stats, nil
	}
	records, pending, dim, err := k.prepareRecords(ctx, docs)
	if err != nil {
		return stats, err
	}
	stats.Encoded, err = k.encodePending(ctx, records, pending, dim)
	if err != nil {
		return stats, err
	}
	current, err := ScanContext(ctx, k.root)
	if err != nil {
		return stats, err
	}
	if k.snapshotID(current) != snapshot {
		return stats, fmt.Errorf("articles changed during synchronization; retry")
	}
	if err := k.publish(ctx, records, snapshot); err != nil {
		return stats, err
	}
	k.ready = true
	return stats, nil
}

func (k *Index) snapshotID(docs []Document) string {
	parts := []string{k.fingerprint}
	for _, doc := range docs {
		parts = append(parts, doc.Path, doc.Version)
	}
	// Strings cannot fail JSON encoding; encoding preserves path boundaries.
	data, _ := json.Marshal(parts)
	return digest(string(data))
}

func (k *Index) hasSnapshot(ctx context.Context, snapshot string) (bool, error) {
	var current string
	err := k.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='snapshot'").Scan(&current)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current == snapshot, ctx.Err()
}
