package daemon_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const askQuestion = "\n\n:::question\nShip it?\n- [ ] Yes\n- [ ] No\n:::\n"

func TestTheAskToolsOpenListShowAndCloseAnAskWithItsImages(t *testing.T) {
	s := newSessionTools(t, "asks_enabled")
	shot := bytes.Repeat([]byte("png"), 300)
	shotPath := filepath.Join(s.w.Path("shop"), "shots", "before.png")
	if err := os.MkdirAll(filepath.Dir(shotPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shotPath, shot, 0o644); err != nil {
		t.Fatal(err)
	}
	askWith := func(args map[string]any) protocol.Ask {
		t.Helper()
		var asked protocol.Ask
		structuredAs(t, s.shapes.call("ask", args), &asked)
		requestID := "ask-image-" + asked.ID
		image := testworld.Request(s.app, protocol.AskImageMessage{Cmd: protocol.CmdAskImage, RequestID: requestID, AskID: asked.ID, Path: shotPath},
			protocol.EventAskImageResult, func(r protocol.AskImageResultMessage) bool { return r.RequestID == requestID })
		if got, _ := base64.StdEncoding.DecodeString(protocol.Deref(image.DataBase64)); !image.Success || !bytes.Equal(got, shot) {
			t.Errorf("ask %v: the home shows %d bytes for its image (%v), want its %d", args, len(got), protocol.Deref(image.Error), len(shot))
		}
		return asked
	}
	asked := askWith(map[string]any{"subject": "Banner", "markdown": "The banner overlaps the tabs:\n\n![before](shots/before.png)" + askQuestion})
	if asked.ID == "" || asked.Subject != "Banner" {
		t.Fatalf("ask answered %+v, want an open ask with the subject", asked)
	}
	s.shapes.call("ask_close", map[string]any{"ask": askWith(map[string]any{"markdown": "Absolute: ![before](" + shotPath + ")" + askQuestion}).ID})

	after := bytes.Repeat([]byte("new"), 300)
	afterPath := filepath.Join(s.w.Path("shop"), "shots", "after.png")
	if err := os.WriteFile(afterPath, after, 0o644); err != nil {
		t.Fatal(err)
	}
	var amended protocol.Ask
	structuredAs(t, s.shapes.call("ask_amend", map[string]any{"ask": asked.ID, "markdown": "Moved the tabs down:\n\n![after](shots/after.png)"}), &amended)
	if last := amended.Parts[len(amended.Parts)-1]; last.AmendedAt == nil || !strings.Contains(protocol.Deref(last.Markdown), "Moved the tabs down") {
		t.Errorf("ask_amend answered the parts %+v, want the amendment last with its time", amended.Parts)
	}
	requestID := "ask-image-amended"
	image := testworld.Request(s.app, protocol.AskImageMessage{Cmd: protocol.CmdAskImage, RequestID: requestID, AskID: asked.ID, Path: afterPath},
		protocol.EventAskImageResult, func(r protocol.AskImageResultMessage) bool { return r.RequestID == requestID })
	if got, _ := base64.StdEncoding.DecodeString(protocol.Deref(image.DataBase64)); !image.Success || !bytes.Equal(got, after) {
		t.Errorf("the home shows %d bytes for the amendment's image (%v), want its %d", len(got), protocol.Deref(image.Error), len(after))
	}

	var listed protocol.AskListResult
	structuredAs(t, s.shapes.call("ask_list", map[string]any{}), &listed)
	if len(listed.Asks) != 1 || listed.Asks[0].ID != asked.ID {
		t.Errorf("ask_list answered %+v, want the one ask", listed)
	}
	s.shapes.call("ask_show", map[string]any{"ask": asked.ID})
	s.shapes.call("ask_close", map[string]any{"ask": asked.ID})
	if gone := s.agent.mustCall(t, "ask_show", map[string]any{"ask": asked.ID}); !gone.IsError {
		t.Errorf("ask_show after ask_close answered %s, want a refusal", resultText(gone))
	}
	s.shapes.finish("ask")
}

func TestAnAgentCannotSeeOrCloseAnAskOfAnotherProfile(t *testing.T) {
	p := newTwoProfileTools(t, "asks_enabled")
	cli := p.w.Client()
	var asked protocol.Ask
	structuredAs(t, p.theirs.mustCall(t, "ask", map[string]any{"markdown": "Ship it?" + askQuestion}), &asked)

	if listed, err := cli.AskList(protocol.SessionID(p.here)); err != nil || len(listed.Asks) != 0 || len(listed.Overseen) != 0 {
		t.Errorf("ask list through run_command from the other profile answered %+v, %v; want nothing", listed, err)
	}
	if _, err := cli.AskShow(protocol.SessionID(p.here), asked.ID); err == nil {
		t.Errorf("ask show of the side profile's ask through run_command succeeded")
	}
	if err := cli.AskClose(protocol.SessionID(p.here), asked.ID, "not mine"); err == nil {
		t.Errorf("ask close of the side profile's ask through run_command succeeded")
	}
	var listed protocol.AskListResult
	structuredAs(t, p.mine.mustCall(t, "ask_list", map[string]any{}), &listed)
	if len(listed.Asks) != 0 || len(listed.Overseen) != 0 {
		t.Errorf("ask_list through /mcp from the other profile answered %+v, want nothing", listed)
	}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"ask_show", map[string]any{"ask": asked.ID}},
		{"ask_close", map[string]any{"ask": asked.ID, "reason": "not mine"}},
	} {
		if refused := p.mine.mustCall(t, call.tool, call.args); !refused.IsError {
			t.Errorf("%s of the side profile's ask through /mcp answered %s, want a refusal", call.tool, resultText(refused))
		}
	}
	if shown, err := cli.AskShow(protocol.SessionID(p.there), asked.ID); err != nil || shown.Ask.ID != asked.ID {
		t.Errorf("the side profile's ask after the other profile's calls: %+v, %v; want it still open", shown, err)
	}
}

func TestTheAskToolRequiresItsMarkdownAndRefusesAnEmptyOneInToolWords(t *testing.T) {
	s := newSessionTools(t, "asks_enabled")
	session, err := s.agent.connect(t, "")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "ask" {
			continue
		}
		schema, _ := tool.InputSchema.(map[string]any)
		if required, _ := schema["required"].([]any); !slices.Contains(required, any("markdown")) {
			t.Errorf("the ask tool's input schema requires %v, want markdown among them", required)
		}
	}
	refused := s.agent.mustCall(t, "ask", map[string]any{"markdown": "  \n"})
	text := resultText(refused)
	if !refused.IsError || !strings.Contains(text, "the ask has no markdown; write it in markdown") || strings.Contains(text, "stdin") || strings.Contains(text, "--file") {
		t.Errorf("an empty ask answered %q, want a refusal that names the markdown argument and no stdin or --file", text)
	}
}
