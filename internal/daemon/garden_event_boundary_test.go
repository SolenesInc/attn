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

func TestGardenSeedEventHandlingHasOneDaemonEntryPoint(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") || name == "garden_events.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "HandleGardenSeedEvent" {
				return true
			}
			offenders = append(offenders, filepath.Base(fset.Position(call.Pos()).String()))
			return true
		})
	}
	if len(offenders) != 0 {
		sort.Strings(offenders)
		t.Fatalf("Garden seed mailbox handling bypasses the validated event consumer at %s", strings.Join(offenders, ", "))
	}
}

func TestLegacyGardenSeedEventNamespaceIsGoneFromDaemonProduction(t *testing.T) {
	legacy := []string{
		"garden.planted", "garden.tended", "garden.parked", "garden.harvested", "garden.withered",
		"garden.replanted", "garden.body_edited", "garden.noted", "garden.artifact.changed",
		"garden.resume_identity_changed", "garden.linked", "garden.unlinked", "garden.harvest_when.changed",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range legacy {
			if strings.Contains(string(body), `"`+event+`"`) {
				t.Errorf("legacy seed event %q remains in %s", event, name)
			}
		}
	}
}
