package ask

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/victorarias/attn/internal/machinename"
	"github.com/victorarias/attn/internal/protocol"
)

// Place is where an ask's markdown was written: the machine id of the asking session, and the folder its
// relative links resolve against, as its images do.
type Place struct {
	Host    string
	BaseDir string
}

var (
	scheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	webURL = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)
)

// References lists the files markdown's links name and the folders named after a code block's language, in
// reading order. A link or a code block that names a machine is on that machine; any other local link is on
// place's. Paths that start with ~ stay as written. It refuses a machine name no machine or several hold.
func References(markdown string, machines []machinename.Machine, place Place) ([]protocol.AskReference, error) {
	source := []byte(strings.ReplaceAll(markdown, "\r\n", "\n"))
	lineAt := func(offset int) int { return strings.Count(string(source[:offset]), "\n") + 1 }
	var found []protocol.AskReference
	var refused error
	_ = ast.Walk(imageMarkdown.Parse(text.NewReader(source)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Link:
			written := string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(n.Destination))))
			reference, ok, err := fileReference(written, machines, place)
			if err != nil {
				refused = &LineError{Line: lineAt(nodeOffset(n, source)), Message: err.Error()}
				return ast.WalkStop, nil
			}
			if ok {
				reference.Line = lineAt(nodeOffset(n, source))
				found = append(found, reference)
			}
		case *ast.FencedCodeBlock:
			if n.Info == nil {
				return ast.WalkContinue, nil
			}
			fields := strings.Fields(string(n.Info.Segment.Value(source)))
			if len(fields) < 2 || webURL.MatchString(fields[1]) {
				return ast.WalkContinue, nil
			}
			line := lineAt(n.Info.Segment.Start)
			address, ok, err := machinename.ReadAddress(fields[1], machines)
			if err != nil {
				refused = &LineError{Line: line, Message: err.Error()}
				return ast.WalkStop, nil
			}
			if ok {
				found = append(found, protocol.AskReference{Kind: protocol.AskReferenceKindCommand, Text: fields[1], Host: protocol.Ptr(address.Machine.ID), Path: cleanPath(address.Rest), Line: line})
			}
		}
		return ast.WalkContinue, nil
	})
	return found, refused
}

func fileReference(written string, machines []machinename.Machine, place Place) (protocol.AskReference, bool, error) {
	target := strings.TrimSpace(written)
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") || webURL.MatchString(target) {
		return protocol.AskReference{}, false, nil
	}
	host, rest := place.Host, target
	address, addressed, err := machinename.ReadAddress(target, machines)
	switch {
	case err != nil:
		return protocol.AskReference{}, false, err
	case addressed:
		host, rest = address.Machine.ID, address.Rest
	case scheme.MatchString(target):
		return protocol.AskReference{}, false, nil
	}
	if cut := strings.IndexAny(rest, "?#"); cut >= 0 {
		rest = rest[:cut]
	}
	if decoded, err := url.PathUnescape(rest); err == nil {
		rest = decoded
	}
	if rest == "" {
		return protocol.AskReference{}, false, nil
	}
	if !filepath.IsAbs(rest) && !strings.HasPrefix(rest, "~") {
		if place.BaseDir == "" {
			return protocol.AskReference{}, false, nil
		}
		rest = filepath.Join(place.BaseDir, rest)
	}
	return protocol.AskReference{Kind: protocol.AskReferenceKindFile, Text: written, Host: protocol.Ptr(host), Path: cleanPath(rest)}, true, nil
}

func cleanPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return "~" + strings.TrimSuffix(filepath.Clean("/"+strings.TrimPrefix(path, "~")), "/")
	}
	return filepath.Clean(path)
}

func nodeOffset(node ast.Node, source []byte) int {
	offset := -1
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := n.(*ast.Text); ok && entering {
			offset = t.Segment.Start
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if offset >= 0 {
		return offset
	}
	for n := node; n != nil; n = n.Parent() {
		if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
			return n.Lines().At(0).Start
		}
	}
	return 0
}
