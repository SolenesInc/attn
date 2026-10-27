package daemon

import (
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

type monitorPolicySources struct {
	chiefs     map[string]protocol.SessionID
	crewBound  map[protocol.SessionID]bool
	autoSettle sessionmonitor.AutoSettleSettings
	headless   bool
	titles     bool
	quiet      bool
}

func (d *Daemon) monitorPolicySources() monitorPolicySources {
	sources := monitorPolicySources{
		chiefs:     d.profileChiefs(),
		crewBound:  map[protocol.SessionID]bool{},
		autoSettle: d.autoSettleSettings(),
		headless:   headless.Enabled(),
		titles:     sessionAutoTitleEnabled() && d.sessionTitleExec != nil,
		quiet:      d.quietNotifications(),
	}
	if members, _, err := d.readCrewMembers(); err == nil {
		for _, member := range members {
			if d.crewBindingLive(member) {
				sources.crewBound[member.BindingSession] = true
			}
		}
	}
	return sources
}

func (d *Daemon) monitorPolicyFrom(sources monitorPolicySources, sessionID protocol.SessionID) sessionmonitor.Policy {
	policy := sessionmonitor.Policy{AutoSettle: sources.autoSettle, Headless: sources.headless, QuietNotifications: sources.quiet}
	session := d.store.Get(sessionID)
	if session == nil {
		return policy
	}
	policy.DriverReportsState = d.pluginDriverReportsState(session.Agent)
	policy.ChiefOfStaff = session.ProfileID != "" && sources.chiefs[session.ProfileID] == sessionID
	policy.TitlesAllowed = sources.titles && !sources.crewBound[sessionID] &&
		d.store.SessionLabels(sessionID).UserLabel == "" &&
		store.SessionLabelIsPlaceholder(session.Label, session.Directory, sessionID)
	return policy
}

func monitorPolicySetting(key string) bool {
	switch key {
	case string(SettingAutoSettleEnabled), string(SettingAutoSettleArmSeconds), string(SettingAutoSettleCountdownSeconds), string(SettingHeadlessTasksEnabled),
		string(SettingQueueQuietNotifications), string(SettingQueueModeEnabled):
		return true
	}
	return false
}

func (d *Daemon) pushMonitorPolicies() {
	r := d.monitors()
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	monitors := r.each()
	if len(monitors) == 0 {
		return
	}
	sources := d.monitorPolicySources()
	for sessionID, monitor := range monitors {
		monitor.SetPolicy(d.monitorPolicyFrom(sources, sessionID))
	}
}

func (d *Daemon) subscribeMonitorPolicyFacts() {
	d.monitorPolicyUnsubscribe = d.eventBus.Subscribe(bus.Filter{FactSessionRenamed, FactCrewBound, FactCrewReleased, FactCrewUpdated}, func(bus.Event) {
		d.life.Go("pushMonitorPolicies", d.pushMonitorPolicies)
	})
}

func (d *Daemon) unsubscribeMonitorPolicyFacts() {
	if d.monitorPolicyUnsubscribe != nil {
		d.monitorPolicyUnsubscribe()
		d.monitorPolicyUnsubscribe = nil
	}
}
