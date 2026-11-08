package main

import (
	"errors"
	"flag"
	"os"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	usageErrorUnknownCommand = "unknown_command"
	usageErrorUnknownFlag    = "unknown_flag"
	usageErrorMissingValue   = "missing_value"
	usageErrorInvalidValue   = "invalid_value"
	usageErrorUsage          = "usage"
	cliErrorRefused          = "refused"
	cliErrorFailed           = "failed"
)

type invocationRecord struct {
	path       string
	caller     protocol.TerminalID
	session    protocol.SessionID
	usageError string
	unrecorded bool
}

var invocation = invocationRecord{path: "attn"}

func setCommandPath(path string) {
	invocation.path = path
}

func actAsCaller(terminal protocol.TerminalID) {
	invocation.caller = terminal
}

func recordAsClosedSession(session protocol.SessionID) {
	invocation.session = session
}

func leaveInvocationUnrecorded() {
	invocation.unrecorded = true
}

func recordUsageError(path, kind string) {
	invocation.path, invocation.usageError = path, kind
}

func exit(code int) {
	recordInvocation(code)
	os.Exit(code)
}

func recordInvocation(code int) {
	if invocation.unrecorded {
		return
	}
	props := map[string]any{"ok": code == 0, "exit_code": code}
	if kind, refusal := invocationError(code); kind != "" {
		props["error_kind"] = kind
		if refusal != "" {
			props["refusal"] = refusal
		}
	}
	c := client.New("")
	if invocation.caller != "" {
		c = c.WithCaller(invocation.caller)
	}
	_ = c.RecordAnalytics("cli", invocation.path, invocation.session, props)
}

func invocationError(code int) (kind, refusal string) {
	if invocation.usageError != "" {
		return invocation.usageError, ""
	}
	if code == 0 {
		return "", ""
	}
	if reason, refused := client.LastRefusal(); refused {
		return cliErrorRefused, string(reason)
	}
	if code == 2 {
		return usageErrorUsage, ""
	}
	return cliErrorFailed, ""
}

func setCommandPathIn(group, word string, known ...string) {
	if slices.Contains(known, word) {
		setCommandPath(group + " " + word)
	}
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	handling := fs.ErrorHandling()
	fs.Init(fs.Name(), flag.ContinueOnError)
	err := fs.Parse(args)
	fs.Init(fs.Name(), handling)
	if err == nil {
		return nil
	}
	if !errors.Is(err, flag.ErrHelp) {
		recordUsageError(invocation.path, flagErrorKind(err))
	}
	if handling == flag.ExitOnError {
		if errors.Is(err, flag.ErrHelp) {
			exit(0)
		}
		exit(2)
	}
	return err
}

func flagErrorKind(err error) string {
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "flag provided but not defined"):
		return usageErrorUnknownFlag
	case strings.HasPrefix(message, "flag needs an argument"):
		return usageErrorMissingValue
	default:
		return usageErrorInvalidValue
	}
}
