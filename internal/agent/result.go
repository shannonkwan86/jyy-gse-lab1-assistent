package agent

import (
	"context"
	"fmt"
	"strings"

	"assistant/internal/knowledge"
)

func (a *Agent) finish(ctx context.Context, act action, state *queryState) (Result, error) {
	result := Result{Trace: state.trace}
	sections := act.Sections
	if len(sections) == 0 && strings.TrimSpace(act.Answer) != "" {
		sections = []Section{{Text: act.Answer, Citations: act.Citations}}
	}
	if !state.searched || len(sections) == 0 || !hasSectionText(sections) {
		return result, fmt.Errorf("final requires search and a nonempty answer")
	}
	if !act.Insufficient && !hasCitations(sections) {
		return result, fmt.Errorf("supported answer requires citations")
	}
	for _, c := range state.seen {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := a.index.ValidateContext(ctx, c); err != nil {
			return result, err
		}
	}
	ids := sectionCitations(sections)
	sources, err := citedSources(ids, state.seen)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Answer, result.Sections, result.Sources = joinSections(sections), sections, sources
	return result, nil
}

func hasSectionText(sections []Section) bool {
	for _, section := range sections {
		if strings.TrimSpace(section.Text) != "" {
			return true
		}
	}
	return false
}

func hasCitations(sections []Section) bool { return len(sectionCitations(sections)) > 0 }

func sectionCitations(sections []Section) []string {
	var ids []string
	for _, section := range sections {
		ids = append(ids, section.Citations...)
	}
	return ids
}

func joinSections(sections []Section) string {
	texts := make([]string, len(sections))
	for i, section := range sections {
		texts[i] = section.Text
	}
	return strings.Join(texts, "\n\n")
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
