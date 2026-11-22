package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	listFile  = "scripts/harnesslint/violations.txt"
	allowFile = "scripts/harnesslint/allow.txt"
	selfDir   = "scripts/harnesslint"
)

var builtinHarnessIDs = []string{"claude", "codex", "sigma"}

var adapterFilePatterns = []string{
	"internal/daemon/codex_*.go",
	"internal/daemon/claude_*.go",
	"internal/daemon/server_*.go",
	"internal/daemon/sigma_*.go",
	"internal/daemon/plugin_driver*.go",
	"internal/daemon/plugin_link*.go",
	"internal/daemon/plugin_server*.go",
	"internal/daemon/plugin_launch_instructions*.go",
	"internal/daemon/plugin_skill_dirs*.go",
	"internal/daemon/link_runtime*.go",
	"internal/agent/claude.go",
	"internal/agent/claude_models.go",
	"internal/agent/claude_spare.go",
	"internal/agent/codex.go",
	"internal/agent/codex_models.go",
	"internal/agent/sigma.go",
}

const adapterPackages = "/internal/harness/internal/"

var adapterImporters = []string{"/internal/harness/builtin", "/internal/harness/conformance"}

var appRoots = []string{"pty-host", "native", "cli", "mobile"}

var appSkippedDirs = map[string]bool{"node_modules": true, "target": true, "dist": true, "build": true, ".git": true, "Pods": true, "DerivedData": true}

var appExtensions = map[string]bool{".rs": true, ".ts": true, ".tsx": true, ".js": true, ".mjs": true, ".swift": true}

var appHarnessLiteral = regexp.MustCompile("[\"'](" + strings.Join(builtinHarnessIDs, "|") + ")[\"']")

type finding struct{ path, fn, what string }

func (f finding) key() string { return f.path + "\t" + f.fn + "\t" + f.what }

func (f finding) String() string {
	if f.fn == "" {
		return f.path + ": " + f.what
	}
	return f.path + " in " + f.fn + ": " + f.what
}

func main() {
	appsOnly := flag.Bool("apps", false, "check only pty-host/, native/, cli/ and mobile/")
	list := flag.Bool("list", false, "print every finding outside the allow-list, listed or not")
	update := flag.Bool("update", false, "remove the listed violations that are gone; never adds one")
	flag.Parse()
	if err := run(*appsOnly, *list, *update); err != nil {
		fmt.Fprintln(os.Stderr, "harnesslint:", err)
		os.Exit(1)
	}
}

