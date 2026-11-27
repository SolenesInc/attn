package daemon

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) attentionFacts(ctx context.Context, session *protocol.Session, now time.Time) attention.Facts {
	view, _ := d.monitorViewFor(session.ID)
	since := protocol.Timestamp(session.StateSince).Time()
	turn := view.Turn.V
	facts := attention.Facts{
		State:        session.State,
		InStateFor:   attention.AgeOf(since, now),
		RunStartedBy: attention.RunStartedByUser,
		TurnOwed:     turn.OpenedAt.After(turn.SettledAt),
		OpenTool:     openTool(session, since, now),
		Background:   backgroundFacts(view.BackgroundTasks.V, now),
		Launch:       attention.LaunchedByUser,
		ResultGoesTo: attention.ResultToUser,
		LastMessage:  d.attentionLastMessage(ctx, session.ID),
	}
	if view.NotificationRun.V || view.NotificationStop.V {
		facts.RunStartedBy = attention.RunStartedByNotification
	}
	if request := view.LastModelRequestAt.V; !request.IsZero() {
		facts.SinceModelRequest = protocol.Ptr(attention.AgeOf(request, now))
	}
	focused := d.sessionFocusedAt(session.ID)
	facts.ReportUnread = !turn.OpenedAt.IsZero() && turn.OpenedAt.After(focused)
	facts.UserFocusedAfterStop = session.State != protocol.SessionStateWorking && focused.After(since)
	if until := turn.SnoozedUntil; until.After(now) {
		facts.Wakes = append(facts.Wakes, attention.Wake{Kind: attention.WakeSnooze, In: protocol.Ptr(attention.AgeOf(now, until))})
	}

	seeds := d.attentionGarden(session.ProfileID)
	if dispatch, delegated := d.gardenDispatch(session.ID); delegated {
		facts.Launch = attention.LaunchedByDelegation
		stake, _ := d.gardenDispatchCrown(session.ID)
		facts.ResultGoesTo = d.resultRecipient(session.ID, stake, dispatch)
		if stake != "" {
			facts.Stake = seeds.seed(stake)
		}
	}
	tended := d.attentionTendedSeeds(session)
	for _, id := range tended {
		if facts.Stake == nil || id != facts.Stake.ID {
			if seed := seeds.seed(id); seed != nil {
				facts.Tended = append(facts.Tended, *seed)
			}
		}
		if reminder, ok := seeds.read.reminders[id]; ok {
			facts.Wakes = append(facts.Wakes, attention.Wake{Kind: attention.WakeReminder, Seed: id, In: protocol.Ptr(attention.AgeOf(now, reminder.At))})
		}
	}
	facts.Wakes = append(facts.Wakes, d.seedWakes(ctx, session, tended, seeds)...)
	facts.Wakes = append(facts.Wakes, d.askWakes(session.ID, now)...)
	facts.Delegates = d.attentionDelegates(ctx, session.ID)
	facts.WatchedPRs = d.watchedPullRequests(session.ID)
	return facts
}

func openTool(session *protocol.Session, since, now time.Time) *attention.Tool {
	if session.State != protocol.SessionStatePendingApproval {
		return nil
	}
	return &attention.Tool{Reason: protocol.Deref(session.StateReason), OpenFor: attention.AgeOf(since, now)}
}

func backgroundFacts(tasks []sessionmonitor.BackgroundTask, now time.Time) []attention.Task {
	facts := make([]attention.Task, 0, len(tasks))
	for _, task := range tasks {
		facts = append(facts, attention.Task{
			ID:          protocol.Deref(task.ID),
			Kind:        protocol.Deref(task.Kind),
			Description: protocol.Deref(task.Description),
			Command:     protocol.Deref(task.Command),
			AgentType:   protocol.Deref(task.AgentType),
			Name:        protocol.Deref(task.Name),
			Server:      protocol.Deref(task.Server),
			Tool:        protocol.Deref(task.Tool),
			WakesAgent:  string(protocol.Deref(task.Wakes)),
			Age:         attention.AgeOf(task.StartedAt, now),
		})
	}
	return facts
}

func (d *Daemon) attentionLastMessage(ctx context.Context, session protocol.SessionID) string {
	answer, err := Call[protocol.MonitorLastAssistantResult](ctx, d.router(), protocol.MonitorLastAssistantMessage{Cmd: protocol.CmdMonitorLastAssistant, SessionID: session})
	if err != nil {
		return ""
	}
	message := []rune(answer.Text)
	return string(message[max(0, len(message)-attention.LastMessageMaxChars):])
}

func (d *Daemon) sessionFocusedAt(session protocol.SessionID) time.Time {
	times, err := d.store.SessionFocusTimes()
	if err != nil {
		d.logf("attention facts: session focus: %v", err)
	}
	return protocol.Timestamp(times[session]).Time()
}

func (d *Daemon) resultRecipient(session protocol.SessionID, stake string, dispatch garden.Execution) attention.Recipient {
	if role := d.sessionDelegationRoles()[session]; role != nil && role.Builtin != nil {
		switch *role.Builtin {
		case protocol.BuiltinDelegationRolePathfinder, protocol.BuiltinDelegationRoleOrchestrator, protocol.BuiltinDelegationRolePrototyper:
			return attention.ResultToUser
		}
	}
	dispatcher, live := d.sessionOfActor(dispatch.Dispatcher)
	if !live || stake == "" {
		return attention.ResultToNobody
	}
	if watching, err := d.store.GardenSeedWatching(d.partyOfSession(dispatcher), stake); err != nil || !watching {
		return attention.ResultToNobody
	}
	return attention.ResultToDispatcher
}

