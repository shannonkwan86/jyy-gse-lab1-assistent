package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInputCancellationAlwaysReportsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, readError := inputLines(ctx, strings.NewReader("pending line\n"))
	select {
	case err := <-readError:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input cancellation closed lines without reporting terminal error")
	}
}
