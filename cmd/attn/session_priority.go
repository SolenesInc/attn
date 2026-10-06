package main

import (
	"flag"
	"fmt"
	"github.com/victorarias/attn/internal/client"
	"os"
	"strings"
)

func runSessionPriority(args []string) {
	if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
		fmt.Fprintln(os.Stderr, "usage: attn session priority on|off [--session <id>]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("session priority", flag.ExitOnError)
	session := fs.String("session", os.Getenv("ATTN_SESSION_ID"), "session to mark")
	fs.Parse(args[1:])
	id := strings.TrimSpace(*session)
	if id == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "session priority: pass --session <id> or run inside an attn session")
		os.Exit(2)
	}
	if err := client.New("").SetSessionPriority(id, args[0] == "on"); err != nil {
		fmt.Fprintf(os.Stderr, "session priority: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s priority %s\n", id, args[0])
}
