package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/shortid"
)

type named[T any] struct {
	rust  string
	value T
}

func writeConstants(out *strings.Builder) {
	for _, c := range []named[string]{
		{"EDGE_BLOCKS", garden.EdgeBlocks},
		{"EDGE_PART_OF", garden.EdgePartOf},
		{"EDGE_SOWN_FROM", garden.EdgeSownFrom},
		{"EDGE_DISCOVERED_FROM", garden.EdgeDiscoveredFrom},
		{"EDGE_RELATES_TO", garden.EdgeRelatesTo},
		{"STATUS_PLANTED", garden.StatusPlanted},
		{"STATUS_GROWING", garden.StatusGrowing},
		{"STATUS_HARVESTED", garden.StatusHarvested},
		{"STATUS_WITHERED", garden.StatusWithered},
		{"STATUS_PARKED", garden.StatusParked},
		{"NOTE_KIND_NOTE", garden.NoteKindNote},
		{"NOTE_KIND_HANDOFF", garden.NoteKindHandoff},
		{"NOTE_KIND_ATTACH", garden.NoteKindAttach},
		{"NOTE_KIND_DETACH", garden.NoteKindDetach},
		{"CHECK_OPEN", string(garden.CheckOpen)},
		{"CHECK_REPORTED", string(garden.CheckReported)},
		{"CHECK_CONFIRMED", string(garden.CheckConfirmed)},
		{"CHECK_REJECTED", string(garden.CheckRejected)},
		{"CHECK_VERB_ADD", string(garden.CheckVerbAdd)},
		{"CHECK_VERB_EDIT", string(garden.CheckVerbEdit)},
		{"CHECK_VERB_REPORT", string(garden.CheckVerbReport)},
		{"CLIENT_KIND_NATIVE_APP", protocol.ClientKindNativeApp},
		{"ERROR_CODE_CONFLICT", protocol.ErrorCodeConflict},
		{"ERROR_CODE_SUBSCRIPTION_LIMIT", protocol.ErrorCodeSubscriptionLimit},
		{"ERROR_CODE_UNAUTHORIZED_CLIENT", protocol.ErrorCodeUnauthorizedClient},
		{"REFUSAL_CONFLICT", string(protocol.RefusalConflict)},
		{"REFUSAL_INVALID_INPUT", string(protocol.RefusalInvalidInput)},
		{"REFUSAL_LIMIT", string(protocol.RefusalLimit)},
		{"REFUSAL_NOT_FOUND", string(protocol.RefusalNotFound)},
		{"REFUSAL_NOT_TENDER", string(protocol.RefusalNotTender)},
		{"REFUSAL_UNCLASSIFIED", string(protocol.RefusalUnclassified)},
		{"REFUSAL_WRONG_STATE", string(protocol.RefusalWrongState)},
		{"STATE_LAUNCHING", protocol.StateLaunching},
		{"STATE_WORKING", protocol.StateWorking},
		{"STATE_PENDING_APPROVAL", protocol.StatePendingApproval},
		{"STATE_WAITING_INPUT", protocol.StateWaitingInput},
		{"STATE_IDLE", protocol.StateIdle},
		{"STATE_UNKNOWN", protocol.StateUnknown},
		{"STATE_SCHEDULED", protocol.StateScheduled},
		{"QUICK_CAPTURE_MAILBOX_SESSION", string(protocol.QuickCaptureMailboxKindSession)},
		{"SHARED_CODEX_RESTART_PENDING", string(protocol.SharedCodexRestartPending)},
		{"SHARED_CODEX_RESTART_RESTARTING", string(protocol.SharedCodexRestartRestarting)},
		{"SHARED_CODEX_RESTART_FAILED", string(protocol.SharedCodexRestartFailed)},
		{"CODEX_SHARED_PROFILE_ENV", harness.CodexSharedProfileEnv},
		{"CODEX_THREAD_PREFIX", harness.CodexThreadPrefix},
		{"NISSE_CONVERSATION_PREFIX", harness.NisseConversationPrefix},
		{"SIGMA_CONVERSATION_PREFIX", harness.SigmaConversationPrefix},
		{"ID_ALPHABET", shortid.Alphabet},
		{"CREW_KEY_REF_PREFIX", crew.KeyRefPrefix},
		{"AGENT_MSG_STATUS_QUEUED", string(protocol.AgentMsgStatusQueued)},
		{"LAUNCH_DESKTOP_KIND_AUTOMATION", string(protocol.LaunchDesktopKindAutomation)},
		{"DOC_OP_EQ", string(docstore.OpEq)},
		{"DOC_OP_LT", string(docstore.OpLt)},
		{"DOC_OP_LTE", string(docstore.OpLte)},
		{"DOC_OP_GT", string(docstore.OpGt)},
		{"DOC_OP_GTE", string(docstore.OpGte)},
	} {
		fmt.Fprintf(out, "pub const %s: &str = %s;\n", c.rust, rustString(c.value))
	}
	out.WriteByte('\n')
	for _, c := range []named[int64]{
		{"MAX_SOCKET_FRAME_BYTES", protocol.MaxSocketFrameBytes},
		{"AGENT_MESSAGE_MAX_CHARS", protocol.AgentMessageMaxChars},
		{"ASK_MAX_IMAGE_BYTES", ask.MaxImageBytes},
		{"ASK_MAX_MARKDOWN_BYTES", ask.MaxMarkdownBytes},
		{"INBOX_DEFAULT_LIMIT", inbox.DefaultInboxLimit},
		{"INBOX_MAX_LIMIT", inbox.MaxInboxLimit},
		{"SEARCH_DEFAULT_RESULTS", garden.DefaultSearchResults},
		{"SEARCH_MAX_RESULTS", garden.MaxSearchResults},
		{"DEFAULT_STALE_WINDOW_SECONDS", int64(garden.DefaultStaleWindow.Seconds())},
		{"ID_BODY_LEN", shortid.BodyLen},
		{"MAX_SLUG_CHARS", garden.MaxSlugChars},
		{"MAX_SLUG_WORDS", garden.MaxSlugWords},
		{"MAX_TITLE_CHARS", garden.MaxTitleChars},
		{"MAX_BODY_BYTES", garden.MaxBodyBytes},
		{"MAX_NOTE_BYTES", garden.MaxNoteBytes},
		{"MAX_CHECKS_PER_SEED", garden.MaxChecksPerSeed},
		{"MAX_CHECK_TEXT_CHARS", garden.MaxCheckTextChars},
		{"MAX_CHECK_SUMMARY_CHARS", garden.MaxCheckSummaryChars},
		{"DOC_DEFAULT_LIMIT", docstore.DefaultLimit},
		{"DOC_MAX_LIMIT", docstore.MaxLimit},
		{"DOC_FIRST_REV", docstore.FirstRev},
		{"DOC_EXPECT_ABSENT", docstore.ExpectAbsent},
	} {
		fmt.Fprintf(out, "pub const %s: i64 = %d;\n", c.rust, c.value)
	}
	out.WriteByte('\n')
	checkVerbs := make([]string, len(garden.CheckVerbs))
	for i, verb := range garden.CheckVerbs {
		checkVerbs[i] = string(verb)
	}
	for _, c := range []named[[]string]{
		{"LINKABLE_KINDS", garden.LinkableKinds},
		{"ROUTING_OVERRIDE_ENV", config.RoutingOverrideEnv()},
		{"ENTRY_KINDS", garden.EntryKinds},
		{"CHECK_VERBS", checkVerbs},
	} {
		items := make([]string, len(c.value))
		for i, item := range c.value {
			items[i] = rustString(item)
		}
		fmt.Fprintf(out, "pub const %s: &[&str] = &[%s];\n", c.rust, strings.Join(items, ", "))
	}
	out.WriteByte('\n')
}

