package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

type stubRemoteCommandResolver struct {
	path map[string]string
}

func (s stubRemoteCommandResolver) EndpointIDForPath(path string) (string, bool) {
	endpointID, ok := s.path[path]
	return endpointID, ok
}

func TestRemoteCommandsRouteToTheDaemonThatOwnsTheirTarget(t *testing.T) {
	type route struct{ session, workspace, endpoint string }
	seedWorkspace := protocol.Ptr("workspace-must-not-route")
	cases := []struct {
		name string
		cmd  string
		msg  interface{}
		want route
	}{
		{
			name: "unregister handled locally",
			cmd:  protocol.CmdUnregister,
			msg:  &protocol.UnregisterMessage{ID: "sess-unregister"},
		},
		{
			name: "session_selected",
			cmd:  protocol.CmdSessionSelected,
			msg:  &protocol.SessionSelectedMessage{ID: "sess-selected"},
			want: route{session: "sess-selected"},
		},
		{
			name: "rename_session",
			cmd:  protocol.CmdRenameSession,
			msg:  &protocol.RenameSessionMessage{SessionID: "sess-rename"},
			want: route{session: "sess-rename"},
		},
		{
			name: "open_markdown",
			cmd:  protocol.CmdOpenMarkdown,
			msg:  &protocol.OpenMarkdownMessage{Path: "/tmp/notes.md", SessionID: protocol.Ptr("sess-open-markdown")},
			want: route{session: "sess-open-markdown"},
		},
		{
			name: "open_markdown without session id",
			cmd:  protocol.CmdOpenMarkdown,
			msg:  &protocol.OpenMarkdownMessage{Path: "/tmp/notes.md"},
		},
		{
			name: "open_seed stays hub local",
			cmd:  protocol.CmdOpenSeed,
			msg:  &protocol.OpenSeedMessage{SeedID: "s-abc123", SessionID: protocol.Ptr("sess-remote")},
		},
		{
			name: "markdown_annotations_submit",
			cmd:  protocol.CmdMarkdownAnnotationsSubmit,
			msg:  &protocol.MarkdownAnnotationsSubmitMessage{Path: protocol.Ptr("/tmp/notes.md"), TargetSessionID: protocol.Ptr("sess-md-submit")},
			want: route{session: "sess-md-submit"},
		},
		{
			name: "markdown_annotations_note_on_seed stays home local",
			cmd:  protocol.CmdMarkdownAnnotationsSubmit,
			msg: &protocol.MarkdownAnnotationsSubmitMessage{
				SeedID: protocol.Ptr("s-abc123"), TargetSeedID: protocol.Ptr("s-abc123"),
			},
		},
		{
			name: "settle_turn",
			cmd:  protocol.CmdSettleTurn,
			msg:  &protocol.SettleTurnMessage{SessionID: "sess-settle"},
			want: route{session: "sess-settle"},
		},
		{
			name: "session_messages_get",
			cmd:  protocol.CmdSessionMessagesGet,
			msg:  &protocol.SessionMessagesGetMessage{SessionID: "sess-messages"},
			want: route{session: "sess-messages"},
		},
		{
			name: "session_annotations_submit",
			cmd:  protocol.CmdSessionAnnotationsSubmit,
			msg:  &protocol.SessionAnnotationsSubmitMessage{SessionID: "sess-anno-submit", Text: "feedback"},
			want: route{session: "sess-anno-submit"},
		},
		{
			name: "session_annotations_get",
			cmd:  protocol.CmdSessionAnnotationsGet,
			msg:  &protocol.SessionAnnotationsGetMessage{SessionID: "sess-anno-get"},
			want: route{session: "sess-anno-get"},
		},
		{
			name: "session_annotations_save",
			cmd:  protocol.CmdSessionAnnotationsSave,
			msg:  &protocol.SessionAnnotationsSaveMessage{SessionID: "sess-anno-save"},
			want: route{session: "sess-anno-save"},
		},
		{
			name: "session_annotations_clear",
			cmd:  protocol.CmdSessionAnnotationsClear,
			msg:  &protocol.SessionAnnotationsClearMessage{SessionID: "sess-anno-clear"},
			want: route{session: "sess-anno-clear"},
		},
		{
			name: "workspace_tile_content_get",
			cmd:  protocol.CmdWorkspaceTileContentGet,
			msg:  &protocol.WorkspaceTileContentGetMessage{WorkspaceID: "workspace-remote"},
			want: route{workspace: "workspace-remote"},
		},
		{
			name: "markdown_annotations_get on a file",
			cmd:  protocol.CmdMarkdownAnnotationsGet,
			msg:  &protocol.MarkdownAnnotationsGetMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-get")},
			want: route{workspace: "workspace-md-get"},
		},
		{
			name: "markdown_annotations_save on a file",
			cmd:  protocol.CmdMarkdownAnnotationsSave,
			msg:  &protocol.MarkdownAnnotationsSaveMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-save")},
			want: route{workspace: "workspace-md-save"},
		},
		{
			name: "markdown_annotations_clear on a file",
			cmd:  protocol.CmdMarkdownAnnotationsClear,
			msg:  &protocol.MarkdownAnnotationsClearMessage{SourceKind: annotationSourceFile, WorkspaceID: protocol.Ptr("workspace-md-clear")},
			want: route{workspace: "workspace-md-clear"},
		},
		{
			name: "markdown_annotations_get on a seed stays home",
			cmd:  protocol.CmdMarkdownAnnotationsGet,
			msg:  &protocol.MarkdownAnnotationsGetMessage{SourceKind: annotationSourceSeed, WorkspaceID: seedWorkspace},
		},
		{
			name: "markdown_annotations_save on a seed stays home",
			cmd:  protocol.CmdMarkdownAnnotationsSave,
			msg:  &protocol.MarkdownAnnotationsSaveMessage{SourceKind: annotationSourceSeed, WorkspaceID: seedWorkspace},
		},
		{
			name: "markdown_annotations_clear on a seed stays home",
			cmd:  protocol.CmdMarkdownAnnotationsClear,
			msg:  &protocol.MarkdownAnnotationsClearMessage{SourceKind: annotationSourceSeed, WorkspaceID: seedWorkspace},
		},
		{
			name: "rename_workspace",
			cmd:  protocol.CmdRenameWorkspace,
			msg:  &protocol.RenameWorkspaceMessage{WorkspaceID: "workspace-rename"},
			want: route{workspace: "workspace-rename"},
		},
		{
			name: "get_file_diff in a remote directory",
			cmd:  protocol.CmdGetFileDiff,
			msg:  &protocol.GetFileDiffMessage{Directory: "/srv/repo"},
			want: route{endpoint: "endpoint-path"},
		},
		{
			name: "list_worktrees of a remote repository",
			cmd:  protocol.CmdListWorktrees,
			msg:  &protocol.ListWorktreesMessage{MainRepo: "/srv/repo"},
			want: route{endpoint: "endpoint-path"},
		},
		{
			name: "get_file_diff in a home directory",
			cmd:  protocol.CmdGetFileDiff,
			msg:  &protocol.GetFileDiffMessage{Directory: "/home/repo"},
		},
	}

	resolver := stubRemoteCommandResolver{path: map[string]string{"/srv/repo": "endpoint-path"}}
	for _, tc := range cases {
		endpoint, _ := remoteCommandScopedEndpointID(tc.msg, resolver)
		got := route{
			session:   remoteCommandSessionID(tc.cmd, tc.msg),
			workspace: remoteCommandWorkspaceID(tc.cmd, tc.msg),
			endpoint:  endpoint,
		}
		if got != tc.want {
			t.Errorf("%s routes to %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