func run(appsOnly, list, update bool) error {
	if _, err := os.Stat(listFile); err != nil {
		return fmt.Errorf("%w; run from the repository root", err)
	}
	allowed, err := readAllowList()
	if err != nil {
		return err
	}
	inScope := func(path string) bool { return !appsOnly || isAppPath(path) }
	findings, err := appFindings()
	if err != nil {
		return err
	}
	if !appsOnly {
		goFound, err := goFindings()
		if err != nil {
			return err
		}
		findings = append(findings, goFound...)
	}
	var kept []finding
	used := map[allowEntry]bool{}
	for _, f := range findings {
		if entry, ok := allowed.match(f); ok {
			used[entry] = true
			continue
		}
		kept = append(kept, f)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].key() < kept[j].key() })
	if list {
		for _, f := range kept {
			fmt.Println(f.key())
		}
		return nil
	}
	listed, err := readLines(listFile)
	if err != nil {
		return err
	}
	remaining := map[string]int{}
	for _, key := range listed {
		remaining[key]++
	}
	var added []finding
	for _, f := range kept {
		if remaining[f.key()] > 0 {
			remaining[f.key()]--
			continue
		}
		added = append(added, f)
	}
	var gone []string
	for _, key := range listed {
		if remaining[key] > 0 && inScope(strings.SplitN(key, "\t", 2)[0]) {
			gone = append(gone, key)
			remaining[key]--
		}
	}
	var staleAllow []string
	for _, entry := range allowed {
		if !used[entry] && inScope(entry.path) {
			staleAllow = append(staleAllow, entry.path+" "+entry.fn)
		}
	}
	if update {
		return shortenList(listed, gone)
	}
	var problems []string
	if len(added) > 0 {
		lines := make([]string, len(added))
		for i, f := range added {
			lines[i] = "  " + f.String()
		}
		problems = append(problems, "generic code names a built-in harness or reaches an adapter's code:\n"+strings.Join(lines, "\n")+
			"\nAsk the harness instead: its facts through d.harness(id), a session's conversation through d.conversation(session). "+
			"Adapter code lives in the files and packages scripts/harnesslint/main.go lists. "+
			"If this code was already listed and was only moved or renamed, rename its line in "+listFile+" in the same landing, so the count stays the same. "+
			"A string that only looks like a harness id goes in "+allowFile+" with its reason; ask your reviewer before adding one.")
	}
	if len(gone) > 0 {
		problems = append(problems, fmt.Sprintf("%d listed violations are gone; run `go run ./scripts/harnesslint -update` to shorten %s:\n  %s",
			len(gone), listFile, strings.Join(gone, "\n  ")))
	}
	if len(staleAllow) > 0 {
		problems = append(problems, "these "+allowFile+" entries match nothing; remove them:\n  "+strings.Join(staleAllow, "\n  "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n\n"))
	}
	if !appsOnly {
		fmt.Printf("harnesslint: %d listed violations remain in %s\n", len(listed), listFile)
	}
	return nil
}

func shortenList(listed, gone []string) error {
	drop := map[string]int{}
	for _, key := range gone {
		drop[key]++
	}
	var keep []string
	for _, key := range listed {
		if drop[key] > 0 {
			drop[key]--
			continue
		}
		keep = append(keep, key)
	}
	content := strings.Join(keep, "\n")
	if content != "" {
		content += "\n"
	}
	fmt.Printf("harnesslint: removed %d gone violations; %d remain\n", len(gone), len(keep))
	return os.WriteFile(listFile, []byte(content), 0o644)
}

type allowEntry struct{ path, fn string }

type allowList []allowEntry

func (a allowList) match(f finding) (allowEntry, bool) {
	for _, entry := range a {
		inPath := entry.path == f.path || strings.HasSuffix(entry.path, "/") && strings.HasPrefix(f.path, entry.path)
		if inPath && (entry.fn == "*" || entry.fn == f.fn) {
			return entry, true
		}
	}
	return allowEntry{}, false
}

func readAllowList() (allowList, error) {
	lines, err := readLines(allowFile)
	if err != nil {
		return nil, err
	}
	var list allowList
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s:%d: want \"<file or directory/> <function or *> <reason>\", got %q", allowFile, i+1, line)
		}
		list = append(list, allowEntry{path: fields[0], fn: fields[1]})
	}
	return list, nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func isAppPath(path string) bool {
	for _, root := range appRoots {
		if strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func isAdapterFile(path string) bool {
	if strings.HasPrefix(path, "internal/harness/internal/") {
		return true
	}
	for _, pattern := range adapterFilePatterns {
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
	}
	return false
}

func appFindings() ([]finding, error) {
	var found []finding
	for _, root := range appRoots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if appSkippedDirs[entry.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !appExtensions[filepath.Ext(path)] || isAppTestFile(path) {
				return nil
			}
			fileFound, err := appFileFindings(path)
			found = append(found, fileFound...)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func isAppTestFile(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(path, "/tests/") || strings.Contains(path, "/__tests__/") ||
		strings.HasSuffix(base, "_test.rs") || strings.HasSuffix(base, "_tests.rs") || base == "tests.rs" ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func appFileFindings(path string) ([]finding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var found []finding
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#[cfg(test)]") {
			break
		}
		for _, match := range appHarnessLiteral.FindAllStringSubmatch(line, -1) {
			found = append(found, finding{path: path, what: fmt.Sprintf("names %q", match[1])})
		}
	}
	return found, scanner.Err()
}

func goFindings() ([]finding, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
		packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}
	var loadErrors []string
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, e := range pkg.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	})
	if len(loadErrors) > 0 {
		return nil, fmt.Errorf("the packages do not type-check:\n  %s", strings.Join(loadErrors, "\n  "))
	}
	rel := func(pos token.Position) string {
		path, err := filepath.Rel(root, pos.Filename)
		if err != nil || strings.HasPrefix(path, "..") {
			return ""
		}
		return filepath.ToSlash(path)
	}
	var found []finding
	for _, pkg := range pkgs {
		found = append(found, importFindings(pkg)...)
		for _, file := range pkg.Syntax {
			path := rel(pkg.Fset.Position(file.Pos()))
			if path == "" || strings.HasSuffix(path, "_test.go") || isAdapterFile(path) || strings.HasPrefix(path, selfDir+"/") {
				continue
			}
			c := checker{info: pkg.TypesInfo, fset: pkg.Fset, rel: rel, path: path, seen: map[string]bool{}}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok {
					c.fn = declName(decl)
					ast.Inspect(decl, c.visit)
					continue
				}
				for _, spec := range gen.Specs {
					c.fn = specName(spec)
					ast.Inspect(spec, c.visit)
				}
			}
			found = append(found, c.found...)
		}
	}
	return found, nil
}

