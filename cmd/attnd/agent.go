package main

import (
	"flag"
	"strings"

	"github.com/victorarias/attn/internal/client"
)

const agentShortIDLength = 8

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
