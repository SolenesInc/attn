package daemon_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAppCommandsAreRefusedBeforeDispatchWithTheFix(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	app := w.App()
	older := applyAppVersion(t, cli, "reviewer", appManifestDeclaration(t, appbuild.Manifest{Name: "reviewer",
		Commands: []appbuild.Command{{Name: "approve"}}}), "export default {}\n")
	applyAppVersion(t, cli, "reviewer", appManifestDeclaration(t, appbuild.Manifest{Name: "reviewer",
		Commands: []appbuild.Command{{Name: "approve"}, {Name: "reject"}}}), "export default { reject: true }\n")
	if _, err := cli.AppRollback("reviewer", older.VersionID); err != nil {
		t.Fatalf("roll back onto the version without reject: %v", err)
	}

	for _, tc := range []struct {
		name, app, command, payload string
		want                        []string
	}{
		{"a command the serving version does not declare", "reviewer", "reject", "", []string{"reject", "approve", "reviewer"}},
		{"a payload over the limit", "reviewer", "approve", `{"note":"` + strings.Repeat("x", 256*1024) + `"}`, []string{"approve", "reviewer", "262144", "document"}},
		{"a payload that is not JSON", "reviewer", "approve", "{not json", []string{"JSON"}},
		{"an app that is not installed", "ghost", "approve", "", []string{"ghost", "attn app apply"}},
	} {
		refused := requestAppCommand(app, tc.app, tc.command, tc.payload)
		requireAppCommandRefused(t, tc.name, refused, tc.want...)
	}

	app.Send(protocol.AppCommandMessage{Cmd: protocol.CmdAppCommand, App: "reviewer", Command: "approve"})
	if refused := testworld.Await(app, protocol.EventCommandError, func(m protocol.CommandErrorMessage) bool {
		return protocol.Deref(m.Cmd) == protocol.CmdAppCommand
	}); !strings.Contains(refused.Error, "request_id") {
		t.Errorf("a command without a request id was refused with %q, want the missing request_id named", refused.Error)
	}

	if _, err := cli.AppSetEnabled("reviewer", false); err != nil {
		t.Fatalf("disable reviewer: %v", err)
	}
	requireAppCommandRefused(t, "a disabled app", requestAppCommand(app, "reviewer", "approve", ""), "disabled", "attn app enable reviewer")
}

func TestAppStatusCarriesTheServingVersionsCommands(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	applyAppVersion(t, cli, "reviewer", appManifestDeclaration(t, appbuild.Manifest{Name: "reviewer", Commands: []appbuild.Command{
		{Name: "approve", Description: "Approve the request."},
		{Name: "reject"},
	}}), "export default {}\n")

	commands := appStatus(t, cli, "reviewer").App.Commands
	if len(commands) != 2 {
		t.Fatalf("commands = %+v, want both", commands)
	}
	if commands[0].Name != "approve" || protocol.Deref(commands[0].Description) != "Approve the request." {
		t.Errorf("first command = %+v", commands[0])
	}
	if commands[1].Name != "reject" || commands[1].Description != nil {
		t.Errorf("second command = %+v, want reject with no description", commands[1])
	}
}

func requestAppCommand(app *testworld.Peer, name, command, payload string) protocol.AppCommandResultMessage {
	app.T.Helper()
	msg := protocol.AppCommandMessage{Cmd: protocol.CmdAppCommand, RequestID: uuid.NewString(), App: name, Command: command}
	if payload != "" {
		msg.Payload = protocol.Ptr(payload)
	}
	return testworld.Request(app, msg, protocol.EventAppCommandResult, func(r protocol.AppCommandResultMessage) bool {
		return r.RequestID == msg.RequestID
	})
}

func requireAppCommandRefused(t *testing.T, what string, result protocol.AppCommandResultMessage, want ...string) {
	t.Helper()
	if result.Success {
		t.Errorf("%s: the command succeeded, want a refusal: %+v", what, result)
		return
	}
	for _, text := range want {
		if !strings.Contains(protocol.Deref(result.Error), text) {
			t.Errorf("%s: the refusal does not say %q: %s", what, text, protocol.Deref(result.Error))
		}
	}
}
