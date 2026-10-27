package guide

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed tutorials/*.md reference/*.md diagrams/*.txt
var content embed.FS

var image = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
var summary = regexp.MustCompile(`<summary>(.*?)</summary>`)

func Topics() []string {
	var topics []string
	for _, section := range []string{"tutorials", "reference"} {
		entries, _ := content.ReadDir(section)
		for _, entry := range entries {
			topics = append(topics, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	sort.Strings(topics)
	return topics
}

func Topic(name string) ([]byte, error) {
	for _, section := range []string{"tutorials", "reference"} {
		if strings.ContainsAny(name, "/\\") {
			break
		}
		data, err := content.ReadFile(section + "/" + name + ".md")
		if err != nil {
			continue
		}
		text := image.ReplaceAllStringFunc(string(data), func(line string) string {
			match := image.FindStringSubmatch(line)
			target := path.Clean(path.Join(section, match[2]))
			if strings.HasPrefix(target, "diagrams/") {
				diagram, err := content.ReadFile(strings.TrimSuffix(target, path.Ext(target)) + ".txt")
				if err == nil {
					return "\n```text\n" + strings.TrimRight(string(diagram), "\n") + "\n```\n"
				}
			}
			return match[1]
		})
		return []byte(text), nil
	}
	return nil, fmt.Errorf("no topic %q; topics: %s\nattn <command> --help prints a command's usage", name, strings.Join(Topics(), ", "))
}

func Index() []byte {
	var out strings.Builder
	out.WriteString("The Attn guide\n\nRead a page with attn guide <topic>.\n")
	for _, section := range []string{"tutorials", "reference"} {
		out.WriteString("\n" + strings.ToUpper(section[:1]) + section[1:] + "\n")
		entries, _ := content.ReadDir(section)
		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".md")
			data, _ := content.ReadFile(section + "/" + entry.Name())
			lines := strings.Split(string(data), "\n")
			fmt.Fprintf(&out, "  %s  %s\n", name, strings.TrimPrefix(lines[0], "# "))
			inCode := false
			for _, line := range lines[1:] {
				if strings.HasPrefix(line, "```") {
					inCode = !inCode
				}
				if inCode {
					continue
				}
				if strings.HasPrefix(line, "##") {
					heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
					fmt.Fprintf(&out, "%s%s\n", strings.Repeat("  ", len(line)-len(strings.TrimLeft(line, "#"))), heading)
				}
				if match := summary.FindStringSubmatch(line); match != nil {
					fmt.Fprintf(&out, "      %s\n", match[1])
				}
			}
		}
	}
	return []byte(out.String())
}
