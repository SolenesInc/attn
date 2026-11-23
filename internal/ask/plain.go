package ask

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Plain is line, one line of an ask such as its subject, as its words read with their inline
// markdown taken off. A line that reads as anything but one paragraph is returned as written, as
// the apps show it.
func Plain(line string) string {
	source := []byte(line)
	document := imageMarkdown.Parse(text.NewReader(source))
	paragraph, ok := document.FirstChild().(*ast.Paragraph)
	if !ok || paragraph.NextSibling() != nil {
		return line
	}
	var words strings.Builder
	_ = ast.Walk(paragraph, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.Text:
			written := node.Segment.Value(source)
			if _, inCode := node.Parent().(*ast.CodeSpan); !inCode {
				written = util.UnescapePunctuations(written)
			}
			words.Write(written)
			if node.SoftLineBreak() || node.HardLineBreak() {
				words.WriteByte(' ')
			}
		case *ast.String:
			words.Write(node.Value)
		case *ast.AutoLink:
			words.Write(node.Label(source))
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			for i := 0; i < node.Segments.Len(); i++ {
				segment := node.Segments.At(i)
				words.Write(segment.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	return words.String()
}
