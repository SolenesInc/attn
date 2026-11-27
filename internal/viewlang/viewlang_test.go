package viewlang_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/viewlang"
	"github.com/victorarias/attn/internal/viewlang/catalog"
)

var update = flag.Bool("update", false, "rewrite each fixture's expected output")

func TestEveryViewOfLanguageVersion1ChecksClean(t *testing.T) {
	for _, path := range fixtures(t, "views") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			checked := viewlang.Check(read(t, path))
			if len(checked.Problems) > 0 {
				t.Fatalf("problems:\n%s", problems(checked))
			}
			parts, err := json.MarshalIndent(map[string]any{"language": checked.Language, "parts": checked.Parts}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			expect(t, strings.TrimSuffix(path, ".md")+".json", string(parts)+"\n")
		})
	}
}

func TestEachBrokenViewNamesItsProblemsLineAndFix(t *testing.T) {
	for _, path := range fixtures(t, "broken") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			checked := viewlang.Check(read(t, path))
			if len(checked.Problems) == 0 {
				t.Fatal("no problem")
			}
			expect(t, strings.TrimSuffix(path, ".md")+".want", problems(checked)+"\n"+placed(checked.Parts, ""))
		})
	}
}

func fixtures(t *testing.T, kind string) []string {
	paths, err := filepath.Glob(filepath.Join("testdata", fmt.Sprintf("v%d", catalog.Language), kind, "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no %s fixtures: %v", kind, err)
	}
	return paths
}

func read(t *testing.T, path string) string {
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func problems(checked viewlang.Checked) string {
	var out strings.Builder
	for _, p := range checked.Problems {
		fmt.Fprintf(&out, "line %d: %s\nfix: %s\n", p.Line, p.Text, p.Fix)
	}
	return out.String()
}

func placed(parts []protocol.ViewPart, indent string) string {
	var out strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&out, "%s%s on line %d", indent, part.Name, part.Line)
		if part.Problem != nil {
			fmt.Fprintf(&out, " shows the problem of line %d", part.Problem.Line)
		}
		out.WriteString("\n")
		for _, container := range []*protocol.ViewPartEach{part.Each, (*protocol.ViewPartEach)(part.OneByOne)} {
			if container != nil {
				out.WriteString(placed(container.Blocks, indent+"  "))
			}
		}
	}
	return out.String()
}

func expect(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run the test with -update to write it", err)
	}
	if string(want) != got {
		t.Errorf("%s differs.\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
