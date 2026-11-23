package ask

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"

	"github.com/victorarias/attn/internal/protocol"
)

// MaxImageBytes caps the images of one ask together. Retina window captures on disk measure 0.4 to
// 1.6 MiB; the images travel base64-encoded in one socket frame of protocol.MaxSocketFrameBytes
// beside at most MaxMarkdownBytes of markdown, which 5 MiB leaves room for.
const MaxImageBytes = 5 << 20

var imageTypes = map[string]string{
	".bmp":  "image/bmp",
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".webp": "image/webp",
}

func ImageType(imagePath string) (string, bool) {
	mimeType, ok := imageTypes[strings.ToLower(path.Ext(strings.TrimRight(imagePath, "/")))]
	return mimeType, ok
}

var imageMarkdown = parser.NewParser(
	parser.WithBlockParsers(parser.DefaultBlockParsers()...),
	parser.WithInlineParsers(parser.DefaultInlineParsers()...),
	parser.WithParagraphTransformers(append(parser.DefaultParagraphTransformers(), util.Prioritized(tableCells{}, 200))...),
)

// tableCells splits a GFM table into one paragraph per cell, as the app's renderer does, so no
// inline span crosses a cell boundary. goldmark's table extension does the same, but importing it
// runs the init of its HTML renderers on every CLI start.
type tableCells struct{}

func (tableCells) Transform(paragraph *ast.Paragraph, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	lines := paragraph.Lines()
	for i := 1; i < lines.Len(); i++ {
		delimiter := lines.At(i)
		columns := delimiterRowColumns(delimiter.Value(source))
		if columns == 0 {
			continue
		}
		header := rowCells(lines.At(i-1), source)
		if len(header) != columns {
			return
		}
		cells := header
		for j := i + 1; j < lines.Len(); j++ {
			row := rowCells(lines.At(j), source)
			cells = append(cells, row[:min(len(row), columns)]...)
		}
		parent := paragraph.Parent()
		after := ast.Node(paragraph)
		for _, cell := range cells {
			cellParagraph := ast.NewParagraph()
			cellParagraph.Lines().Append(cell)
			parent.InsertAfter(parent, after, cellParagraph)
			after = cellParagraph
		}
		lines.SetSliced(0, i-1)
		if lines.Len() == 0 {
			parent.RemoveChild(parent, paragraph)
		}
		return
	}
}

func delimiterRowColumns(line []byte) int {
	if width, _ := util.IndentWidth(line, 0); width > 3 {
		return 0
	}
	columns := bytes.Split(line, []byte("|"))
	if util.IsBlank(columns[0]) {
		columns = columns[1:]
	}
	if len(columns) > 0 && util.IsBlank(columns[len(columns)-1]) {
		columns = columns[:len(columns)-1]
	}
	for _, column := range columns {
		dashes := bytes.TrimSuffix(bytes.TrimPrefix(bytes.TrimSpace(column), []byte(":")), []byte(":"))
		if len(dashes) == 0 || len(bytes.Trim(dashes, "-")) != 0 {
			return 0
		}
	}
	return len(columns)
}

func rowCells(line text.Segment, source []byte) []text.Segment {
	line = line.TrimLeftSpace(source)
	line = line.TrimRightSpace(source)
	value := line.Value(source)
	start, stop := 0, len(value)
	if stop > 0 && value[0] == '|' {
		start++
	}
	if stop > start && value[stop-1] == '|' && (stop < 2 || value[stop-2] != '\\') {
		stop--
	}
	var cells []text.Segment
	for cellStart, i := start, start; i <= stop; i++ {
		if i == stop || value[i] == '|' && (i == 0 || value[i-1] != '\\') {
			cell := text.NewSegment(line.Start+cellStart, line.Start+i)
			cell = cell.TrimLeftSpace(source)
			cells = append(cells, cell.TrimRightSpace(source))
			cellStart = i + 1
		}
	}
	return cells
}

// ImagePaths lists the local images the ask's parts show, as the app resolves them against
// baseDir: absolute paths with an image extension, each once, in reading order. It passes over
// every other image target, as reading an ask stored before ShownImages refused them must.
func ImagePaths(parts []protocol.AskPart, baseDir string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, markdown := range renderedMarkdown(parts) {
		for _, target := range imageTargets(markdown) {
			resolved, err := resolveImage(baseDir, target.src)
			if err == nil && !seen[resolved] {
				seen[resolved] = true
				paths = append(paths, resolved)
			}
		}
	}
	return paths
}

