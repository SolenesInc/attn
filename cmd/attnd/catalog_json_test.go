package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/catalog"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

// The results of the commands whose --json also reads the result to settle the exit status.
var jsonDaemonResults = map[string]any{
	"agent msg":          protocol.AgentMsgResult{Status: "sent"},
	"agent close":        protocol.AgentCloseResult{Rule: "other"},
	"agent reload":       protocol.AgentReloadResult{},
	"agent inbox":        protocol.AgentInboxBatchResult{},
	"callout anchors":    protocol.CalloutAnchorsResult{AppShowingProfile: true},
	"delegate":           map[string]any{"state": "completed", "result": map[string]any{"session_id": "s-1"}},
	"review check":       protocol.ReviewCheckResult{},
	"seed standby":       protocol.SeedStandbyResult{},
	"seed export":        protocol.SeedExportResult{Text: protocol.Ptr("# seed\n")},
	"session snooze":     protocol.SessionSnoozeResult{},
	"session unsnooze":   protocol.SessionSnoozeResult{},
	"terminal run":       protocol.TerminalRunResult{Exited: protocol.Ptr(true), ExitCode: protocol.Ptr(3)},
	"terminal wait":      protocol.TerminalWaitResult{Exited: protocol.Ptr(true), ExitCode: protocol.Ptr(3)},
	"vent":               protocol.Vent{ID: "v-abc123"},
	"vent withdraw":      protocol.VentWithdrawResult{ID: "v-abc123"},
	"session transcript": protocol.SessionTranscriptResult{AtEnd: true, Events: []protocol.SessionTranscriptEvent{{}}},
}

// The commands that exit with what they ran, and the status the stand-in result gives.
var jsonExitCodes = map[string]int{"terminal run": 3, "terminal wait": 3}

func TestEveryCatalogCommandThatOffersJSONPrintsItsResultAsJSON(t *testing.T) {
	t.Parallel()
	all, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	attn := testworld.AttnBinary(t)
	dir, err := os.MkdirTemp("", "attn-json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go answerEveryCommand(listener)

	checked := 0
	for i := range all.Commands {
		cmd := &all.Commands[i]
		if cmd.Result == "" || !cmd.ThroughLayer() {
			continue
		}
		checked++
		t.Run(cmd.Name, func(t *testing.T) {
			args := append(strings.Fields(cmd.Name), requiredArgs(t, all, cmd)...)
			run := exec.Command(attn, append(args, "--json")...)
			run.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "ATTN_DATA_DIR=" + dir, "ATTN_SOCKET_PATH=" + socket, "ATTN_TERMINAL_ID=term-1", "ATTN_INSIDE_APP=1"}
			run.Dir = dir
			run.Stdin = strings.NewReader(`{"title":"x","children":[{"title":"y"}]}`)
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			err := run.Run()
			code := 0
			var exited *exec.ExitError
			if errors.As(err, &exited) {
				code, err = exited.ExitCode(), nil
			}
			if err != nil || code != jsonExitCodes[cmd.Name] {
				t.Fatalf("attn %q exited %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", args, code, err, jsonExitCodes[cmd.Name], stdout.String(), stderr.String())
			}
			decoder := json.NewDecoder(&stdout)
			values := 0
			for {
				var value any
				err := decoder.Decode(&value)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("attn %q --json printed something that is not JSON: %v\nstdout:\n%s", args, err, stdout.String())
				}
				values++
			}
			if values == 0 {
				t.Fatalf("attn %q --json printed nothing\nstderr:\n%s", args, stderr.String())
			}
		})
	}
	if checked == 0 {
		t.Fatal("the catalog offered no command with --json")
	}
}

func requiredArgs(t *testing.T, all *catalog.Catalog, cmd *catalog.Command) []string {
	t.Helper()
	var flags, positionals []string
	for _, arg := range cmd.Args {
		if !arg.Required || arg.Internal() || arg.Piped {
			continue
		}
		value := "x"
		if arg.File || arg.Stdin {
			value = "-"
		}
		kind := all.Types[arg.Type].Schema
		switch {
		case kind["type"] == "integer":
			value = "1"
		case kind["enum"] != nil:
			value = kind["enum"].([]any)[0].(string)
		}
		switch {
		case arg.CLI == "words":
			positionals = append(positionals, "anchor", "text")
		case arg.CLI == "pos" || arg.CLI == "rest":
			positionals = append(positionals, value)
		case strings.HasPrefix(arg.CLI, "-") && kind["type"] == "boolean":
			flags = append(flags, strings.TrimSuffix(arg.CLI, "+"))
		case strings.HasPrefix(arg.CLI, "-"):
			flags = append(flags, strings.TrimSuffix(arg.CLI, "+"), value)
		default:
			t.Fatalf("%s: no way to pass %s as %q; teach requiredArgs", cmd.Name, arg.Name, arg.CLI)
		}
	}
	return slices.Concat(flags, positionals)
}

func answerEveryCommand(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer connection.Close()
			var request map[string]any
			if err := json.NewDecoder(connection).Decode(&request); err != nil {
				return
			}
			reply := map[string]any{"ok": true, "success": true}
			if request["cmd"] == "run_command" {
				name, _ := request["command"].(string)
				reply["command_result"] = map[string]any{"marker": name}
				if result, ok := jsonDaemonResults[name]; ok {
					reply["command_result"] = result
				}
			}
			_ = json.NewEncoder(connection).Encode(reply)
		}()
	}
}

func TestPresenceOutsideAttnPrintsItsAnswerAsJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	run := exec.Command(testworld.AttnBinary(t), "presence", "--json")
	run.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "ATTN_DATA_DIR=" + dir}
	run.Dir = dir
	var stdout bytes.Buffer
	run.Stdout = &stdout
	err := run.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 1 {
		t.Fatalf("attn presence --json outside attn: %v, want exit 1", err)
	}
	var answer protocol.PresenceResult
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("attn presence --json outside attn printed %q, not JSON: %v", stdout.String(), err)
	}
	if answer.SessionID != "" || len(answer.SeedIds) != 0 {
		t.Fatalf("attn presence --json outside attn answered %+v, want no session and no seeds", answer)
	}
}
