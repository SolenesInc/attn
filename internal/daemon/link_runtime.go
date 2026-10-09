package daemon

import (
	"context"
	"errors"
	"fmt"
	"syscall"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

type linkRuntime interface {
	harness.Link
	kind() string

	canRun(req *spawnRequest) bool
	enabledForNewLaunches() bool
	admit(profile, conversation string) error
	prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error
	launchFailed(t harness.TerminalID, profile string)
	respawnEnv(t harness.TerminalID) []string

	holder(profile, conversation string) protocol.SessionID
	resolveCaller(t protocol.TerminalID) (session protocol.SessionID, view harness.TerminalID, ours bool)
	shownConversation(t harness.TerminalID) string
	runsLink(t harness.TerminalID) bool
	keepsLoaded(session *protocol.Session) bool

	renamed(sessionID protocol.SessionID, label string)
	closed(sessionID protocol.SessionID)
	profileReleased(profile string)
	setAsideAtClose(sessionID protocol.SessionID, conversation string) bool
	restore(profile, conversation string) error
	transcriptPath(path string) string

	terminalDropped(t harness.TerminalID)
	terminalExited(t harness.TerminalID)
	recoverProcesses(ctx context.Context)
	recoverViews(ctx context.Context)
}

func (d *Daemon) links() []linkRuntime {
	return []linkRuntime{d.codexShared()}
}

func (d *Daemon) linkNamed(kind string) linkRuntime {
	for _, l := range d.links() {
		if kind != "" && l.kind() == kind {
			return l
		}
	}
	return nil
}

func (d *Daemon) linkOf(sessionID protocol.SessionID) linkRuntime {
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return nil
	}
	return d.linkNamed(intent.Link)
}

func (d *Daemon) launchLink(req *spawnRequest) linkRuntime {
	if intent, ok := d.store.LaunchIntent(req.msg.ID); ok {
		if l := d.linkNamed(intent.Link); l != nil && l.canRun(req) {
			return l
		}
		return nil
	}
	for _, l := range d.links() {
		if l.canRun(req) && l.enabledForNewLaunches() {
			return l
		}
	}
	return nil
}

func (d *Daemon) linkProcess(t harness.TerminalID) bool {
	for _, l := range d.links() {
		if l.runsLink(t) {
			return true
		}
	}
	return false
}

func (d *Daemon) linkConversation(sessionID protocol.SessionID) string {
	if d.linkOf(sessionID) == nil {
		return ""
	}
	return d.store.GetSessionConversation(sessionID).NativeID
}

func (d *Daemon) hidden(sessionID protocol.SessionID) bool {
	if len(d.terminals().Of(harness.SessionID(sessionID))) > 0 {
		return false
	}
	return d.store.Get(sessionID) != nil && d.linkConversation(sessionID) != ""
}

func (d *Daemon) keepsWhenLeft(sessionID protocol.SessionID) bool {
	return d.linkConversation(sessionID) != ""
}

func (d *Daemon) linkKeepsLive(session *protocol.Session) bool {
	l := d.linkOf(session.ID)
	return l != nil && (l.keepsLoaded(session) || d.hidden(session.ID))
}

func (d *Daemon) movedOn(sessionID protocol.SessionID, t harness.TerminalID) bool {
	l := d.linkOf(sessionID)
	if l == nil {
		return false
	}
	shown := l.shownConversation(t)
	conversation := d.store.GetSessionConversation(sessionID).NativeID
	return shown != "" && conversation != "" && conversation != shown
}

func (d *Daemon) linkCaller(t protocol.TerminalID) (session protocol.SessionID, view harness.TerminalID, ours bool) {
	for _, l := range d.links() {
		if session, view, ours = l.resolveCaller(t); ours {
			return session, view, true
		}
	}
	return "", "", false
}

func (d *Daemon) linkRenamed(sessionID protocol.SessionID, label string) {
	if l := d.linkOf(sessionID); l != nil {
		l.renamed(sessionID, label)
	}
}

func (d *Daemon) mirrorLabel(sessionID protocol.SessionID) {
	if session := d.store.Get(sessionID); session != nil && !sessionLabelIsPlaceholder(session.Label, session.Directory, session.ID) {
		d.linkRenamed(session.ID, session.Label)
	}
}

func (d *Daemon) linkTerminalDropped(t harness.TerminalID) {
	for _, l := range d.links() {
		l.terminalDropped(t)
	}
}

func (d *Daemon) linkTerminalExited(t harness.TerminalID) {
	for _, l := range d.links() {
		l.terminalExited(t)
	}
}

func (d *Daemon) recoverLinkProcesses(ctx context.Context) {
	for _, l := range d.links() {
		l.recoverProcesses(ctx)
	}
}

func (d *Daemon) recoverLinkViews(ctx context.Context) {
	for _, l := range d.links() {
		l.recoverViews(ctx)
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

func (d *Daemon) showHidden(sessionID protocol.SessionID) error {
	session := d.store.Get(sessionID)
	intent, ok := d.store.LaunchIntent(sessionID)
	conversation := d.linkConversation(sessionID)
	if session == nil || !ok || conversation == "" {
		return fmt.Errorf("session %s holds no linked conversation to show", sessionID)
	}
	spawn, policy := buildStoredIntentSpawn(session, intent, 80, 24)
	spawn.ResumeSessionID = protocol.Ptr(conversation)
	policy.launchPlacement = &launchPlacement{direction: layouttree.DirectionVertical}
	policy.userStarted = true
	client := newInternalWSClient()
	d.handleSpawnSessionWithPolicy(client, spawn, policy)
	if _, err := readInternalActionResult(client); err != nil {
		return fmt.Errorf("show session %s: %w", sessionID, err)
	}
	return nil
}

func (d *Daemon) hide(sessionID protocol.SessionID, t harness.TerminalID) {
	lifecycle := d.sessionLifecycleLockFor(sessionID)
	lifecycle.Lock()
	unlock := d.lockTerminalEnds(sessionID)
	if err := d.ptyBackend.Kill(context.Background(), t, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.logf("stopping terminal %s: %v", t, err)
	}
	d.dropTerminal(t)
	unlock()
	lifecycle.Unlock()
	d.publishFact(FactSessionReregistered, string(sessionID), nil)
}
