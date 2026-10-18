package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func writeSettingsHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn settings <command> [--profile <name|id>]

Read and change settings. list shows which apply to one profile or all profiles.
Inside attn, use the session's profile. Outside attn, list and profile settings
need --profile when several profiles exist. Daemon settings need no profile.

commands:
  list [--all] [--json]  show keys, scopes, values and descriptions;
                       --all adds read-only values attn computes
  get <key> [--json]    show the saved value; empty when unset
  set <key> <value>     change a setting; "" restores the default
                       if the description allows it
`)
}

func runSettings() {
	args := os.Args[2:]
	if len(args) == 0 || hasHelpFlag(args) {
		writeSettingsHelp(os.Stdout)
		return
	}
	verb := args[0]
	fs := flag.NewFlagSet("settings "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "profile name or id outside an attn session")
	jsonOut := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "include read-only values")
	var positionals []string
	rest := args[1:]
	usage := func(message string) {
		fmt.Fprintln(os.Stderr, "settings: "+message)
		writeSettingsHelp(os.Stderr)
		os.Exit(2)
	}
	for {
		if err := fs.Parse(rest); err != nil {
			usage(err.Error())
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	switch verb {
	case "list":
		if len(positionals) != 0 {
			usage("list takes no arguments")
		}
	case "get":
		if len(positionals) != 1 || *all {
			usage("get needs one key")
		}
	case "set":
		if len(positionals) != 2 || *all {
			usage("set needs a key and value")
		}
	default:
		usage(fmt.Sprintf("unknown command %q", verb))
	}
	session := currentSessionOrExit()
	c := client.New("")
	refusal := func(err error) { fmt.Fprintln(os.Stderr, "settings: "+err.Error()); os.Exit(1) }
	if verb == "set" {
		entry, err := c.SetSetting(session, *profile, positionals[0], positionals[1])
		if err != nil {
			refusal(err)
		}
		if *jsonOut {
			printJSON(entry)
		} else {
			fmt.Printf("%s = %s\n", entry.Key, protocol.Deref(entry.Value))
		}
		return
	}
	key := ""
	if verb == "get" {
		key = positionals[0]
	}
	result, err := c.Settings(session, *profile, key, *all)
	if err != nil {
		refusal(err)
	}
	if verb == "get" {
		entry := result.Entries[0]
		if *jsonOut {
			printJSON(entry)
		} else {
			fmt.Println(protocol.Deref(entry.Value))
		}
		return
	}
	if *jsonOut {
		printJSON(result)
		return
	}
	if result.ProfileID != "" {
		fmt.Printf("profile %s (%s)\n", result.ProfileName, result.ProfileID)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, entry := range result.Entries {
		scope := "all profiles"
		if entry.Scope == protocol.SettingScopeProfile {
			scope = "this profile"
		}
		value := protocol.Deref(entry.Value)
		if value == "" {
			value = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", entry.Key, scope, value, entry.Description)
	}
	_ = w.Flush()
}
