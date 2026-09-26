package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestBrowserTargetFromRemoteWorkspace(t *testing.T) {
	target, err := browserTargetFromRemoteWorkspace(&protocol.Workspace{
		ID: "remote-workspace",
		Layout: &protocol.WorkspaceLayout{
			WorkspaceID:  "remote-workspace",
			ActivePaneID: "",
			LayoutJson:   `{"type":"tile","tile_id":"tile-browser","tile_kind":"browser","tile_params":"https://example.com"}`,
		},
	}, "endpoint-1")
	if err != nil {
		t.Fatalf("browserTargetFromRemoteWorkspace() error = %v", err)
	}
	if target.workspaceID != "remote-workspace" || target.remoteEndpointID != "endpoint-1" {
		t.Fatalf("target = %+v", target)
	}
	if target.anchorLeafID != browserTileID || !browserTileInWorkspace(target.layout) {
		t.Fatalf("remote target did not preserve the browser tile: %+v", target)
	}
}
