package tendril

import (
	"cmp"
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strconv"
)

var forbiddenBuiltins = map[string]string{
	"complex": "complex numbers are not supported",
	"real":    "complex numbers are not supported",
	"imag":    "complex numbers are not supported",
}

type gateErrors struct {
	fset *token.FileSet
	errs []*Error
}

func (g *gateErrors) add(pos token.Pos, msg string) {
	g.errs = append(g.errs, &Error{Pos: g.fset.Position(pos), Msg: msg})
}

func (g *gateErrors) err() error {
	if len(g.errs) == 0 {
		return nil
	}
	slices.SortFunc(g.errs, func(a, b *Error) int {
		return cmp.Or(cmp.Compare(a.Pos.Line, b.Pos.Line), cmp.Compare(a.Pos.Column, b.Pos.Column))
	})
	errs := make([]error, len(g.errs))
	for i, e := range g.errs {
		errs[i] = e
	}
	return errors.Join(errs...)
}

func checkSyntax(fset *token.FileSet, f *ast.File) error {
	g := &gateErrors{fset: fset}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			if path, _ := strconv.Unquote(n.Path.Value); path == "unsafe" {
				g.add(n.Pos(), `package "unsafe" is not available to scripts`)
			}
		case *ast.BranchStmt:
			if n.Tok == token.GOTO {
				g.add(n.Pos(), "goto is not supported")
			}
		case *ast.FuncDecl:
			if n.Type.TypeParams != nil {
				g.add(n.Pos(), "generic functions are not supported")
			}
		case *ast.TypeSpec:
			if n.TypeParams != nil {
				g.add(n.Pos(), "generic types are not supported")
			}
		case *ast.StructType:
			for _, field := range n.Fields.List {
				if len(field.Names) == 0 {
					g.add(field.Pos(), "embedded fields are not supported")
				}
			}
		}
		return true
	})
	return g.err()
}

func checkTypes(fset *token.FileSet, info *types.Info) error {
	g := &gateErrors{fset: fset}
	for e, tv := range info.Types {
		if msg := unsupportedType(tv.Type); msg != "" {
			g.add(e.Pos(), msg)
		}
	}
	for id, obj := range info.Defs {
		if obj == nil {
			continue
		}
		if msg := unsupportedType(obj.Type()); msg != "" {
			g.add(id.Pos(), msg)
		}
	}
	for id, obj := range info.Uses {
		if b, ok := obj.(*types.Builtin); ok {
			if why, bad := forbiddenBuiltins[b.Name()]; bad {
				g.add(id.Pos(), why)
			}
		}
	}
	return g.err()
}

func unsupportedType(t types.Type) string {
	var msg string
	var walk func(t types.Type, depth int)
	walk = func(t types.Type, depth int) {
		if t == nil || msg != "" || depth > 8 {
			return
		}
		switch u := types.Unalias(t).(type) {
		case *types.Named:
			if u.Obj().Pkg() == nil {
				return
			}
			walk(u.Underlying(), depth+1)
		case *types.Basic:
			switch {
			case u.Info()&types.IsComplex != 0:
				msg = "complex numbers are not supported"
			case u.Kind() == types.UnsafePointer:
				msg = "unsafe pointers are not supported"
			}
		case *types.Chan:
			walk(u.Elem(), depth+1)
		case *types.Array:
			if elems := flatLen(u); elems > maxAggregateSlots {
				msg = "arrays larger than 1<<20 elements are not supported"
				return
			}
			walk(u.Elem(), depth+1)
		case *types.Pointer:
			walk(u.Elem(), depth+1)
		case *types.Slice:
			walk(u.Elem(), depth+1)
		case *types.Map:
			if b, ok := u.Key().Underlying().(*types.Basic); !ok || b.Info()&types.IsOrdered == 0 && b.Kind() != types.Bool {
				msg = "map keys must be strings, numbers or booleans"
				return
			}
			walk(u.Elem(), depth+1)
		case *types.Struct:
			for i := range u.NumFields() {
				walk(u.Field(i).Type(), depth+1)
			}
		case *types.Signature:
			for v := range u.Params().Variables() {
				walk(v.Type(), depth+1)
			}
			for v := range u.Results().Variables() {
				walk(v.Type(), depth+1)
			}
		case *types.Tuple:
			for v := range u.Variables() {
				walk(v.Type(), depth+1)
			}
		}
	}
	walk(t, 0)
	return msg
}

const maxAggregateSlots = 1 << 20

func flatLen(t types.Type) int64 {
	switch u := t.Underlying().(type) {
	case *types.Array:
		n := flatLen(u.Elem())
		if u.Len() > 0 && n > maxAggregateSlots/u.Len() {
			return maxAggregateSlots + 1
		}
		return u.Len() * n
	case *types.Struct:
		var n int64
		for i := range u.NumFields() {
			n += flatLen(u.Field(i).Type())
		}
		return n
	}
	return 1
}
