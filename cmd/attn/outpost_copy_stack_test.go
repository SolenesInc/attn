package main_test

import (
	"reflect"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type copiedSession struct {
	Label, Directory, Agent, Host string
	Priority                      bool
}

func sessionsOn(t *testing.T, s *testworld.Stack, host string) map[protocol.SessionID]copiedSession {
	t.Helper()
	sessions, err := s.Client().Query("")
	if err != nil {
		t.Fatal(err)
	}
	out := map[protocol.SessionID]copiedSession{}
	for _, session := range sessions {
		if session.Host == nil || host != "" && *session.Host != host {
			continue
		}
		out[session.ID] = copiedSession{Label: session.Label, Directory: session.Directory, Agent: session.Agent, Host: *session.Host, Priority: protocol.Deref(session.Priority)}
	}
	return out
}

func TestOutpostCopyFollowsTheHomeForItsTerminalsOnly(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
	}
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	remote.Start()
	home.Start()
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(p, "outpost", protocol.OutpostStateConnected).ID

	awaitCopy := func() {
		t.Helper()
		for {
			applied.Await()
			want, got := sessionsOn(t, home, host), sessionsOn(t, remote, "")
			applied.Release()
			if reflect.DeepEqual(want, got) {
				return
			}
		}
	}
	spawnRemote := func(label, directory string) protocol.SessionID {
		t.Helper()
		return protocol.SessionID(home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), remote.Path(directory), func(msg *protocol.SpawnSessionMessage) {
			msg.Host = &host
			msg.Label = &label
		}))
	}
	remoteShell := spawnRemote("remote shell", "remote")
	home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local"))
	priority := func(on bool) {
		t.Helper()
		p.Send(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: remoteShell, Priority: on})
		testworld.AwaitSession(p, string(remoteShell), func(s protocol.Session) bool { return protocol.Deref(s.Priority) == on })
	}
	awaitCopy()
	created := sessionsOn(t, remote, "")
	if _, ok := created[remoteShell]; !ok || len(created) != 1 {
		t.Fatalf("copy after create = %v, want only the shell whose host is the outpost", created)
	}

	priority(true)
	awaitCopy()
	priority(false)
	awaitCopy()
	before := sessionsOn(t, remote, "")

	remote.Stop()
	staged := remote.PauseAt(pausepoint.CopyBatchStaged)
	remote.Start()
	priority(true)
	staged.Await()
	remote.Kill()
	staged.Release()
	home.Stop()
	remote.Start()
	remote.App()
	if got := sessionsOn(t, remote, ""); !reflect.DeepEqual(got, before) {
		t.Fatalf("outpost killed mid-batch and restarted: copy %v, want the previous whole copy %v", got, before)
	}
	home.Start()
	p = home.App()
	outpostState(p, "outpost", protocol.OutpostStateConnected)
	staged.Await()
	staged.Release()
	awaitCopy()
	if !sessionsOn(t, remote, "")[remoteShell].Priority {
		t.Fatal("the batch the outpost lost was not sent again")
	}

	testworld.Request(p, protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: remoteShell}, protocol.EventSessionCloseResult,
		func(r protocol.SessionCloseResultMessage) bool { return r.SessionID == remoteShell })
	awaitCopy()
	spawnRemote("kept shell", "kept")
	awaitCopy()

	remote.Stop()
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
	testworld.Await[protocol.OutpostsUpdatedMessage](p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	remote.Start()
	remote.App()
	if got := sessionsOn(t, remote, ""); len(got) != 0 {
		t.Fatalf("a daemon started after an offline leave kept the copy: %v", got)
	}

}

func TestReconnectRestoresTheCopyAfterAnOutpostLeaves(t *testing.T) {
	a := testworld.NewSSHAgent(t)
	remote, home := testworld.NewStack(t), testworld.NewStack(t)
	applied := remote.PauseEvery(pausepoint.CopyBatchApplied)
	for _, s := range []*testworld.Stack{remote, home} {
		a.Authorize(s.World)
		s.Start()
	}
	address := bridgeAddress(t)
	requireStdout(t, remote.Attn("settings", "set", "outpost_listen", address), address)
	p := enableOutposts(t, home)
	p.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Address: address})
	host := outpostState(p, "outpost", protocol.OutpostStateConnected).ID
	home.Spawn(p, fakeagent.Harness(protocol.SessionAgentShell), remote.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &host })
	awaitCopy := func() {
		t.Helper()
		for {
			applied.Await()
			equal := reflect.DeepEqual(sessionsOn(t, home, host), sessionsOn(t, remote, ""))
			applied.Release()
			if equal {
				return
			}
		}
	}
	awaitCopy()
	requireStdout(t, home.Attn("enrollment", "leave"), "not enrolled")
	if kept := sessionsOn(t, home, host); len(kept) != 1 {
		t.Fatalf("a home leaving enrollment dropped its own shell: %v", kept)
	}
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	awaitCopy()
	if copied := sessionsOn(t, remote, ""); len(copied) != 1 {
		t.Fatalf("re-pairing did not restore the home's shell: %v", copied)
	}
	p.Send(protocol.RemoveOutpostMessage{Cmd: protocol.CmdRemoveOutpost, ID: host})
	testworld.Await(p, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool { return len(m.Outposts) == 0 })
	requireStdout(t, remote.Attn("enrollment", "leave"), "no longer enrolled")
	if copied := sessionsOn(t, remote, ""); len(copied) != 0 {
		t.Fatalf("leave after Remove kept copied shells: %v", copied)
	}
}
