package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func mdAnchor(startLine, endLine, start int, exact string) *protocol.MarkdownAnnotationAnchor {
	return &protocol.MarkdownAnnotationAnchor{
		BlockID:   "b",
		StartLine: startLine,
		EndLine:   endLine,
		Start:     start,
		End:       start + len(exact),
		Exact:     exact,
	}
}

func fileAnnotationSource(path string) annotationDocumentSource {
	return annotationDocumentSource{kind: annotationSourceFile, path: path}
}

func TestMarkdownAnnotationPayload(t *testing.T) {
	const header = "# Markdown Annotations\n\nFile: /doc.md\n\n"
	const closing = "---\nPlease address the annotation feedback above."
	looksGood := func(id string, line int, quote string) protocol.MarkdownAnnotation {
		return protocol.MarkdownAnnotation{ID: id, Type: "comment", Anchor: mdAnchor(line, line, 0, quote),
			QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"), QuickLabelTip: protocol.Ptr("nice"), CreatedAt: line}
	}
	for _, tc := range []struct {
		name     string
		anns     []protocol.MarkdownAnnotation
		orphaned map[string]bool
		want     string
	}{
		{
			name: "mixed kinds ordered by line with globals last and a label summary",
			anns: []protocol.MarkdownAnnotation{
				{ID: "del", Type: "deletion", Anchor: mdAnchor(30, 30, 0, "old paragraph"), CreatedAt: 1},
				{ID: "glob", Type: "global", Text: protocol.Ptr("a global comment"), CreatedAt: 2},
				{ID: "range", Type: "comment", Anchor: mdAnchor(12, 18, 4, "the selected text"), Text: protocol.Ptr("the reviewer's comment"), CreatedAt: 3},
				{ID: "ql", Type: "comment", Anchor: mdAnchor(5, 5, 2, "selected text"),
					QuickLabelID: protocol.Ptr("looks-good"), QuickLabelText: protocol.Ptr("👍 Looks good"),
					QuickLabelTip: protocol.Ptr("Keep more of this"), CreatedAt: 4},
			},
			want: header +
				"I've reviewed this document and have 4 pieces of feedback:\n\n" +
				"## 1. (line 5) [👍 Looks good] Feedback on: \"selected text\"\n> Keep more of this\n\n" +
				"## 2. (lines 12–18) Feedback on: \"the selected text\"\n> the reviewer's comment\n\n" +
				"## 3. (line 30) Remove this\n```\nold paragraph\n```\n> I don't want this in the document.\n\n" +
				"## 4. General feedback about the document\n> a global comment\n\n" +
				"---\n## Label Summary\n\n- **👍 Looks good**: 1\n\nPlease address the annotation feedback above.",
		},
		{
			name: "one comment reads singular and has no summary",
			anns: []protocol.MarkdownAnnotation{
				{ID: "c", Type: "comment", Anchor: mdAnchor(7, 7, 0, "text"), Text: protocol.Ptr("note"), CreatedAt: 1},
			},
			want: header +
				"I've reviewed this document and have 1 piece of feedback:\n\n" +
				"## 1. (line 7) Feedback on: \"text\"\n> note\n\n" + closing,
		},
		{
			name: "every global comment is kept in creation order",
			anns: []protocol.MarkdownAnnotation{
				{ID: "later", Type: "global", Text: protocol.Ptr("second overall note"), CreatedAt: 20},
				{ID: "earlier", Type: "global", Text: protocol.Ptr("first overall note"), CreatedAt: 10},
			},
			want: header +
				"I've reviewed this document and have 2 pieces of feedback:\n\n" +
				"## 1. General feedback about the document\n> first overall note\n\n" +
				"## 2. General feedback about the document\n> second overall note\n\n" + closing,
		},
		{
			name: "an orphaned anchor is labelled as moved",
			anns: []protocol.MarkdownAnnotation{
				{ID: "orph", Type: "comment", Anchor: mdAnchor(7, 9, 0, "moved text"), Text: protocol.Ptr("still relevant"), CreatedAt: 1},
			},
			orphaned: map[string]bool{"orph": true},
			want: header +
				"I've reviewed this document and have 1 piece of feedback:\n\n" +
				"## 1. (~line 7, moved) Feedback on: \"moved text\"\n> still relevant\n\n" + closing,
		},
		{
			name: "quick labels render their tips and are counted in first-seen order",
			anns: []protocol.MarkdownAnnotation{
				looksGood("a", 1, "one"),
				{ID: "b", Type: "comment", Anchor: mdAnchor(2, 2, 0, "two"), QuickLabelID: protocol.Ptr("confusing"), CreatedAt: 2},
				looksGood("c", 3, "three"),
			},
			want: header +
				"I've reviewed this document and have 3 pieces of feedback:\n\n" +
				"## 1. (line 1) [👍 Looks good] Feedback on: \"one\"\n> nice\n\n" +
				"## 2. (line 2) [confusing] Feedback on: \"two\"\n\n" +
				"## 3. (line 3) [👍 Looks good] Feedback on: \"three\"\n> nice\n\n" +
				"---\n## Label Summary\n\n- **👍 Looks good**: 2\n- **confusing**: 1\n\nPlease address the annotation feedback above.",
		},
		{
			name: "items on one line are ordered by start offset",
			anns: []protocol.MarkdownAnnotation{
				{ID: "later", Type: "comment", Anchor: mdAnchor(4, 4, 20, "tail"), Text: protocol.Ptr("second"), CreatedAt: 1},
				{ID: "earlier", Type: "comment", Anchor: mdAnchor(4, 4, 2, "head"), Text: protocol.Ptr("first"), CreatedAt: 2},
			},
			want: header +
				"I've reviewed this document and have 2 pieces of feedback:\n\n" +
				"## 1. (line 4) Feedback on: \"head\"\n> first\n\n" +
				"## 2. (line 4) Feedback on: \"tail\"\n> second\n\n" + closing,
		},
		{
			name: "an item without an anchor still renders",
			anns: []protocol.MarkdownAnnotation{
				{ID: "x", Type: "comment", Text: protocol.Ptr("dangling"), CreatedAt: 1},
			},
			want: header +
				"I've reviewed this document and have 1 piece of feedback:\n\n" +
				"## 1. Feedback on: \"\"\n> dangling\n\n" + closing,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMarkdownAnnotationPayload(fileAnnotationSource("/doc.md"), tc.anns, tc.orphaned); got != tc.want {
				t.Fatalf("payload mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}
