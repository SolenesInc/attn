package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/testhistory"
)

const recipeList = "scripts/landing-tests"

type history struct {
	receipts []testhistory.Receipt
	skipped  map[string]int
}

func loadHistory(dir string) (history, error) {
	loaded := history{skipped: map[string]int{}}
	seen := map[string]bool{}
	for _, sub := range []string{"receipts", "shared"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return history{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, sub, entry.Name()))
			if err != nil {
				return history{}, err
			}
			var receipt testhistory.Receipt
			switch {
			case json.Unmarshal(data, &receipt) != nil:
				loaded.skipped["unreadable"]++
			case receipt.Schema != testhistory.Schema:
				loaded.skipped["schema "+receipt.Schema]++
			case !seen[receipt.InvocationID]:
				seen[receipt.InvocationID] = true
				loaded.receipts = append(loaded.receipts, receipt)
			}
		}
	}
	return loaded, nil
}

func (h history) availableAt(at time.Time) []testhistory.Receipt {
	var available []testhistory.Receipt
	for _, receipt := range h.receipts {
		if finished := receipt.Command.FinishedAt; finished != nil && !finished.After(at) {
			available = append(available, receipt)
		}
	}
	return available
}

func mainline(ref string) (testhistory.Mainline, error) {
	out, err := git("rev-list", "--first-parent", "--reverse", ref)
	if err != nil {
		return testhistory.Mainline{}, fmt.Errorf("git rev-list %s: %v", ref, err)
	}
	return testhistory.NewMainline(strings.Fields(out)), nil
}

func gitOutput(args ...string) ([]byte, error) {
	out, err := exec.Command("git", args...).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
	}
	return out, err
}

func repoRoot() (string, error) {
	return git("rev-parse", "--show-toplevel")
}

type tree struct {
	revision string
}

var worktree = tree{}

func (t tree) read(path string) ([]byte, error) {
	if t.revision == "" {
		root, err := repoRoot()
		if err != nil {
			return nil, err
		}
		return os.ReadFile(filepath.Join(root, path))
	}
	return gitOutput("show", t.revision+":"+path)
}

func (t tree) recipes() (testhistory.RecipeList, error) {
	text, err := t.read(recipeList)
	if err != nil {
		return testhistory.RecipeList{}, err
	}
	return testhistory.ParseRecipeList(string(text))
}

var goTestFunc = regexp.MustCompile(`^func (Test\w*)\(`)

var goTestName = regexp.MustCompile(`^Test(?:[^a-z]\w*)?$`)

func isGoTest(name string) bool {
	return goTestName.MatchString(name) && name != "TestMain"
}

func (t tree) inventory() (testhistory.Inventory, error) {
	inventory := testhistory.Inventory{}
	args := []string{"grep", "-n"}
	if t.revision == "" {
		args = append(args, "--untracked", "-E", `^func Test`)
	} else {
		args = append(args, "-E", `^func Test`, t.revision)
	}
	out, err := exec.Command("git", append(args, "--", "*_test.go")...).Output()
	var exitErr *exec.ExitError
	if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
		return nil, fmt.Errorf("git grep for Go tests: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if t.revision != "" {
			line = strings.TrimPrefix(line, t.revision+":")
		}
		file, rest, ok := strings.Cut(line, ":")
		if !ok || !goTestFile(file) {
			continue
		}
		_, source, _ := strings.Cut(rest, ":")
		if match := goTestFunc.FindStringSubmatch(source); match != nil && isGoTest(match[1]) {
			inventory[goUnit(file, match[1])] = true
		}
	}
	files, err := t.files("app/")
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if unit, ok := testhistory.AppTestUnit(file); ok {
			inventory[unit] = true
		}
	}
	return inventory, nil
}

func goTestFile(path string) bool {
	if !strings.HasSuffix(path, "_test.go") {
		return false
	}
	for _, dir := range strings.Split(filepath.Dir(path), "/") {
		if dir == "testdata" || strings.HasPrefix(dir, "_") || strings.HasPrefix(dir, ".") && dir != "." {
			return false
		}
	}
	return true
}

