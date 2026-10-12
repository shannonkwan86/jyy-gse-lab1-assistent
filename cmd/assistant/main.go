package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"assistant/internal/agent"
	"assistant/internal/knowledge"
	"assistant/internal/llm"
)

func run(ctx context.Context, args []string, in io.Reader, out, log io.Writer) error {
	flags := flag.NewFlagSet("assistant", flag.ContinueOnError)
	flags.SetOutput(log)
	configPath := flags.String("config", "configs/config.local.json", "configuration path")
	query := flags.String("query", "", "run one query and exit")
	jsonOutput := flags.Bool("json", false, "JSON result for one query")
	scan := flags.String("scan", "", "offline scan of a Markdown article directory; no API calls")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *scan != "" {
		docs, err := knowledge.Scan(*scan)
		if err != nil {
			return err
		}
		type item struct {
			Path   string `json:"path"`
			Title  string `json:"title"`
			Chunks int    `json:"chunks"`
		}
		items := make([]item, 0, len(docs))
		for _, d := range docs {
			items = append(items, item{d.Path, d.Title, len(d.Chunks)})
		}
		return json.NewEncoder(out).Encode(items)
	}
	if *jsonOutput && *query == "" {
		return fmt.Errorf("-json requires -query")
	}
	c, err := llm.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	chat, embedding := llm.New(c.Chat), llm.New(c.Embedding)
	k, err := knowledge.Open(c.ArticlesDir, c.IndexDB, c.Embedding.BaseURL+"|"+c.Embedding.Model, embedding)
	if err != nil {
		return err
	}
	defer k.Close()
	a := agent.New(chat, k, c.MaxSteps, func(s string) { fmt.Fprintln(log, s) })
	one := func(q string) error {
		started := time.Now()
		callCtx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
		defer cancel()
		defer func() {
			b, _ := json.Marshal(map[string]any{"chat": chat.Usage(), "embedding": embedding.Usage(), "elapsed_ms": time.Since(started).Milliseconds()})
			fmt.Fprintln(log, "session cumulative usage:", string(b))
		}()
		stats, e := k.Sync(callCtx)
		if e != nil {
			return fmt.Errorf("index synchronization failed: %w", e)
		}
		fmt.Fprintf(log, "indexed %d articles / %d chunks; encoded %d changed chunks\n", stats.Documents, stats.Chunks, stats.Encoded)
		r, e := a.Query(callCtx, q)
		if e != nil {
			return e
		}
		if *jsonOutput {
			return json.NewEncoder(out).Encode(r)
		}
		fmt.Fprintln(out, r.Answer)
		for i, s := range r.Sources {
			fmt.Fprintf(out, "\n[%d] %s · %s\n%s:%d-%d\nID: %s · version: %s\n%s\n", i+1, s.Title, s.Heading, s.Path, s.Start, s.End, s.ID, s.Version[:12], s.Text)
		}
		return nil
	}
	if *query != "" {
		return one(*query)
	}
	fmt.Fprintln(out, "博客助手：输入问题；/exit 退出；Ctrl+C 取消并退出。")
	// Channel permits Ctrl+C to stop even while stdin is awaiting a line.
	lines := make(chan string)
	readError := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 4096), 64*1024)
		for s.Scan() {
			select {
			case lines <- s.Text():
			case <-ctx.Done():
				return
			}
		}
		readError <- s.Err()
		close(lines)
	}()
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
			if err = one(q); err != nil {
				fmt.Fprintln(log, "error:", err)
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
	}
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
