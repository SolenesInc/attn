package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func readCrewManagementFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "app", "scripts", "real-app-harness", "fixtures", name))
	if err != nil {
		t.Fatalf("read crew management fixture %s: %v", name, err)
	}
	return string(content)
}

func TestCrewCharter_ReadsAndWritesTheCanonicalFileWithContentCAS(t *testing.T) {
	d := newCrewDaemon(t)
	member, _, err := d.resolveCrewMember("trellis")
	if err != nil {
		t.Fatal(err)
	}
	charter := readCrewManagementFixture(t, "trellis-charter.md")
	if err := os.WriteFile(member.CharterPath, []byte(charter), 0o644); err != nil {
		t.Fatal(err)
	}
	read, err := d.crewCharterGet("trellis")
	if err != nil {
		t.Fatalf("read charter: %v", err)
	}
	if read.Charter.Content != charter || read.Charter.Token == "" {
		t.Fatalf("charter = %+v", read.Charter)
	}

	written, err := d.crewCharterSet("trellis", charter+"\n<!-- changed -->\n", read.Charter.Token)
	if err != nil {
		t.Fatalf("write charter: %v", err)
	}
	if written.Conflict || written.Charter.Token == read.Charter.Token {
		t.Fatalf("write result = %+v", written)
	}
	content, err := os.ReadFile(member.CharterPath)
	if err != nil || string(content) != written.Charter.Content {
		t.Fatalf("canonical charter = %q, err=%v", content, err)
	}
}

func TestCrewCharter_ExternalEditWinsAConflictAndIsReturnedInFull(t *testing.T) {
	d := newCrewDaemon(t)
	read, err := d.crewCharterGet("alder")
	if err != nil {
		t.Fatal(err)
	}
	member, _, err := d.resolveCrewMember("alder")
	if err != nil {
		t.Fatal(err)
	}
	external := "# Alder\n\nExternally rewritten while the editor was open.\n"
	if err := os.WriteFile(member.CharterPath, []byte(external), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := d.crewCharterSet("alder", "stale local edit", read.Charter.Token)
	if err != nil {
		t.Fatalf("conflicting write: %v", err)
	}
	if !result.Conflict || result.Charter.Content != external || result.Charter.Token == read.Charter.Token {
		t.Fatalf("conflict = %+v", result)
	}
	content, _ := os.ReadFile(member.CharterPath)
	if string(content) != external {
		t.Fatalf("conflict overwrote canonical charter: %q", content)
	}
}

func TestCrewHandoffs_ReadsCompleteNewestFirstHistoryAndRealDates(t *testing.T) {
	d := newCrewDaemon(t)
	member, _, err := d.resolveCrewMember("trellis")
	if err != nil {
		t.Fatal(err)
	}
	body := readCrewManagementFixture(t, "trellis-2026-09-01T21-37Z.md")
	filename := "2026-09-01T21-37Z-trellis.md"
	if err := os.WriteFile(filepath.Join(member.HomeDir, crew.HandoffsDirName, filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := d.crewHandoffsGet("trellis")
	if err != nil {
		t.Fatalf("read handoffs: %v", err)
	}
	if len(result.Handoffs) != 2 || result.Handoffs[0].Filename != filename || result.Handoffs[0].Content != body {
		t.Fatalf("handoffs = %+v", result.Handoffs)
	}
	wantDate := time.Date(2026, 9, 1, 21, 37, 0, 0, time.UTC)
	if !result.Handoffs[0].OccurredAt.Equal(wantDate) || result.Handoffs[0].Token == "" {
		t.Fatalf("latest handoff date/token = %s/%q", result.Handoffs[0].OccurredAt, result.Handoffs[0].Token)
	}
}

func TestCrewHandoffs_MissingDirectoryIsAnHonestEmptyHistory(t *testing.T) {
	d := newCrewDaemon(t)
	member, _, err := d.resolveCrewMember("keel")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(member.HomeDir, crew.HandoffsDirName)); err != nil {
		t.Fatal(err)
	}
	result, err := d.crewHandoffsGet("keel")
	if err != nil || result.Handoffs == nil || len(result.Handoffs) != 0 {
		t.Fatalf("missing history = %+v, err=%v", result, err)
	}
}

func TestCrewDocuments_WebSocketResultsCorrelateAndOutpostsAreRefused(t *testing.T) {
	d := newCrewDaemon(t)
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleCrewCharterGetWS(client, &protocol.CrewCharterGetMessage{
		Cmd: protocol.CmdCrewCharterGet, Member: "alder", RequestID: protocol.Ptr("charter-1"),
	})
	var response protocol.CrewCharterGetResultMessage
	raw := <-client.send
	if err := json.Unmarshal(raw.payload, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.RequestID != "charter-1" || protocol.Deref(response.Member) != "alder" || response.Charter == nil {
		t.Fatalf("websocket response = %+v", response)
	}
	d.handleCrewCharterSetWS(client, &protocol.CrewCharterSetMessage{
		Cmd: protocol.CmdCrewCharterSet, Member: "alder", Content: "must not be written", ExpectedToken: response.Charter.Token,
	})
	var refused protocol.CrewCharterSetResultMessage
	raw = <-client.send
	if err := json.Unmarshal(raw.payload, &refused); err != nil {
		t.Fatal(err)
	}
	if refused.Success || refused.RequestID != "" || !strings.Contains(protocol.Deref(refused.Error), "request id") {
		t.Fatalf("missing-id response = %+v", refused)
	}
	current, err := d.crewCharterGet("alder")
	if err != nil || current.Charter.Content == "must not be written" {
		t.Fatalf("missing-id write reached charter: %+v, err=%v", current, err)
	}

	const home = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	outpost := newEnrolledDaemon(t, home)
	t.Cleanup(outpost.stopEventBus)
	writeCrewHomes(t, outpost.dataRoot)
	outpost.ensureCrewCollections()
	outpost.importCrewHomes()
	_, err = outpost.crewCharterGet("alder")
	if err == nil || !strings.Contains(err.Error(), home) {
		t.Fatalf("outpost read error = %v", err)
	}
}

func TestCrewDocuments_StoredPathsRemainBehindTheCrewRootFence(t *testing.T) {
	d := newCrewDaemon(t)
	outside := filepath.Join(t.TempDir(), crew.CharterFileName)
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := d.updateCrewMember("alder", func(member *crew.Member) (bool, error) {
		member.CharterPath = outside
		return true, nil
	})
	if err == nil || !strings.Contains(err.Error(), "outside this daemon's crew root") {
		t.Fatalf("path-fence write error = %v", err)
	}
}
