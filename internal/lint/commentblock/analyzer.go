// Package commentblock reports comment blocks longer than two lines.
package commentblock

import (
	"bytes"
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const MaxLines = 2

var Analyzer = &analysis.Analyzer{
	Name: "commentblock",
	Doc:  "reports comment blocks longer than two lines; directives, cgo preambles, and generated files are exempt",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		tf := pass.Fset.File(file.Pos())
		src, err := pass.ReadFile(tf.Name())
		if err != nil {
			return nil, err
		}
		preamble := cgoPreamble(file)
		for _, group := range file.Comments {
			if group == preamble || hasDirective(group) {
				continue
			}
			for _, block := range splitTrailing(tf, src, group) {
				lines := tf.Line(block.End()) - tf.Line(block.Pos()) + 1
				if lines > MaxLines {
					pass.Reportf(block.Pos(), "comment block spans %d lines; the limit is %d. Compress it or delete it", lines, MaxLines)
				}
			}
		}
	}
	return nil, nil
}

// go/parser groups a trailing comment with full-line comments starting on the next line; those are separate blocks.
func splitTrailing(tf *token.File, src []byte, group *ast.CommentGroup) []*ast.CommentGroup {
	var out []*ast.CommentGroup
	var cur *ast.CommentGroup
	for _, c := range group.List {
		line := tf.Line(c.Pos())
		ownLine := len(bytes.TrimSpace(src[tf.Offset(tf.LineStart(line)):tf.Offset(c.Pos())])) == 0
		if cur == nil || !ownLine || tf.Line(cur.End()) != line-1 {
			cur = &ast.CommentGroup{}
			out = append(out, cur)
		}
		cur.List = append(cur.List, c)
	}
	return out
}

func hasDirective(group *ast.CommentGroup) bool {
	for _, c := range group.List {
		for _, p := range []string{"//go:", "//nolint", "//export ", "//line ", "//sys", "//extern "} {
			if strings.HasPrefix(c.Text, p) {
				return true
			}
		}
	}
	return false
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