func writeSettings(out *strings.Builder) {
	out.WriteString("pub struct Setting {\n    pub key: &'static str,\n    pub machine: bool,\n    pub default_on: bool,\n}\n\n")
	out.WriteString("pub struct Feature {\n    pub key: &'static str,\n    pub name: &'static str,\n    pub stage: &'static str,\n    pub applies: &'static str,\n}\n\n")
	out.WriteString("pub const SETTINGS: &[Setting] = &[\n")
	for _, s := range setting.All() {
		fmt.Fprintf(out, "    Setting { key: %s, machine: %t, default_on: %t },\n",
			rustString(s.Key), s.Class == setting.Machine, s.Kind == setting.Bool && !s.IsFamily() && s.Default == setting.On)
	}
	out.WriteString("];\n\npub const FEATURES: &[Feature] = &[\n")
	for _, s := range setting.Features() {
		fmt.Fprintf(out, "    Feature { key: %s, name: %s, stage: %s, applies: %s },\n",
			rustString(s.Key), rustString(s.Name), rustString(s.Stage.String()), rustString(string(s.Applies)))
	}
	out.WriteString("];\n\n")
}

func writePrintableRanges(out *strings.Builder) {
	var ranges [][2]rune
	for r := rune(0); r <= 0x10ffff; r++ {
		if !strconv.IsPrint(r) {
			continue
		}
		if last := len(ranges) - 1; last >= 0 && ranges[last][1] == r-1 {
			ranges[last][1] = r
			continue
		}
		ranges = append(ranges, [2]rune{r, r})
	}
	out.WriteString("pub const GO_PRINTABLE_RANGES: &[(u32, u32)] = &[\n")
	for _, printable := range ranges {
		fmt.Fprintf(out, "    (0x%x, 0x%x),\n", printable[0], printable[1])
	}
	out.WriteString("];\n")
}
