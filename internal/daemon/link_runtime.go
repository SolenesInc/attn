package daemon

import (
	"context"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type linkRuntime interface {
	kind() string
	serves(agent string) bool

	canRun(req *spawnRequest) bool
	enabledForNewLaunches() bool
	admit(profile, conversation string) error
	prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error
	relaunch(opts *ptybackend.SpawnOptions) error

	runsLink(t harness.TerminalID) bool
	serverTerminal(profile string) harness.TerminalID

	profileReleased(profile string)

	recoverProcesses(ctx context.Context)
	recoverViews(ctx context.Context)
}

func (d *Daemon) links() []linkRuntime {
	links := []linkRuntime{d.codexShared()}
	for _, l := range d.appServerLinks() {
		links = append(links, l)
	}
	return links
}

type sharedConversationLink interface {
	renamed(sessionID protocol.SessionID, label string)
	restore(profile, conversation string) error
	transcriptPath(path string) string
}

func (d *Daemon) restoreConversation(agent string, lifetime harness.Lifetime, profile, conversation string) error {
	return d.restoreConversationIn(d.linkFor(agent, lifetime), profile, conversation)
}

func (d *Daemon) restoreConversationIn(l linkRuntime, profile, conversation string) error {
	if shared, ok := l.(sharedConversationLink); ok {
		return shared.restore(profile, conversation)
	}
	return nil
}

func (d *Daemon) linkedTranscriptPath(agent string, lifetime harness.Lifetime, path string) string {
	if lifetime != harness.InServer {
		return path
	}
	if l, ok := d.linkServing(agent).(sharedConversationLink); ok {
		return l.transcriptPath(path)
	}
	return path
}

func (d *Daemon) renameLinkedConversation(sessionID protocol.SessionID, label string) {
	if l, ok := d.linkOf(sessionID).(sharedConversationLink); ok {
		l.renamed(sessionID, label)
	}
}

func (d *Daemon) linkOf(sessionID protocol.SessionID) linkRuntime {
	agent, lifetime := d.store.SessionLifetime(sessionID)
	if lifetime != harness.InServer {
		return nil
	}
	return d.linkServing(agent)
}

func (d *Daemon) linkFor(agent string, lifetime harness.Lifetime) linkRuntime {
	if lifetime != harness.InServer {
		return nil
	}
	return d.linkServing(agent)
}

func (d *Daemon) linkServing(agent string) linkRuntime {
	for _, l := range d.links() {
		if l.serves(agent) {
			return l
		}
	}
	return nil
}

func lifetimeOf(l linkRuntime) harness.Lifetime {
	if l == nil {
		return harness.InTerminal
	}
	return harness.InServer
}

func (d *Daemon) launchLink(req *spawnRequest) linkRuntime {
	if req.existingSession != nil && !req.policy.fromLedger {
		if l := d.linkOf(req.msg.ID); l != nil && l.canRun(req) {
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
	_, server := d.appServerProcess(d.life.Context(), t)
	return server
}
