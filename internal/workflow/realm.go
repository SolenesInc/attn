package workflow

import (
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

type Meta struct {
	Name        string
	Description string
	WhenToUse   string
	Phases      []string
	Model       string
}

// goja's New() installs no fs, net, process, require, crypto, performance, setTimeout or console, and nothing else is injected, so the realm exposes only the host surface plus the shims below.
func installDeterminismBans(vm *goja.Runtime) error {
	throw := func(api, substitute string) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			panic(vm.ToValue((&ErrDeterminismBan{API: api, Substitute: substitute}).Error()))
		}
	}

	dateObj := vm.Get("Date")
	if dateObj != nil {
		if obj, ok := dateObj.(*goja.Object); ok {
			if err := obj.Set("now", throw(
				"Date.now()",
				"Pass an explicit timestamp through args instead (the workflow runtime forbids reading the wall clock).",
			)); err != nil {
				return err
			}
		}
	}

	if err := banArglessNewDate(vm); err != nil {
		return err
	}

	mathObj := vm.Get("Math")
	if mathObj != nil {
		if obj, ok := mathObj.(*goja.Object); ok {
			if err := obj.Set("random", throw(
				"Math.random()",
				"Derive any needed randomness deterministically from args (e.g. a seed passed in) — the workflow runtime forbids non-deterministic randomness.",
			)); err != nil {
				return err
			}
		}
	}

	// Belt-and-suspenders: performance/crypto are absent in this build, so these are no-ops today.
	for _, name := range []string{"performance", "crypto"} {
		if v := vm.Get(name); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			if err := vm.Set(name, goja.Undefined()); err != nil {
				return err
			}
		}
	}

	return nil
}

// In JS so instance prototype/inheritance is preserved exactly.
func banArglessNewDate(vm *goja.Runtime) error {
	banMsg := (&ErrDeterminismBan{
		API:        "new Date()",
		Substitute: "Construct dates from an explicit argument, e.g. new Date(timestamp) — argless new Date() and any Date() call read the wall clock and are forbidden.",
	}).Error()

	if err := vm.Set("__wfDateBanMsg", banMsg); err != nil {
		return err
	}
	// The IIFE captures the ban thrower so the text survives dropping the temporary global.
	// A plain `Date()` call ignores its args per spec and returns the current time, so it is banned with OR without args.
	const shim = `(function(){
		var OrigDate = Date;
		function WfDate() {
			// Plain function call Date(...) (no new): always reads the wall clock.
			if (new.target === undefined) {
				return __wfThrowDateBan();
			}
			// Argless construction new Date(): reads the wall clock.
			if (arguments.length === 0) {
				return __wfThrowDateBan();
			}
			// new Date(<arg...>) is deterministic. Reflect.construct keeps
			// subclass/new.target semantics and forwards args.
			return Reflect.construct(OrigDate, arguments, new.target);
		}
		WfDate.prototype = OrigDate.prototype;
		WfDate.parse = OrigDate.parse;
		WfDate.UTC = OrigDate.UTC;
		// Date.now is already replaced with the banned throw before this runs; copy it.
		WfDate.now = OrigDate.now;
		Date = WfDate;
	})();`
	if err := vm.Set("__wfThrowDateBan", func(goja.FunctionCall) goja.Value {
		panic(vm.ToValue(banMsg))
	}); err != nil {
		return err
	}
	if _, err := vm.RunString(shim); err != nil {
		return err
	}
	// Safe to drop: __wfThrowDateBan stays reachable through WfDate's closure.
	return vm.Set("__wfDateBanMsg", goja.Undefined())
}

