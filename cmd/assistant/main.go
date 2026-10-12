package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"assistant/internal/knowledge"
)

type options struct {
	configPath string
	query      string
	jsonOutput bool
	scan       string
}

func parseOptions(args []string, log io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("assistant", flag.ContinueOnError)
	flags.SetOutput(log)
	flags.StringVar(&opts.configPath, "config", "configs/config.local.json", "configuration path")
	flags.StringVar(&opts.query, "query", "", "run one query and exit")
	flags.BoolVar(&opts.jsonOutput, "json", false, "JSON result for one query")
	flags.StringVar(&opts.scan, "scan", "", "offline scan of a Markdown article directory; no API calls")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 {
		return opts, fmt.Errorf("unexpected positional arguments")
	}
	if opts.scan == "" && opts.jsonOutput && opts.query == "" {
		return opts, fmt.Errorf("-json requires -query")
	}
	return opts, nil
}

func run(ctx context.Context, args []string, in io.Reader, out, log io.Writer) error {
	opts, err := parseOptions(args, log)
	if err != nil {
		return err
	}
	if opts.scan != "" {
		return offlineScan(ctx, opts.scan, out)
	}
	app, err := newApplication(opts.configPath, log)
	if err != nil {
		return err
	}
	defer app.index.Close()
	if opts.query != "" {
		return app.query(ctx, opts.query, opts.jsonOutput, out)
	}
	return app.interact(ctx, in, out)
}

func offlineScan(ctx context.Context, root string, out io.Writer) error {
	docs, err := knowledge.ScanContext(ctx, root)
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

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
