//go:build unix

package knowledge

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestScanSkipsNonRegularMarkdown(t *testing.T) {
	root := t.TempDir()
	writeArticle(t, filepath.Join(root, "normal.md"), "正常正文")
	fifo := filepath.Join(root, "blocked.md")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	// Release the old blocking implementation so a regression fails, not hangs.
	done := make(chan struct{})
	timer := time.AfterFunc(100*time.Millisecond, func() {
		defer close(done)
		f, err := os.OpenFile(fifo, os.O_RDWR, 0)
		if err == nil {
			f.Close()
		}
	})
	t.Cleanup(func() {
		if !timer.Stop() {
			<-done
		}
	})
	docs, err := Scan(root)
	if err != nil || len(docs) != 1 || filepath.Base(docs[0].Path) != "normal.md" {
		t.Fatalf("nonregular source was read: %+v %v", docs, err)
	}
}