// goja does not parse ES module syntax, and `export const meta = {...}` is authoring sugar.
func stripExport(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "export ") {
			indent := line[:len(line)-len(trimmed)]
			b.WriteString(indent)
			b.WriteString(strings.TrimPrefix(trimmed, "export "))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// `meta` must be the FIRST statement and its initializer a PURE object literal; src must already have `export` stripped.
func parseMeta(src string) (*Meta, error) {
	body, err := topLevelStatements(src)
	if err != nil {
		return nil, nil
	}
	if len(body) == 0 {
		return nil, nil
	}

	declIdx, decl := findMetaDecl(body)
	if decl == nil {
		return nil, nil
	}
	if declIdx != 0 {
		return nil, &ErrMeta{Reason: "meta must be the first statement in the workflow"}
	}

	objLit, ok := decl.Initializer.(*ast.ObjectLiteral)
	if !ok {
		return nil, &ErrMeta{Reason: "meta must be a plain object literal (no expressions or function calls)"}
	}

	m := &Meta{}
	for _, prop := range objLit.Value {
		pk, ok := prop.(*ast.PropertyKeyed)
		if !ok {
			return nil, &ErrMeta{Reason: "meta may only contain plain key/value pairs"}
		}
		if pk.Computed {
			return nil, &ErrMeta{Reason: "meta keys must be literal (computed keys are not allowed)"}
		}
		key, ok := literalKey(pk.Key)
		if !ok {
			return nil, &ErrMeta{Reason: "meta keys must be string/identifier literals"}
		}
		switch key {
		case "name":
			s, ok := stringValue(pk.Value)
			if !ok {
				return nil, &ErrMeta{Reason: "meta.name must be a string literal"}
			}
			m.Name = s
		case "description":
			s, ok := stringValue(pk.Value)
			if !ok {
				return nil, &ErrMeta{Reason: "meta.description must be a string literal"}
			}
			m.Description = s
		case "whenToUse":
			s, ok := stringValue(pk.Value)
			if !ok {
				return nil, &ErrMeta{Reason: "meta.whenToUse must be a string literal"}
			}
			m.WhenToUse = s
		case "model":
			s, ok := stringValue(pk.Value)
			if !ok {
				return nil, &ErrMeta{Reason: "meta.model must be a string literal"}
			}
			m.Model = s
		case "phases":
			arr, ok := pk.Value.(*ast.ArrayLiteral)
			if !ok {
				return nil, &ErrMeta{Reason: "meta.phases must be an array literal of strings"}
			}
			for _, el := range arr.Value {
				s, ok := stringValue(el)
				if !ok {
					return nil, &ErrMeta{Reason: "meta.phases entries must be string literals"}
				}
				m.Phases = append(m.Phases, s)
			}
		default:
		}
	}
	return m, nil
}

// Parsed inside the same async wrapper the engine runs, so top-level await/return parse.
func topLevelStatements(src string) ([]ast.Statement, error) {
	wrapped := "(async function __wf__(){\n" + src + "\n})()"
	prog, err := parser.ParseFile(nil, "workflow.js", wrapped, 0)
	if err != nil {
		return nil, err
	}
	if len(prog.Body) == 0 {
		return nil, nil
	}
	exprStmt, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil, nil
	}
	call, ok := exprStmt.Expression.(*ast.CallExpression)
	if !ok {
		return nil, nil
	}
	fn, ok := call.Callee.(*ast.FunctionLiteral)
	if !ok || fn.Body == nil {
		return nil, nil
	}
	return fn.Body.List, nil
}

func findMetaDecl(body []ast.Statement) (int, *ast.Binding) {
	for i, st := range body {
		var bindings []*ast.Binding
		switch d := st.(type) {
		case *ast.LexicalDeclaration:
			bindings = d.List
		case *ast.VariableStatement:
			bindings = d.List
		default:
			continue
		}
		for _, b := range bindings {
			if id, ok := b.Target.(*ast.Identifier); ok && id.Name.String() == "meta" {
				return i, b
			}
		}
	}
	return -1, nil
}

// goja parses a string literal and a bareword identifier both as StringLiteral.
func literalKey(e ast.Expression) (string, bool) {
	switch k := e.(type) {
	case *ast.StringLiteral:
		return k.Value.String(), true
	case *ast.Identifier:
		return k.Name.String(), true
	default:
		return "", false
	}
}

func stringValue(e ast.Expression) (string, bool) {
	sl, ok := e.(*ast.StringLiteral)
	if !ok {
		return "", false
	}
	return sl.Value.String(), true
}
