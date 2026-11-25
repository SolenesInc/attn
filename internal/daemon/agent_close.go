package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const agentCloseReasonMaxChars = garden.MaxReasonChars

func (d *Daemon) runAgentClose(ctx context.Context, c command.Caller, a command.AgentCloseArgs) (any, error) {
	target := strings.TrimSpace(a.Target)
	switch {
	case a.Self && target != "":
		return nil, command.Usage("{self} closes the caller and cannot take a target")
	case a.Compact && !a.Self:
		return nil, command.Usage("{compact} compacts your own conversation, so it only goes with {self}")
	case !a.Self && target == "":
		return nil, command.Usage("name the session or seed to close, or pass {self}")
	case strings.TrimSpace(a.Reason) == "":
		return nil, command.Refuse("close_reason_required", `a close needs a reason: {reason} "why this session is done". The session row stays in the ledger and the reason is what the next reader gets`)
	case c.Session == "":
		return nil, command.Refuse(command.CodeNoSender, "no caller: this shell is not an attn session, so pass --source-session <id> (`{attn agent list}` names the sessions)")
	}
	if a.Self {
		target = string(c.Session)
	}
	msg := &agentCloseRequest{SourceSessionID: c.Session, Reason: a.Reason}
	if a.Compact {
		msg.Compact = protocol.Ptr(true)
	}
	if garden.ValidateID(target) == nil {
		msg.TargetSeedID = protocol.Ptr(target)
	} else {
		msg.TargetSessionID = target
	}
	result, closing, refusal := d.beginAgentClose(msg)
	if refusal != nil {
		if refusal.code == "session_not_found" {
			return nil, command.Refuse(refusal.code, "no session matches %q; `{attn agent list}` names sessions; to close yourself, use `{attn agent close --self -m \"reason\"}`", target)
		}
		return nil, command.Refuse(refusal.code, "%s", refusal.message)
	}
	if protocol.Deref(result.PendingCompact) {
		return result, nil
	}
	return command.Then{Result: result, After: func() { d.finishSessionClose(result.TargetSessionID, closing) }}, nil
}

func (d *Daemon) beginAgentClose(msg *agentCloseRequest) (*protocol.AgentCloseResult, sessionCloseInFlight, *agentCloseRefusal) {
	caller := d.store.Get(msg.SourceSessionID)
	if caller == nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"sender_session_not_found", fmt.Sprintf(
			"the caller %q is not a session on this daemon; a close is attributed to the session that asked for it", protocol.TrimID(msg.SourceSessionID))}
	}

	reason := strings.TrimSpace(msg.Reason)
	switch {
	case reason == "":
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_reason_required",
			"a close needs a reason: the ledger keeps the row, and the reason is all the next reader gets"}
	case utf8.RuneCountInString(reason) > agentCloseReasonMaxChars:
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_reason_required", fmt.Sprintf(
			"that reason is %d characters and the limit is %d; say why it is done and put the detail on the seed",
			utf8.RuneCountInString(reason), agentCloseReasonMaxChars)}
	}

	target, refusal := d.resolveAgentCloseTarget(msg, caller)
	if refusal != nil {
		return nil, sessionCloseInFlight{}, refusal
	}

	if dispatch, ok := d.gardenDispatch(target.ID); caller.ID != target.ID && ok && strings.TrimSpace(dispatch.Crown) != "" {
		if err := d.requireSeedInProfile(dispatch.Crown, caller.ProfileID, false); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"cross_profile", err.Error()}
		}
	}

	if err := d.mayActOnSession(caller.ID, target, "close"); err != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_not_authorized", err.Error()}
	}
	rule := protocol.AgentCloseRuleDispatcher
	if caller.ID == target.ID {
		rule = protocol.AgentCloseRuleSelf
	} else if d.chiefOfProfile(target.ProfileID) == caller.ID {
		rule = protocol.AgentCloseRuleChiefOfStaff
	}
	compact := protocol.Deref(msg.Compact)
	if compact && rule != protocol.AgentCloseRuleSelf {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"compact_requires_self",
			"--compact compacts the conversation of the session that closes itself, so it only goes with --self"}
	}
	if compact {
		if err := d.conversation(target.ID).CanCompact(); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"compact_unavailable", protocol.MentionedText(err)}
		}
	}
	if rule == protocol.AgentCloseRuleSelf {
		if err := d.unreadStandbyMailRefusal(target, ""); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"standby_mail_unread", err.Error()}
		}
		if err := d.refuseStrandingSelfClose(target.ID); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"asks_stranded", err.Error()}
		}
		if d.on(setting.GardenV2Enabled) {
			if err := d.refuseGrowingSeedsSelfClose(target, ""); err != nil {
				return nil, sessionCloseInFlight{}, &agentCloseRefusal{"growing_seeds_held", protocol.MentionedText(err)}
			}
		}
	}
	if protectErr := d.sessionCloseError(target.ID); protectErr != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"session_close_protected", protectErr.Error()}
	}

	if compact {
		onStandby, err := d.store.SeedsOnStandbyTendedBy(target.ProfileID, d.partyOfSession(target.ID))
		if err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", err.Error()}
		}
		if _, err := Call[struct{}](context.Background(), d.router(), protocol.CompactingCloseBeginMessage{Cmd: protocol.CmdCompactingCloseBegin, SessionID: target.ID, Reason: reason, OnStandby: onStandby}); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", err.Error()}
		}
		return &protocol.AgentCloseResult{
			TargetSessionID: target.ID,
			Label:           sessionDisplayName(target),
			Reason:          reason,
			Rule:            rule,
			SeedIds:         []string{},
			PendingCompact:  protocol.Ptr(true),
		}, sessionCloseInFlight{}, nil
	}

	d.logf("agent close: session %s closes %s as %s: %s", caller.ID, target.ID, rule, reason)
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: who.Session(caller.ID).Actor(), Reason: reason}, nil)
	if err != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", fmt.Sprintf(
			"session %s is still running: %v", shortSessionID(target.ID), err)}
	}

	return &protocol.AgentCloseResult{
		TargetSessionID: target.ID,
		Label:           sessionDisplayName(target),
		Reason:          reason,
		Rule:            rule,
		SeedIds:         d.noteCloseOnTendedSeeds(target, caller, rule, reason),
	}, closing, nil
}

