package main

import (
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
)

const (
	usageErrorUnknownCommand = "unknown_command"
	usageErrorUnknownFlag    = "unknown_flag"
	usageErrorMissingValue   = "missing_value"
	usageErrorInvalidValue   = "invalid_value"
)

func recordUsageError(verb, kind string) {
	_ = client.New("").RecordAnalytics("cli", verb, map[string]any{"ok": false, "error_kind": kind})
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
		recordUsageError(fs.Name(), flagErrorKind(err))
	}
	if handling == flag.ExitOnError {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
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
