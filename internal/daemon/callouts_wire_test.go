package daemon_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCalloutsCheckTheLiveSettingAndRefuseInvalidSteps(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	session, _, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	defer exitShells(app, string(session.ID))
	step := []protocol.CalloutShowStep{{Anchor: "garden", Text: "Work you and your agents track"}}
	for _, enabled := range []string{"false", "true", "false"} {
		setSetting(t, app, "callouts_enabled", enabled)
		_, showErr := cli.CalloutShow(session.ID, step)
		_, anchorsErr := cli.CalloutAnchors(session.ID)
		if enabled == "true" {
			calloutRefused(t, showErr, "the attn app is not showing your profile")
			if anchorsErr != nil {
				t.Fatal(anchorsErr)
			}
		} else {
			for _, err := range []error{showErr, anchorsErr} {
				calloutRefused(t, err, "Settings > Experimental > Agent callouts")
			}
		}
	}
	setSetting(t, app, "callouts_enabled", "true")
	for _, tc := range []struct {
		steps []protocol.CalloutShowStep
		want  string
	}{
		{nil, "at least one step"},
		{[]protocol.CalloutShowStep{{Anchor: "queue-bar", Text: "Queue"}}, `unknown anchor "queue-bar"; anchors: sidebar, home, commands, garden, desktops, queue, crew, settings-button, agent:<who>, desktop:<which>`},
		{[]protocol.CalloutShowStep{{Anchor: "home", Text: " "}}, "step 1 needs its text"},
		{[]protocol.CalloutShowStep{{Anchor: "home", Text: strings.Repeat("é", 161)}}, "step 1 is 161 characters; the limit is 160"},
		{make([]protocol.CalloutShowStep, 21), "a callout has at most 20 steps; this one has 21"},
	} {
		_, err := cli.CalloutShow(session.ID, tc.steps)
		calloutRefused(t, err, tc.want)
	}
	listed, err := cli.CalloutAnchors(session.ID)
	if err != nil || listed.AppShowingProfile || len(listed.Anchors) != 10 || listed.Anchors[0].OnScreen != nil {
		t.Fatalf("anchors without an app = %+v (%v), want the directory with no visibility claims", listed, err)
	}
}

func TestCalloutsReachOnlyTheTrustedAppShowingTheCallersProfile(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "callouts_enabled", "true")
	session, desktopID, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	defer exitShells(app, string(session.ID))
	profileID := app.SelectedProfile()
	mustProfileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: "name-callout-desktop", DesktopID: desktopID, Name: "Shop"}, "name-callout-desktop")
	target := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
	selectProfile(target, profileID)
	otherProfile := createProfile(app, "Other world")
	newest := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
	selectProfile(newest, otherProfile.ID)
	mobile := calloutAppPeer(w.World, "http://localhost:3000", browserHostToken)
	selectProfile(mobile, profileID)

	answered := make(chan browserControlAnswer, 1)
	go func() {
		data, err := cli.CalloutShow(session.ID, []protocol.CalloutShowStep{
			{Anchor: "garden", Text: "Work tracked here"},
			{Anchor: "agent:" + string(session.ID)[:8], Text: "Enter goes to this agent"},
			{Anchor: "desktop:" + desktopID, Text: "Enter goes to this desktop"},
		})
		answered <- browserControlAnswer{data, err}
	}()
	request := testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil)
	if request.ProfileID != profileID || len(request.Steps) != 3 || request.From == "" || request.Steps[1].Anchor != protocol.CalloutAnchorAgent || protocol.Deref(request.Steps[1].SessionID) != session.ID || protocol.Deref(request.Steps[1].Label) == "" || protocol.Deref(request.Steps[2].DesktopID) != desktopID {
		t.Fatalf("app request = %+v, want resolved agent and desktop steps with labels", request)
	}
	if label := protocol.Deref(request.Steps[2].Label); label != "Shop" {
		t.Errorf("desktop label = %q, want its displayed name Shop", label)
	}
	newest.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: request.RequestID, Missing: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
	browserPeerCaughtUp(newest)
	target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: request.RequestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
	if got := <-answered; got.err != nil || got.data != "showing a callout with 3 steps; the user steps through it" {
		t.Fatalf("callout show = %q (%v), want the target app's acceptance", got.data, got.err)
	}

	listed := make(chan *protocol.CalloutAnchorsResult, 1)
	go func() {
		result, err := cli.CalloutAnchors(session.ID)
		if err != nil {
			t.Error(err)
		}
		listed <- result
	}()
	anchors := testworld.Await[protocol.CalloutAnchorsRequestMessage](target, protocol.EventCalloutAnchorsRequest, nil)
	if anchors.ProfileID != profileID {
		t.Fatalf("anchor request profile = %q, want %q", anchors.ProfileID, profileID)
	}
	target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: anchors.RequestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorHome}})
	result := <-listed
	if result == nil || !result.AppShowingProfile || protocol.Deref(result.Anchors[0].OnScreen) || !protocol.Deref(result.Anchors[1].OnScreen) || result.Anchors[8].OnScreen != nil {
		t.Fatalf("anchor visibility = %+v, want fixed visibility and dynamic anchors without it", result)
	}
	target.Close()
	_, err := cli.CalloutShow(session.ID, []protocol.CalloutShowStep{{Anchor: "home", Text: "Home"}})
	calloutRefused(t, err, "the attn app is not showing your profile")
}