func (d *Daemon) attentionTendedSeeds(session *protocol.Session) []string {
	presence, err := d.presenceWithSeeds(command.Caller{Session: session.ID, Profile: session.ProfileID})
	if err != nil {
		d.logf("attention facts: tended seeds of %s: %v", session.ID, err)
	}
	return presence.SeedIds
}

type attentionSeeds struct {
	read gardenRead
	byID map[string]garden.Seed
}

func (d *Daemon) attentionGarden(profile string) attentionSeeds {
	read, err := d.readGarden(profile)
	if err != nil {
		d.logf("attention facts: garden of %s: %v", profile, err)
	}
	byID := make(map[string]garden.Seed, len(read.seeds))
	for _, seed := range read.seeds {
		byID[seed.ID] = seed
	}
	return attentionSeeds{read: read, byID: byID}
}

func (s attentionSeeds) seed(id string) *attention.Seed {
	seed, ok := s.byID[id]
	if !ok {
		return nil
	}
	tally := s.read.plots.EffectiveCheckTally(id)
	return &attention.Seed{ID: id, Title: seed.Title, Status: string(seed.Status), ChecksDone: tally.Reported + tally.Confirmed, ChecksTotal: tally.Total}
}

func (d *Daemon) seedWakes(ctx context.Context, session *protocol.Session, tended []string, seeds attentionSeeds) []attention.Wake {
	party := d.partyOfSession(session.ID)
	var wakes []attention.Wake
	watched, err := d.store.GardenSeedsWatchedBy(party)
	if err != nil {
		d.logf("attention facts: seeds watched by %s: %v", session.ID, err)
	}
	for _, id := range watched {
		seed, ok := seeds.byID[id]
		if !ok || slices.Contains(tended, id) {
			continue
		}
		wake := attention.Wake{Kind: attention.WakeWatchedSeed, Seed: id, SeedStatus: string(seed.Status)}
		if !seed.Tender.IsZero() {
			wake.TenderLive = protocol.Ptr(d.partyLive(ctx, seed.Tender))
		}
		wakes = append(wakes, wake)
	}
	standby, err := d.store.SeedsOnStandbyTendedBy(session.ProfileID, party)
	if err != nil {
		d.logf("attention facts: standby of %s: %v", session.ID, err)
	}
	for _, id := range standby {
		wakes = append(wakes, attention.Wake{Kind: attention.WakeStandby, Seed: id})
	}
	return wakes
}

func (d *Daemon) partyLive(ctx context.Context, party who.Party) bool {
	if session, ok := party.Session(); ok {
		return d.sessionLive(ctx, session)
	}
	_, live := d.liveSessionOf(party)
	return live
}

func (d *Daemon) askWakes(session protocol.SessionID, now time.Time) []attention.Wake {
	asks, err := d.store.OpenAsksFrom(session)
	if err != nil {
		d.logf("attention facts: asks of %s: %v", session, err)
	}
	wakes := make([]attention.Wake, 0, len(asks))
	for _, ask := range asks {
		wake := attention.Wake{Kind: attention.WakeAsk, Subject: ask.Subject}
		if asked := protocol.Timestamp(ask.CreatedAt).Time(); !asked.IsZero() {
			wake.Waited = protocol.Ptr(attention.AgeOf(asked, now))
		}
		wakes = append(wakes, wake)
	}
	return wakes
}

func (d *Daemon) attentionDelegates(ctx context.Context, session protocol.SessionID) []attention.Delegate {
	party := d.partyOfSession(session)
	var delegates []attention.Delegate
	for delegate, actor := range d.gardenDispatchersBySession() {
		dispatcher, _ := d.sessionOfActor(actor)
		record := d.store.Get(delegate)
		if dispatcher != session || record == nil {
			continue
		}
		view, _ := d.monitorViewFor(delegate)
		entry := attention.Delegate{
			Session:      delegate,
			Live:         d.sessionLive(ctx, delegate),
			State:        record.State,
			OwesUserTurn: view.Turn.V.OpenedAt.After(view.Turn.V.SettledAt),
		}
		if stake, ok := d.gardenDispatchCrown(delegate); ok {
			entry.Stake = stake
			entry.ReportsHere, _ = d.store.GardenSeedWatching(party, stake)
		}
		delegates = append(delegates, entry)
	}
	slices.SortFunc(delegates, func(a, b attention.Delegate) int { return strings.Compare(string(a.Session), string(b.Session)) })
	return delegates
}

func (d *Daemon) watchedPullRequests(session protocol.SessionID) []attention.PR {
	var prs []attention.PR
	for _, pr := range d.sessionPullRequestsTo(session, d.inboxRoleAddresses(session)) {
		if !protocol.Deref(pr.Watching) {
			continue
		}
		prs = append(prs, attention.PR{URL: pr.URL, State: pr.State, CI: protocol.Deref(pr.CIStatus), CheckedAt: protocol.Deref(pr.StatusFetchedAt)})
	}
	return prs
}
