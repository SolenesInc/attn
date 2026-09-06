package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCmd string
		wantErr bool
	}{
		{
			name:    "register message",
			input:   `{"cmd":"register","id":"abc","label":"test","dir":"/tmp","workspace_id":"workspace-abc"}`,
			wantCmd: CmdRegister,
		},
		{
			name:    "delegate message",
			input:   `{"cmd":"delegate","source_session_id":"abc","brief":"Investigate this","agent":"codex"}`,
			wantCmd: CmdDelegate,
		},
		{
			name:    "ticket comment message",
			input:   `{"cmd":"ticket_comment","source_session_id":"abc","ticket_id":"tk","comment":"lgtm"}`,
			wantCmd: CmdTicketComment,
		},
		{
			name:    "ticket list message",
			input:   `{"cmd":"ticket_list","status":"working"}`,
			wantCmd: CmdTicketList,
		},
		{
			name:    "ticket subscribe message",
			input:   `{"cmd":"ticket_subscribe","source_session_id":"abc","ticket_id":"tk"}`,
			wantCmd: CmdTicketSubscribe,
		},
		{
			name:    "ticket unsubscribe message",
			input:   `{"cmd":"ticket_unsubscribe","source_session_id":"abc","ticket_id":"tk"}`,
			wantCmd: CmdTicketUnsubscribe,
		},
		{
			name:    "ticket take message",
			input:   `{"cmd":"ticket_take","source_session_id":"abc","ticket_id":"tk","confirm":true}`,
			wantCmd: CmdTicketTake,
		},
		{
			name:    "crew sleep message",
			input:   `{"cmd":"crew_sleep","member":"trellis","request_id":"req-1"}`,
			wantCmd: CmdCrewSleep,
		},
		{
			name:    "state message",
			input:   `{"cmd":"state","id":"abc","state":"waiting"}`,
			wantCmd: CmdState,
		},
		{
			name:    "query message",
			input:   `{"cmd":"query","filter":"waiting"}`,
			wantCmd: CmdQuery,
		},
		{
			name:    "session selected message",
			input:   `{"cmd":"session_selected","id":"abc"}`,
			wantCmd: CmdSessionSelected,
		},
		{
			name:    "unregister message",
			input:   `{"cmd":"unregister","id":"abc"}`,
			wantCmd: CmdUnregister,
		},
		{
			name:    "workspace layout get message",
			input:   `{"cmd":"workspace_layout_get","workspace_id":"ws1"}`,
			wantCmd: CmdWorkspaceLayoutGet,
		},
		{
			name:    "workspace layout set split ratio message",
			input:   `{"cmd":"workspace_layout_set_split_ratio","workspace_id":"ws1","split_id":"split-1","ratio":0.3}`,
			wantCmd: CmdWorkspaceLayoutSetSplitRatio,
		},
		{
			name:    "clear warnings message",
			input:   `{"cmd":"clear_warnings"}`,
			wantCmd: CmdClearWarnings,
		},
		{
			name:    "list plugins message",
			input:   `{"cmd":"list_plugins"}`,
			wantCmd: CmdListPlugins,
		},
		{
			name:    "install plugin message",
			input:   `{"cmd":"install_plugin","source":"git@example.com:team/plugin.git"}`,
			wantCmd: CmdInstallPlugin,
		},
		{
			name:    "install bundled plugin message",
			input:   `{"cmd":"install_bundled_plugin","name":"attn-example"}`,
			wantCmd: CmdInstallBundledPlugin,
		},
		{
			name:    "uninstall plugin message",
			input:   `{"cmd":"uninstall_plugin","name":"attn-example"}`,
			wantCmd: CmdUninstallPlugin,
		},
		{
			name:    "remove plugin message",
			input:   `{"cmd":"remove_plugin","name":"demo"}`,
			wantCmd: CmdRemovePlugin,
		},
		{
			name:    "set plugin priority message",
			input:   `{"cmd":"set_plugin_priority","name":"demo","priority":10}`,
			wantCmd: CmdSetPluginPriority,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "missing cmd",
			input:   `{"id":"abc"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := ParseMessage([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
		})
	}
}

func TestParseRegister(t *testing.T) {
	input := `{"cmd":"register","id":"abc123","label":"drumstick","dir":"/home/user/project"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdRegister {
		t.Fatalf("cmd = %q, want %q", cmd, CmdRegister)
	}

	msg, ok := data.(*RegisterMessage)
	if !ok {
		t.Fatalf("data type = %T, want *RegisterMessage", data)
	}
	if msg.ID != "abc123" {
		t.Errorf("ID = %q, want %q", msg.ID, "abc123")
	}
	if Deref(msg.Label) != "drumstick" {
		t.Errorf("Label = %q, want %q", Deref(msg.Label), "drumstick")
	}
}

func TestParseSessionTranscript(t *testing.T) {
	input := `{"cmd":"session_transcript","target_session_id":"session-1","after_cursor":"v1:0123456789abcdef0123456789abcdef:42:0"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdSessionTranscript {
		t.Fatalf("cmd = %q, want %q", cmd, CmdSessionTranscript)
	}
	msg, ok := data.(*SessionTranscriptMessage)
	if !ok || msg.TargetSessionID != "session-1" || Deref(msg.AfterCursor) != "v1:0123456789abcdef0123456789abcdef:42:0" {
		t.Fatalf("message = %#v", data)
	}
}

func TestParseAgentInboxSupportsBatchAndLegacyMessages(t *testing.T) {
	for _, tt := range []struct {
		name      string
		input     string
		messageID string
		limit     int
	}{
		{
			name: "batch", input: `{"cmd":"agent_inbox","recipient_session_id":"session-1","limit":7}`,
			limit: 7,
		},
		{
			name: "legacy message", input: `{"cmd":"agent_inbox","recipient_session_id":"session-1","message_id":"message-1"}`,
			messageID: "message-1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd, data, err := ParseMessage([]byte(tt.input))
			if err != nil || cmd != CmdAgentInbox {
				t.Fatalf("ParseMessage() = %q, %#v, %v", cmd, data, err)
			}
			msg, ok := data.(*AgentInboxMessage)
			if !ok || msg.RecipientSessionID != "session-1" || Deref(msg.MessageID) != tt.messageID || Deref(msg.Limit) != tt.limit {
				t.Fatalf("message = %#v", data)
			}
		})
	}
}

func TestParseDelegatePlacementAndWorktree(t *testing.T) {
	input := `{"cmd":"delegate","source_session_id":"source-1","brief":"Investigate this","agent":"codex","placement":"new_workspace","worktree":{"repo":"/repo","branch":"feat/delegated","starting_from":"main"}}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}
	if cmd != CmdDelegate {
		t.Fatalf("cmd = %q, want %q", cmd, CmdDelegate)
	}
	msg := data.(*DelegateMessage)
	if Deref(msg.Placement) != "new_workspace" || msg.Worktree == nil || msg.Worktree.Branch != "feat/delegated" {
		t.Fatalf("delegate message = %+v", msg)
	}
	if Deref(msg.Worktree.Repo) != "/repo" || Deref(msg.Worktree.StartingFrom) != "main" {
		t.Fatalf("delegate worktree = %+v", msg.Worktree)
	}
}

func TestParseMessageRejectsRetiredDispatchCommands(t *testing.T) {
	retired := []string{
		"list_dispatches",
		"submit_dispatch_outcome",
		"handoff_dispatch",
		"get_dispatch",
		"resolve_dispatch_request",
		"send_dispatch_message",
		"list_dispatch_messages",
		"read_dispatch_message",
		"acknowledge_dispatch_message",
		"wake_dispatch_agent",
		"report_dispatch",
	}
	for _, cmd := range retired {
		_, _, err := ParseMessage([]byte(`{"cmd":"` + cmd + `","source_session_id":"worker-1"}`))
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("retired %q error = %v, want unknown command", cmd, err)
		}
	}
}

