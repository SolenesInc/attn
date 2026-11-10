package daemon

import (
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/prompts"

	"github.com/victorarias/attn/internal/protocol"
)

func formatMarkdownAnnotationPayload(source annotationDocumentSource, machine string, anns []protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	entries, summary := formatMarkdownEntries(inReadingOrder(anns), 1, orphaned)
	return prompts.RenderText("annotation-markdown", "submit", prompts.Values{
		"has_title": fmt.Sprint(source.seedTitle != ""), "seed": fmt.Sprint(source.kind == annotationSourceSeed), "seed_id": source.seedID, "title": source.seedTitle, "path": source.path, "machine": machine,
		"count": fmt.Sprint(len(anns)), "singular": fmt.Sprint(len(anns) == 1), "entries": entries, "summary_rows": summary,
	})
}

func formatReviewPayload(source annotationDocumentSource, machine, verdict string, anns []protocol.MarkdownAnnotation, orphaned map[string]bool) string {
	var marks, edits []protocol.MarkdownAnnotation
	for _, a := range inReadingOrder(anns) {
		if a.Type == protocol.MarkdownAnnotationEdit {
			edits = append(edits, a)
		} else {
			marks = append(marks, a)
		}
	}
	entries, _ := formatMarkdownEntries(marks, 1, orphaned)
	editEntries, _ := formatMarkdownEntries(edits, len(marks)+1, orphaned)
	return prompts.RenderText("annotation-markdown", "review", prompts.Values{
		"path": source.path, "machine": machine, "document": shellQuote(source.path),
		"has_marks": fmt.Sprint(len(marks) > 0), "count": fmt.Sprint(len(marks)), "singular": fmt.Sprint(len(marks) == 1), "entries": entries,
		"has_edits": fmt.Sprint(len(edits) > 0), "edit_count": fmt.Sprint(len(edits)), "edit_singular": fmt.Sprint(len(edits) == 1), "edit_entries": editEntries,
		"approve": fmt.Sprint(verdict == protocol.ReviewVerdictApprove), "request_changes": fmt.Sprint(verdict == protocol.ReviewVerdictRequestChanges),
	})
}

func formatQuestionPayload(source annotationDocumentSource, machine string, question protocol.MarkdownAnnotation) string {
	mark := markValues(question)
	values := prompts.Values{"path": source.path, "machine": machine, "document": shellQuote(source.path), "id": question.ID}
	for _, name := range []string{"has_file", "place", "fence", "quote", "comment"} {
		values[name] = mark[name]
	}
	return prompts.RenderText("annotation-markdown", "question", values)
}

// inReadingOrder puts marks in the order a reader meets them: by their line in
// the document, then by their line in the file a stop shows, with general
// feedback last in the order it was written.
func inReadingOrder(anns []protocol.MarkdownAnnotation) []protocol.MarkdownAnnotation {
	var anchored, globals []protocol.MarkdownAnnotation
	for _, a := range anns {
		if a.Type == protocol.MarkdownAnnotationGlobal {
			globals = append(globals, a)
		} else {
			anchored = append(anchored, a)
		}
	}
	sort.SliceStable(anchored, func(i, j int) bool {
		a, b := readingPlace(anchored[i]), readingPlace(anchored[j])
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	sort.SliceStable(globals, func(i, j int) bool {
		return globals[i].CreatedAt < globals[j].CreatedAt
	})
	return append(anchored, globals...)
}

func readingPlace(a protocol.MarkdownAnnotation) [3]int {
	if a.Anchor == nil {
		return [3]int{}
	}
	return [3]int{a.Anchor.StartLine, protocol.Deref(a.Anchor.FileStartLine), a.Anchor.Start}
}

func formatMarkdownEntries(sorted []protocol.MarkdownAnnotation, first int, orphaned map[string]bool) (entries, summary string) {
	var out strings.Builder
	var labelOrder []string
	labelCounts := map[string]int{}
	for i, a := range sorted {
		values := markValues(a)
		if values["quick_label"] == "true" && a.Type != protocol.MarkdownAnnotationDeletion && a.Type != protocol.MarkdownAnnotationGlobal {
			label := values["label"]
			if _, seen := labelCounts[label]; !seen {
				labelOrder = append(labelOrder, label)
			}
			labelCounts[label]++
		}
		values["index"], values["orphaned"] = fmt.Sprint(first+i), fmt.Sprint(orphaned[a.ID])
		out.WriteString(prompts.RenderText("annotation-markdown", "entry", values))
	}
	var rows strings.Builder
	for _, label := range labelOrder {
		fmt.Fprintf(&rows, "- **%s**: %d\n", label, labelCounts[label])
	}
	return out.String(), rows.String()
}

func markValues(a protocol.MarkdownAnnotation) prompts.Values {
	start, end, quote, file := 0, 0, "", ""
	if a.Anchor != nil {
		start, end, quote, file = a.Anchor.StartLine, a.Anchor.EndLine, a.Anchor.Exact, protocol.Deref(a.Anchor.File)
	}
	answer := protocol.Deref(a.Answer)
	return prompts.Values{
		"has_anchor": fmt.Sprint(a.Anchor != nil && file == ""), "multiline": fmt.Sprint(end > start),
		"start_line": fmt.Sprint(start), "end_line": fmt.Sprint(end), "quote": quote, "comment": protocol.Deref(a.Text),
		"deletion": fmt.Sprint(a.Type == protocol.MarkdownAnnotationDeletion), "global": fmt.Sprint(a.Type == protocol.MarkdownAnnotationGlobal),
		"question": fmt.Sprint(a.Type == protocol.MarkdownAnnotationQuestion), "has_answer": fmt.Sprint(answer != ""), "answer": answer,
		"edit": fmt.Sprint(a.Type == protocol.MarkdownAnnotationEdit), "was": protocol.Deref(a.EditWas), "now": protocol.Deref(a.EditNow),
		"was_fence": codeFence(protocol.Deref(a.EditWas)), "now_fence": codeFence(protocol.Deref(a.EditNow)),
		"has_file": fmt.Sprint(file != ""), "place": filePlace(a.Anchor), "fence": codeFence(quote),
		"quick_label": fmt.Sprint(protocol.Deref(a.QuickLabelID) != ""), "label": markdownQuickLabelDisplay(a),
		"tip": protocol.Deref(a.QuickLabelTip), "has_tip": fmt.Sprint(protocol.Deref(a.QuickLabelTip) != ""),
	}
}

func filePlace(anchor *protocol.MarkdownAnnotationAnchor) string {
	if anchor == nil || anchor.File == nil {
		return ""
	}
	from, to := protocol.Deref(anchor.FileStartLine), protocol.Deref(anchor.FileEndLine)
	if to > from {
		return fmt.Sprintf("%s:%d–%d", *anchor.File, from, to)
	}
	return fmt.Sprintf("%s:%d", *anchor.File, from)
}

// codeFence is a backtick fence longer than any backtick run in the code it
// encloses, so the code cannot close it.
func codeFence(code string) string {
	longest, run := 0, 0
	for _, r := range code {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func markdownQuickLabelDisplay(a protocol.MarkdownAnnotation) string {
	if text := protocol.Deref(a.QuickLabelText); text != "" {
		return text
	}
	return protocol.Deref(a.QuickLabelID)
}
