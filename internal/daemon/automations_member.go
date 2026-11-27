package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) deliverAutomationMessage(run *store.AutomationRun, req automation.WorkRequest) error {
	definition, err := d.store.GetAutomationDefinition(req.DefinitionID)
	if err != nil {
		return err
	}
	if definition == nil || !definition.Enabled {
		return errors.New("automation definition is disabled; refusing pending delivery")
	}
	key, err := automationMemberKey(req.Member)
	if err != nil {
		return err
	}
	member, _, err := d.crewMember(key.String())
	if err != nil {
		return err
	}
	if member.Retired {
		return fmt.Errorf("%s is retired, so the message was not delivered", member.Name)
	}
	to := who.Member(member.Key).Address()
	inputPath, err := d.ensureAutomationOccurrenceInput(req)
	if err != nil {
		return err
	}
	text := prompts.RenderText("automation", "member-message", prompts.Values{
		"definition": definition.Name, "brief": req.Prompt, "input_path": inputPath,
	})
	item := inbox.Item{ID: "automation-run/" + run.ID, To: to, Kind: inbox.AutomationMessage, Source: strconv.Itoa(definition.ID), Hint: definition.Name, Text: text}
	if _, err := d.store.PutInbox(item, time.Now()); err != nil {
		return fmt.Errorf("put the message in %s's mailbox: %w", member.Name, err)
	}
	if err := d.store.MarkAutomationRunDelivered(run.ID, time.Now()); err != nil {
		return err
	}
	d.broadcastAutomationsChanged(run.DefinitionID)
	d.kickInboxAfterCommit(to)
	return nil
}

func (d *Daemon) addressAutomationMember(spec automation.DefinitionSpec, profileID string) (automation.DefinitionSpec, []byte, error) {
	member, found, err := d.crewMemberIn(profileID, spec.Member)
	if err != nil {
		return spec, nil, err
	}
	if !found {
		return spec, nil, fmt.Errorf("member: no crew member %q in this profile; {attn crew list} names the members", spec.Member)
	}
	if member.Retired {
		return spec, nil, fmt.Errorf("member: %s is retired", member.Name)
	}
	spec.Member = crew.KeyRefPrefix + member.Key.String()
	canonical, err := json.Marshal(spec)
	return spec, canonical, err
}

func automationMemberKey(ref string) (who.MemberKey, error) {
	key, keyed := strings.CutPrefix(ref, crew.KeyRefPrefix)
	if !keyed {
		return who.MemberKey{}, fmt.Errorf("automation member %q is not a %s<key> reference", ref, crew.KeyRefPrefix)
	}
	return who.ParseMemberKey(key)
}

func (d *Daemon) automationMemberView(ref string) *protocol.PartyView {
	if ref == "" {
		return nil
	}
	key, err := automationMemberKey(ref)
	if err != nil {
		d.logf("%v", err)
		return nil
	}
	view := d.partyView(who.Member(key))
	return &view
}
