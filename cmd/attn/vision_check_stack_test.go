package main_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

type visionClaude struct {
	t   *testing.T
	dir string
}

func installVisionClaude(t *testing.T, dir string) visionClaude {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %[1]q/argv\ncat > %[1]q/stdin\ncat %[1]q/reply\n", dir)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return visionClaude{t: t, dir: dir}
}

func (c visionClaude) replies(lines ...string) {
	c.t.Helper()
	if err := os.WriteFile(filepath.Join(c.dir, "reply"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		c.t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(c.dir, "stdin"))
}

func (c visionClaude) argv() []string {
	c.t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.dir, "argv"))
	if err != nil {
		c.t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

type visionMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Source struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		} `json:"content"`
	} `json:"message"`
}

func (c visionClaude) received() (visionMessage, bool) {
	c.t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.dir, "stdin"))
	if os.IsNotExist(err) {
		return visionMessage{}, false
	}
	if err != nil {
		c.t.Fatal(err)
	}
	line := strings.TrimSuffix(string(raw), "\n")
	if strings.Contains(line, "\n") {
		c.t.Fatalf("claude received more than one stream-json line:\n%s", raw)
	}
	var msg visionMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		c.t.Fatalf("claude received %q, not a stream-json message: %v", line, err)
	}
	return msg, true
}

func TestVisionCheckAsksClaudeAboutTheImageAndReportsItsLastResult(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	claude := installVisionClaude(t, t.TempDir())
	images := t.TempDir()
	vision := func(args ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{
			Args: append([]string{"vision-check"}, args...),
			Env:  []string{"PATH=" + filepath.Join(claude.dir, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")},
		})
	}
	image := func(name, body string) string {
		t.Helper()
		path := filepath.Join(images, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	answered := `{"type":"result","subtype":"success","is_error":false,"result":"the button is blue","total_cost_usd":0.0234,"num_turns":1}`

	claude.replies(
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+strings.Repeat("x", 300*1024)+`"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"first, overridden","total_cost_usd":0.01,"num_turns":1}`,
		answered,
	)
	shot := image("shot.png", "PNG pixels")
	checked := vision(shot, "what color is the button?", "--json")
	var out struct {
		Answer   string  `json:"answer"`
		Model    string  `json:"model"`
		CostUSD  float64 `json:"cost_usd"`
		NumTurns int     `json:"num_turns"`
		IsError  bool    `json:"is_error"`
	}
	checked.JSON(t, &out)
	if checked.Code != 0 || out.Answer != "the button is blue" || out.Model != "sonnet" || out.CostUSD != 0.0234 || out.NumTurns != 1 || out.IsError {
		t.Errorf("vision-check --json exited %d with %+v, want the last result event's answer, cost and turns from sonnet", checked.Code, out)
	}
	if argv := claude.argv(); !slices.Contains(argv, "--model") || argv[slices.Index(argv, "--model")+1] != "sonnet" {
		t.Errorf("claude ran with %q, want --model sonnet by default", argv)
	}
	msg, _ := claude.received()
	if msg.Type != "user" || msg.Message.Role != "user" || len(msg.Message.Content) != 2 {
		t.Fatalf("claude received %+v, want one user message with a text and an image block", msg)
	}
	text, img := msg.Message.Content[0], msg.Message.Content[1]
	if text.Type != "text" || text.Text != "what color is the button?" {
		t.Errorf("the first block is %+v, want the question", text)
	}
	if img.Type != "image" || img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data != base64.StdEncoding.EncodeToString([]byte("PNG pixels")) {
		t.Errorf("the second block is %+v, want the image as base64 png", img)
	}

	claude.replies(answered)
	plain := vision(shot, "what color is the button?", "--model", "opus")
	if plain.Code != 0 || plain.Stdout != "the button is blue\n" || !strings.Contains(plain.Stderr, "model=opus cost_usd=0.0234 turns=1") {
		t.Errorf("vision-check exited %d with stdout %q and stderr %q, want the bare answer and opus's cost on stderr", plain.Code, plain.Stdout, plain.Stderr)
	}
	if argv := claude.argv(); argv[slices.Index(argv, "--model")+1] != "opus" {
		t.Errorf("claude ran with %q, want --model opus", argv)
	}

	for name, want := range map[string]string{
		"photo.jpg": "image/jpeg", "photo.jpeg": "image/jpeg", "PHOTO.JPG": "image/jpeg",
		"anim.gif": "image/gif", "pic.webp": "image/webp", "x.PNG": "image/png",
	} {
		claude.replies(answered)
		if result := vision(image(name, name), "what is this?"); result.Code != 0 {
			t.Errorf("vision-check %s exited %d: %s", name, result.Code, result.Stderr)
		}
		if msg, sent := claude.received(); !sent || msg.Message.Content[1].Source.MediaType != want {
			t.Errorf("vision-check %s sent claude %+v, want media type %s", name, msg, want)
		}
	}

	for _, name := range []string{"file.bmp", "file", "file.txt", "file.tiff"} {
		claude.replies(answered)
		refused := vision(image(name, name), "what is this?")
		if _, sent := claude.received(); refused.Code != 1 || !strings.Contains(refused.Stderr, "unsupported image extension") || sent {
			t.Errorf("vision-check %s exited %d with %q and reached claude %t, want a refusal before asking", name, refused.Code, refused.Stderr, sent)
		}
	}

	for _, tc := range []struct {
		name  string
		reply []string
		want  string
	}{
		{name: "an error result", reply: []string{`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"hit max turns"}`}, want: "hit max turns"},
		{name: "no result event", reply: []string{`{"type":"system","subtype":"init"}`, `{"type":"assistant","message":{"role":"assistant","content":[]}}`}, want: "no result event"},
	} {
		claude.replies(tc.reply...)
		if failed := vision(shot, "what color is the button?"); failed.Code != 1 || failed.Stdout != "" || !strings.Contains(failed.Stderr, tc.want) {
			t.Errorf("vision-check over %s exited %d with stdout %q and stderr %q, want exit 1 saying %q", tc.name, failed.Code, failed.Stdout, failed.Stderr, tc.want)
		}
	}

	if usage := vision(shot); usage.Code != 2 || !strings.Contains(usage.Stderr, "usage: attn vision-check <image> <question>") {
		t.Errorf("vision-check without a question exited %d with %q, want the usage", usage.Code, usage.Stderr)
	}
}
