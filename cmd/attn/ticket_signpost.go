package main

import (
	"fmt"
	"io"
	"os"
)

var ticketSignposts = map[string][]string{
	"list":        {"attn seed ls"},
	"show":        {"attn seed show <seed-id>"},
	"inbox":       {"attn seed ready"},
	"new":         {`attn seed plant "<title>" -m "<brief>"`},
	"comment":     {`attn seed note <seed-id> -m "<note>"`},
	"attach":      {"attn seed attach <seed-id> --path <file>"},
	"attach-plan": {"attn seed attach <seed-id> --path <file> --repo <repository>"},
	"take":        {"attn seed tend <seed-id>"},
	"subscribe":   {"attn seed watch <seed-id>"},
	"unsubscribe": {"attn seed unwatch <seed-id>"},
	"status":      {`attn seed note <seed-id> -m "<progress or decision needed>"`, `attn seed harvest <seed-id> -m "<outcome>"`, "attn seed park <seed-id>", `attn seed wither <seed-id> -m "<reason>"`},
}

func signpostTicketVerb(verb string) {
	fprintTicketSignpost(os.Stderr, verb)
	os.Exit(2)
}

func fprintTicketSignpost(w io.Writer, verb string) {
	commands := ticketSignposts[verb]
	if len(commands) == 0 {
		commands = []string{"attn seed --help"}
	}
	for _, command := range commands {
		fmt.Fprintln(w, command)
	}
}
