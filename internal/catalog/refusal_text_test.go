package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var refusalConstructors = map[string]bool{
	"protocol.Refuse": true,
	"command.Refuse":  true,
	"command.Usage":   true,
	"Refuse":          true,
	"Usage":           true,
}

var plainErrorConstructors = map[string]bool{"fmt.Errorf": true, "errors.New": true}

var plainErrorPackages = []string{"internal/daemon", "internal/garden"}

var knownPlainErrors = map[string]bool{
	"internal/daemon/agent_close.go: nobody is tending %s, so there is nobody to reach; leave it on the log instead: attn seed note %s -m \"…\"":                                                                                                                                 true,
	"internal/daemon/agent_close.go: %s is tended by %s, who is not in an attn session; message them by name: attn agent msg %s \"…\"":                                                                                                                                           true,
	"internal/daemon/asks.go: nobody is tending %s, and an ask is about a seed you tend; tend it first (attn seed tend %s), or ask without --seed":                                                                                                                               true,
	"internal/daemon/code_nav.go: the review names no absolute repository in its frontmatter. Run `attn review start <document>` from the repository under review":                                                                                                               true,
	"internal/daemon/code_nav.go: %s did not answer within %s (codeNavRequestTripwire); `attn nav status` shows what it is doing":                                                                                                                                                true,
	"internal/daemon/code_nav.go: nothing is linked as %s in %s; `attn nav status` lists what is":                                                                                                                                                                                true,
	"internal/daemon/code_nav.go: no definition of %s is set for %s; `attn nav status` lists them":                                                                                                                                                                               true,
	"internal/daemon/delegation_preferences.go: enabling attn-workflow needs the explicit install: attn delegate roles install, or Add Attn roles in Settings > Delegation":                                                                                                      true,
	"internal/daemon/asks.go: you are retired, so the user takes no new asks from you; file your letter with `attn handoff`":                                                                                                                                                     true,
	"internal/daemon/asks.go: asks are off for this session because it launched without them; ask in the conversation instead, and run `attn turn open` if a notification started your run":                                                                                      true,
	"internal/daemon/asks.go: %s is not your ask. Say why you withdraw it, and attn tells its asker: attn ask close %s -m \"<why>\"":                                                                                                                                             true,
	"internal/daemon/crew_handoff.go: %s's session has filed no letter yet, so there is no turnover to retry — write one with `attn handoff -m \"<your letter>\"`":                                                                                                               true,
	"internal/daemon/crew_handoff.go: %s's filed letter is recorded at %s but is not readable there (%v); file this session's letter again with `attn handoff -m \"<your letter>\"`":                                                                                             true,
	"internal/daemon/vents.go: vent %s was already sent to attn's maintainers, so it stays an attn vent":                                                                                                                                                                         true,
	"internal/daemon/vents.go: the limit is 1 vent per turn, and this turn already has %s. Withdraw it (attn vent withdraw %s) and send one vent that covers both":                                                                                                               true,
	"internal/daemon/codex_update.go: your shell %s gives attn no command marks, so attn can't run Codex's update and tell when it finishes; run attn codex update in a terminal instead":                                                                                        true,
	"internal/daemon/conversation_ledger.go: %w; unlinked crew letters: %s; letters are unchanged and attn's copy remains; fix the error and retry `attn conversation forget conversation:%s --unlink-crew-letters`":                                                             true,
	"internal/daemon/daemon.go: Port %s is also used by attn instance: %s. ":                                                                                                                                                                                                     true,
	"internal/daemon/daemon.go: Run `attn instance claim-port --instance <name>` for whichever of this instance or those has no ws-port yet, then reinstall its app. ":                                                                                                           true,
	"internal/daemon/daemon.go: No other attn instance on this machine uses that port, so the usual owner is a daemon for the same instance running somewhere else — ":                                                                                                           true,
	"internal/daemon/crew_handoff.go: this session is not a crew member's session, so it has no letter to file. A crew handoff is a member's own letter to its successor; the note you write for whoever tends a piece of work next is `attn seed note <id> -m \"…\" --handoff`": true,
	"internal/daemon/senders.go: profile %q has no paired sender named or with id %q; attn send senders lists them":                                                                                                                                                              true,
	"internal/daemon/senders.go: sending as the user needs a paired sender's token; pair this program once with attn send pair --name <name>, which the user allows in the attn app":                                                                                             true,
	"internal/daemon/senders.go: the sender token matches no paired sender; it was unpaired or never allowed. Pair again with attn send pair --name <name>":                                                                                                                      true,
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

type refusalLiteral struct {
	where   string
	key     string
	text    string
	refusal bool
}

func TestRefusalsNameCommandsByMention(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..")
	literals := refusalLiterals(t, root)
	if len(literals) == 0 {
		t.Fatal("found no refusal text to check")
	}
	seen := map[string]bool{}
	for _, lit := range literals {
		sample := formatVerb.ReplaceAllString(lit.text, "s-7k3f9m")
		if !lit.refusal {
			if _, plain := c.PlainCommand(sample); plain {
				seen[lit.key] = true
				if !knownPlainErrors[lit.key] {
					t.Errorf("%s: an error that names a command is a refusal: build it with protocol.Refuse or command.Refuse and write the command as {attn ...}: %q", lit.where, lit.text)
				}
			}
			if strings.Contains(lit.text, "{attn ") {
				t.Errorf("%s: only a refusal renders {attn ...}; build this error with protocol.Refuse or command.Refuse: %q", lit.where, lit.text)
			}
			continue
		}
		for _, problem := range c.MentionProblems(sample) {
			t.Errorf("%s: %s", lit.where, problem)
		}
	}
	for key := range knownPlainErrors {
		if !seen[key] {
			t.Errorf("%s no longer names a command in plain words; delete it from knownPlainErrors", key)
		}
	}
}

func refusalLiterals(t *testing.T, root string) []refusalLiteral {
	t.Helper()
	var out []refusalLiteral
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		plainScope := false
		for _, pkg := range plainErrorPackages {
			plainScope = plainScope || filepath.ToSlash(filepath.Dir(rel)) == pkg
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		collect := func(expr ast.Expr, refusal bool) {
			ast.Inspect(expr, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
				}
				out = append(out, refusalLiteral{where: fset.Position(lit.Pos()).String(), key: rel + ": " + text, text: text, refusal: refusal})
				return true
			})
		}
		refusalTypes := map[string]bool{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Refusal" && fn.Recv != nil {
				refusalTypes[receiverType(fn)] = true
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Recv != nil && (node.Name.Name == "Error" || node.Name.Name == "Mentioned") && refusalTypes[receiverType(node)] {
					ast.Inspect(node.Body, func(m ast.Node) bool {
						if call, ok := m.(*ast.CallExpr); ok && calleeName(call.Fun) == "fmt.Sprintf" {
							for _, arg := range call.Args {
								collect(arg, true)
							}
						}
						return true
					})
				}
			case *ast.CallExpr:
				name := calleeName(node.Fun)
				switch {
				case refusalConstructors[name]:
					for _, arg := range node.Args {
						collect(arg, true)
					}
				case plainScope && plainErrorConstructors[name]:
					for _, arg := range node.Args {
						collect(arg, false)
					}
				}
			case *ast.CompositeLit:
				if calleeName(node.Type) == "command.Refusal" || calleeName(node.Type) == "Refusal" {
					for _, elt := range node.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							collect(kv.Value, true)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func calleeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok {
			return pkg.Name + "." + e.Sel.Name
		}
	}
	return ""
}

func receiverType(fn *ast.FuncDecl) string {
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
