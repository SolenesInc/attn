package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/store"
)

type commandListing int

const (
	unlisted commandListing = iota
	inCapabilityMap
	inOtherCommands
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
	listing    commandListing
	args       string
	summary    string
	help       func(io.Writer)
	banner     bool
	checks     instanceChecks
	unrecorded bool
}

func topLevelCommands() []topLevelCommand {
	return []topLevelCommand{
		{names: []string{"seed"}, run: runSeed, listing: inCapabilityMap, banner: true},
		{names: []string{"delegate"}, run: runDelegate, listing: inCapabilityMap, banner: true},
		{names: []string{"pr"}, run: runPRCommand, listing: inCapabilityMap},
		{names: []string{"agent"}, run: runAgent, listing: inCapabilityMap, banner: true},
		{names: []string{"session"}, run: runSession, listing: inCapabilityMap, banner: true},
		{names: []string{"terminal"}, run: runTerminal, listing: inCapabilityMap, banner: true},
		{names: []string{"open"}, run: runOpen, listing: inCapabilityMap, banner: true,
			help: func(w io.Writer) { fmt.Fprintln(w, "usage: attn open <file.md|seed-id> [--session <id>]") }},
		{names: []string{"browser"}, run: runBrowser, listing: inCapabilityMap, banner: true, help: printBrowserUsage},
		{names: []string{"callout"}, run: runCallout, listing: inCapabilityMap},
		{names: []string{"journal"}, run: runJournal, listing: inCapabilityMap, banner: true},
		{names: []string{"crew"}, run: runCrew, listing: inCapabilityMap, banner: true},
		{names: []string{"automation"}, run: runAutomationCommand, listing: inCapabilityMap, banner: true,
			help: func(w io.Writer) { fmt.Fprint(w, automationUsageText) }},
		{names: []string{"settings"}, run: runSettings, listing: inCapabilityMap},

		{names: []string{"presence"}, run: runPresence, listing: inOtherCommands,
			args: "[--session-id|--seed-ids]", summary: "check whether the shell runs inside attn, or print only its session or seeds",
			help: func(w io.Writer) { fmt.Fprintln(w, "usage: attn presence [--session-id | --seed-ids]") }},
		{names: []string{"ask"}, run: runAsk, listing: inOtherCommands, banner: true,
			args: "[list|close]", summary: "ask the user something they answer from attn"},
		{names: []string{"turn"}, run: runTurn, listing: inOtherCommands, banner: true,
			args: "open|settle", summary: "put yourself in the user's queue, or take yourself out"},
		{names: []string{"vent"}, run: runVent, listing: inOtherCommands, banner: true,
			args: "[withdraw]", summary: "report what got in your way"},
		{names: []string{"handoff"}, run: func() { runHandoff(os.Args[2:]) }, listing: inOtherCommands, banner: true,
			args: `-m "<letter>"`, summary: "file this crew member's letter; the day turns over", help: writeHandoffHelp},
		{names: []string{"onboarding"}, run: runOnboarding, listing: inOtherCommands, banner: true,
			args: "[done|again]", summary: "show, finish or restart the Chief's onboarding"},
		{names: []string{"skill"}, run: runSkill, listing: inOtherCommands,
			args: "[--reference <name>|--list]", summary: "print the map above, or a capability's rules"},
		{names: []string{"guide"}, run: runGuide, listing: inOtherCommands,
			args: "[<topic>]", summary: "read the user guide"},
		{names: []string{"send"}, run: runSend, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "send files and a message to a session as the user; pair, senders, unpair"},
		{names: []string{"list"}, run: runList, listing: inOtherCommands, banner: true,
			summary: "list sessions and profiles",
			help:    func(w io.Writer) { fmt.Fprintln(w, "usage: attn list\n\nPrint sessions and profiles as JSON.") }},
		{names: []string{"activity"}, run: runActivity, listing: inOtherCommands, banner: true,
			args: "[clear <id>]", summary: "what each agent is doing right now", help: func(w io.Writer) { fmt.Fprint(w, activityUsage) }},
		{names: []string{"state"}, run: runState, listing: inOtherCommands, banner: true,
			args: "explain <id>", summary: "replay why a session's state is what it is"},
		{names: []string{"conversation"}, run: runConversation, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "list, keep forever or forget attn's conversation copies"},
		{names: []string{"worktree"}, run: runWorktree, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "every tracked worktree, the sweep and the keep pin"},
		{names: []string{"preflight"}, run: runPreflight, listing: inOtherCommands, banner: true,
			summary: "diagnose tools, paths, routing, and launch settings"},
		{names: []string{"codex"}, run: runCodex, listing: inOtherCommands, banner: true,
			args: "status|update|restart", summary: "shared Codex versions, its update and restart"},
		{names: []string{"update"}, run: runUpdate, listing: inOtherCommands, banner: true,
			args: "status [--json]", summary: "how attn was installed and whether an update is available",
			help: func(w io.Writer) { fmt.Fprintln(w, updateUsage) }},
		{names: []string{"plugin"}, run: runPluginCommand, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "install, link, list or remove plugins", help: writePluginHelp},
		{names: []string{"debug"}, run: runDebug, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "probe debug artifacts (incidents, logs)"},
		{names: []string{"db"}, run: runDB, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "database maintenance (restore from backup)"},
		{names: []string{"bus"}, run: runBus, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "event bus: consumer cursors, lag, kill switch"},
		{names: []string{"enrollment"}, run: runEnrollment, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "this data folder's enrollment: status, leave"},
		{names: []string{"doc"}, run: runDoc, listing: inOtherCommands, banner: true,
			args: "<command>", summary: "document store: collections, documents, live queries"},
		{names: []string{"vision-check"}, run: runVisionCheck, listing: inOtherCommands,
			args: "<image> <question>", summary: "answer a question about an image (single LLM call)",
			help: func(w io.Writer) { fmt.Fprintln(w, visionCheckUsage) }},
		{names: []string{"daemon"}, run: runDaemonCommand, listing: inOtherCommands, banner: true, checks: checkNothing,
			args: "[ensure|stop]", summary: "run the daemon, ensure it is running, or stop it"},
		{names: []string{"instance"}, run: runInstance, listing: inOtherCommands, checks: checkInstanceOnly,
			args: "<status|resolve|list>", summary: "show / resolve the active instance's resources"},
		{names: []string{"instance-env"}, run: runInstanceEnv, listing: inOtherCommands, checks: checkNothing,
			args: "<instance|--unset>", summary: "print shell commands for selecting an instance"},
		{names: []string{"prompts"}, run: runPrompts, listing: inOtherCommands,
			args: "<command>", summary: "inspect prompt sources and scenario composition"},
		{names: []string{"version", "--version"}, run: runVersionCommand, listing: inOtherCommands, checks: checkNothing,
			args: "[--json]", summary: "print version, channel and commit"},

		{names: []string{"help", "-h", "--help"}, run: func() { writeHelp(os.Stdout) }},
		{names: []string{"ticket"}, run: runTicket, banner: true},
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
	recordUsageError("attn", usageErrorUnknownCommand)
	fmt.Fprintf(os.Stderr, "attn %s: unknown command %q\n\n", buildinfo.Version, name)
	writeHelp(os.Stderr)
	exit(1)
}

func writeHelp(w io.Writer) {
	fmt.Fprintln(w, "usage: attn <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, prompts.RenderText("session", "attn-map", prompts.Values{}))
	fmt.Fprint(w, "\nOther commands:\n")
	for _, command := range topLevelCommands() {
		if command.listing == inOtherCommands {
			fmt.Fprintf(w, "  %-33s %s\n", strings.TrimSpace(command.names[0]+" "+command.args), command.summary)
		}
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
