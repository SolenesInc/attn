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
	"print":   "print writes to the host's stderr; return values instead",
	"println": "println writes to the host's stderr; return values instead",
	"recover": "recover is not available; runtime errors end the run",
	"close":   "channels are not available in scripts",
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
		case *ast.GoStmt:
			g.add(n.Pos(), "go statements are not available in scripts")
		case *ast.SelectStmt, *ast.SendStmt, *ast.ChanType:
			g.add(n.Pos(), "channels are not available in scripts")
		case *ast.DeferStmt:
			g.add(n.Pos(), "defer is not supported")
		case *ast.LabeledStmt:
			g.add(n.Pos(), "labels are not supported")
		case *ast.BranchStmt:
			if n.Label != nil || n.Tok == token.GOTO || n.Tok == token.FALLTHROUGH {
				g.add(n.Pos(), n.Tok.String()+" with labels, goto and fallthrough are not supported")
			}
		case *ast.ArrayType:
			if n.Len != nil {
				g.add(n.Pos(), "arrays are not supported; use slices")
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
		case *ast.UnaryExpr:
			switch n.Op {
			case token.AND:
				if _, ok := ast.Unparen(n.X).(*ast.CompositeLit); !ok {
					g.add(n.Pos(), "& is only supported on composite literals")
				}
			case token.ARROW:
				g.add(n.Pos(), "channels are not available in scripts")
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
		if fn, ok := obj.(*types.Func); ok {
			if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
				if ptr, ok := recv.Type().(*types.Pointer); ok {
					if _, isStruct := ptr.Elem().Underlying().(*types.Struct); !isStruct {
						g.add(id.Pos(), "pointer receivers are only supported on struct types")
					}
				}
			}
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
			msg = "channels are not available in scripts"
		case *types.Array:
			msg = "arrays are not supported; use slices"
		case *types.Pointer:
			if _, ok := u.Elem().Underlying().(*types.Struct); !ok {
				msg = "pointers are only supported to struct types"
				return
			}
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
