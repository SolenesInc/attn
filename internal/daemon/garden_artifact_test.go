package daemon

import (
	"net"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func artifactNote(t *testing.T, d *Daemon, seedID, kind, body string, artifact *protocol.SeedArtifactReference) protocol.Response {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd:             protocol.CmdSeedNote,
		SourceSessionID: protocol.Ptr("sess-a"),
		SeedID:          seedID,
		Body:            body,
		Kind:            protocol.Ptr(kind),
		Artifact:        artifact,
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
}

func markdownArtifact(path string) *protocol.SeedArtifactReference {
	return &protocol.SeedArtifactReference{Kind: garden.ArtifactMarkdownFile, Path: protocol.Ptr(path)}
}

func TestSeedArtifactsPageToTheEndOfTheLog(t *testing.T) {
	d := newGardenDaemon(t)
	d.gardenNotePageSize = 3
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Long haul"})
	if resp := artifactNote(t, d, seed.ID, garden.NoteKindAttach, "", markdownArtifact("plan.md")); !resp.Ok {
		t.Fatalf("attach: %v", protocol.Deref(resp.Error))
	}
	for i := 0; i < 3*d.gardenNotePageSize; i++ {
		note(t, d, "sess-a", seed.ID, "another day of work", "")
	}

	result := show(t, d, seed.ID)
	if len(result.References) != 1 || protocol.Deref(result.References[0].Path) != "plan.md" {
		t.Fatalf("references = %+v, want plan.md still current several pages down", result.References)
	}

	if resp := artifactNote(t, d, seed.ID, garden.NoteKindDetach, "", markdownArtifact("plan.md")); !resp.Ok {
		t.Fatalf("detach: %v", protocol.Deref(resp.Error))
	}
	if after := show(t, d, seed.ID); len(after.References) != 0 {
		t.Fatalf("references = %+v, want the detach to have taken it back", after.References)
	}
}
