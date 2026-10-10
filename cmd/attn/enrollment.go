package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
)

func runEnrollment() {
	if len(os.Args) < 3 {
		runEnrollmentStatus(nil)
		return
	}
	switch os.Args[2] {
	case "-h", "--help", "help":
		writeEnrollmentHelp(os.Stdout)
	case "status":
		runEnrollmentStatus(os.Args[3:])
	case "leave":
		runEnrollmentLeave(os.Args[3:])
	case "--json":
		runEnrollmentStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "enrollment: unknown command %q\n", os.Args[2])
		writeEnrollmentHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeEnrollmentHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn enrollment <command>

Older attn versions could enroll a data folder to another daemon. This attn
never does, and refuses to start in a folder that is still enrolled.

commands:
  status [--json]
        show this daemon's id and whether its data folder is enrolled to
        another daemon.

  leave [--json]
        release the enrollment so the data folder is its own again.
`)
}

func enrollmentDataRoot() string {
	return config.DataDir()
}

func runEnrollmentStatus(args []string) {
	fs := flag.NewFlagSet("enrollment status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the status as JSON")
	_ = fs.Parse(args)

	root := enrollmentDataRoot()
	status, err := enrollment.Load(root)
	if err != nil && *asJSON {
		printJSON(map[string]any{
			"daemon_id": status.DaemonID,
			"error":     err.Error(),
		})
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment: %v\n", err)
		if errors.Is(err, enrollment.ErrNoRecord) {
			fmt.Fprintf(os.Stderr, "No daemon has started in %s yet; the record is written at startup.\n", root)
		}
		os.Exit(1)
	}

	if *asJSON {
		printJSON(map[string]any{
			"daemon_id":      status.DaemonID,
			"home_daemon_id": status.HomeDaemonID,
			"is_home":        status.IsHome(),
			"enrollment":     status.Describe(),
		})
		return
	}
	writeEnrollmentStatus(os.Stdout, status)
}

func writeEnrollmentStatus(w io.Writer, status enrollment.Status) {
	fmt.Fprintf(w, "daemon:     %s\n", status.DaemonID)
	fmt.Fprintf(w, "enrollment: %s\n", status.Describe())
	if !status.IsHome() {
		fmt.Fprintln(w, "\nThis data folder was enrolled to another daemon by an older attn; run `attn enrollment leave`.")
	}
}

func runEnrollmentLeave(args []string) {
	fs := flag.NewFlagSet("enrollment leave", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	_ = fs.Parse(args)

	result, err := enrollment.Leave(enrollmentDataRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment leave: %v\n", err)
		if errors.Is(err, enrollment.ErrNoDaemonID) {
			fmt.Fprintf(os.Stderr, "No daemon has started in %s yet, so there is nothing to release.\n", enrollmentDataRoot())
		}
		os.Exit(1)
	}
	if *asJSON {
		if err := fprintJSON(os.Stdout, result); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
		}
		return
	}
	fmt.Fprintln(os.Stdout, result.Message)
}
