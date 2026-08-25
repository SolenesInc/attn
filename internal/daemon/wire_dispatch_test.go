package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every wire command name stays greppable at its dispatch site via a `// wire: <name>`
// comment. A new command, or a second dispatch site, without that comment fails here.

var (
	wireConstRe   = regexp.MustCompile(`\b(Cmd[A-Za-z0-9]+)\s*=\s*"([a-z0-9_]+)"`)
	dispatchRe    = regexp.MustCompile(`^\s*case ((?:protocol\.Cmd[A-Za-z0-9]+)(?:,\s*protocol\.Cmd[A-Za-z0-9]+)*):`)
	cmdRefRe      = regexp.MustCompile(`protocol\.(Cmd[A-Za-z0-9]+)`)
	wireCommentRe = regexp.MustCompile(`//\s*wire:\s*(.*)$`)
)

var dispatchFiles = []string{"websocket.go", "daemon.go", "automations.go"}

type dispatchSite struct {
	where     string
	constants []string
	annotated []string
}

func wireCommands(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "protocol", "constants.go"))
	if err != nil {
		t.Fatalf("read constants.go: %v", err)
	}
	out := map[string]string{}
	for _, m := range wireConstRe.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 100 {
		t.Fatalf("parsed only %d Cmd constants; the regex probably stopped matching", len(out))
	}
	return out
}

func dispatchSites(t *testing.T) []dispatchSite {
	t.Helper()
	var sites []dispatchSite
	for _, name := range dispatchFiles {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if !dispatchRe.MatchString(line) {
				continue
			}
			comment := ""
			if m := wireCommentRe.FindStringSubmatch(line); m != nil {
				comment = m[1]
			} else {
				for j := i - 1; j >= 0; j-- {
					trimmed := strings.TrimSpace(lines[j])
					if !strings.HasPrefix(trimmed, "//") {
						break
					}
					comment = strings.TrimPrefix(trimmed, "//") + " " + comment
				}
			}
			site := dispatchSite{
				where: fmt.Sprintf("%s:%d", name, i+1),
				annotated: strings.FieldsFunc(comment, func(r rune) bool {
					return r == ',' || r == ' ' || r == '\t'
				}),
			}
			for _, c := range cmdRefRe.FindAllStringSubmatch(line, -1) {
				site.constants = append(site.constants, c[1])
			}
			sites = append(sites, site)
		}
	}
	if len(sites) < 100 {
		t.Fatalf("found only %d dispatch cases across %v; the regex probably stopped matching", len(sites), dispatchFiles)
	}
	return sites
}

func TestWireCommandsAreGreppable(t *testing.T) {
	commands := wireCommands(t)
	sites := dispatchSites(t)

	dispatched := map[string]bool{}
	for _, site := range sites {
		for _, name := range site.constants {
			wire, known := commands[name]
			if !known {
				t.Errorf("%s: dispatches unknown constant protocol.%s", site.where, name)
				continue
			}
			dispatched[name] = true
			if !slices.Contains(site.annotated, wire) {
				t.Errorf("%s: protocol.%s dispatched without its wire name; add `// wire: %s` so grepping %q reaches this case",
					site.where, name, wire, wire)
			}
		}
	}

	for name, wire := range commands {
		if !dispatched[name] {
			t.Errorf("protocol.%s (%q) has no dispatch case in %v; it is either dead or handled somewhere this test does not look",
				name, wire, dispatchFiles)
		}
	}
}

func TestWireCommentsMatchTheirConstants(t *testing.T) {
	commands := wireCommands(t)
	byWire := map[string]string{}
	for name, wire := range commands {
		byWire[wire] = name
	}

	for _, site := range dispatchSites(t) {
		expected := make([]string, 0, len(site.constants))
		for _, name := range site.constants {
			expected = append(expected, commands[name])
		}
		for _, annotated := range site.annotated {
			if slices.Contains(expected, annotated) {
				continue
			}
			if other, isWire := byWire[annotated]; isWire {
				t.Errorf("%s: annotated %q, which is protocol.%s's wire name, but dispatches %v",
					site.where, annotated, other, site.constants)
			}
		}
	}
}
