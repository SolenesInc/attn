package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	markdownAnnotationTypeComment  = "comment"
	markdownAnnotationTypeDeletion = "deletion"
	markdownAnnotationTypeGlobal   = "global"
)

// Orphanhood is client-derived and non-persisted, so it travels in the submit
// message rather than being read back.
func formatMarkdownAnnotationPayload(source annotationDocumentSource, anns []protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	var anchored, globals []protocol.MarkdownAnnotation
	for _, a := range anns {
		if a.Type == markdownAnnotationTypeGlobal {
			globals = append(globals, a)
		} else {
			anchored = append(anchored, a)
		}
	}
	sort.SliceStable(anchored, func(i, j int) bool {
		li, si := anchorSortKey(anchored[i])
		lj, sj := anchorSortKey(anchored[j])
		if li != lj {
			return li < lj
		}
		return si < sj
	})
	sort.SliceStable(globals, func(i, j int) bool {
		return globals[i].CreatedAt < globals[j].CreatedAt
	})
	sorted := append(anchored, globals...)

	var entries strings.Builder
	var labelOrder []string
	labelCounts := map[string]int{}
	for i, a := range sorted {
		start, end, quote := 0, 0, ""
		if a.Anchor != nil {
			start, end, quote = a.Anchor.StartLine, a.Anchor.EndLine, a.Anchor.Exact
		}
		quick := protocol.Deref(a.QuickLabelID) != ""
		label := markdownQuickLabelDisplay(a)
		if a.Type != markdownAnnotationTypeDeletion && a.Type != markdownAnnotationTypeGlobal && quick {
			if _, seen := labelCounts[label]; !seen {
				labelOrder = append(labelOrder, label)
			}
			labelCounts[label]++
		}
		entries.WriteString(prompts.RenderText("annotation-markdown", "entry", prompts.Values{
			"index": fmt.Sprint(i + 1), "has_anchor": fmt.Sprint(a.Anchor != nil), "orphaned": fmt.Sprint(orphaned[a.ID]), "multiline": fmt.Sprint(end > start),
			"start_line": fmt.Sprint(start), "end_line": fmt.Sprint(end), "quote": quote, "comment": protocol.Deref(a.Text),
			"deletion": fmt.Sprint(a.Type == markdownAnnotationTypeDeletion), "global": fmt.Sprint(a.Type == markdownAnnotationTypeGlobal),
			"quick_label": fmt.Sprint(quick), "label": label, "tip": protocol.Deref(a.QuickLabelTip), "has_tip": fmt.Sprint(protocol.Deref(a.QuickLabelTip) != ""),
		}))
	}
	var summary strings.Builder
	for _, label := range labelOrder {
		fmt.Fprintf(&summary, "- **%s**: %d\n", label, labelCounts[label])
	}
	return prompts.RenderText("annotation-markdown", "submit", prompts.Values{
		"has_title": fmt.Sprint(source.seedTitle != ""), "seed": fmt.Sprint(source.kind == annotationSourceSeed), "seed_id": source.seedID, "title": source.seedTitle, "path": source.path,
		"count": fmt.Sprint(len(sorted)), "singular": fmt.Sprint(len(sorted) == 1), "entries": entries.String(), "summary_rows": summary.String(),
	})
}

func anchorSortKey(a protocol.MarkdownAnnotation) (line, start int) {
	if a.Anchor == nil {
		return 0, 0
	}
	return a.Anchor.StartLine, a.Anchor.Start
}

func markdownQuickLabelDisplay(a protocol.MarkdownAnnotation) string {
	if text := protocol.Deref(a.QuickLabelText); text != "" {
		return text
	}
	return protocol.Deref(a.QuickLabelID)
}
