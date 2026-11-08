package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/prompts"
)

func runSkill() {
	exit(writeSkill(os.Stdout, os.Stderr, os.Args[2:]))
}

func writeSkill(stdout, stderr io.Writer, args []string) int {
	reference := ""
	list := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printSkillHelp(stdout)
			return 0
		case "--list":
			list = true
		case "--reference":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				fmt.Fprintln(stderr, "skill: --reference requires a name; run `attn skill --list` for the bundled names")
				return 2
			}
			reference = strings.TrimSpace(args[i])
		default:
			fmt.Fprintf(stderr, "skill: unknown argument %q\n", args[i])
			printSkillHelp(stderr)
			return 2
		}
	}

	if list && reference != "" {
		fmt.Fprintln(stderr, "skill: --list and --reference are mutually exclusive; pass one")
		return 2
	}
	if list {
		for _, name := range agent.SkillReferenceNames() {
			fmt.Fprintln(stdout, name)
		}
		return 0
	}

	if reference == "" {
		fmt.Fprintln(stdout, prompts.RenderText("session", "attn-map", prompts.Values{}))
		return 0
	}
	content, err := agent.SkillFile("references/" + strings.TrimSuffix(reference, ".md") + ".md")
	if err != nil {
		fmt.Fprintf(stderr, "skill: no bundled reference %q; bundled references: %s\n",
			reference, strings.Join(agent.SkillReferenceNames(), ", "))
		return 1
	}
	if _, err := stdout.Write(content); err != nil {
		fmt.Fprintf(stderr, "skill: write output: %v\n", err)
		return 1
	}
	return 0
}

func printSkillHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn skill [--reference <name>] [--list]

print what attn offers agents, and the rules for each capability. The text is
release-matched to the running version.

  (no flags)            print the map of what attn can do
  --reference <name>    print one capability's rules, e.g. garden
  --list                list bundled reference names
`)
}
