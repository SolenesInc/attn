package guide

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

//go:embed tutorials/*.md reference/*.md diagrams/*.txt
var content embed.FS

var sections = []string{"tutorials", "reference"}

var readingOrder = []string{"getting-started", "building-with-roles"}

var imageLink = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
var summaryLine = regexp.MustCompile(`<summary>(.*?)</summary>`)

type page struct {
	topic string
	text  string
}

func sectionPages(section string) []page {
	entries, _ := content.ReadDir(section)
	pages := make([]page, 0, len(entries))
	for _, entry := range entries {
		data, _ := content.ReadFile(section + "/" + entry.Name())
		pages = append(pages, page{topic: strings.TrimSuffix(entry.Name(), ".md"), text: string(data)})
	}
	slices.SortStableFunc(pages, func(a, b page) int { return readingRank(a.topic) - readingRank(b.topic) })
	return pages
}

func readingRank(topic string) int {
	if rank := slices.Index(readingOrder, topic); rank >= 0 {
		return rank
	}
	return len(readingOrder)
}

func Topics() []string {
	var topics []string
	for _, section := range sections {
		for _, p := range sectionPages(section) {
			topics = append(topics, p.topic)
		}
	}
	return topics
}

func Topic(name string) ([]byte, error) {
	if !strings.ContainsAny(name, `/\`) {
		for _, section := range sections {
			if data, err := content.ReadFile(section + "/" + name + ".md"); err == nil {
				return []byte(forTerminal(section, string(data))), nil
			}
		}
	}
	return nil, fmt.Errorf("no topic %q; topics: %s\nattn <command> --help prints a command's usage", name, strings.Join(Topics(), ", "))
}

func forTerminal(section, text string) string {
	return imageLink.ReplaceAllStringFunc(text, func(link string) string {
		match := imageLink.FindStringSubmatch(link)
		alt, target := match[1], path.Clean(path.Join(section, match[2]))
		if strings.HasPrefix(target, "diagrams/") {
			if diagram, err := content.ReadFile(strings.TrimSuffix(target, path.Ext(target)) + ".txt"); err == nil {
				return "\n```text\n" + strings.TrimRight(string(diagram), "\n") + "\n```\n"
			}
		}
		return alt
	})
}

func Index() []byte {
	var out strings.Builder
	out.WriteString("The Attn guide\n\nRead a page with attn guide <topic>.\n")
	for _, section := range sections {
		out.WriteString("\n" + strings.ToUpper(section[:1]) + section[1:] + "\n")
		for _, p := range sectionPages(section) {
			writeOutline(&out, p)
		}
	}
	return []byte(out.String())
}

func writeOutline(out *strings.Builder, p page) {
	lines := strings.Split(p.text, "\n")
	fmt.Fprintf(out, "  %s  %s\n", p.topic, strings.TrimPrefix(lines[0], "# "))
	inCode := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
		}
		if inCode {
			continue
		}
		if strings.HasPrefix(line, "##") {
			level := len(line) - len(strings.TrimLeft(line, "#"))
			fmt.Fprintf(out, "%s%s\n", strings.Repeat("  ", level), strings.TrimSpace(line[level:]))
		}
		if match := summaryLine.FindStringSubmatch(line); match != nil {
			fmt.Fprintf(out, "      %s\n", match[1])
		}
	}
}
