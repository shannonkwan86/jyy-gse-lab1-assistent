package main

import (
	"assistant/internal/agent"
	"assistant/internal/llm"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticFilesArePrivateAndUnique(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug")
	for i := 0; i < 2; i++ {
		if _, err := saveDiagnostic(dir, "private response", "syntax at byte 2"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries: %v", entries)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("directory not private")
	}
	for _, entry := range entries {
		if entry.Name() == ".gitignore" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("permissions: %v", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]string
		if err := json.Unmarshal(raw, &data); err != nil || data["response"] != "private response" {
			t.Fatalf("record: %s %v", raw, err)
		}
	}
}

func TestDiagnosticRejectsSymlinkDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug")
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := saveDiagnostic(path, "private", "syntax"); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestDiagnosticWriteFailurePreservesParseError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(dir, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	a := &application{agent: agent.New(failedChat{}, nil, 8, nil), log: &log}
	a.enableDiagnostics(dir)
	_, err := a.agent.Query(context.Background(), "query")
	if err == nil || !strings.Contains(err.Error(), "JSON syntax error") || !strings.Contains(log.String(), "could not save") || strings.Contains(log.String(), "PRIVATE") {
		t.Fatalf("err=%v log=%s", err, log.String())
	}
}

type failedChat struct{}

func (failedChat) Chat(context.Context, []llm.Message) (string, error) { return "PRIVATE", nil }

func TestDiagnosticsDisabledDoesNotWrite(t *testing.T) {
	var log bytes.Buffer
	a := &application{agent: agent.New(failedChat{}, nil, 8, nil), log: &log}
	a.enableDiagnostics("")
	if _, err := a.agent.Query(context.Background(), "query"); err == nil {
		t.Fatal("malformed accepted")
	}
	if log.Len() != 0 {
		t.Fatalf("default diagnostic output: %s", log.String())
	}
}
