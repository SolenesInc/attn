package daemon

import (
	"fmt"
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

	var b strings.Builder
	piece := "pieces"
	if len(sorted) == 1 {
		piece = "piece"
	}
	subject := "File: " + source.path
	if source.kind == annotationSourceSeed {
		subject = "Seed: " + source.seedID
		if source.seedTitle != "" {
			subject += " — " + source.seedTitle
		}
	}
	fmt.Fprintf(&b, "# Markdown Annotations\n\n%s\n\nI've reviewed this document and have %d %s of feedback:\n\n", subject, len(sorted), piece)

	var labelOrder []string
	labelCounts := map[string]int{}

	for i, a := range sorted {
		fmt.Fprintf(&b, "## %d. ", i+1)
		label := markdownAnnotationLineLabel(a, orphaned)
		if label != "" {
			b.WriteString(label)
			b.WriteString(" ")
		}
		exact := ""
		if a.Anchor != nil {
			exact = a.Anchor.Exact
		}
		switch {
		case a.Type == markdownAnnotationTypeDeletion:
			b.WriteString("Remove this\n")
			fmt.Fprintf(&b, "```\n%s\n```\n", exact)
			b.WriteString("> I don't want this in the document.\n")
		case a.Type == markdownAnnotationTypeGlobal:
			b.WriteString("General feedback about the document\n")
			fmt.Fprintf(&b, "> %s\n", protocol.Deref(a.Text))
		case a.QuickLabelID != nil && *a.QuickLabelID != "":
			display := markdownQuickLabelDisplay(a)
			if _, seen := labelCounts[display]; !seen {
				labelOrder = append(labelOrder, display)
			}
			labelCounts[display]++
			fmt.Fprintf(&b, "[%s] Feedback on: \"%s\"\n", display, exact)
			if tip := protocol.Deref(a.QuickLabelTip); tip != "" {
				fmt.Fprintf(&b, "> %s\n", tip)
			}
		default:
			fmt.Fprintf(&b, "Feedback on: \"%s\"\n", exact)
			fmt.Fprintf(&b, "> %s\n", protocol.Deref(a.Text))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	if len(labelOrder) > 0 {
		b.WriteString("## Label Summary\n\n")
		for _, display := range labelOrder {
			fmt.Fprintf(&b, "- **%s**: %d\n", display, labelCounts[display])
		}
		b.WriteString("\n")
	}
	b.WriteString("Please address the annotation feedback above.")
	return b.String()
}

func anchorSortKey(a protocol.MarkdownAnnotation) (line, start int) {
	if a.Anchor == nil {
		return 0, 0
	}
	return a.Anchor.StartLine, a.Anchor.Start
}

func markdownAnnotationLineLabel(a protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	if a.Anchor == nil {
		return ""
	}
	if orphaned[a.ID] {
		return fmt.Sprintf("(~line %d, moved)", a.Anchor.StartLine)
	}
	if a.Anchor.EndLine <= a.Anchor.StartLine {
		return fmt.Sprintf("(line %d)", a.Anchor.StartLine)
	}
	return fmt.Sprintf("(lines %d–%d)", a.Anchor.StartLine, a.Anchor.EndLine)
}

func markdownQuickLabelDisplay(a protocol.MarkdownAnnotation) string {
	if text := protocol.Deref(a.QuickLabelText); text != "" {
		return text
	}
	return protocol.Deref(a.QuickLabelID)
}
