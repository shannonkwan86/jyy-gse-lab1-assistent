package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"assistant/internal/knowledge"
)

// Standalone identifier markers are reserved for [chunkID] citations.
var citationMarker = regexp.MustCompile(`\[([A-Za-z0-9_-]+)\]`)
var markdownCode = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`]*`")

func (a *Agent) finish(ctx context.Context, act action, state *queryState) (Result, error) {
	result := Result{Trace: state.trace}
	if !state.searched || strings.TrimSpace(act.Answer) == "" {
		return result, fmt.Errorf("final requires search and a nonempty answer")
	}
	if !act.Insufficient && len(act.Citations) == 0 {
		return result, fmt.Errorf("supported answer requires citations")
	}
	if err := validateCitationMarkers(act.Answer, act.Citations); err != nil {
		return result, err
	}
	for _, c := range state.seen {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := a.index.ValidateContext(ctx, c); err != nil {
			return result, err
		}
	}
	sources, err := citedSources(act.Citations, state.seen)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Answer, result.Sources = act.Answer, sources
	return result, nil
}

func validateCitationMarkers(answer string, citations []string) error {
	// Brackets in code examples are data, not citation markers.
	answer = markdownCode.ReplaceAllString(answer, "")
	listed := make(map[string]bool, len(citations))
	for _, id := range citations {
		listed[id] = true
	}
	marked := map[string]bool{}
	for _, match := range citationMarker.FindAllStringSubmatchIndex(answer, -1) {
		// Markdown links are prose, not standalone citation markers.
		if match[1] < len(answer) && answer[match[1]] == '(' {
			continue
		}
		id := answer[match[2]:match[3]]
		if !listed[id] {
			return fmt.Errorf("answer citation is not listed: %s", id)
		}
		marked[id] = true
	}
	for id := range listed {
		if !marked[id] {
			return fmt.Errorf("listed citation is missing from answer: %s", id)
		}
	}
	return nil
}

func citedSources(ids []string, seen map[string]knowledge.Chunk) ([]knowledge.Chunk, error) {
	sources := make([]knowledge.Chunk, 0, len(ids))
	unique := map[string]bool{}
	for _, id := range ids {
		c, ok := seen[id]
		if !ok {
			return nil, fmt.Errorf("citation was not read")
		}
		if !unique[id] {
			sources = append(sources, c)
			unique[id] = true
		}
	}
	return sources, nil
}
