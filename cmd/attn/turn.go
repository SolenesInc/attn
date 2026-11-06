package main

import (
	"fmt"
	"io"
	"os"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func runTurn() {
	exit(executeTurn(os.Args[2:], os.Stdout, os.Stderr))
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
	case protocol.AgentTurnOutcomeOpensAtStop:
		return "Your turn opens when you stop; it then shows in the user's queue and notifies their phone, if they use them."
	case protocol.AgentTurnOutcomeOpened:
		return "Your turn is open; it shows in the user's queue and notifies their phone, if they use them."
	case protocol.AgentTurnOutcomeSnoozed:
		return "The user snoozed you; your turn opens once the snooze ends and you have stopped."
	case protocol.AgentTurnOutcomeSettled:
		return "Your turn is settled."
	case protocol.AgentTurnOutcomeWithdrawn:
		return "Your turn will not open when you stop."
	default:
		return "No turn was open; nothing changed."
	}
}

func writeTurnHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn turn open
       attn turn settle

Ask for the user when you cannot continue without them, or when they should
read what you finished, and take it back again. Your turn shows in the user's
queue and notifies their phone, if they use them. When a notification from attn
woke you (a message, a seed update, a pull request update, an ask's answer),
your stop does not notify their phone and may not put you in the queue, so the
user may not read what you write. Open your turn when you need them or have
finished work for them to read.

commands:
  open      ask for the user. Run while you work, it opens your turn when you
            stop, so the user is not sent to you while you are still busy.
            A later notification does not settle a turn you opened; the user
            does, or attn turn settle.
  settle    settle a turn you opened, or cancel one that was going to open
            when you stop. A turn that opened when you stopped on your own is
            the user's to settle.

Both act on the session running the command.

Use attn ask instead when you can keep working while the user decides.
`)
}
