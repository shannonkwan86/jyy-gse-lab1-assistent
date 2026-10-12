package knowledge

import (
	"context"
	"errors"
	"testing"
)

func TestScanHonorsCancellationBeforeOpeningRoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanContext(ctx, "/does-not-exist"); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan should stop before filesystem access: %v", err)
	}
}

func TestReadSourceHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readSource(ctx, "/does-not-exist"); !errors.Is(err, context.Canceled) {
		t.Fatalf("read should stop before filesystem access: %v", err)
	}
}