type agentCloseRequest struct {
	TargetSessionID string
	TargetSeedID    *string
	SourceSessionID protocol.SessionID
	Reason          string
	Compact         *bool
}

type agentCloseRefusal struct {
	code    string
	message string
}

func (d *Daemon) resolveAgentCloseTarget(msg *agentCloseRequest, caller *protocol.Session) (*protocol.Session, *agentCloseRefusal) {
	reference := protocol.SessionID(strings.TrimSpace(msg.TargetSessionID))
	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return nil, &agentCloseRefusal{"cross_profile", err.Error()}
		}
		if seed.Tender != d.partyOfSession(caller.ID) {
			if err := d.requireSeedInProfile(seedID, caller.ProfileID, false); err != nil {
				return nil, &agentCloseRefusal{"cross_profile", err.Error()}
			}
		}
		if reference != "" {
			return nil, &agentCloseRefusal{"ambiguous_target",
				"a close ends one session; name a session or a seed, not both"}
		}
		tender, err := d.seedTenderSession(seedID)
		if err != nil {
			return nil, &agentCloseRefusal{"seed_untended", err.Error()}
		}
		reference = protocol.SessionID(tender)
	}
	target, err := d.resolveLiveSession(string(reference), "")
	switch {
	case err == nil:
		return target, nil
	case errors.Is(err, errAmbiguousSession):
		return nil, &agentCloseRefusal{err.Error(), fmt.Sprintf(
			"%q matches more than one session; give more of the full id (`attn agent list --json` carries full ids)", reference)}
	default:
		return nil, &agentCloseRefusal{err.Error(), fmt.Sprintf(
			"no session matches %q; `attn agent list` names the sessions this daemon can reach", reference)}
	}
}

func (d *Daemon) refuseGrowingSeedsSelfClose(session *protocol.Session, goingOnStandby string) error {
	all, err := d.rememberedGardenSeeds()
	if err != nil {
		if docstore.IsUndeclaredCollection(err) {
			return nil
		}
		return fmt.Errorf("reading the seeds %s tends: %w", session.ID, err)
	}
	tender := who.Session(session.ID)
	var held []string
	for _, seed := range all.seeds {
		if seed.ProfileID != session.ProfileID || seed.Status != garden.StatusGrowing || seed.Tender != tender || seed.ID == goingOnStandby {
			continue
		}
		standby, err := d.seedStandby(seed)
		if err != nil {
			return fmt.Errorf("reading standby for %s: %w", seed.ID, err)
		}
		if standby != nil {
			continue
		}
		held = append(held, fmt.Sprintf("%s (%s)", seed.ID, seed.Title))
	}
	if len(held) == 0 {
		return nil
	}
	if goingOnStandby != "" {
		return protocol.Refuse(protocol.RefusalWrongState,
			"%s was not placed on standby because closing this session leaves %d other growing seed(s) untended: %s. Finish or park them first, or keep the session open while putting earlier seeds on standby (`{attn seed standby <id> --stay -m \"…\"}`) before putting the last seed on standby to close",
			goingOnStandby, len(held), strings.Join(held, ", "))
	}
	standbyHint := "`{attn seed standby <id> -m \"…\"}`"
	if len(held) > 1 {
		standbyHint = "`{attn seed standby <id> --stay -m \"…\"}` for earlier seeds and `{attn seed standby <id> -m \"…\"}` on the last"
	}
	return protocol.Refuse(protocol.RefusalWrongState,
		"closing this session leaves %d growing seed(s) untended: %s. Finish or hand off each seed first with `{attn seed harvest <id> -m \"…\"}`, `{attn seed park <id> -m \"…\"}`, `{attn seed wither <id> -m \"…\"}`, or %s",
		len(held), strings.Join(held, ", "), standbyHint)
}

