package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileConfig struct {
	ArticlesDir    string `json:"articles_dir"`
	IndexDB        string `json:"index_db"`
	Chat           Config `json:"chat"`
	Embedding      Config `json:"embedding"`
	MaxSteps       int    `json:"max_steps"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// LoadConfig does not open the corpus or send data to any service.
// Relative paths are relative to the configuration file, not the shell cwd.
func LoadConfig(path string) (FileConfig, error) {
	var c FileConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if c.ArticlesDir == "" || c.IndexDB == "" {
		return c, fmt.Errorf("articles_dir and index_db are required")
	}
	for name, p := range map[string]*Config{"chat": &c.Chat, "embedding": &c.Embedding} {
		if p.BaseURL == "" || p.Model == "" {
			return c, fmt.Errorf("%s base_url and model are required", name)
		}
		if p.APIKeyEnv == "" && p.APIKey == "" {
			return c, fmt.Errorf("%s requires api_key_env or a local api_key", name)
		}
		if p.APIKeyEnv != "" {
			p.APIKey = os.Getenv(p.APIKeyEnv)
		}
		if p.APIKey == "" {
			return c, fmt.Errorf("%s API key is missing; configure local api_key or %s", name, p.APIKeyEnv)
		}
	}
	if c.MaxSteps == 0 {
		c.MaxSteps = 8
	}
	if c.MaxSteps < 1 || c.MaxSteps > 20 {
		return c, fmt.Errorf("max_steps must be 1..20")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 180
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
		return c, fmt.Errorf("timeout_seconds must be 1..3600")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	for _, p := range []*string{&c.ArticlesDir, &c.IndexDB} {
		if !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	return c, nil
}
