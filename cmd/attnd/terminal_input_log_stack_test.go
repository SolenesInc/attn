package main_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWhatTheUserTypesAtAHiddenPromptStaysOutOfEveryLog(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.StartWith("DEBUG=debug")
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	secret := "pw-" + uuid.NewString()[:8]

	app.TypeLine(shell, `stty -echo; printf '__%s\n' hidden; read -r answer; printf '__read_%d\n' "${#answer}"`)
	app.AwaitScreen(shell, "__hidden")
	app.TypeLine(shell, secret)
	app.AwaitScreen(shell, "__read_"+strconv.Itoa(len(secret)))

	logs := 0
	err := filepath.WalkDir(s.Dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".log") {
			return err
		}
		logs++
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.Lines(string(content)) {
			if strings.Contains(line, secret) {
				t.Errorf("%s holds the text typed at a hidden prompt: %s", path, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if logs == 0 {
		t.Fatalf("found no .log file under %s to check", s.Dir)
	}
}
