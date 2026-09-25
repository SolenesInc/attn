package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type flowSpec struct {
	Name     string
	Title    string
	Summary  string
	Command  string
	Origin   string
	TestPkg  string
	TestName string
}

var flows = []flowSpec{
	{
		Name:     "state",
		Title:    "Hook reports session state",
		Summary:  "A harness hook runs `attn` which sends `state` over the daemon socket; the daemon classifies the claim and projects it to the app.",
		Command:  "state",
		Origin:   "(*github.com/victorarias/attn/internal/client.Client).UpdateState",
		TestPkg:  "./internal/daemon",
		TestName: "TestDaemon_StateUpdate",
	},
	{
		Name:     "register",
		Title:    "Session registers with the daemon",
		Summary:  "A wrapped harness registers its session; the daemon records it, publishes facts, and consumers project the new session.",
		Command:  "register",
		Origin:   "(*github.com/victorarias/attn/internal/client.Client).Register",
		TestPkg:  "./internal/daemon",
		TestName: "TestDaemon_RegisterAndQuery",
	},
	{
		Name:     "pty_input",
		Title:    "Keystrokes reach a session",
		Summary:  "The app sends `pty_input` over the WebSocket; the daemon records user activity and writes the bytes to the session's PTY.",
		Command:  "pty_input",
		TestPkg:  "./internal/daemon",
		TestName: "TestAutoSettle_TypingFreezesTheCountdownAndQuietResumesIt",
	},
}

func main() {
	root := flag.String("root", ".", "attn repository root")
	out := flag.String("out", "flowviz.html", "output HTML file")
	skipCoverage := flag.Bool("skip-coverage", false, "build static flows without running scenario tests")
	flag.Parse()

	if err := run(*root, *out, !*skipCoverage); err != nil {
		fmt.Fprintln(os.Stderr, "flowviz:", err)
		os.Exit(1)
	}
}

func run(root, out string, withCoverage bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	started := time.Now()
	prog, err := loadProgram(root)
	if err != nil {
		return err
	}
	logStep("loaded %d packages, call graph built", len(prog.pkgs), started)

	dispatch, err := findDispatch(prog)
	if err != nil {
		return err
	}
	consumers := findBusConsumers(prog)
	logStep("resolved %d commands and %d bus consumers", len(dispatch), len(consumers), started)

	page := pageData{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Commit:      git(root, "rev-parse", "HEAD"),
		Repo:        githubURL(git(root, "remote", "get-url", "origin")),
		Funcs:       map[string]*funcInfo{},
	}
	for _, spec := range flows {
		var cov *coverage
		if withCoverage && spec.TestName != "" {
			cov, err = runScenario(root, spec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "flowviz: scenario %s: %v (continuing without coverage)\n", spec.TestName, err)
			}
		}
		flow, err := buildFlow(prog, dispatch, consumers, cov, spec, page.Funcs)
		if err != nil {
			return fmt.Errorf("flow %s: %w", spec.Name, err)
		}
		page.Flows = append(page.Flows, flow)
		logStep("flow %s: %d spans", spec.Name, flow.Spans, started)
	}
	return writePage(out, page)
}

func logStep(format string, args ...any) {
	started := args[len(args)-1].(time.Time)
	fmt.Fprintf(os.Stderr, "[%5.1fs] %s\n", time.Since(started).Seconds(), fmt.Sprintf(format, args[:len(args)-1]...))
}
