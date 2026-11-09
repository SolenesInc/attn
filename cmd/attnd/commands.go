package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
)

type instanceChecks int

const (
	checkInstanceAndRouting instanceChecks = iota
	checkInstanceOnly
	checkNothing
)

type topLevelCommand struct {
	names      []string
	run        func()
	help       func(io.Writer)
	banner     bool
	checks     instanceChecks
	unrecorded bool
}

func topLevelCommands() []topLevelCommand {
	return []topLevelCommand{
		{names: []string{"delegate"}, run: runDelegate, banner: true},
		{names: []string{"pr"}, run: runPRCommand},
		{names: []string{"agent"}, run: runAgent, banner: true},
		{names: []string{"session"}, run: runSession, banner: true},
		{names: []string{"terminal"}, run: runTerminal, banner: true},
		{names: []string{"open"}, run: runOpen, banner: true,
			help: func(w io.Writer) { fmt.Fprintln(w, "usage: attn open <file.md|seed-id> [--session <id>]") }},
		{names: []string{"browser"}, run: runBrowser, banner: true, help: printBrowserUsage},
		{names: []string{"journal"}, run: runJournal, banner: true},
		{names: []string{"crew"}, run: runCrew, banner: true},
		{names: []string{"automation"}, run: runAutomationCommand, banner: true,
			help: func(w io.Writer) { fmt.Fprint(w, automationUsageText) }},

		{names: []string{"presence"}, run: runPresence,
			help: func(w io.Writer) { fmt.Fprintln(w, "usage: attn presence [--session-id | --seed-ids]") }},
		{names: []string{"ask"}, run: runAsk, banner: true},
		{names: []string{"turn"}, run: runTurn, banner: true},
		{names: []string{"vent"}, run: runVent, banner: true},
		{names: []string{"handoff"}, run: func() { runHandoff(os.Args[2:]) }, banner: true,
			help: writeHandoffHelp},
		{names: []string{"onboarding"}, run: runOnboarding, banner: true},
		{names: []string{"skill"}, run: runSkill},
		{names: []string{"guide"}, run: runGuide},
		{names: []string{"send"}, run: runSend, banner: true},
		{names: []string{"list"}, run: runList, banner: true,
			help: func(w io.Writer) { fmt.Fprintln(w, "usage: attn list\n\nPrint sessions and profiles as JSON.") }},
		{names: []string{"activity"}, run: runActivity, banner: true,
			help: func(w io.Writer) { fmt.Fprint(w, activityUsage) }},
		{names: []string{"state"}, run: runState, banner: true},
		{names: []string{"conversation"}, run: runConversation, banner: true},
		{names: []string{"worktree"}, run: runWorktree, banner: true},
		{names: []string{"preflight"}, run: runPreflight, banner: true},
		{names: []string{"codex"}, run: runCodex, banner: true},
		{names: []string{"update"}, run: runUpdate, banner: true,
			help: func(w io.Writer) { fmt.Fprintln(w, updateUsage) }},
		{names: []string{"plugin"}, run: runPluginCommand, banner: true,
			help: writePluginHelp},
		{names: []string{"debug"}, run: runDebug, banner: true},
		{names: []string{"db"}, run: runDB, banner: true},
		{names: []string{"bus"}, run: runBus, banner: true},
		{names: []string{"enrollment"}, run: runEnrollment, banner: true},
		{names: []string{"doc"}, run: runDoc, banner: true},
		{names: []string{"vision-check"}, run: runVisionCheck,
			help: func(w io.Writer) { fmt.Fprintln(w, visionCheckUsage) }},
		{names: []string{"daemon"}, run: runDaemonCommand, banner: true, checks: checkNothing},
		{names: []string{"instance"}, run: runInstance, checks: checkInstanceOnly},
		{names: []string{"prompts"}, run: runPrompts},
		{names: []string{"version", "--version"}, run: runVersionCommand, checks: checkNothing},

		{names: []string{"profile", "profile-env"}, run: runRenamedProfileCommand, checks: checkNothing},
		{names: []string{"--check-schema"}, run: runCheckSchema, checks: checkNothing, unrecorded: true},
		{names: []string{"--protocol-version"}, run: runProtocolVersion, checks: checkNothing, unrecorded: true},
		{names: []string{"--build-info-json"}, run: runBuildInfoJSON, checks: checkNothing, unrecorded: true},
		{names: []string{"pty-worker"}, run: runPTYWorker, unrecorded: true},
		{names: []string{"_outpost-install"}, run: runOutpostInstall},
		{names: []string{"_update-apply"}, run: runUpdateApply, checks: checkNothing, unrecorded: true},
		{names: []string{"_bridge"}, run: runBridge, unrecorded: true},
		{names: []string{"_probe-tui"}, run: runProbeTUI},
	}
}

func topLevelCommandNamed(name string) (topLevelCommand, bool) {
	for _, command := range topLevelCommands() {
		for _, candidate := range command.names {
			if candidate == name {
				return command, true
			}
		}
	}
	return topLevelCommand{}, false
}

func run() {
	if len(os.Args) < 2 {
		checkInstance(checkInstanceAndRouting)
		maybePrintInstanceBanner()
		runWrapper()
		return
	}
	command, known := topLevelCommandNamed(os.Args[1])
	checkInstance(command.checks)
	if !known {
		runUnknownCommand(os.Args[1])
		return
	}
	setCommandPath(os.Args[1])
	if command.unrecorded {
		leaveInvocationUnrecorded()
	}
	if command.help != nil && len(os.Args) == 3 && isHelpArg(os.Args[2]) {
		command.help(os.Stdout)
		return
	}
	if command.banner {
		maybePrintInstanceBanner()
	}
	command.run()
}

func checkInstance(checks instanceChecks) {
	if checks == checkNothing {
		return
	}
	if err := config.ValidateInstance(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	if checks == checkInstanceOnly {
		return
	}
	if err := config.ValidateInstanceRouting(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
}

func runUnknownCommand(name string) {
	if strings.HasPrefix(name, "-") {
		maybePrintInstanceBanner()
		runWrapper()
		return
	}
	fmt.Fprintf(os.Stderr, "attnd: unknown command %q\n", name)
	exit(1)
}

func writeHelp(w io.Writer) {
	executable, err := os.Executable()
	if err == nil {
		help := exec.Command(filepath.Join(filepath.Dir(executable), "attn"), "help")
		help.Stdout = w
		help.Stderr = w
		err = help.Run()
	}
	if err != nil {
		fmt.Fprintln(w, "run `attn help` for the commands")
	}
}

func runVersionCommand() {
	setCommandPath("version")
	runVersion(os.Args[1:])
}

func runRenamedProfileCommand() {
	fmt.Fprintln(os.Stderr, renamedProfileCommandMessage(os.Args[1:]))
	exit(2)
}

func runCheckSchema() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: attn --check-schema <database>")
		exit(2)
	}
	if err := store.CheckSchema(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
}

func writePluginHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn plugin <command>

commands:
  install --path <dir>      install the plugin in a local directory
  link --path <dir>         link a local plugin directory in place instead of copying it
  list                      print installed plugins and manifest issues as JSON
  uninstall|remove <name>   remove an installed plugin
`)
}
