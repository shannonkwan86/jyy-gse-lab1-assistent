// Package llm adapts Chat Completions and Embeddings compatible HTTP services.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
	APIKey    string `json:"api_key,omitempty"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Usage struct {
	Calls            int `json:"calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReportedCalls    int `json:"reported_calls"`
}
type Client struct {
	config Config
	http   *http.Client
	mu     sync.Mutex
	usage  Usage
}

func New(c Config) *Client {
	return &Client{config: c, http: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Usage() Usage { c.mu.Lock(); defer c.mu.Unlock(); return c.usage }
func (c *Client) request(ctx context.Context, path string, payload any, out any) error {
	base, err := url.Parse(c.config.BaseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return fmt.Errorf("invalid model base URL")
	}
	if base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1")) {
		return fmt.Errorf("model URL requires HTTPS (except loopback)")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.config.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	c.mu.Lock()
	c.usage.Calls++
	c.mu.Unlock()
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("model request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("model service returned HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 8*1024*1024 {
		return fmt.Errorf("model response exceeds limit")
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid model JSON response")
	}
	var u struct {
		Usage *struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &u) == nil && u.Usage != nil {
		c.mu.Lock()
		c.usage.ReportedCalls++
		c.usage.PromptTokens += u.Usage.Prompt
		c.usage.CompletionTokens += u.Usage.Completion
		c.mu.Unlock()
	}
	return nil
}
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	var out struct {
		Choices []struct {
			Finish  string  `json:"finish_reason"`
			Message Message `json:"message"`
		} `json:"choices"`
	}
	err := c.request(ctx, "/chat/completions", map[string]any{"model": c.config.Model, "messages": messages, "max_tokens": 2500, "response_format": map[string]string{"type": "json_object"}}, &out)
	if err != nil {
		return "", err
	}
	if len(out.Choices) != 1 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("missing model answer")
	}
	if out.Choices[0].Finish != "stop" {
		return "", fmt.Errorf("model answer not complete (%s)", out.Choices[0].Finish)
	}
	return out.Choices[0].Message.Content, nil
}
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	err := c.request(ctx, "/embeddings", map[string]any{"model": c.config.Model, "input": texts, "encoding_format": "float"}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch")
	}
	vectors := make([][]float64, len(texts))
	dimension := 0
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) || vectors[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, fmt.Errorf("invalid embedding index or dimension")
		}
		norm := 0.0
		for _, x := range d.Embedding {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("nonfinite embedding")
			}
			norm += x * x
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, fmt.Errorf("invalid embedding norm")
		}
		if dimension != 0 && dimension != len(d.Embedding) {
			return nil, fmt.Errorf("inconsistent embedding dimensions")
		}
		dimension = len(d.Embedding)
		vectors[d.Index] = d.Embedding
	}
	return vectors, nil
}
