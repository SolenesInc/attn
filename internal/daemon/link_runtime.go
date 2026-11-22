package daemon

import (
	"context"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

type linkRuntime interface {
	kind() string
	serves(agent string) bool

	canRun(req harness.LaunchRequest) bool
	enabledForNewLaunches() bool
	admit(profile, conversation string) error
	prepareLaunch(opts *ptybackend.SpawnOptions, profile string) error
	relaunch(opts *ptybackend.SpawnOptions) error

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

func planOn(l linkRuntime, req harness.LaunchRequest) harness.Planned {
	if l == nil || !l.canRun(req) {
		return harness.Planned{Lifetime: harness.InTerminal}
	}
	if req.Lifetime == harness.InServer || req.Lifetime == "" && l.enabledForNewLaunches() {
		return harness.Planned{Lifetime: harness.InServer}
	}
	return harness.Planned{Lifetime: harness.InTerminal}
}

func (d *Daemon) launchInLink(l linkRuntime, opts *ptybackend.SpawnOptions, req harness.LaunchRequest) error {
	if err := l.admit(req.Profile, opts.ResumeSessionID); err != nil {
		return err
	}
	if opts.ResumeSessionID != "" {
		if err := d.restoreConversationIn(l, req.Profile, opts.ResumeSessionID); err != nil {
			return err
		}
	}
	if req.Reason == harness.Reload {
		return l.relaunch(opts)
	}
	return l.prepareLaunch(opts, req.Profile)
}

func (d *Daemon) locateTranscript(driver agentdriver.Driver, ref harness.ConversationRef) string {
	if ref.Path != "" {
		return d.linkedTranscriptPath(string(ref.Harness), ref.Lifetime, ref.Path)
	}
	id := strings.TrimSpace(ref.ID)
	finder, ok := agentdriver.GetTranscriptFinder(driver)
	if id == "" || !ok {
		return ""
	}
	return strings.TrimSpace(finder.FindTranscriptForResume(id))
}
