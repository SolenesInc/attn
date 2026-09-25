package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

const (
	maxDepth       = 40
	maxSpans       = 8000
	maxSourceLines = 200
)

type span struct {
	ID       int     `json:"id"`
	Fn       string  `json:"fn,omitempty"`
	Label    string  `json:"label,omitempty"`
	Kind     string  `json:"kind"`
	Site     string  `json:"site,omitempty"`
	Ran      string  `json:"ran,omitempty"`
	Ref      int     `json:"ref,omitempty"`
	Note     string  `json:"note,omitempty"`
	Weight   int     `json:"w"`
	Children []*span `json:"c,omitempty"`
}

type funcInfo struct {
	Name  string `json:"name"`
	Full  string `json:"full"`
	Pkg   string `json:"pkg"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Src   string `json:"src"`
	Trunc bool   `json:"trunc,omitempty"`
}

type flowData struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Command  string `json:"command"`
	Scenario string `json:"scenario,omitempty"`
	Spans    int    `json:"spans"`
	Ran      int    `json:"ran"`
	Root     *span  `json:"root"`
}

type expansion struct {
	fn  *ssa.Function
	ran string
}

type flowBuilder struct {
	p         *program
	consumers []busConsumer
	cov       *coverage
	funcs     map[string]*funcInfo
	funcIDs   map[*ssa.Function]string
	expanded  map[expansion]int
	nextID    int
	ran       int
}

func buildFlow(p *program, dispatch map[string][]commandRoute, consumers []busConsumer, cov *coverage, spec flowSpec, funcs map[string]*funcInfo) (flowData, error) {
	routes := dispatch[spec.Command]
	if len(routes) == 0 {
		return flowData{}, fmt.Errorf("command %q has no dispatch case", spec.Command)
	}
	b := &flowBuilder{p: p, consumers: consumers, cov: cov, funcs: funcs, funcIDs: map[*ssa.Function]string{}, expanded: map[expansion]int{}}
	for fn, id := range existingFuncIDs(p, funcs) {
		b.funcIDs[fn] = id
	}

	root := b.newSpan("flow")
	root.Label = spec.Title
	root.Ran = b.rootRan()

	var crossings []*span
	for _, route := range routes {
		crossing := b.newSpan("boundary")
		crossing.Label = fmt.Sprintf("%s over the %s", route.Command, route.Transport)
		crossing.Note = "dispatched by " + route.Switch
		crossing.Ran = root.Ran
		crossing.Children = []*span{b.enter(route.Handler, "entry", "", root.Ran, nil)}
		crossings = append(crossings, crossing)
	}

	if spec.Origin != "" {
		originFn := p.byName[spec.Origin]
		if originFn == nil {
			return flowData{}, fmt.Errorf("origin %s not found", spec.Origin)
		}
		origin := b.enter(originFn, "entry", "", b.entryRan(originFn, root.Ran), nil)
		origin.Children = append(origin.Children, crossings...)
		root.Children = []*span{origin}
	} else {
		root.Children = crossings
	}
	weigh(root)

	flow := flowData{
		Name: spec.Name, Title: spec.Title, Summary: spec.Summary, Command: spec.Command,
		Spans: b.nextID, Ran: b.ran, Root: root,
	}
	if cov != nil {
		flow.Scenario = cov.Test
	}
	return flow, nil
}

func existingFuncIDs(p *program, funcs map[string]*funcInfo) map[*ssa.Function]string {
	out := map[*ssa.Function]string{}
	for id, info := range funcs {
		if fn := p.byName[info.Full]; fn != nil {
			out[fn] = id
		}
	}
	return out
}

func (b *flowBuilder) rootRan() string {
	if b.cov == nil {
		return ""
	}
	return ranYes.String()
}

func (b *flowBuilder) newSpan(kind string) *span {
	b.nextID++
	return &span{ID: b.nextID, Kind: kind}
}

func (b *flowBuilder) enter(fn *ssa.Function, kind, site, ran string, stack []*ssa.Function) *span {
	s := b.newSpan(kind)
	s.Fn = b.funcID(fn)
	s.Site = site
	s.Ran = ran
	if ran == ranYes.String() {
		b.ran++
	}
	for _, onStack := range stack {
		if onStack == fn {
			s.Kind = "cycle"
			return s
		}
	}
	key := expansion{fn, ran}
	if first, ok := b.expanded[key]; ok {
		s.Ref = first
		return s
	}
	if len(stack) >= maxDepth || b.nextID >= maxSpans {
		s.Note = "not expanded: flow size limit"
		return s
	}
	b.expanded[key] = s.ID
	s.Children = b.callees(fn, ran, append(stack, fn))
	return s
}

type callSite struct {
	site    ssa.CallInstruction
	callees []*ssa.Function
}

func (b *flowBuilder) callees(fn *ssa.Function, parentRan string, stack []*ssa.Function) []*span {
	node := b.p.graph.Nodes[fn]
	if node == nil {
		return nil
	}
	sites := map[ssa.CallInstruction]*callSite{}
	var order []*callSite
	for _, edge := range node.Out {
		if edge.Site == nil {
			continue
		}
		cs, ok := sites[edge.Site]
		if !ok {
			cs = &callSite{site: edge.Site}
			sites[edge.Site] = cs
			order = append(order, cs)
		}
		cs.callees = append(cs.callees, edge.Callee.Func)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].site.Pos() < order[j].site.Pos() })

	var out []*span
	for _, cs := range order {
		out = append(out, b.visitSite(cs, parentRan, stack)...)
	}
	return out
}

func (b *flowBuilder) visitSite(cs *callSite, parentRan string, stack []*ssa.Function) []*span {
	common := cs.site.Common()
	site := b.p.relPos(cs.site.Pos())
	siteRan := b.siteRan(cs.site.Pos(), parentRan)

	if facts, ok := b.publishedFacts(common); ok {
		var out []*span
		for _, fact := range facts {
			out = append(out, b.busSpan(fact, site, siteRan, stack))
		}
		return out
	}

	kind := "call"
	switch {
	case isGo(cs.site):
		kind = "go"
	case isDefer(cs.site):
		kind = "defer"
	case common.StaticCallee() == nil:
		kind = "dynamic"
	}

	var own []*ssa.Function
	for _, callee := range cs.callees {
		if origin := callee.Origin(); origin != nil {
			callee = origin
		}
		if isOwn(callee) && !isPlumbing(callee) {
			own = append(own, callee)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].String() < own[j].String() })

	var out []*span
	for _, callee := range own {
		calleeRan := siteRan
		if kind == "dynamic" && siteRan == ranYes.String() {
			calleeRan = b.entryRan(callee, siteRan)
		}
		s := b.enter(callee, kind, site, calleeRan, stack)
		if kind == "dynamic" && len(own) > 1 {
			s.Note = fmt.Sprintf("one of %d possible targets", len(own))
		}
		out = append(out, s)
	}
	return out
}

func isGo(site ssa.CallInstruction) bool {
	_, ok := site.(*ssa.Go)
	return ok
}

func isDefer(site ssa.CallInstruction) bool {
	_, ok := site.(*ssa.Defer)
	return ok
}

func isPlumbing(fn *ssa.Function) bool {
	name := fn.Name()
	if fn.Signature.Recv() != nil && (name == "Error" || name == "String") {
		return true
	}
	return name == "logf" || strings.HasSuffix(fn.String(), "internal/protocol.Ptr") ||
		strings.HasSuffix(fn.String(), "internal/protocol.Deref")
}

func (b *flowBuilder) publishedFacts(common *ssa.CallCommon) ([]string, bool) {
	callee := common.StaticCallee()
	if callee == nil {
		return nil, false
	}
	full := callee.String()
	var nameArg int
	switch {
	case strings.HasSuffix(full, "internal/daemon.Daemon).publishFact"):
		nameArg = 1
	case strings.HasSuffix(full, "internal/bus.Bus).Publish"):
		nameArg = 1
	case strings.HasSuffix(full, "internal/bus.Bus).PublishFrom"):
		nameArg = 2
	default:
		return nil, false
	}
	if len(common.Args) <= nameArg {
		return nil, false
	}
	facts := constStrings(common.Args[nameArg], map[ssa.Value]bool{})
	if len(facts) == 0 {
		return []string{"(fact name computed at runtime)"}, true
	}
	sort.Strings(facts)
	return facts, true
}

func constStrings(v ssa.Value, seen map[ssa.Value]bool) []string {
	if seen[v] {
		return nil
	}
	seen[v] = true
	switch v := v.(type) {
	case *ssa.Const:
		if v.Value != nil {
			return []string{strings.Trim(v.Value.ExactString(), `"`)}
		}
	case *ssa.Phi:
		var out []string
		for _, edge := range v.Edges {
			out = append(out, constStrings(edge, seen)...)
		}
		return out
	}
	return nil
}

