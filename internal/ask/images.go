package ask

import (
	"bytes"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
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

var imageMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

// ImagePaths lists the local images the ask's parts show, as the app resolves them against
// baseDir: absolute paths with an image extension, each once, in reading order.
func ImagePaths(parts []protocol.AskPart, baseDir string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, markdown := range renderedMarkdown(parts) {
		for _, target := range imageTargets(markdown) {
			resolved, ok := localImagePath(baseDir, target)
			if ok && !seen[resolved] {
				seen[resolved] = true
				paths = append(paths, resolved)
			}
		}
	}
	return paths
}

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

func imageTargets(markdown string) []string {
	source := []byte(markdown)
	var targets []string
	_ = ast.Walk(imageMarkdown.Parser().Parse(text.NewReader(source)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Image:
			targets = append(targets, string(n.Destination))
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

func htmlImageSources(raw []byte) []string {
	var sources []string
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
					sources = append(sources, attr.Val)
				}
			}
		}
	}
}

func localImagePath(baseDir, target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") {
		return "", false
	}
	ref, err := url.Parse(target)
	if err != nil {
		return "", false
	}
	if ref.Scheme != "" && !strings.EqualFold(ref.Scheme, "file") {
		return "", false
	}
	base := &url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(baseDir, "ask.md"))}
	resolved := base.ResolveReference(ref)
	if resolved.Host != "" {
		return "", false
	}
	if _, ok := ImageType(resolved.Path); !ok {
		return "", false
	}
	return resolved.Path, true
}