func TestParseWorkspaceLayoutSetSplitRatio(t *testing.T) {
	input := `{"cmd":"workspace_layout_set_split_ratio","workspace_id":"ws1","split_id":"split-1","ratio":0.3}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutSetSplitRatio {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutSetSplitRatio)
	}
	msg, ok := data.(*WorkspaceLayoutSetSplitRatioMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutSetSplitRatioMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.SplitID != "split-1" {
		t.Errorf("ids = %q/%q, want ws1/split-1", msg.WorkspaceID, msg.SplitID)
	}
	if msg.Ratio != 0.3 {
		t.Errorf("ratio = %v, want 0.3", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutDockTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_tile","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","tile_id":"tile-md","tile_kind":"markdown","ratio":0.3}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutDockTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutDockTile)
	}
	msg, ok := data.(*WorkspaceLayoutDockTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutDockTileMessage", data)
	}
	if msg.AnchorPaneID != "pane-a" || msg.TileID != "tile-md" || msg.TileKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want pane-a/tile-md/markdown", msg.AnchorPaneID, msg.TileID, msg.TileKind)
	}
	if msg.Edge != WorkspaceLayoutDockEdgeRight {
		t.Errorf("edge = %q, want right", msg.Edge)
	}
	if msg.Ratio == nil || *msg.Ratio != 0.3 {
		t.Errorf("ratio = %v, want 0.3", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutUndockTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_undock_tile","workspace_id":"ws1","tile_id":"tile-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutUndockTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutUndockTile)
	}
	msg, ok := data.(*WorkspaceLayoutUndockTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutUndockTileMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" {
		t.Errorf("fields = %q/%q, want ws1/tile-md", msg.WorkspaceID, msg.TileID)
	}
}

func TestParseWorkspaceLayoutUpdateTile(t *testing.T) {
	input := `{"cmd":"workspace_layout_update_tile","workspace_id":"ws1","tile_id":"tile-browser","tile_params":"https://example.com/docs","request_id":"request-1"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutUpdateTile {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutUpdateTile)
	}
	msg, ok := data.(*WorkspaceLayoutUpdateTileMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutUpdateTileMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-browser" || msg.TileParams != "https://example.com/docs" || msg.RequestID != "request-1" {
		t.Errorf("unexpected fields: %+v", msg)
	}
}

