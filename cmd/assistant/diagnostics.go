package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (a *application) enableDiagnostics(dir string) {
	if dir == "" {
		return
	}
	a.agent.SetParseFailureHandler(func(raw, reason string) {
		path, err := saveDiagnostic(dir, raw, reason)
		if err != nil {
			fmt.Fprintln(a.log, "warning: could not save action diagnostic")
			return
		}
		fmt.Fprintf(a.log, "action diagnostic saved: %s\n", path)
	})
}

func prepareDiagnosticDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("diagnostic directory must be a private real directory")
	}
	// Keep opt-in records ignored even when the user chooses another repo directory.
	path := filepath.Join(dir, ".gitignore")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("diagnostic ignore file must be regular")
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil || string(contents) != "*\n" {
			return fmt.Errorf("diagnostic ignore file is not managed")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString("*\n")
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func saveDiagnostic(dir, raw, reason string) (string, error) {
	if err := prepareDiagnosticDirectory(dir); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "action-*.json")
	if err != nil {
		return "", err
	}
	path := f.Name()
	err = json.NewEncoder(f).Encode(map[string]string{"response": raw, "reason": reason})
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		os.Remove(path)
		if err != nil {
			return "", err
		}
		return "", closeErr
	}
	return path, nil
}
