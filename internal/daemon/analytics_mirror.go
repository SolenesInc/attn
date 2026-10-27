package daemon

import (
	"cmp"
	"context"
	"strings"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	analyticsMirrorConsumer = "analytics-mirror"
	ventFactPrefix          = "vent."
	sessionFactPrefix       = "session."
	worktreeFactPrefix      = "worktree."
)

func (d *Daemon) startAnalyticsMirror(secret []byte) error {
	if d.eventBus.Registered(analyticsMirrorConsumer) {
		return nil
	}
	return d.eventBus.Register(analyticsMirrorConsumer, bus.All, func(_ context.Context, ev bus.Event) error {
		if strings.HasPrefix(ev.Name, ventFactPrefix) {
			return nil
		}
		return d.store.AppendAnalyticsRecord(analytics.Record{
			Stream:  analytics.StreamBus,
			Seq:     ev.Seq,
			Name:    ev.Name,
			Subject: analytics.HashSubject(secret, ev.Subject),
			At:      ev.CreatedAt,
			Bytes:   len(ev.Payload),
			Props:   analytics.Props(ev.Payload, analytics.SafeValues[ev.Name], d.derivedFactTraits(ev)),
		})
	})
}

func (d *Daemon) stopAnalyticsMirror() error {
	return d.eventBus.Unregister(analyticsMirrorConsumer)
}

func (d *Daemon) derivedFactTraits(ev bus.Event) analytics.Derived {
	switch {
	case strings.HasPrefix(ev.Name, sessionFactPrefix):
		session := d.store.Get(protocol.SessionID(ev.Subject))
		if session == nil {
			return analytics.Derived{}
		}
		intent, _ := d.store.LaunchIntent(session.ID)
		return analytics.Derived{
			Session: &analytics.SessionTraits{
				Harness: session.Agent,
				Model:   cmp.Or(d.leadingAgentModel(session), intent.Model),
				Launch:  d.sessionLaunch(session.ID),
				State:   string(session.State),
			},
			Repo: repoTraits(session.Directory),
		}
	case strings.HasPrefix(ev.Name, worktreeFactPrefix):
		return analytics.Derived{Repo: repoTraits(ev.Subject)}
	default:
		return analytics.Derived{}
	}
}

func (d *Daemon) sessionLaunch(id protocol.SessionID) analytics.Launch {
	switch {
	case d.sessionIsChief(id):
		return analytics.LaunchChief
	case d.crewMemberBoundTo(id) != "":
		return analytics.LaunchCrew
	case d.automationProvenanceForSession(id) != nil:
		return analytics.LaunchAutomation
	case d.store.SessionDelegated(id):
		return analytics.LaunchDelegation
	}
	return analytics.LaunchUser
}

func repoTraits(dir string) *analytics.RepoTraits {
	if dir == "" {
		return nil
	}
	traits, ok := git.ReadRepoTraits(dir)
	if !ok {
		return nil
	}
	return &analytics.RepoTraits{AgentsMD: traits.HasAgentsMD, ClaudeMD: traits.HasClaudeMD, Worktree: traits.IsWorktree, Remote: traits.HasRemote}
}