func TestCalloutsRefuseRepliesAfterTheAppSwitchesProfiles(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "callouts_enabled", "true")
	session, _, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	defer exitShells(app, string(session.ID))
	profileID := app.SelectedProfile()
	other := createProfile(app, "Other world")
	target := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
	for _, anchors := range []bool{false, true} {
		selectProfile(target, profileID)
		answered := make(chan error, 1)
		go func() {
			if anchors {
				_, err := cli.CalloutAnchors(session.ID)
				answered <- err
			} else {
				_, err := cli.CalloutShow(session.ID, []protocol.CalloutShowStep{{Anchor: "garden", Text: "Work"}})
				answered <- err
			}
		}()
		var requestID string
		if anchors {
			requestID = testworld.Await[protocol.CalloutAnchorsRequestMessage](target, protocol.EventCalloutAnchorsRequest, nil).RequestID
		} else {
			requestID = testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil).RequestID
		}
		selectProfile(target, other.ID)
		target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: requestID, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden}})
		calloutRefused(t, <-answered, "the attn app switched away from your profile")
	}
}

func TestCalloutsReportMissingAnchorsAndDisconnects(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "callouts_enabled", "true")
	session, _, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	defer exitShells(app, string(session.ID))
	target := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
	selectProfile(target, app.SelectedProfile())
	for _, disconnect := range []bool{false, true} {
		answered := make(chan error, 1)
		go func() {
			_, err := cli.CalloutShow(session.ID, []protocol.CalloutShowStep{{Anchor: "garden", Text: "Work"}, {Anchor: "commands", Text: "Commands"}})
			answered <- err
		}()
		request := testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil)
		if disconnect {
			target.Close()
			calloutRefused(t, <-answered, "the attn app disconnected")
		} else {
			target.Send(protocol.CalloutResultMessage{Cmd: protocol.CmdCalloutResult, RequestID: request.RequestID, Missing: []protocol.CalloutAnchor{protocol.CalloutAnchorGarden, protocol.CalloutAnchorCommands}, OnScreen: []protocol.CalloutAnchor{protocol.CalloutAnchorSidebar, protocol.CalloutAnchorHome}})
			calloutRefused(t, <-answered, "not on screen: garden, commands; on screen now: sidebar, home")
		}
	}
}

func TestCalloutsTreatAnotherProfilesAgentAsNotFound(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "callouts_enabled", "true")
	caller, _, _ := w.RequestSpawn(app, shellHarness, w.Path("caller"))
	other := createProfile(app, "Private")
	otherApp := w.AppOn(other.ID)
	stranger, _, _ := w.RequestSpawn(otherApp, shellHarness, w.Path("stranger"))
	defer exitShells(app, string(caller.ID))
	defer exitShells(otherApp, string(stranger.ID))
	for _, target := range []string{string(stranger.ID), string(stranger.ID)[:8], "zz9"} {
		_, err := cli.CalloutShow(caller.ID, []protocol.CalloutShowStep{{Anchor: "agent:" + target, Text: "Who?"}})
		calloutRefused(t, err, "agent:"+target+" is not an agent in your profile")
	}
}

func TestCalloutsNameTheReplyTimeoutWhenTheAppDoesNotAnswer(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", browserHostToken)
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		injectAgent(t, w, "callout-source")
		setSetting(t, app, "callouts_enabled", "true")
		target := calloutAppPeer(w.World, "tauri://localhost", browserHostToken)
		selectProfile(target, app.SelectedProfile())
		answered := make(chan error, 1)
		go func() {
			_, err := cli.CalloutShow("callout-source", []protocol.CalloutShowStep{{Anchor: "home", Text: "Home"}})
			answered <- err
		}()
		testworld.Await[protocol.CalloutShowRequestMessage](target, protocol.EventCalloutShowRequest, nil)
		w.advance(10 * time.Second)
		calloutRefused(t, <-answered, "the attn app did not answer within 10s")
	})
}

func calloutRefused(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("callout error = %v, want %q", err, want)
	}
}

func calloutAppPeer(w *testworld.World, origin, token string) *testworld.Peer {
	w.T.Helper()
	p := w.Connect(protocol.ClientHelloMessage{
		Cmd: protocol.CmdClientHello, ClientKind: "tauri-app", Version: "protocol-" + protocol.ProtocolVersion,
		BrowserHostToken: protocol.Ptr(token), ClientToken: protocol.Ptr(config.ClientToken()),
	}, http.Header{"Origin": []string{origin}})
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}