func TestParseWorkspaceLayoutMoveLeafToWorkspace(t *testing.T) {
	input := `{"cmd":"workspace_layout_move_leaf_to_workspace","source_workspace_id":"ws1","target_workspace_id":"ws2","leaf_id":"pane-a","anchor_id":"pane-b","edge":"left","ratio":0.32}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceLayoutMoveLeafToWorkspace {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceLayoutMoveLeafToWorkspace)
	}
	msg, ok := data.(*WorkspaceLayoutMoveLeafToWorkspaceMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceLayoutMoveLeafToWorkspaceMessage", data)
	}
	if msg.SourceWorkspaceID != "ws1" || msg.TargetWorkspaceID != "ws2" || msg.LeafID != "pane-a" || Deref(msg.AnchorID) != "pane-b" {
		t.Errorf("fields = %q/%q/%q/%q, want ws1/ws2/pane-a/pane-b", msg.SourceWorkspaceID, msg.TargetWorkspaceID, msg.LeafID, Deref(msg.AnchorID))
	}
	if msg.Edge != WorkspaceLayoutDockEdgeLeft {
		t.Errorf("edge = %q, want left", msg.Edge)
	}
	if msg.Ratio == nil || *msg.Ratio != 0.32 {
		t.Errorf("ratio = %v, want 0.32", msg.Ratio)
	}
}

func TestParseWorkspaceLayoutDockTileParamsRoundTrip(t *testing.T) {
	input := `{"cmd":"workspace_layout_dock_tile","workspace_id":"ws1","anchor_pane_id":"pane-a","edge":"right","tile_id":"tile-md","tile_kind":"markdown","tile_params":"/abs/file.md"}`
	_, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	msg := data.(*WorkspaceLayoutDockTileMessage)
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" || msg.TileKind != "markdown" {
		t.Errorf("fields = %q/%q/%q, want ws1/tile-md/markdown", msg.WorkspaceID, msg.TileID, msg.TileKind)
	}
	if Deref(msg.TileParams) != "/abs/file.md" {
		t.Errorf("tile_params = %q, want /abs/file.md", Deref(msg.TileParams))
	}
}

func TestParseWorkspaceTileContentGet(t *testing.T) {
	input := `{"cmd":"workspace_tile_content_get","workspace_id":"ws1","tile_id":"tile-md"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdWorkspaceTileContentGet {
		t.Fatalf("cmd = %q, want %q", cmd, CmdWorkspaceTileContentGet)
	}
	msg, ok := data.(*WorkspaceTileContentGetMessage)
	if !ok {
		t.Fatalf("data type = %T, want *WorkspaceTileContentGetMessage", data)
	}
	if msg.WorkspaceID != "ws1" || msg.TileID != "tile-md" {
		t.Errorf("fields = %q/%q, want ws1/tile-md", msg.WorkspaceID, msg.TileID)
	}
}

func TestParseOpenMarkdown(t *testing.T) {
	input := `{"cmd":"open_markdown","path":"/abs/file.md","session_id":"sess-1"}`
	cmd, data, err := ParseMessage([]byte(input))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdOpenMarkdown {
		t.Fatalf("cmd = %q, want %q", cmd, CmdOpenMarkdown)
	}
	msg, ok := data.(*OpenMarkdownMessage)
	if !ok {
		t.Fatalf("data type = %T, want *OpenMarkdownMessage", data)
	}
	if msg.Path != "/abs/file.md" || msg.SessionID == nil || *msg.SessionID != "sess-1" {
		t.Errorf("fields = %q/%v, want /abs/file.md/sess-1", msg.Path, msg.SessionID)
	}
}

func TestParseSeedReaderMessages(t *testing.T) {
	t.Run("open seed", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"open_seed","seed_id":"s-abc123","session_id":"sess-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		msg, ok := data.(*OpenSeedMessage)
		if cmd != CmdOpenSeed || !ok || msg.SeedID != "s-abc123" || Deref(msg.SessionID) != "sess-1" {
			t.Fatalf("parsed (%q, %T, %+v)", cmd, data, msg)
		}
	})
	t.Run("get seed document", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"seed_document_get","seed_id":"s-abc123","request_id":"req-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		msg, ok := data.(*SeedDocumentGetMessage)
		if cmd != CmdSeedDocumentGet || !ok || msg.SeedID != "s-abc123" || msg.RequestID != "req-1" {
			t.Fatalf("parsed (%q, %T, %+v)", cmd, data, msg)
		}
	})
	t.Run("edit seed body", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"seed_edit","seed_id":"s-abc123","body":"# revised"}`))
		if err != nil {
			t.Fatal(err)
		}
		msg, ok := data.(*SeedEditMessage)
		if cmd != CmdSeedEdit || !ok || msg.SeedID != "s-abc123" || msg.Body != "# revised" {
			t.Fatalf("parsed (%q, %T, %+v)", cmd, data, msg)
		}
	})
	t.Run("set seed resume identity", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"seed_set_resume","seed_id":"s-abc123","resume_session_id":"native-1","resume_cwd":"/tmp/work","resume_agent":"copilot"}`))
		if err != nil {
			t.Fatal(err)
		}
		msg, ok := data.(*SeedSetResumeMessage)
		if cmd != CmdSeedSetResume || !ok || msg.SeedID != "s-abc123" || Deref(msg.ResumeSessionID) != "native-1" || Deref(msg.ResumeCwd) != "/tmp/work" || Deref(msg.ResumeAgent) != "copilot" {
			t.Fatalf("parsed (%q, %T, %+v)", cmd, data, msg)
		}
	})
}

