package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAReferenceRendersForAToolAsTheCorpusSays(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := filepath.Glob(filepath.Join("testdata", "teaching", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) == 0 {
		t.Fatal("the corpus is empty")
	}
	for _, path := range corpus {
		if strings.HasSuffix(path, ".tool.md") {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		t.Run(name, func(t *testing.T) {
			written := readCorpus(t, name+".md")
			want := readCorpus(t, name+".tool.md")
			if got := c.TeachingToolText(written); got != want {
				t.Errorf("rendered\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func readCorpus(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "teaching", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}
