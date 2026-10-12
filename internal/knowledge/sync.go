package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type indexRecord struct {
	chunk      Chunk
	vector     []byte
	key, input string
}

func (k *Index) prepareRecords(ctx context.Context, docs []Document) ([]indexRecord, []int, int, error) {
	var records []indexRecord
	for _, doc := range docs {
		for _, c := range doc.Chunks {
			if err := ctx.Err(); err != nil {
				return nil, nil, 0, err
			}
			input := c.Title + "\n" + c.Heading + "\n" + c.Text
			records = append(records, indexRecord{chunk: c, input: input, key: digest(k.fingerprint + "\n" + input)})
		}
	}
	cached, err := k.cachedVectors(ctx, records)
	if err != nil {
		return nil, nil, 0, err
	}
	var pending []int
	dim := 0
	for i := range records {
		r := &records[i]
		data, ok := cached[r.key]
		if !ok {
			pending = append(pending, i)
			continue
		}
		var v []float64
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, nil, 0, err
		}
		if err := checkDimensions(v, &dim); err != nil {
			return nil, nil, 0, err
		}
		r.vector = data
	}
	return records, pending, dim, nil
}

func (k *Index) cachedVectors(ctx context.Context, records []indexRecord) (map[string][]byte, error) {
	cached := map[string][]byte{}
	for start := 0; start < len(records); start += 100 {
		end := min(start+100, len(records))
		keys := make([]any, 0, end-start)
		for _, r := range records[start:end] {
			keys = append(keys, r.key)
		}
		if err := k.cachedBatch(ctx, keys, cached); err != nil {
			return nil, err
		}
	}
	return cached, nil
}

func (k *Index) cachedBatch(ctx context.Context, keys []any, cached map[string][]byte) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := k.db.QueryContext(ctx, "SELECT key,vector FROM embeddings WHERE key IN ("+placeholders+")", keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var vector []byte
		if err := rows.Scan(&key, &vector); err != nil {
			return err
		}
		cached[key] = vector
	}
	return rows.Err()
}

func checkDimensions(vector []float64, dim *int) error {
	if err := validVector(vector); err != nil {
		return err
	}
	if *dim != 0 && len(vector) != *dim {
		return fmt.Errorf("embedding dimensions changed")
	}
	*dim = len(vector)
	return nil
}

func (k *Index) encodePending(ctx context.Context, records []indexRecord, pending []int, dim int) (int, error) {
	encoded := 0
	for start := 0; start < len(pending); start += 10 {
		batch := pending[start:min(start+10, len(pending))]
		texts := make([]string, len(batch))
		for j, i := range batch {
			texts[j] = records[i].input
		}
		vectors, err := k.embed.Embed(ctx, texts)
		if err != nil {
			return encoded, fmt.Errorf("index embedding: %w", err)
		}
		if len(vectors) != len(texts) {
			return encoded, fmt.Errorf("embedding count mismatch")
		}
		for _, v := range vectors {
			if err := checkDimensions(v, &dim); err != nil {
				return encoded, err
			}
		}
		for j, i := range batch {
			data, err := json.Marshal(vectors[j])
			if err != nil {
				return encoded, err
			}
			records[i].vector = data
			if _, err := k.db.ExecContext(ctx, "INSERT OR REPLACE INTO embeddings(key,vector) VALUES(?,?)", records[i].key, data); err != nil {
				return encoded, err
			}
			encoded++
		}
	}
	return encoded, nil
}

func (k *Index) publish(ctx context.Context, records []indexRecord, snapshot string) error {
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks"); err != nil {
		return err
	}
	for _, r := range records {
		c := r.chunk
		_, err := tx.ExecContext(ctx, `INSERT INTO chunks(id,path,title,heading,start,end,version,text,keywords,vector)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, c.ID, c.Path, c.Title, c.Heading, c.Start, c.End, c.Version,
			c.Text, strings.Join(terms(c.Title+" "+c.Heading+" "+c.Text), " "), r.vector)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO metadata(key,value) VALUES('fingerprint',?),('snapshot',?)", k.fingerprint, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}