func importFindings(pkg *packages.Package) []finding {
	if strings.Contains(pkg.PkgPath+"/", adapterPackages) || slices.ContainsFunc(adapterImporters, func(s string) bool { return strings.HasSuffix(pkg.PkgPath, s) }) {
		return nil
	}
	var found []finding
	for path := range pkg.Imports {
		if strings.Contains(path+"/", adapterPackages) {
			found = append(found, finding{path: pkg.PkgPath, what: "imports the adapter package " + path + "; only internal/harness/builtin and internal/harness/conformance may"})
		}
	}
	return found
}

type checker struct {
	info  *types.Info
	fset  *token.FileSet
	rel   func(token.Position) string
	path  string
	fn    string
	seen  map[string]bool
	found []finding
}

func (c *checker) report(what string) {
	if c.seen[c.fn+"\t"+what] {
		return
	}
	c.seen[c.fn+"\t"+what] = true
	c.found = append(c.found, finding{path: c.path, fn: c.fn, what: what})
}

func (c *checker) visit(node ast.Node) bool {
	expr, ok := node.(ast.Expr)
	if !ok {
		return true
	}
	if id, ok := c.harnessID(expr); ok {
		c.report(fmt.Sprintf("names %q", id))
		return false
	}
	if ident, ok := expr.(*ast.Ident); ok {
		if obj := c.info.Uses[ident]; obj != nil && c.declaredInAdapter(obj.Pos()) {
			c.report("reaches " + objectName(obj) + " (" + c.rel(c.fset.Position(obj.Pos())) + ")")
		}
	}
	if tv, ok := c.info.Types[expr]; ok && tv.Type != nil {
		if named := namedType(tv.Type); named != nil && c.declaredInAdapter(named.Obj().Pos()) {
			c.report("reaches " + objectName(named.Obj()) + " (" + c.rel(c.fset.Position(named.Obj().Pos())) + ")")
		}
	}
	return true
}

func (c *checker) harnessID(expr ast.Expr) (string, bool) {
	tv, ok := c.info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	value := constant.StringVal(tv.Value)
	return value, slices.Contains(builtinHarnessIDs, value)
}

func (c *checker) declaredInAdapter(pos token.Pos) bool {
	if !pos.IsValid() {
		return false
	}
	path := c.rel(c.fset.Position(pos))
	return path != "" && isAdapterFile(path)
}

func namedType(t types.Type) *types.Named {
	for {
		switch u := t.(type) {
		case *types.Pointer:
			t = u.Elem()
		case *types.Alias:
			t = types.Unalias(u)
		case *types.Named:
			return u
		default:
			return nil
		}
	}
}

func objectName(obj types.Object) string {
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			if named := namedType(recv.Type()); named != nil {
				return named.Obj().Name() + "." + fn.Name()
			}
		}
	}
	if v, ok := obj.(*types.Var); ok && v.IsField() {
		return "field " + v.Name()
	}
	return obj.Name()
}

func declName(decl ast.Decl) string {
	fn := decl.(*ast.FuncDecl)
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + receiverName(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

func specName(spec ast.Spec) string {
	switch spec := spec.(type) {
	case *ast.ValueSpec:
		names := make([]string, len(spec.Names))
		for i, name := range spec.Names {
			names[i] = name.Name
		}
		return strings.Join(names, ",")
	case *ast.TypeSpec:
		return spec.Name.Name
	}
	return "imports"
}

func receiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverName(expr.X)
	case *ast.IndexExpr:
		return receiverName(expr.X)
	case *ast.IndexListExpr:
		return receiverName(expr.X)
	case *ast.Ident:
		return expr.Name
	}
	return "?"
}
