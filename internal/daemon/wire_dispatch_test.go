package daemon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var (
	wireConstRe = regexp.MustCompile(`\b(Cmd[A-Za-z0-9]+)\s*=\s*"([a-z0-9_]+)"`)
	dispatchRe  = regexp.MustCompile(`(?m)^\s*case ((?:protocol\.Cmd[A-Za-z0-9]+)(?:,\s*protocol\.Cmd[A-Za-z0-9]+)*):`)
	cmdRefRe    = regexp.MustCompile(`protocol\.(Cmd[A-Za-z0-9]+)`)
)

var dispatchFiles = []string{"websocket.go", "daemon.go", "automations.go"}

type dispatchSite struct {
	where     string
	constants []string
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
		for _, match := range dispatchRe.FindAllIndex(src, -1) {
			line := src[match[0]:match[1]]
			site := dispatchSite{
				where: fmt.Sprintf("%s:%d", name, 1+bytes.Count(src[:match[0]], []byte("\n"))),
			}
			for _, c := range cmdRefRe.FindAllSubmatch(line, -1) {
				site.constants = append(site.constants, string(c[1]))
			}
			sites = append(sites, site)
		}
	}
	if len(sites) < 100 {
		t.Fatalf("found only %d dispatch cases across %v; the regex probably stopped matching", len(sites), dispatchFiles)
	}
	return sites
}

func TestWireCommandsHaveDispatchCases(t *testing.T) {
	commands := wireCommands(t)
	sites := dispatchSites(t)

	dispatched := map[string]bool{}
	for _, site := range sites {
		for _, name := range site.constants {
			_, known := commands[name]
			if !known {
				t.Errorf("%s: dispatches unknown constant protocol.%s", site.where, name)
				continue
			}
			dispatched[name] = true
		}
	}

	for name, wire := range commands {
		if !dispatched[name] {
			t.Errorf("protocol.%s (%q) has no dispatch case in %v; it is either dead or handled somewhere this test does not look",
				name, wire, dispatchFiles)
		}
	}
}
