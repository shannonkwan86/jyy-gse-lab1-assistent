package agent

import (
	"assistant/internal/llm"
	"context"
	"strings"
	"testing"
)

func TestActionParseDiagnosticDoesNotExposeResponse(t *testing.T) {
	for _, raw := range []string{`{"action":"PRIVATE_MARKER"`, `{"action":123}`} {
		a := New(responseFunc(func(context.Context, []llm.Message) (string, error) { return raw, nil }), nil, 8, nil)
		_, _, err := a.nextAction(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "byte") || strings.Contains(err.Error(), "PRIVATE_MARKER") {
			t.Fatalf("unsafe or missing diagnostic: %v", err)
		}
	}
}

func TestParseFailureHandlerReceivesOnlyFailedResponse(t *testing.T) {
	for _, raw := range []string{`{"action":"search","query":"缓存"}`, `private malformed response`} {
		a := New(responseFunc(func(context.Context, []llm.Message) (string, error) { return raw, nil }), nil, 8, nil)
		calls := 0
		a.SetParseFailureHandler(func(response, reason string) {
			calls++
			if response != raw || reason == "" {
				t.Fatal("missing failure record")
			}
		})
		_, _, err := a.nextAction(context.Background(), nil)
		if (err == nil && calls != 0) || (err != nil && calls != 1) {
			t.Fatalf("handler calls=%d err=%v", calls, err)
		}
	}
}
