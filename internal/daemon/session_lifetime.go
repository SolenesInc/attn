package daemon

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) lifetime(sessionID protocol.SessionID) harness.Lifetime {
	agent, lifetime := d.store.SessionLifetime(sessionID)
	if lifetime == harness.InServer && d.harnessOf(agent).Facts().ServerTerminal == nil {
		return harness.InTerminal
	}
	return lifetime
}

func (d *Daemon) serverOwns(sessionID protocol.SessionID) bool {
	return d.ownedByServer(d.store.SessionLifetime(sessionID))
}

func (d *Daemon) ownedByServer(agent string, lifetime harness.Lifetime) bool {
	return lifetime == harness.InServer && d.harnessOf(agent).Facts().Recovery == harness.ByServer
}

func (d *Daemon) terminalCarriesLaunch(sessionID protocol.SessionID) bool {
	return d.lifetime(sessionID) != harness.InServer
}

func (d *Daemon) conversationShowsInManyTerminals(sessionID protocol.SessionID) bool {
	return d.lifetime(sessionID) == harness.InServer
}

func (d *Daemon) monitorAnchor(sessionID protocol.SessionID) (harness.TerminalID, bool) {
	agent, lifetime := d.store.SessionLifetime(sessionID)
	session := d.store.Get(sessionID)
	anchor := d.harnessOf(agent).Facts().ServerTerminal
	if lifetime != harness.InServer || anchor == nil {
		return "", false
	}
	if session == nil {
		return "", true
	}
	return anchor(session.ProfileID), true
}

func (d *Daemon) admitConversation(session *protocol.Session, conversation string) error {
	if !d.serverOwns(session.ID) {
		return nil
	}
	return d.harnessOf(session.Agent).Conversations().Admit(harness.ConversationRef{Harness: harness.ID(session.Agent), Session: session.ID, Profile: session.ProfileID, Host: protocol.Deref(session.Host), ID: conversation, Lifetime: harness.InServer})
}

func (d *Daemon) released(agent, profile string) {
	d.harnessOf(agent).Released(profile)
}

func (d *Daemon) serverConversation(sessionID protocol.SessionID) string {
	if d.lifetime(sessionID) != harness.InServer {
		return ""
	}
	return d.store.GetSessionConversation(sessionID).NativeID
}

func (d *Daemon) hidden(sessionID protocol.SessionID) bool {
	if len(d.terminals().Of(harness.SessionID(sessionID))) > 0 {
		return false
	}
	return d.store.Get(sessionID) != nil && d.serverConversation(sessionID) != ""
}

func (d *Daemon) forgetProgramStatus(sessionID protocol.SessionID) {
	d.updateEvidence(sessionID, nil, func(e *sessionmonitor.Evidence) {
		e.Heartbeat = nil
		e.ClearProgramStatus()
	})
}

func (d *Daemon) keepsWhenLeft(sessionID protocol.SessionID) bool {
	return d.serverConversation(sessionID) != ""
}

func (d *Daemon) linkRenamed(sessionID protocol.SessionID, label string) {
	if _, err := Call[struct{}](context.Background(), d.router(), protocol.LinkRenameMessage{Cmd: protocol.CmdLinkRename, SessionID: sessionID, Label: label}); err != nil {
		d.logf("link rename: session=%s: %v", sessionID, err)
	}
}

func (d *Daemon) mirrorLabel(sessionID protocol.SessionID) {
	if session := d.store.Get(sessionID); session != nil && !store.SessionLabelIsPlaceholder(session.Label, session.Directory, session.ID) {
		d.linkRenamed(session.ID, session.Label)
	}
}

func (d *Daemon) decorateSessionHidden(clone *protocol.Session) {
	clone.Hidden = nil
	if d.hidden(clone.ID) {
		clone.Hidden = protocol.Ptr(true)
	}
}

func (d *Daemon) decorateLedgerEntriesHidden(entries []protocol.SessionLedgerEntry) {
	for i := range entries {
		d.decorateLedgerEntryHidden(&entries[i])
	}
}

func (d *Daemon) decorateLedgerEntryHidden(entry *protocol.SessionLedgerEntry) {
	if protocol.Deref(entry.ClosedAt) == "" && d.hidden(entry.ID) {
		entry.Hidden = protocol.Ptr(true)
	}
}

func (d *Daemon) serverHolder(profile, host, conversation string) protocol.SessionID {
	sessionID := d.store.OpenSessionHoldingOnHost(profile, d.machineFor(host).ID(), conversation)
	if sessionID == "" || d.lifetime(sessionID) != harness.InServer {
		return ""
	}
	return sessionID
}

func (d *Daemon) spawnShowing(sessionID protocol.SessionID, cols, rows int) (*protocol.SpawnSessionMessage, internalSpawnPolicy, error) {
	session := d.store.Get(sessionID)
	intent, ok := d.store.LaunchIntent(sessionID)
	conversation := d.serverConversation(sessionID)
	if session == nil || !ok || conversation == "" {
		return nil, internalSpawnPolicy{}, fmt.Errorf("session %s holds no linked conversation to show", sessionID)
	}
	spawn, policy := buildStoredIntentSpawn(session, intent, cols, rows)
	spawn.ResumeSessionID = protocol.Ptr(conversation)
	policy.launchPlacement = &launchPlacement{direction: layouttree.DirectionVertical}
	policy.userStarted = true
	return spawn, policy, nil
}

func (d *Daemon) showHidden(sessionID protocol.SessionID, beside *launchPlacement) error {
	spawn, policy, err := d.spawnShowing(sessionID, 80, 24)
	if err != nil {
		return err
	}
	if beside != nil {
		policy.launchPlacement = beside
	}
	policy.onlyIfHidden = true
	return d.resumeAsMember(sessionID, func() error {
		if rejection := d.runSpawnPipeline(spawn, policy); rejection != nil {
			return fmt.Errorf("show session %s: %w", sessionID, rejection.reason())
		}
		return nil
	})
}
