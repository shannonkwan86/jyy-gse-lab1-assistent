package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"assistant/internal/knowledge"
	"assistant/internal/llm"
)

type action struct {
	Action       string    `json:"action"`
	Query        string    `json:"query"`
	Answer       string    `json:"answer"`
	Citations    []string  `json:"citations"`
	Insufficient bool      `json:"insufficient"`
	Sections     []Section `json:"sections"`
}

type Section struct {
	Text      string   `json:"text"`
	Citations []string `json:"citations"`
}

type queryState struct {
	seen     map[string]knowledge.Chunk
	searched bool
	trace    []string
}

func (a *Agent) nextAction(ctx context.Context, messages []llm.Message) (action, string, error) {
	var act action
	if err := ctx.Err(); err != nil {
		return act, "", err
	}
	raw, err := a.chat.Chat(ctx, messages)
	if err != nil {
		return act, "", err
	}
	if err := json.Unmarshal([]byte(raw), &act); err != nil {
		reason := parseFailureReason(err)
		if a.parseFailure != nil {
			a.parseFailure(raw, reason)
		}
		return act, "", fmt.Errorf("invalid agent JSON action: %s", reason)
	}
	return act, raw, nil
}

func (a *Agent) execute(ctx context.Context, act action, state *queryState) (string, error) {
	var data any
	var err error
	switch act.Action {
	case "search":
		data, err = a.search(ctx, act.Query, state)
	default:
		return "", fmt.Errorf("unknown agent action %q", act.Action)
	}
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(data)
	return string(b), err
}

func (a *Agent) search(ctx context.Context, query string, state *queryState) (any, error) {
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 1000 {
		return nil, fmt.Errorf("invalid search query")
	}
	hits, err := a.index.Search(ctx, query, 5)
	if err != nil {
		return nil, err
	}
	for i := range hits {
		chunk, err := a.index.ReadContext(ctx, hits[i].ID)
		if err != nil {
			return nil, fmt.Errorf("read search evidence: %w", err)
		}
		hits[i].Chunk = chunk
	}
	state.searched = true
	for _, hit := range hits {
		state.seen[hit.ID] = hit.Chunk
	}
	return map[string]any{"results": hits}, nil
}

// Describe location and category without embedding model-controlled text.
func parseFailureReason(err error) string {
	switch failure := err.(type) {
	case *json.SyntaxError:
		return fmt.Sprintf("JSON syntax error at byte %d", failure.Offset)
	case *json.UnmarshalTypeError:
		return fmt.Sprintf("JSON type mismatch at byte %d (expected %s)", failure.Offset, failure.Type)
	default:
		return "JSON decoding failed"
	}
}