func TestParseBrowserMessages(t *testing.T) {
	t.Run("open browser", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"open_browser","url":"http://localhost:3000","session_id":"sess-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		if cmd != CmdOpenBrowser {
			t.Fatalf("cmd = %q, want %q", cmd, CmdOpenBrowser)
		}
		msg, ok := data.(*OpenBrowserMessage)
		if !ok || msg.URL != "http://localhost:3000" || Deref(msg.SessionID) != "sess-1" {
			t.Fatalf("message = %#v, want open browser payload", data)
		}
	})

	t.Run("browser control result", func(t *testing.T) {
		cmd, data, err := ParseMessage([]byte(`{"cmd":"browser_control_result","request_id":"req-1","success":true,"data":"ok"}`))
		if err != nil {
			t.Fatal(err)
		}
		if cmd != CmdBrowserControlResult {
			t.Fatalf("cmd = %q, want %q", cmd, CmdBrowserControlResult)
		}
		msg, ok := data.(*BrowserControlResultMessage)
		if !ok || msg.RequestID != "req-1" || !msg.Success || Deref(msg.Data) != "ok" {
			t.Fatalf("message = %#v, want browser control result payload", data)
		}
	})
}

func TestParseSetChiefOfStaff(t *testing.T) {
	cmd, data, err := ParseMessage([]byte(`{"cmd":"set_chief_of_staff","session_id":"session-1","chief_of_staff":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if cmd != CmdSetChiefOfStaff {
		t.Fatalf("cmd = %q, want %q", cmd, CmdSetChiefOfStaff)
	}
	msg, ok := data.(*SetChiefOfStaffMessage)
	if !ok || msg.SessionID != "session-1" || !msg.ChiefOfStaff {
		t.Fatalf("message = %#v, want chief-of-staff assignment", data)
	}
}

func TestParseAutoModeSettingsCommands(t *testing.T) {
	cases := []struct {
		input string
		cmd   string
		check func(t *testing.T, data any)
	}{
		{
			input: `{"cmd":"automode_rule_add","pattern":["git","push"],"decision":"prompt",` +
				`"justification":"it leaves the machine","request_id":"r1"}`,
			cmd: CmdAutoModeRuleAdd,
			check: func(t *testing.T, data any) {
				msg, ok := data.(*AutoModeRuleAddMessage)
				if !ok {
					t.Fatalf("data type = %T", data)
				}
				if strings.Join(msg.Pattern, " ") != "git push" || Deref(msg.Decision) != "prompt" {
					t.Errorf("rule = %+v", msg)
				}
				if Deref(msg.Justification) != "it leaves the machine" || msg.RequestID != "r1" {
					t.Errorf("rule = %+v", msg)
				}
			},
		},
		{
			input: `{"cmd":"automode_rule_remove","pattern":["git","push"]}`,
			cmd:   CmdAutoModeRuleRemove,
			check: func(t *testing.T, data any) {
				msg, ok := data.(*AutoModeRuleRemoveMessage)
				if !ok {
					t.Fatalf("data type = %T", data)
				}
				if strings.Join(msg.Pattern, " ") != "git push" || msg.RequestID != nil {
					t.Errorf("rule remove = %+v", msg)
				}
			},
		},
		{
			input: `{"cmd":"automode_host_add","host":"crates.io","decision":"allow","request_id":"r1"}`,
			cmd:   CmdAutoModeHostAdd,
			check: func(t *testing.T, data any) {
				msg, ok := data.(*AutoModeHostAddMessage)
				if !ok {
					t.Fatalf("data type = %T", data)
				}
				if msg.Host != "crates.io" || msg.Decision != "allow" {
					t.Errorf("host add = %+v", msg)
				}
			},
		},
		{
			input: `{"cmd":"automode_host_remove","host":"crates.io","decision":"deny"}`,
			cmd:   CmdAutoModeHostRemove,
			check: func(t *testing.T, data any) {
				msg, ok := data.(*AutoModeHostRemoveMessage)
				if !ok {
					t.Fatalf("data type = %T", data)
				}
				if msg.Host != "crates.io" || msg.Decision != "deny" {
					t.Errorf("host remove = %+v", msg)
				}
			},
		},
		{
			input: `{"cmd":"automode_policy_set","approval_policy":"never"}`,
			cmd:   CmdAutoModePolicySet,
			check: func(t *testing.T, data any) {
				msg, ok := data.(*AutoModePolicySetMessage)
				if !ok {
					t.Fatalf("data type = %T", data)
				}
				if Deref(msg.ApprovalPolicy) != "never" || msg.SandboxMode != nil {
					t.Errorf("policy set = %+v", msg)
				}
			},
		},
	}
	for _, tc := range cases {
		cmd, data, err := ParseMessage([]byte(tc.input))
		if err != nil {
			t.Fatalf("parse %s: %v", tc.input, err)
		}
		if cmd != tc.cmd {
			t.Fatalf("cmd = %q, want %q", cmd, tc.cmd)
		}
		tc.check(t, data)
	}
}

func TestParseMessageRejectsRetiredAutoModeCommands(t *testing.T) {
	for _, input := range []string{
		`{"cmd":"automode_model_set","models":["a/one"],"request_id":"r1"}`,
		`{"cmd":"automode_models","request_id":"r1"}`,
		`{"cmd":"automode_pattern_add","list":"allow","pattern":"git status*","request_id":"r1"}`,
		`{"cmd":"automode_pattern_remove","list":"allow","pattern":"git status*","request_id":"r1"}`,
	} {
		if _, _, err := ParseMessage([]byte(input)); err == nil ||
			!strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%s error = %v, want unknown command", input, err)
		}
	}
}

// The app reads a rule pattern as a list of alternatives per token, so a plain token
// arrives as a one-entry list rather than a bare string.
func TestAutoModeConfigResultRoundTripsARule(t *testing.T) {
	result := AutoModeConfigResultMessage{
		Event:     EventAutoModeConfigResult,
		RequestID: "r1",
		Success:   true,
		Config: &AutoModeConfigInfo{
			ApprovalPolicy: "on-request",
			SandboxMode:    "workspace-write",
			Rules: []AutoModeRuleInfo{{
				Pattern:  [][]string{{"git"}, {"push", "pull"}},
				Decision: "prompt",
				Match:    [][]string{{"git", "push", "origin"}},
				NotMatch: [][]string{},
			}},
			ShippedRules:         []AutoModeRuleInfo{},
			LegacyPatterns:       []string{"git status*"},
			ShippedDeniedDomains: []string{},
			Network: AutoModeNetworkInfo{
				Enabled: true, AllowedDomains: []string{"crates.io"}, DeniedDomains: []string{},
			},
		},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AutoModeConfigResultMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Config == nil || len(back.Config.Rules) != 1 {
		t.Fatalf("config = %+v", back.Config)
	}
	rule := back.Config.Rules[0]
	if len(rule.Pattern) != 2 || rule.Pattern[0][0] != "git" || len(rule.Pattern[1]) != 2 {
		t.Errorf("pattern = %v", rule.Pattern)
	}
	if len(rule.Match) != 1 || strings.Join(rule.Match[0], " ") != "git push origin" {
		t.Errorf("match = %v, want the example carried through untouched", rule.Match)
	}
	if back.Config.LegacyPatterns[0] != "git status*" || back.Config.Network.AllowedDomains[0] != "crates.io" {
		t.Errorf("config = %+v", back.Config)
	}
	for _, field := range []string{"approval_policy", "sandbox_mode", "rules", "shipped_rules",
		"network", "shipped_denied_domains", "legacy_patterns"} {
		if !strings.Contains(string(raw), `"`+field+`"`) {
			t.Errorf("wire config is missing %q: %s", field, raw)
		}
	}
	for _, gone := range []string{`"allow"`, `"hard_deny"`, `"models"`} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("wire config still carries %s: %s", gone, raw)
		}
	}
}
