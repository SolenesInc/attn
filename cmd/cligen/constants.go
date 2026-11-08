package main

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
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
	out.WriteString("pub struct Setting {\n    pub key: &'static str,\n    pub machine: bool,\n}\n\n")
	out.WriteString("pub struct Feature {\n    pub key: &'static str,\n    pub name: &'static str,\n    pub stage: &'static str,\n    pub applies: &'static str,\n}\n\n")
	out.WriteString("pub const SETTINGS: &[Setting] = &[\n")
	for _, s := range setting.All() {
		fmt.Fprintf(out, "    Setting { key: %s, machine: %t },\n", rustString(s.Key), s.Class == setting.Machine)
	}
	out.WriteString("];\n\npub const FEATURES: &[Feature] = &[\n")
	for _, s := range setting.Features() {
		fmt.Fprintf(out, "    Feature { key: %s, name: %s, stage: %s, applies: %s },\n",
			rustString(s.Key), rustString(s.Name), rustString(s.Stage.String()), rustString(string(s.Applies)))
	}
	out.WriteString("];\n\n")
}