func (b *flowBuilder) busSpan(fact, site, ran string, stack []*ssa.Function) *span {
	s := b.newSpan("bus")
	s.Label = fact
	s.Site = site
	s.Ran = ran
	if ran == ranYes.String() {
		b.ran++
	}
	for _, consumer := range b.consumers {
		if !consumer.matches(fact) {
			continue
		}
		child := b.enter(consumer.Handler, "consumer", consumer.At, b.entryRan(consumer.Handler, ran), stack)
		child.Note = "subscribed to " + strings.Join(consumer.Patterns, ", ")
		s.Children = append(s.Children, child)
	}
	if len(s.Children) == 0 {
		s.Note = "no in-process consumer matched; projected to clients"
	}
	return s
}

func (b *flowBuilder) siteRan(pos token.Pos, parentRan string) string {
	if b.cov == nil {
		return ""
	}
	if parentRan == ranNo.String() {
		return ranNo.String()
	}
	position := b.p.fset.Position(pos)
	return b.cov.at(modulePath+"/"+b.p.rel(position.Filename), position.Line, position.Column).String()
}

func (b *flowBuilder) entryRan(fn *ssa.Function, parentRan string) string {
	if b.cov == nil {
		return ""
	}
	if parentRan == ranNo.String() {
		return ranNo.String()
	}
	body := funcBody(fn)
	if body == nil || len(body.List) == 0 {
		return parentRan
	}
	return b.siteRan(body.List[0].Pos(), parentRan)
}

