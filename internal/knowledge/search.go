package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func digest(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

// Chinese unigrams and bigrams avoid relying on whitespace tokenization.
func terms(s string) []string {
	set := map[string]bool{}
	var word []rune
	flush := func() {
		if len(word) > 0 {
			set[string(word)] = true
			word = nil
		}
	}
	var last rune
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Han, r) {
			flush()
			set[string(r)] = true
			if last != 0 {
				set[string([]rune{last, r})] = true
			}
			last = r
		} else {
			last = 0
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				word = append(word, r)
			} else {
				flush()
			}
		}
	}
	flush()
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

type Hit struct {
	Chunk
	Score float64 `json:"score"`
}

func (k *Index) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if !k.ready {
		return nil, fmt.Errorf("index not synchronized")
	}
	if strings.TrimSpace(query) == "" || limit < 1 || limit > 10 {
		return nil, fmt.Errorf("invalid search query or limit")
	}
	rows, err := k.db.QueryContext(ctx, "SELECT id,path,title,heading,start,end,version,text,keywords,vector FROM chunks ORDER BY id")
	if err != nil {
		return nil, err
	}
	type candidate struct {
		hit           Hit
		lex, semantic float64
		vector        []float64
	}
	var cs []candidate
	qt := terms(query)
	for rows.Next() {
		var c candidate
		var keywords string
		var data []byte
		h := &c.hit.Chunk
		if err = rows.Scan(&h.ID, &h.Path, &h.Title, &h.Heading, &h.Start, &h.End, &h.Version, &h.Text, &keywords, &data); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(data, &c.vector); err != nil {
			rows.Close()
			return nil, err
		}
		set := map[string]bool{}
		for _, t := range strings.Fields(keywords) {
			set[t] = true
		}
		for _, t := range qt {
			if set[t] {
				c.lex++
			}
			if strings.Contains(strings.ToLower(h.Title+" "+h.Heading), t) {
				c.lex += 2
			}
		}
		cs = append(cs, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return []Hit{}, nil
	}
	vectors, err := k.embed.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("query embedding count mismatch")
	}
	q := vectors[0]
	if err = validVector(q); err != nil {
		return nil, err
	}
	for i := range cs {
		v := cs[i].vector
		if len(v) != len(q) {
			return nil, fmt.Errorf("query embedding dimension mismatch")
		}
		dot, a, b := 0.0, 0.0, 0.0
		for j, x := range v {
			dot += x * q[j]
			a += x * x
			b += q[j] * q[j]
		}
		cs[i].semantic = dot / math.Sqrt(a*b)
	}
	ranks := make([]int, len(cs))
	for i := range ranks {
		ranks[i] = i
	}
	sort.SliceStable(ranks, func(i, j int) bool { return cs[ranks[i]].semantic > cs[ranks[j]].semantic })
	for rank, i := range ranks {
		cs[i].hit.Score += 1 / float64(60+rank+1)
	}
	sort.SliceStable(ranks, func(i, j int) bool { return cs[ranks[i]].lex > cs[ranks[j]].lex })
	rank := 0
	for _, i := range ranks {
		if cs[i].lex > 0 {
			rank++
			cs[i].hit.Score += 1 / float64(60+rank)
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].hit.Score > cs[j].hit.Score })
	if len(cs) > limit {
		cs = cs[:limit]
	}
	out := make([]Hit, len(cs))
	for i, c := range cs {
		out[i] = c.hit
	}
	return out, nil
}
func (k *Index) Read(id string) (Chunk, error) {
	var c Chunk
	err := k.db.QueryRow("SELECT id,path,title,heading,start,end,version,text FROM chunks WHERE id=?", id).Scan(&c.ID, &c.Path, &c.Title, &c.Heading, &c.Start, &c.End, &c.Version, &c.Text)
	if err != nil {
		return c, err
	}
	return c, k.Validate(c)
}
func (k *Index) Validate(c Chunk) error {
	rel, err := filepath.Rel(k.root, c.Path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("source outside root")
	}
	resolved, err := filepath.EvalSymlinks(c.Path)
	if err != nil || resolved != c.Path {
		return fmt.Errorf("source path changed or became a symbolic link")
	}
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		return fmt.Errorf("source unavailable: %w", err)
	}
	if digest(string(raw)) != c.Version {
		return fmt.Errorf("source changed: %s; repeat query", c.Path)
	}
	return nil
}
