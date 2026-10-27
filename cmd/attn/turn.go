package main

import (
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func runTurn() {
	os.Exit(executeTurn(os.Args[2:], os.Stdout, os.Stderr))
}

func executeTurn(args []string, stdout, stderr io.Writer) int {
	if hasHelpFlag(args) {
		writeTurnHelp(stdout)
		return 0
	}
	if len(args) != 1 || (args[0] != string(protocol.AgentTurnActionOpen) && args[0] != string(protocol.AgentTurnActionSettle)) {
		writeTurnHelp(stderr)
		return 2
	}
	sessionID := currentSessionOrExit()
	result, err := client.New("").AgentTurn(sessionID, protocol.AgentTurnAction(args[0]))
	if err != nil {
		fmt.Fprintf(stderr, "attn turn %s: %v\n", args[0], err)
		return 1
	}
	fmt.Fprintln(stdout, agentTurnOutcomeText(result.Outcome))
	return 0
}

func agentTurnOutcomeText(outcome protocol.AgentTurnOutcome) string {
	switch outcome {
	case protocol.AgentTurnOutcomeQueueOff:
		return "The user does not work in the queue flow, so there is no queue to join; nothing changed."
	case protocol.AgentTurnOutcomeOpensAtStop:
		return "Your turn opens when you stop; the user will come to you then."
	case protocol.AgentTurnOutcomeOpened:
		return "Your turn is open; the user will come to you."
	case protocol.AgentTurnOutcomeSnoozed:
		return "The user snoozed you; your turn opens once the snooze ends and you have stopped."
	case protocol.AgentTurnOutcomeSettled:
		return "Your turn is settled; you are out of the user's queue."
	case protocol.AgentTurnOutcomeWithdrawn:
		return "Your turn will not open when you stop."
	default:
		return "No turn was open; nothing changed."
	}
}

func writeTurnHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn turn open
       attn turn settle

Put yourself in the user's queue when you cannot continue without them, and
take yourself out again. When a notification from attn woke you (a message, a
seed update, a pull request update, an ask's answer), your stop may not put
you in the queue, and the user may not read what you write. Open your turn
when you need them.

commands:
  open      ask for the user. Run while you work, it opens your turn when you
            stop, so the user is not sent to you while you are still busy.
            A later notification does not settle a turn you opened; the user
            does, or attn turn settle.
  settle    settle a turn you opened, or cancel one that was going to open
            when you stop. A turn that opened when you stopped on your own is
            the user's to settle.

Both act on the session running the command. When the user does not work in
the queue flow, both succeed and change nothing.

Use attn ask instead when you can keep working while the user decides.
`)
}
