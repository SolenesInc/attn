package fakeagent

import (
	"errors"
	"os"

	"github.com/google/uuid"
)

var copilotComposer = composer{prompt: "> "}

var copilotFlags = flagSpec{
	values: map[string]bool{"--interactive": true, "-i": true, "--model": true},
}

type copilot struct {
	term         *terminal
	conversation string
	prompt       string
}

func runCopilot(cfg config) int {
	return serve(cfg, copilotComposer, &copilot{})
}

func (c *copilot) begin(term *terminal) error {
	c.term = term
	c.prompt = copilotFlags.parse(os.Args[1:]).value("--interactive", "-i")
	c.conversation = uuid.NewString()
	return nil
}

func (c *copilot) launch() launch {
	return launch{Harness: Copilot, ConversationID: c.conversation}
}

func (c *copilot) initialPrompt() string { return c.prompt }

func (c *copilot) submit(string) error { return nil }

func (c *copilot) reply(text string, afterStop bool) error {
	if afterStop {
		return errors.New("copilot has no Stop hook to reply after")
	}
	c.term.print(text)
	return nil
}
