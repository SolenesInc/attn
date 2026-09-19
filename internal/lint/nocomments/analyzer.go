package nocomments

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "nocomments",
	Doc:  "reports every comment; tool directives, cgo preambles, example output and generated files are exempt",
	Run:  run,
}

var (
	directive    = regexp.MustCompile(`^//(go:|line |export |extern |sys(nb)? |nolint|lint:| ?\+build)`)
	outputPrefix = regexp.MustCompile(`(?i)^[[:space:]]*(unordered )?output:`)
)

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		preamble := cgoPreamble(file)
		exampleOutputs := exampleOutputGroups(file)
		for _, group := range file.Comments {
			if group == preamble || exampleOutputs[group] {
				continue
			}
			if prose := firstProse(group); prose != nil {
				pass.Reportf(prose.Pos(), "comment found: this repository takes no prose comments. Say it with names and structure, or delete it")
			}
		}
	}
	return nil, nil
}

func firstProse(group *ast.CommentGroup) *ast.Comment {
	for _, c := range group.List {
		if !directive.MatchString(c.Text) {
			return c
		}
	}
	return nil
}

func exampleOutputGroups(file *ast.File) map[*ast.CommentGroup]bool {
	groups := map[*ast.CommentGroup]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Example") || len(fn.Type.Params.List) != 0 || fn.Type.Results != nil {
			continue
		}
		var last *ast.CommentGroup
		for _, group := range file.Comments {
			if fn.Body.Pos() < group.Pos() && group.End() < fn.Body.End() {
				last = group
			}
		}
		if last != nil && outputPrefix.MatchString(last.Text()) {
			groups[last] = true
		}
	}
	return groups
}

func cgoPreamble(file *ast.File) *ast.CommentGroup {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gen.Specs {
			if imp, ok := spec.(*ast.ImportSpec); ok && imp.Path.Value == `"C"` {
				return gen.Doc
			}
		}
	}
	return nil
}