// ShownImages lists the images of ImagePaths for an ask being opened or amended, and refuses
// the first image target that the app or the phone cannot show.
func ShownImages(parts []protocol.AskPart, baseDir string) ([]string, error) {
	for _, markdown := range renderedMarkdown(parts) {
		for _, target := range imageTargets(markdown) {
			if target.html {
				return nil, protocol.Refuse(protocol.RefusalInvalidInput, "the ask shows %s in an HTML <img> tag, which the app leaves out; %s", target.src, whatAsksShow)
			}
			if _, err := resolveImage(baseDir, target.src); err != nil {
				return nil, protocol.Refuse(protocol.RefusalInvalidInput, "%w; %s", err, whatAsksShow)
			}
		}
	}
	return ImagePaths(parts, baseDir), nil
}

const whatAsksShow = "an ask shows a diagram drawn in a ```mermaid code block, and a local PNG, JPEG, GIF, WebP, BMP or TIFF file written ![alt](path)"

func renderedMarkdown(parts []protocol.AskPart) []string {
	var out []string
	for _, part := range parts {
		switch {
		case part.Markdown != nil:
			out = append(out, *part.Markdown)
		case part.Question != nil && part.Question.Context != nil:
			out = append(out, *part.Question.Context)
		}
	}
	return out
}

type imageTarget struct {
	src  string
	html bool
}

func imageTargets(markdown string) []imageTarget {
	source := []byte(markdown)
	var targets []imageTarget
	_ = ast.Walk(imageMarkdown.Parse(text.NewReader(source)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Image:
			targets = append(targets, imageTarget{src: string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(n.Destination))))})
		case *ast.RawHTML:
			targets = append(targets, htmlImageSources(segmentsText(n.Segments, source))...)
		case *ast.HTMLBlock:
			raw := segmentsText(n.Lines(), source)
			if n.HasClosure() {
				raw = append(raw, n.ClosureLine.Value(source)...)
			}
			targets = append(targets, htmlImageSources(raw)...)
		}
		return ast.WalkContinue, nil
	})
	return targets
}

func segmentsText(segments *text.Segments, source []byte) []byte {
	var out []byte
	for i := range segments.Len() {
		segment := segments.At(i)
		out = append(out, segment.Value(source)...)
	}
	return out
}

func htmlImageSources(raw []byte) []imageTarget {
	var sources []imageTarget
	tokens := html.NewTokenizer(bytes.NewReader(raw))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return sources
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data != "img" {
				continue
			}
			for _, attr := range token.Attr {
				if attr.Key == "src" {
					sources = append(sources, imageTarget{src: attr.Val, html: true})
				}
			}
		}
	}
}

// ResolveImage resolves an image target, with its markdown escapes and HTML references already decoded, to the
// absolute local path the app shows, or reports that the target is no local image.
func ResolveImage(baseDir, target string) (string, bool) {
	resolved, err := resolveImage(baseDir, target)
	return resolved, err == nil
}

func resolveImage(baseDir, target string) (string, error) {
	given := strings.TrimSpace(target)
	target = string(util.URLEscape([]byte(given), false))
	if target == "" || strings.HasPrefix(target, "#") {
		return "", fmt.Errorf("the ask shows an image with no file (%q)", given)
	}
	if strings.HasPrefix(target, "//") {
		return "", fmt.Errorf("the ask shows the remote image %s, which the app and the phone do not fetch", given)
	}
	ref, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("the ask shows the image %q, which is no path", given)
	}
	if strings.EqualFold(ref.Scheme, "data") {
		return "", errors.New("the ask shows an image written as a data: URL, which the app and the phone do not draw")
	}
	if ref.Scheme != "" && !strings.EqualFold(ref.Scheme, "file") {
		return "", fmt.Errorf("the ask shows the remote image %s, which the app and the phone do not fetch", given)
	}
	base := &url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(baseDir, "ask.md"))}
	resolved := base.ResolveReference(ref)
	if resolved.Host != "" {
		return "", fmt.Errorf("the ask shows the image %s from another machine, which the app and the phone do not fetch", given)
	}
	if _, ok := ImageType(resolved.Path); !ok {
		if strings.EqualFold(path.Ext(resolved.Path), ".svg") {
			return "", fmt.Errorf("the ask shows the SVG image %s, which the app and the phone do not draw", given)
		}
		return "", fmt.Errorf("the ask shows %s as an image, which is no image file the app and the phone show", given)
	}
	return resolved.Path, nil
}