func funcBody(fn *ssa.Function) *ast.BlockStmt {
	switch syntax := fn.Syntax().(type) {
	case *ast.FuncDecl:
		return syntax.Body
	case *ast.FuncLit:
		return syntax.Body
	}
	return nil
}

func (b *flowBuilder) funcID(fn *ssa.Function) string {
	if id, ok := b.funcIDs[fn]; ok {
		return id
	}
	id := fmt.Sprintf("f%d", len(b.funcs)+1)
	b.funcIDs[fn] = id
	b.funcs[id] = b.describe(fn)
	return id
}

func (b *flowBuilder) describe(fn *ssa.Function) *funcInfo {
	syntax := fn.Syntax()
	start := b.p.fset.Position(syntax.Pos())
	end := b.p.fset.Position(syntax.End())
	src, truncated := readLines(start.Filename, start.Line, end.Line)
	return &funcInfo{
		Name:  shortName(fn),
		Full:  fn.String(),
		Pkg:   strings.TrimPrefix(fn.Pkg.Pkg.Path(), modulePath+"/"),
		File:  b.p.rel(start.Filename),
		Line:  start.Line,
		Src:   src,
		Trunc: truncated,
	}
}

func shortName(fn *ssa.Function) string {
	top := fn
	for top.Parent() != nil {
		top = top.Parent()
	}
	name := strings.ReplaceAll(fn.Name(), "$", ".func")
	if recv := top.Signature.Recv(); recv != nil {
		t := recv.Type().String()
		return t[strings.LastIndex(t, ".")+1:] + "." + name
	}
	return name
}

var fileCache = map[string][]string{}

func readLines(filename string, from, to int) (string, bool) {
	lines, ok := fileCache[filename]
	if !ok {
		data, err := os.ReadFile(filename)
		if err != nil {
			return "", false
		}
		lines = strings.Split(string(data), "\n")
		fileCache[filename] = lines
	}
	truncated := false
	if to-from+1 > maxSourceLines {
		to = from + maxSourceLines - 1
		truncated = true
	}
	if from < 1 || to > len(lines) {
		return "", false
	}
	return strings.Join(lines[from-1:to], "\n"), truncated
}

func weigh(s *span) int {
	s.Weight = 1
	for _, child := range s.Children {
		s.Weight += weigh(child)
	}
	return s.Weight
}
