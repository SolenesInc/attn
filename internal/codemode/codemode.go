package codemode

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/tendril"
)

//go:embed api/attn.go
var apiSource string

var Limits = tendril.Limits{Fuel: 50_000_000, Memory: 256 << 20, Depth: 1000, Goroutines: 64}

var env = sync.OnceValues(func() (*tendril.Env, error) {
	attn := tendril.NewPackage("attn").Source(Schema()).Concurrent().
		Type("Seed", Seed{}).
		Type("Note", Note{}).
		Type("PlantOptions", PlantOptions{}).
		Type("Agent", Agent{}).
		Func("Me", me).
		Func("Plant", plant).
		Func("Show", show).
		Func("Seeds", seeds).
		Func("Search", search).
		Func("AddNote", addNote).
		Func("Tend", tend).
		Func("Park", transition("park")).
		Func("Harvest", transition("harvest")).
		Func("Agents", agents).
		Func("Message", message)
	return tendril.NewEnv(append(tendril.Std(), attn)...)
})

func Schema() string {
	_, rest, _ := strings.Cut(apiSource, "//go:build ignore\n\n")
	return rest
}

func Check(filename, src string) (*tendril.Program, error) {
	e, err := env()
	if err != nil {
		return nil, err
	}
	return e.Compile(filename, src)
}

func Run(ctx context.Context, caller Caller, filename, src string, stdout, stderr io.Writer) (tendril.Stats, error) {
	prog, err := Check(filename, src)
	if err != nil {
		return tendril.Stats{}, err
	}
	return prog.Main(WithCaller(ctx, caller), Limits, stdout, stderr)
}

func Doc(name string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "attn.go", Schema(), parser.ParseComments)
	if err != nil {
		return "", err
	}
	for _, d := range f.Decls {
		var node ast.Node
		var doc *ast.CommentGroup
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == name {
				node = d
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
					node, doc = &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{ts}}, d.Doc
				}
			}
		}
		if node == nil {
			continue
		}
		var b bytes.Buffer
		if doc != nil {
			for _, c := range doc.List {
				b.WriteString(c.Text + "\n")
			}
		}
		if err := format.Node(&b, fset, node); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	return "", fmt.Errorf("attn has no %s", name)
}

func Stub() (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "attn.go", Schema(), parser.ParseComments)
	if err != nil {
		return "", err
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body == nil {
			fd.Body = &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
				Fun:  ast.NewIdent("panic"),
				Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"attn: runs only inside attn code"`}},
			}}}}
		}
	}
	var b bytes.Buffer
	if err := format.Node(&b, fset, f); err != nil {
		return "", err
	}
	return b.String(), nil
}