func (d *Daemon) noteCloseOnTendedSeeds(
	target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string,
) []string {
	noted := []string{}
	all, err := d.rememberedGardenSeeds()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("agent close: reading the seeds %s tended: %v", target.ID, err)
		}
		return noted
	}
	tender := who.Session(target.ID)
	autoPark := d.on(setting.GardenV2Enabled) && rule != protocol.AgentCloseRuleSelf
	for _, seed := range all.seeds {
		if seed.ProfileID != target.ProfileID || seed.Tender != tender {
			continue
		}
		standby, err := d.seedStandby(seed)
		if err != nil {
			d.logf("agent close: reading the standby of %s: %v", seed.ID, err)
		}
		onStandby := standby != nil
		if autoPark && seed.Status == garden.StatusGrowing && !onStandby {
			comment := agentCloseAutoParkNote(target, caller, rule, reason)
			ask := garden.Ask{Actor: d.partyOfSession(caller.ID).Actor(), CauseSession: caller.ID}
			if _, _, _, err := d.applySeedTransitionDetailed(seed.ID, garden.VerbPark, ask, comment); err == nil {
				noted = append(noted, seed.ID)
				continue
			} else {
				d.logf("agent close: auto-parking %s after closing %s: %v", seed.ID, target.ID, err)
			}
		}
		body := agentCloseSeedNote(target, caller, rule, reason, onStandby)
		if _, err := d.appendSeedNote(seed.ID, body, d.partyOfSession(caller.ID).Actor(), garden.NoteKindNote, nil, false, caller.ID); err != nil {
			d.logf("agent close: noting the close of %s on %s: %v", target.ID, seed.ID, err)
			continue
		}
		noted = append(noted, seed.ID)
	}
	return noted
}

func agentCloseCloser(caller *protocol.Session, rule protocol.AgentCloseRule) string {
	closer := agentCloseSessionRef(caller)
	switch rule {
	case protocol.AgentCloseRuleSelf:
		return "itself"
	case protocol.AgentCloseRuleChiefOfStaff:
		return "the chief of staff, " + closer
	case protocol.AgentCloseRuleDispatcher:
		return "its dispatcher, " + closer
	default:
		return closer
	}
}

func agentCloseAutoParkNote(target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string) string {
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed was parked automatically so whoever comes next can pick it up. "+
			"`attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), agentCloseCloser(caller, rule), reason, shortSessionID(target.ID))
}

func agentCloseSessionRef(session *protocol.Session) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return fmt.Sprintf("%s (%s)", label, shortSessionID(session.ID))
	}
	return shortSessionID(session.ID)
}

func agentCloseSeedNote(target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string, onStandby bool) string {
	closer := agentCloseCloser(caller, rule)
	next := "so whoever comes next takes it or parks it"
	if onStandby {
		next = "and the seed is on standby, so its next seed mail reopens that session"
	}
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed did not move. It still names that session as its tender, %s. "+
			"`attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), closer, reason, next, shortSessionID(target.ID))
}

func (d *Daemon) seedTenderSession(seedID string) (string, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", err
	}
	if session, ok := seed.Tender.Session(); ok {
		return string(session), nil
	}
	if session, ok := d.liveSessionOf(seed.Tender); ok {
		return string(session), nil
	}
	if !seed.Tender.IsZero() {
		name := d.partyName(seed.Tender)
		return "", fmt.Errorf(
			"%s is tended by %s, who is not in an attn session; message them by name: attn agent msg %s \"…\"",
			seed.ID, name, name)
	}
	return "", fmt.Errorf(
		"nobody is tending %s, so there is nobody to reach; leave it on the log instead: attn seed note %s -m \"…\"",
		seed.ID, seed.ID)
}
