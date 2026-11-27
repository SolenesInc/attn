package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/script"
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
		{names: []string{"pr"}, run: runPRCommand},

		{names: []string{"preflight"}, run: runPreflight, banner: true},
		{names: []string{"update"}, run: runUpdate, banner: true,
			help: func(w io.Writer) { fmt.Fprintln(w, updateUsage) }},
		{names: []string{"plugin"}, run: runPluginCommand, banner: true,
			help: func(w io.Writer) { fmt.Fprint(w, pluginUsage) }},
		{names: []string{"debug"}, run: runDebug, banner: true},
		{names: []string{"db"}, run: runDB, banner: true},
		{names: []string{"bus"}, run: runBus, banner: true},
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
		{names: []string{"_outpost-install"}, run: runOutpostInstall},
		{names: []string{"_outpost-switch"}, run: runOutpostSwitch},
		{names: []string{"_update-apply"}, run: runUpdateApply, checks: checkNothing, unrecorded: true},
		{names: []string{"_bridge"}, run: runBridge, unrecorded: true},
		{names: []string{"_reverse-bridge"}, run: runReverseBridge, unrecorded: true},
		{names: []string{"_probe-tui"}, run: runProbeTUI},
		{names: []string{"_run-command"}, run: runWithoutDaemon, checks: checkNothing, unrecorded: true},
		{names: []string{script.Command}, run: runScript, checks: checkNothing, unrecorded: true},
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
		openAppWithDeepLink()
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
		openAppWithDeepLink()
		return
	}
	fmt.Fprintf(os.Stderr, "attnd: unknown command %q\n", name)
	exit(1)
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
