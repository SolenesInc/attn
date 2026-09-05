package daemon

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{
		"wake": crewWakePrompt, "successor": crewNapPrompt, "sleep-requested": crewRequestedSleepPrompt,
		"heartbeat": crewHeartbeatPrompt, "sleep-away": crewSleepPrompt,
		"ticket-nudge": ticketNudgePrompt, "chief-inbox": chiefInboxNudgePrompt("/tmp/book {{literal}}"),
	}
	for _, body := range []string{"", " Brief λ {{literal}}\nnext line "} {
		for _, seed := range []string{"", "s-example"} {
			out["delegation/"+body+"/"+seed] = withLeafIdentity(delegatedBriefPrompt(body, seed))
			for _, review := range []bool{false, true} {
				for _, pr := range []*automation.PullRequestInput{nil, {Number: 12, URL: "https://example.org/review/12", HeadSHA: "abc"}} {
					out[fmt.Sprintf("automation/%s/%s/%t/%t", body, seed, review, pr != nil)] = automationSessionPrompt(body, "/tmp/input", seed, "Daily \"review\"", pr, review)
				}
			}
		}
	}
	for mask := 0; mask < 16; mask++ {
		s := transcript.ConversationSlice{}
		if mask&1 != 0 {
			s.Brief = "Brief {{literal}}"
		}
		if mask&2 != 0 {
			s.Rescoping = []string{"Scope λ", "Scope two"}
		}
		if mask&4 != 0 {
			s.Summary = "Summary"
		}
		if mask&8 != 0 {
			s.AgentTurns = []string{"Status one", "Status two"}
		}
		out[fmt.Sprint("title/", mask)] = sessionTitleInstructions + "\n\n" + prompts.RenderText("session-title", "generate", prompts.Values{"conversation": s.Render()})
		out[fmt.Sprint("reconcile/", mask)] = buildTicketReconcilePrompt(ticketReconcileInputs{TicketID: "s-example", Title: "Task", Brief: "Brief", StatusAtClaim: store.TicketStatus("open"), CloseContext: "Reason"}, s)
	}
	out["inbox-notification"] = agentMailboxDoorbellText
	for _, kind := range []string{"comment", "global", "deletion"} {
		for mask := 0; mask < 8; mask++ {
			a := protocol.MarkdownAnnotation{ID: "a", Type: kind, Anchor: mdAnchor(2, 4, 0, "quote λ\n{{literal}}"), Text: protocol.Ptr("comment")}
			if mask&1 != 0 {
				a.QuickLabelID = protocol.Ptr("verify")
				a.QuickLabelText = protocol.Ptr("Verify this")
			}
			if mask&2 != 0 {
				a.QuickLabelTip = protocol.Ptr("tip")
			}
			out[fmt.Sprintf("markdown/%s/%d", kind, mask)] = formatMarkdownAnnotationPayload(fileAnnotationSource("/tmp/doc.md"), []protocol.MarkdownAnnotation{a}, map[string]bool{"a": mask&4 != 0})
		}
	}

	out["garden-advisor/system"] = gardenAdvisorSystemPrompt
	for _, task := range []gardenAdvisorTask{gardenAdviceTask, gardenHandoffTask} {
		out["garden-advisor/"+task.name] = task.prompt(`{"body":"Task λ {{literal}}"}`)
	}
	out["chief/assignment"] = chiefSeedAssignmentPrompt("s-example")
	for _, body := range []string{"", " Task λ {{literal}} "} {
		for _, handoff := range []string{"", " Next {{literal}} "} {
			out["handover/"+body+"/"+handoff] = handoverPrompt(body, handoff, "s-example")
		}
	}
	out["garden-update"] = mailboxItemContent(agentmailbox.Delivery{Item: agentmailbox.Item{Kind: agentmailbox.KindGardenSeed, SourceID: "s-example", Hint: "note"}})
	prompttest.Equal(t, "daemon", out)
}
