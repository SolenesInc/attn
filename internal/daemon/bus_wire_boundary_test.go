package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var hubSendMethods = map[string]bool{
	"Broadcast":                    true,
	"BroadcastValue":               true,
	"BroadcastRawText":             true,
	"SendRawTextToMatchingClients": true,
	"SendValueToMatchingClients":   true,
}

var daemonSendHelpers = map[string]bool{
	"broadcastMessage":      true,
	"broadcastRawWSMessage": true,
}

// Adding an entry is a design decision, not a formality: it says this traffic is
// not a state change any consumer could subscribe to.
var wireSenderExceptions = map[string]string{
	"buildWireProjections": "the projection table itself",

	"broadcastMessage":      "the generic value sender every projection uses",
	"broadcastRawWSMessage": "the remote relay: the fact was already published on the remote daemon's bus, and re-publishing it locally would duplicate it",

	"broadcastFsChanged":   "filesystem change bursts, coalesced per watcher rather than per file",
	"broadcastTileContent": "workspace tile content bytes, sent only to the clients subscribed to that tile",
}

func TestWireTrafficComesFromProjections(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := fn.Name.Name
			if strings.HasPrefix(caller, "project") || wireSenderExceptions[caller] != "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case isHubSend(sel), isDaemonSendHelper(sel):
					offenders = append(offenders, filepath.Base(fset.Position(call.Pos()).String())+"\t"+caller)
				}
				return true
			})
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these functions push to WebSocket clients without going through a fact:\n\t%s\n\n"+
			"Publish a fact naming the entity that changed and move the push into a projection "+
			"(see internal/daemon/bus.go). If the traffic genuinely is not a state change, add the "+
			"function to wireSenderExceptions with the reason.",
			strings.Join(offenders, "\n\t"))
	}
}

func isHubSend(sel *ast.SelectorExpr) bool {
	if !hubSendMethods[sel.Sel.Name] {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && inner.Sel.Name == "wsHub"
}

func isDaemonSendHelper(sel *ast.SelectorExpr) bool {
	if !daemonSendHelpers[sel.Sel.Name] {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "d"
}
