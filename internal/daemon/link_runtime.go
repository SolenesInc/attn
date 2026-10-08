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

// A linkRuntime runs sessions whose harness attn reaches over a link as well as through their
// terminals, so a session's conversation can outlive every terminal that shows it.
type linkRuntime interface {
	harness.Link
	kind() string

	// fits reports whether the link can run req; enabled, whether launches not yet recorded take it.
	fits(req *spawnRequest) bool
	enabled() bool
	// admit refuses a launch over this link that cannot resume conversation now.
	admit(profile, conversation string) error
	prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error
	launchFailed(t harness.TerminalID, profile string)
	respawnEnv(t harness.TerminalID) []string

	// holder is the open session running conversation over this link.
	holder(profile, conversation string) protocol.SessionID
	// caller resolves the terminal ids the link's own process gives the hooks and tools it runs.
	caller(t protocol.TerminalID) (session protocol.SessionID, view harness.TerminalID, ours bool)
	// shows is the conversation terminal t shows, or "" while unknown.
	shows(t harness.TerminalID) string
	// process reports whether t runs the link itself rather than a view.
	process(t harness.TerminalID) bool
	// holds reports whether the link keeps session's conversation loaded with no terminal.
	holds(session *protocol.Session) bool

	renamed(sessionID protocol.SessionID, label string)
	closed(sessionID protocol.SessionID)
	// released follows a session's teardown; the profile may no longer need the link.
	released(profile string)
	// setAside reports whether closing the session put conversation where restore brings it back.
	setAside(sessionID protocol.SessionID, conversation string) bool
	restore(profile, conversation string) error
	transcriptPath(path string) string

	terminalDropped(t harness.TerminalID)
	terminalExited(t harness.TerminalID)
	// recoverProcesses runs before startup decides which sessions are live; recoverViews after.
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

// A session keeps the link it launched with.
func (d *Daemon) linkOf(sessionID protocol.SessionID) linkRuntime {
	intent, ok := d.store.LaunchIntent(sessionID)
	if !ok {
		return nil
	}
	return d.linkNamed(intent.Link)
}

func (d *Daemon) launchLink(req *spawnRequest) linkRuntime {
	if intent, ok := d.store.LaunchIntent(req.msg.ID); ok {
		if l := d.linkNamed(intent.Link); l != nil && l.fits(req) {
			return l
		}
		return nil
	}
	for _, l := range d.links() {
		if l.fits(req) && l.enabled() {
			return l
		}
	}
	return nil
}

func (d *Daemon) linkProcess(t harness.TerminalID) bool {
	for _, l := range d.links() {
		if l.process(t) {
			return true
		}
	}
	return false
}

// linkConversation is the conversation a link session holds, "" for any other session.
func (d *Daemon) linkConversation(sessionID protocol.SessionID) string {
	if d.linkOf(sessionID) == nil {
		return ""
	}
	return d.store.GetSessionConversation(sessionID).NativeID
}

// A hidden session is open and live over its link while no terminal shows it.
func (d *Daemon) hidden(sessionID protocol.SessionID) bool {
	if len(d.terminals().Of(harness.SessionID(sessionID))) > 0 {
		return false
	}
	return d.store.Get(sessionID) != nil && d.linkConversation(sessionID) != ""
}

func (d *Daemon) keepsWhenLeft(sessionID protocol.SessionID) bool {
	return d.linkConversation(sessionID) != ""
}

// linkKeepsLive exempts from startup pruning a session no terminal runs.
func (d *Daemon) linkKeepsLive(session *protocol.Session) bool {
	l := d.linkOf(session.ID)
	return l != nil && (l.holds(session) || d.hidden(session.ID))
}

// movedOn reports whether t has switched to another conversation than the session it is bound to.
func (d *Daemon) movedOn(sessionID protocol.SessionID, t harness.TerminalID) bool {
	l := d.linkOf(sessionID)
	if l == nil {
		return false
	}
	shown := l.shows(t)
	conversation := d.store.GetSessionConversation(sessionID).NativeID
	return shown != "" && conversation != "" && conversation != shown
}

func (d *Daemon) linkCaller(t protocol.TerminalID) (session protocol.SessionID, view harness.TerminalID, ours bool) {
	for _, l := range d.links() {
		if session, view, ours = l.caller(t); ours {
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

// mirrorLabel hands the link a label the user or attn chose, so its own listings agree.
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

// showHidden opens a terminal for a hidden session, attached to the conversation its link keeps.
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
