package knowledge

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
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

const splitVersion = "section-v1-1200"

// Scan never follows symbolic links or expands Markdown links/images.
func Scan(root string) ([]Document, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("article root must be a real directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	var docs []Document
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(raw) {
			return fmt.Errorf("invalid UTF-8: %s", path)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		start := 0
		meta := struct {
			Title     string `yaml:"title"`
			Draft     bool   `yaml:"draft"`
			Encrypted bool   `yaml:"encrypted"`
		}{}
		if len(lines) > 0 && strings.TrimPrefix(lines[0], "\ufeff") == "---" {
			end := 1
			for end < len(lines) && strings.TrimSpace(lines[end]) != "---" {
				end++
			}
			if end == len(lines) {
				return fmt.Errorf("unclosed front matter: %s", path)
			}
			if err = yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
				return fmt.Errorf("front matter %s: %w", path, err)
			}
			start = end + 1
		}
		if meta.Draft || meta.Encrypted {
			return nil
		}
		title := meta.Title
		if title == "" {
			title = strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		}
		version := fmt.Sprintf("%x", sha256.Sum256(raw))
		doc := Document{Path: path, Title: title, Version: version}
		heading := ""
		from := -1
		size := 0
		fence := ""
		flush := func(end int) {
			if from < 0 {
				return
			}
			text := strings.TrimSpace(strings.Join(lines[from:end], "\n"))
			if text != "" {
				id := fmt.Sprintf("%x", sha256.Sum256([]byte(path+version+fmt.Sprint(from))))
				doc.Chunks = append(doc.Chunks, Chunk{ID: id, Path: path, Title: title, Heading: heading, Start: from + 1, End: end, Version: version, Text: text})
			}
			from = -1
			size = 0
		}
		for i := start; i < len(lines); i++ {
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
			if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
				mark := trim[:3]
				if fence == "" {
					fence = mark
				} else if fence == mark {
					fence = ""
				}
			}
			if from >= 0 && ((trim == "" && fence == "" && size >= 600) || size >= 1200) {
				flush(i + 1)
			}
		}
		flush(len(lines))
		docs = append(docs, doc)
		return nil
	})
	return docs, err
}
