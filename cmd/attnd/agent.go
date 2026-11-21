package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
)

const agentShortIDLength = 8

func runAgent() {
	if len(os.Args) < 3 || os.Args[2] != "attachment" {
		recordUsageError("agent", usageErrorUnknownCommand)
		fmt.Fprintln(os.Stderr, "usage: attn agent <command> (attn agent --help)")
		exit(2)
	}
	setCommandPath("agent attachment")
	runAgentAttachment(os.Args[3:])
}

func crewProfileFlag(fs *flag.FlagSet) *string {
	return fs.String("profile", "", "the profile whose crew to address, by name or id; inside attn, your session's profile")
}

func crewClient(profile string) *client.Client {
	return client.New("").WithGardenProfile(strings.TrimSpace(profile), currentSessionOrExit())
}

func agentShortID(id string) string {
	if len(id) <= agentShortIDLength {
		return id
	}
	return id[:agentShortIDLength]
}
