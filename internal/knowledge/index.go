package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

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

func Open(root, dbPath, fingerprint string, e Embedder) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if e == nil || fingerprint == "" {
		return nil, fmt.Errorf("embedding provider and identity required")
	}
	if err = os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL); CREATE TABLE IF NOT EXISTS chunks(id TEXT PRIMARY KEY,path TEXT NOT NULL,title TEXT NOT NULL,heading TEXT NOT NULL,start INTEGER NOT NULL,end INTEGER NOT NULL,version TEXT NOT NULL,text TEXT NOT NULL,keywords TEXT NOT NULL,vector BLOB NOT NULL); CREATE TABLE IF NOT EXISTS embeddings(key TEXT PRIMARY KEY,vector BLOB NOT NULL);`)
	if err != nil {
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
	docs, err := Scan(k.root)
	if err != nil {
		return stats, err
	}
	stats.Documents = len(docs)
	type record struct {
		c          Chunk
		vector     []byte
		key, input string
	}
	var records []record
	var pending []int
	dim := 0
	check := func(v []float64) error {
		if err := validVector(v); err != nil {
			return err
		}
		if dim != 0 && len(v) != dim {
			return fmt.Errorf("embedding dimensions changed")
		}
		dim = len(v)
		return nil
	}
	for _, doc := range docs {
		for _, c := range doc.Chunks {
			if err = ctx.Err(); err != nil {
				return stats, err
			}
			input := c.Title + "\n" + c.Heading + "\n" + c.Text
			key := digest(k.fingerprint + "\n" + input)
			r := record{c: c, key: key, input: input}
			err = k.db.QueryRowContext(ctx, "SELECT vector FROM embeddings WHERE key=?", key).Scan(&r.vector)
			if err == sql.ErrNoRows {
				pending = append(pending, len(records))
			} else if err != nil {
				return stats, err
			} else {
				var v []float64
				if err = json.Unmarshal(r.vector, &v); err != nil {
					return stats, err
				}
				if err = check(v); err != nil {
					return stats, err
				}
			}
			records = append(records, r)
		}
	}
	for start := 0; start < len(pending); start += 10 {
		end := start + 10
		if end > len(pending) {
			end = len(pending)
		}
		texts := make([]string, end-start)
		for j, i := range pending[start:end] {
			texts[j] = records[i].input
		}
		vectors, e := k.embed.Embed(ctx, texts)
		if e != nil {
			return stats, fmt.Errorf("index embedding: %w", e)
		}
		if len(vectors) != len(texts) {
			return stats, fmt.Errorf("embedding count mismatch")
		}
		for _, v := range vectors {
			if e = check(v); e != nil {
				return stats, e
			}
		}
		for j, i := range pending[start:end] {
			encoded, _ := json.Marshal(vectors[j])
			records[i].vector = encoded
			if _, e = k.db.ExecContext(ctx, "INSERT OR REPLACE INTO embeddings VALUES(?,?)", records[i].key, encoded); e != nil {
				return stats, e
			}
			stats.Encoded++
		}
	}
	current, err := Scan(k.root)
	if err != nil {
		return stats, err
	}
	if !sameDocuments(docs, current) {
		return stats, fmt.Errorf("articles changed during synchronization; retry")
	}
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM chunks"); err != nil {
		return stats, err
	}
	for _, r := range records {
		c := r.c
		_, err = tx.ExecContext(ctx, "INSERT INTO chunks VALUES(?,?,?,?,?,?,?,?,?,?)", c.ID, c.Path, c.Title, c.Heading, c.Start, c.End, c.Version, c.Text, strings.Join(terms(c.Title+" "+c.Heading+" "+c.Text), " "), r.vector)
		if err != nil {
			return stats, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO metadata VALUES('fingerprint',?)", k.fingerprint); err != nil {
		return stats, err
	}
	if err = tx.Commit(); err != nil {
		return stats, err
	}
	stats.Chunks = len(records)
	k.ready = true
	return stats, nil
}
func sameDocuments(a, b []Document) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Version != b[i].Version {
			return false
		}
	}
	return true
}