func goUnit(file, test string) string {
	dir := filepath.Dir(file)
	if dir == "." {
		return "go . " + test
	}
	return "go ./" + dir + " " + test
}

func (t tree) files(prefix string) ([]string, error) {
	var out []byte
	var err error
	if t.revision == "" {
		out, err = gitOutput("ls-files", "--cached", "--others", "--exclude-standard", "--", prefix)
	} else {
		out, err = gitOutput("ls-tree", "-r", "--name-only", t.revision, "--", prefix)
	}
	return strings.Fields(string(out)), err
}

type change struct {
	paths []string
	tests []string
}

func changesBetween(base string, head tree) (change, error) {
	diffArgs := []string{base}
	if head.revision != "" {
		diffArgs = append(diffArgs, head.revision)
	}
	names, err := gitOutput(append(append([]string{"diff", "--name-only"}, diffArgs...), "--")...)
	if err != nil {
		return change{}, err
	}
	changed := change{paths: strings.Fields(string(names))}
	added := map[string][]lineRange{}
	if head.revision == "" {
		untracked, err := gitOutput("ls-files", "--others", "--exclude-standard")
		if err != nil {
			return change{}, err
		}
		for _, path := range strings.Fields(string(untracked)) {
			changed.paths = append(changed.paths, path)
			added[path] = []lineRange{{1, 1 << 30}}
		}
	}
	hunks, err := gitOutput(append(append([]string{"diff", "-U0", "--no-renames", "--dst-prefix=b/", "--diff-filter=d"}, diffArgs...), "--", "*_test.go")...)
	if err != nil {
		return change{}, err
	}
	for path, ranges := range changedLines(hunks) {
		added[path] = append(added[path], ranges...)
	}
	tests := map[string]bool{}
	for path, ranges := range added {
		if !goTestFile(path) {
			continue
		}
		source, err := head.read(path)
		if err != nil {
			return change{}, err
		}
		for _, name := range testsTouching(path, source, ranges) {
			tests[goUnit(path, name)] = true
		}
	}
	present, err := gitOutput(append(append([]string{"diff", "--name-only", "--diff-filter=d"}, diffArgs...), "--", "app/")...)
	if err != nil {
		return change{}, err
	}
	for _, path := range append(strings.Fields(string(present)), keys(added)...) {
		if unit, ok := testhistory.AppTestUnit(path); ok {
			tests[unit] = true
		}
	}
	changed.tests = keys(tests)
	return changed, nil
}

func keys[V any](set map[string]V) []string {
	list := make([]string, 0, len(set))
	for key := range set {
		list = append(list, key)
	}
	sort.Strings(list)
	return list
}

type lineRange struct{ first, last int }

var hunkHeader = regexp.MustCompile(`^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

func changedLines(diff []byte) map[string][]lineRange {
	ranges := map[string][]lineRange{}
	var path string
	scanner := bufio.NewScanner(strings.NewReader(string(diff)))
	scanner.Buffer(make([]byte, 0, 64*1024), len(diff)+1)
	for scanner.Scan() {
		line := scanner.Text()
		if name, ok := strings.CutPrefix(line, "+++ b/"); ok {
			path = name
			continue
		}
		match := hunkHeader.FindStringSubmatch(line)
		if match == nil || path == "" {
			continue
		}
		first, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		ranges[path] = append(ranges[path], lineRange{first, first + max(count, 1) - 1})
	}
	return ranges
}

func testsTouching(path string, source []byte, ranges []lineRange) []string {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var tests []string
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Recv != nil || !isGoTest(function.Name.Name) {
			continue
		}
		start, end := files.Position(function.Pos()).Line, files.Position(function.End()).Line
		for _, changed := range ranges {
			if changed.first <= end && changed.last >= start {
				tests = append(tests, function.Name.Name)
				break
			}
		}
	}
	return tests
}

func selectionKey() testhistory.CostKey {
	return testhistory.CostKey{Lane: testhistory.Lane{OS: runtime.GOOS, Arch: runtime.GOARCH}, Machine: machineClass()}
}
