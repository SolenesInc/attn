package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/config"
)

var instanceRoutingOverrides = config.RoutingOverrideEnv()

func runInstanceEnv() {
	runInstanceEnvArgs(os.Args[2:])
}

func runInstanceEnvArgs(args []string) {
	fishMode := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--fish":
			fishMode = true
		case "-h", "--help":
			printInstanceEnvHelp()
			return
		default:
			filtered = append(filtered, a)
		}
	}

	if len(filtered) == 0 {
		printInstanceEnvHelp()
		os.Exit(1)
	}

	arg := strings.TrimSpace(filtered[0])
	if arg == "--unset" || arg == "none" || arg == "default" {
		writeInstanceEnv(os.Stdout, "", fishMode)
		return
	}

	if err := config.ValidateInstanceName(arg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	writeInstanceEnv(os.Stdout, arg, fishMode)
}

func writeInstanceEnv(w io.Writer, instance string, fishMode bool) {
	for _, name := range instanceRoutingOverrides {
		if fishMode {
			fmt.Fprintf(w, "set -e %s\n", name)
		} else {
			fmt.Fprintf(w, "unset %s\n", name)
		}
	}
	if fishMode {
		if instance == "" {
			fmt.Fprintln(w, "set -e ATTN_INSTANCE")
			return
		}
		fmt.Fprintf(w, "set -gx ATTN_INSTANCE %s\n", instance)
		return
	}
	if instance == "" {
		fmt.Fprintln(w, "unset ATTN_INSTANCE")
		return
	}
	fmt.Fprintf(w, "export ATTN_INSTANCE=%s\n", instance)
}

func printInstanceEnvHelp() {
	fmt.Fprintln(os.Stderr, `attn instance-env — emit shell commands to set or clear ATTN_INSTANCE

Usage:
  eval "$(attn instance-env dev)"              # bash/zsh: export ATTN_INSTANCE=dev
  eval "$(attn instance-env --unset)"           # bash/zsh: unset ATTN_INSTANCE
  attn instance-env --fish dev | source         # fish: set -gx ATTN_INSTANCE dev
  attn instance-env --fish --unset | source     # fish: set -e ATTN_INSTANCE

Instance names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is reserved for the
development sibling install (port 29849, data dir ~/.attn-dev). Selecting or
clearing an instance also clears inherited ATTN data-dir, socket, database, config,
websocket port, and plugin-directory overrides so the selected instance is
authoritative — one of those left behind makes every attn command refuse to run.`)
}
