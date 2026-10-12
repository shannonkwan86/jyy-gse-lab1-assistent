package knowledge

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Chunk struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Heading string `json:"heading"`
	Start   int    `json:"line_start"`
	End     int    `json:"line_end"`
	Version string `json:"version"`
	Text    string `json:"text"`
}
type Document struct {
	Path, Title, Version string
	Chunks               []Chunk
}

type frontMatter struct {
	Title     string `yaml:"title"`
	Draft     bool   `yaml:"draft"`
	Encrypted bool   `yaml:"encrypted"`
}

const splitVersion = "section-v1-1200"

// Scan is the offline convenience entry point. Queries use ScanContext.
func Scan(root string) ([]Document, error) {
	return ScanContext(context.Background(), root)
}

// ScanContext skips symbolic links, nonregular files and hidden directories.
func ScanContext(ctx context.Context, root string) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := articleRoot(root)
	if err != nil {
		return nil, err
	}
	var docs []Document
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		doc, err := parseDocument(ctx, path)
		if err == nil && doc != nil {
			docs = append(docs, *doc)
		}
		return err
	})
	if err == nil {
		err = ctx.Err()
	}
	return docs, err
}

func parseDocument(ctx context.Context, path string) (*Document, error) {
	raw, err := readSource(ctx, path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8: %s", path)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	meta, start, err := parseFrontMatter(lines)
	if err != nil {
		return nil, fmt.Errorf("front matter %s: %w", path, err)
	}
	if meta.Draft || meta.Encrypted {
		return nil, nil
	}
	if meta.Title == "" {
		meta.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	doc := Document{Path: path, Title: meta.Title, Version: digest(string(raw))}
	if err := splitDocument(ctx, &doc, lines, start); err != nil {
		return nil, err
	}
	return &doc, nil
}

func parseFrontMatter(lines []string) (frontMatter, int, error) {
	var meta frontMatter
	if len(lines) == 0 || strings.TrimPrefix(lines[0], "\ufeff") != "---" {
		return meta, 0, nil
	}
	end := 1
	for end < len(lines) && strings.TrimSpace(lines[end]) != "---" {
		end++
	}
	if end == len(lines) {
		return meta, 0, fmt.Errorf("unclosed front matter")
	}
	err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta)
	return meta, end + 1, err
}

func splitDocument(ctx context.Context, doc *Document, lines []string, start int) error {
	heading, fence := "", ""
	from, size := -1, 0
	flush := func(end int) {
		if from >= 0 {
			appendChunk(doc, heading, lines, from, end)
		}
		from, size = -1, 0
	}
	for i := start; i < len(lines); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := lines[i]
		trim := strings.TrimSpace(line)
		if fence == "" && strings.HasPrefix(trim, "#") {
			flush(i)
			heading = strings.TrimSpace(strings.TrimLeft(trim, "#"))
		}
		if from < 0 && trim != "" {
			from = i
		}
		size += utf8.RuneCountInString(line)
		fence = nextFence(fence, trim)
		if from >= 0 && ((trim == "" && fence == "" && size >= 600) || size >= 1200) {
			flush(i + 1)
		}
	}
	flush(len(lines))
	return ctx.Err()
}

func appendChunk(doc *Document, heading string, lines []string, from, end int) {
	text := strings.TrimSpace(strings.Join(lines[from:end], "\n"))
	if text == "" {
		return
	}
	id := digest(doc.Path + doc.Version + fmt.Sprint(from))
	doc.Chunks = append(doc.Chunks, Chunk{ID: id, Path: doc.Path, Title: doc.Title,
		Heading: heading, Start: from + 1, End: end, Version: doc.Version, Text: text})
}

func nextFence(fence, line string) string {
	if !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
		return fence
	}
	mark := line[:3]
	if fence == "" {
		return mark
	}
	if fence == mark {
		return ""
	}
	return fence
}
