package prompts

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

var (
	referenceCitation = regexp.MustCompile("([A-Z][A-Za-z' ]*?) ?\\(`attn skill --reference ([a-z-]+)`\\)")
	markdownHeading   = regexp.MustCompile(`(?m)^#+ (.+)$`)
)

func TestEveryReferenceCitationNamesAHeadingOfThatReference(t *testing.T) {
	references, err := fs.Glob(Files(), "content/skills/attn/references/*.md")
	if err != nil {
		t.Fatal(err)
	}
	headings := map[string]map[string]bool{}
	for _, source := range references {
		raw, err := fs.ReadFile(Files(), source)
		if err != nil {
			t.Fatal(err)
		}
		titles := map[string]bool{}
		for _, match := range markdownHeading.FindAllStringSubmatch(string(raw), -1) {
			titles[strings.TrimSpace(match[1])] = true
		}
		headings[strings.TrimSuffix(path.Base(source), ".md")] = titles
	}
	citations := 0
	err = fs.WalkDir(Files(), "content", func(source string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(source, ".md") {
			return err
		}
		raw, err := fs.ReadFile(Files(), source)
		if err != nil {
			return err
		}
		prose := strings.Join(strings.Fields(string(raw)), " ")
		for _, match := range referenceCitation.FindAllStringSubmatch(prose, -1) {
			citations++
			title, reference := strings.TrimSpace(match[1]), match[2]
			titles, ok := headings[reference]
			if !ok {
				t.Errorf("%s cites %q in reference %q, which does not exist", source, title, reference)
				continue
			}
			if !endsWithHeading(title, titles) {
				t.Errorf("%s cites %q, which is no heading of reference %q", source, title, reference)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if citations == 0 {
		t.Fatal("found no citations; the citation pattern no longer matches the prompts")
	}
}

func endsWithHeading(title string, headings map[string]bool) bool {
	words := strings.Fields(title)
	for i := range words {
		if headings[strings.Join(words[i:], " ")] {
			return true
		}
	}
	return false
}
