package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/selfupdate"
)

const updateUsage = `usage: attn update status [--json]
       attn update request [--reason <text>]

status shows how attn was installed, the current build, what is available, and
the current update run. On an outpost, the home answers.

request asks the user to update attn. The user decides in the app; the asking
session gets an inbox notice when the update finishes.`

func runUpdate() {
	if len(os.Args) >= 3 && os.Args[2] == "request" {
		runUpdateRequest()
		return
	}
	if len(os.Args) < 3 || os.Args[2] != "status" {
		fmt.Fprintln(os.Stderr, updateUsage)
		os.Exit(2)
	}
	flags := flag.NewFlagSet("update status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "")
	if err := flags.Parse(os.Args[3:]); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, updateUsage)
		os.Exit(2)
	}
	status, err := client.New("").UpdateStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn update status: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(status)
		return
	}
	fprintUpdateStatus(os.Stdout, *status)
}

func fprintUpdateStatus(w io.Writer, status protocol.UpdateStatus) {
	if outpost := protocol.Deref(status.Outpost); outpost != "" {
		fmt.Fprintf(w, "Install:    outpost %s; its home is a %s install\n", outpost, selfupdate.Kind(protocol.Deref(status.HomeKind)).Label())
	} else {
		fmt.Fprintf(w, "Install:    %s\n", selfupdate.Kind(status.Kind).Label())
	}
	fmt.Fprintf(w, "Build:      %s, commit %s\n", status.Version, status.Commit)
	fmt.Fprintf(w, "Available:  %s\n", updateAvailability(status))
	fmt.Fprintf(w, "Update run: %s\n", updateRunSummary(status.Run))
	if refusal := protocol.Deref(status.Refusal); refusal != "" {
		fmt.Fprintln(w, refusal)
	}
}

func updateRunSummary(run *protocol.UpdateRun) string {
	if run == nil {
		return "none"
	}
	if outcome := protocol.Deref(run.Outcome); outcome != "" {
		return string(run.Phase) + ": " + outcome
	}
	return string(run.Phase) + ", log at " + run.LogPath
}

func runUpdateRequest() {
	flags := flag.NewFlagSet("update request", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reason := flags.String("reason", "", "")
	if err := flags.Parse(os.Args[3:]); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, updateUsage)
		os.Exit(2)
	}
	c := client.New("")
	caller, err := currentSession(c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn update request: %v\n", err)
		os.Exit(1)
	}
	result, err := c.RequestUpdate(caller, *reason)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attn update request: %v\n", err)
		os.Exit(1)
	}
	if result.JoinedRun != nil {
		fmt.Printf("An update is already running (phase %s). You will get an inbox notice when it finishes.\n", result.JoinedRun.Phase)
		return
	}
	fmt.Println("Asked the user to update attn. You will get an inbox notice when it finishes, or if the user declines.")
}

func updateAvailability(status protocol.UpdateStatus) string {
	if version := protocol.Deref(status.AvailableVersion); version != "" {
		return version + " " + protocol.Deref(status.AvailableURL)
	}
	if checkError := protocol.Deref(status.CheckError); checkError != "" {
		return "unknown, the last check failed: " + checkError
	}
	if checkedAt := protocol.Deref(status.CheckedAt); checkedAt != "" {
		return "up to date, checked " + checkedAt
	}
	return "not checked"
}
