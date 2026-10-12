package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"assistant/internal/agent"
	"assistant/internal/knowledge"
	"assistant/internal/llm"
)

type application struct {
	chat, embedding *llm.Client
	index           *knowledge.Index
	agent           *agent.Agent
	timeout         time.Duration
	log             io.Writer
}

func newApplication(configPath string, log io.Writer) (*application, error) {
	c, err := llm.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	chat, embedding := llm.New(c.Chat), llm.New(c.Embedding)
	k, err := knowledge.Open(c.ArticlesDir, c.IndexDB, c.Embedding.BaseURL+"|"+c.Embedding.Model, embedding)
	if err != nil {
		return nil, err
	}
	a := agent.New(chat, k, c.MaxSteps, func(s string) { fmt.Fprintln(log, s) })
	return &application{chat: chat, embedding: embedding, index: k, agent: a,
		timeout: time.Duration(c.TimeoutSeconds) * time.Second, log: log}, nil
}

func (a *application) query(ctx context.Context, q string, jsonOutput bool, out io.Writer) error {
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	defer a.reportUsage(started)
	stats, err := a.index.Sync(callCtx)
	if err != nil {
		return fmt.Errorf("index synchronization failed: %w", err)
	}
	fmt.Fprintf(a.log, "indexed %d articles / %d chunks; encoded %d changed chunks\n", stats.Documents, stats.Chunks, stats.Encoded)
	r, err := a.agent.Query(callCtx, q)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(out).Encode(r)
	}
	return writeResult(out, r)
}

func (a *application) reportUsage(started time.Time) {
	data, _ := json.Marshal(map[string]any{"chat": a.chat.Usage(), "embedding": a.embedding.Usage(),
		"elapsed_ms": time.Since(started).Milliseconds()})
	fmt.Fprintln(a.log, "session cumulative usage:", string(data))
}

func writeResult(out io.Writer, result agent.Result) error {
	if _, err := fmt.Fprintln(out, result.Answer); err != nil {
		return err
	}
	for i, s := range result.Sources {
		_, err := fmt.Fprintf(out, "\n[%d] %s · %s\n%s:%d-%d\nID: %s · version: %s\n%s\n",
			i+1, s.Title, s.Heading, s.Path, s.Start, s.End, s.ID, s.Version[:12], s.Text)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *application) interact(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fmt.Fprintln(out, "博客助手：输入问题；/exit 退出；Ctrl+C 取消并退出。")
	lines, readError := inputLines(ctx, in)
	for {
		fmt.Fprint(out, "> ")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return <-readError
			}
			q := strings.TrimSpace(line)
			if q == "/exit" || q == "/quit" {
				return nil
			}
			if q == "" {
				continue
			}
			if err := a.query(ctx, q, false, out); err != nil {
				fmt.Fprintln(a.log, "error:", err)
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
	}
}

func inputLines(ctx context.Context, in io.Reader) (<-chan string, <-chan error) {
	lines := make(chan string)
	readError := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			readError <- err
			close(readError)
			close(lines)
		}()
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 4096), 64*1024)
		for s.Scan() {
			select {
			case lines <- s.Text():
			case <-ctx.Done():
				err = ctx.Err()
				return
			}
		}
		err = s.Err()
	}()
	return lines, readError
}
