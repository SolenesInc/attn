package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func protocolCommands(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../protocol/constants.go", nil, 0)
	if err != nil {
		t.Fatalf("parse protocol constants: %v", err)
	}

	commands := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
			return true
		}
		name := spec.Names[0].Name
		if len(name) < 4 || name[:3] != "Cmd" {
			return true
		}
		literal, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		wire, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		commands[wire] = name
		return true
	})

	if len(commands) < 100 {
		t.Fatalf("found only %d Cmd* constants in ../protocol/constants.go; the parse is broken, not the protocol", len(commands))
	}
	return commands
}

const sessionLedgerIsPerDaemon = "the ledger records the sessions this daemon ran; another daemon's rows are read there"

var sessionCommandsAnsweredWhereTheyLand = map[string]string{
	protocol.CmdRegister:            "arrives from the agent process over the unix socket",
	protocol.CmdState:               "arrives from the agent process over the unix socket",
	protocol.CmdStop:                "arrives from the agent process over the unix socket",
	protocol.CmdTodos:               "arrives from the agent process over the unix socket",
	protocol.CmdFilesEdited:         "arrives from the agent process over the unix socket",
	protocol.CmdHeartbeat:           "arrives from the agent process over the unix socket",
	protocol.CmdHookNotification:    "arrives from the agent process over the unix socket",
	protocol.CmdHookStopFailure:     "arrives from the agent process over the unix socket",
	protocol.CmdHookCompaction:      "arrives from the agent process over the unix socket",
	protocol.CmdSetSessionResumeID:  "arrives from the agent process over the unix socket",
	protocol.CmdSessionInstructions: "arrives from the agent process over the unix socket",
	protocol.CmdSessionTranscript:   "arrives from the agent process over the unix socket",
	protocol.CmdStateExplain:        "arrives from the agent process over the unix socket",
	protocol.CmdAgentPeek:           "arrives from the agent process over the unix socket",
	protocol.CmdAgentMsg:            "arrives from the agent process over the unix socket",
	protocol.CmdAgentClose:          "arrives from the agent process over the unix socket",
	protocol.CmdAgentInbox:          "arrives from the agent process over the unix socket",
	protocol.CmdAgentMsgStatus:      "arrives from the agent process over the unix socket",
	protocol.CmdTicketCreate:        "arrives from the agent process over the unix socket",
	protocol.CmdTicketInbox:         "arrives from the agent process over the unix socket",
	protocol.CmdSetTicketStatus:     "arrives from the agent process over the unix socket",
	protocol.CmdOpenSentFiles:       "arrives from the agent process over the unix socket",

	protocol.CmdUnregister: "handleUnregisterWS forwards to the endpoint itself",

	protocol.CmdSessionList:   sessionLedgerIsPerDaemon,
	protocol.CmdSessionShow:   sessionLedgerIsPerDaemon,
	protocol.CmdSessionReopen: sessionLedgerIsPerDaemon,

	protocol.CmdTicketAttach:   "the ticket board is the hub's own store",
	protocol.CmdBrowserControl: "handleRemoteBrowserControl resolves the browser host itself",
}

func routingProbe(wire string) []byte {
	return []byte(`{"cmd":"` + wire + `","id":"probe","session_id":"probe","target_session_id":"probe",` +
		`"workspace_id":"probe","source_workspace_id":"probe","source_kind":"file","endpoint_id":"probe","directory":"/probe"}`)
}

func routedByAnyRouter(wire string, msg interface{}) bool {
	return remoteCommandSessionID(wire, msg) != "" ||
		remoteCommandWorkspaceID(wire, msg) != "" ||
		remoteCommandPTYTargetID(wire, msg) != ""
}

func TestSessionScopedCommandsReachTheSessionOwner(t *testing.T) {
	unrouted := []string{}
	for wire := range protocolCommands(t) {
		meta, ok := CommandMeta[wire]
		if !ok || meta.Scope != ScopeSession {
			continue
		}
		if reason := sessionCommandsAnsweredWhereTheyLand[wire]; reason != "" {
			continue
		}

		_, msg, err := protocol.ParseMessage(routingProbe(wire))
		if err != nil || msg == nil {
			t.Fatalf("%s is scoped to a session but its message would not parse (%v); the probe payload "+
				"in this test needs the field it identifies its target with", wire, err)
		}

		if routedByAnyRouter(wire, msg) {
			continue
		}
		unrouted = append(unrouted, wire)
	}

	sort.Strings(unrouted)
	if len(unrouted) > 0 {
		t.Fatalf("%d session-scoped command(s) are answered by whichever daemon receives them, so a hub "+
			"answers them against its own store for a session it does not own:\n  %v\n"+
			"Add each to remoteCommandSessionID (or the workspace/PTY router that fits), or to "+
			"sessionCommandsAnsweredWhereTheyLand with the reason it is safe.", len(unrouted), unrouted)
	}
}

func TestSessionCommandExceptionsAreStillNeeded(t *testing.T) {
	commands := protocolCommands(t)
	for wire, reason := range sessionCommandsAnsweredWhereTheyLand {
		if reason == "" {
			t.Errorf("%s is excepted with no reason; the reason is the whole point of the entry", wire)
		}
		if _, ok := commands[wire]; !ok {
			t.Errorf("%s is excepted from session routing but is not a protocol command any more; drop the entry", wire)
			continue
		}
		meta, ok := CommandMeta[wire]
		if !ok || meta.Scope != ScopeSession {
			t.Errorf("%s is excepted from session routing but is not scoped to a session; drop the entry", wire)
			continue
		}
		if _, msg, err := protocol.ParseMessage(routingProbe(wire)); err == nil && msg != nil {
			if routedByAnyRouter(wire, msg) {
				t.Errorf("%s is routed now, so its exception (%q) is stale; drop the entry", wire, reason)
			}
		}
	}
}
