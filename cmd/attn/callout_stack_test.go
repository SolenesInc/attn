package main_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentShowsACalloutAndGetsItsRefusals(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_BROWSER_HOST_TOKEN=callout-stack-secret")
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	refused(t, s.Run(testworld.Invocation{Session: session, Args: []string{"callout", "show", "garden", "Work tracked here"}}), 1,
		"callout show: callouts are off: the user can turn them on in Settings > Experimental > Agent callouts")

	enabled := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "callouts_enabled", Value: "true", RequestID: protocol.Ptr("callouts")},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == "callouts" })
	if !protocol.Deref(enabled.Success) {
		t.Fatal(protocol.Deref(enabled.Error))
	}
	token, err := os.ReadFile(filepath.Join(s.Dir, config.ClientTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	host := s.Connect(protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion,
		BrowserHostToken: protocol.Ptr("callout-stack-secret"), ClientToken: protocol.Ptr(strings.TrimSpace(string(token))),
	}, http.Header{"Origin": []string{"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](host, protocol.EventInitialState, nil)

	show := s.Launch(testworld.Invocation{Session: session, Args: []string{"callout", "show", "garden", "Work tracked here", "agent:" + session[:8], "Enter goes here"}})
	request := testworld.Await[protocol.CalloutShowRequestMessage](host, protocol.EventCalloutShowRequest, nil)
	if len(request.Steps) != 2 || string(protocol.Deref(request.Steps[1].SessionID)) != session {
		t.Fatalf("CLI request = %+v, want the two steps and resolved agent", request)
	}
	host.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: request.RequestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
	if result := show.Wait(); result.Code != 0 || result.Stdout != "showing a callout with 2 steps; the user steps through it\n" || result.Stderr != "" {
		t.Fatalf("callout show = %+v", result)
	}
	refused(t, s.Run(testworld.Invocation{Session: session, Args: []string{"callout", "show", "garden"}}), 1,
		`callout show: an anchor needs its text: "garden" is the last argument`)
	if help := s.Attn("callout", "--help"); help.Code != 0 || !strings.Contains(help.Stdout, "attn callout anchors [--json]") {
		t.Fatalf("callout help = %+v", help)
	}
}
