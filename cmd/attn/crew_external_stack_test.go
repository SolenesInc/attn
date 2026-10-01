package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestABareCrewWrapperKeepsItsDayAcrossDaemonRestarts(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	id := uuid.NewString()
	launch := s.LaunchInTerminal(testworld.Invocation{Args: []string{"--member", "keel"}, Dir: s.Dir, Env: []string{"ATTN_INSIDE_APP=1", "ATTN_AGENT=claude", "ATTN_SESSION_ID=" + id}})
	agent := s.Launched(id)
	for range 2 {
		s.Stop()
		s.Start()
		s.App() // Initial state waits for startup recovery.
		awake, err := s.Client().CrewWake("keel", "claude")
		if err != nil || !awake.AlreadyAwake || awake.SessionID != id {
			t.Fatalf("wake of live bare wrapper = %+v, %v", awake, err)
		}
		if err := s.Client().RegisterAsMember(uuid.NewString(), "duplicate", s.Dir, "claude", "keel"); err == nil {
			t.Fatal("second wrapper claimed a live member")
		}
	}
	agent.Exit(0)
	if got := launch.Wait(); got.Code != 0 {
		t.Fatalf("wrapper exit = %+v", got)
	}
	if asleep, err := s.Client().CrewSleep("keel"); err != nil || !asleep.AlreadyAsleep {
		t.Fatalf("sleep after normal unregister = %+v, %v", asleep, err)
	}
}

func TestACrashedBareCrewWrapperReleasesItsDay(t *testing.T) {
	t.Parallel()
	for _, restartDaemon := range []bool{false, true} {
		for _, action := range []string{"wake", "sleep", "restart", "bare"} {
			t.Run(fmt.Sprintf("restart-daemon=%t/%s", restartDaemon, action), func(t *testing.T) {
				s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
				writeCharter(t, s, "keel")
				s.Start()
				id := uuid.NewString()
				launch := s.LaunchInTerminal(testworld.Invocation{Args: []string{"--member", "keel"}, Dir: s.Dir, Env: []string{"ATTN_INSIDE_APP=1", "ATTN_AGENT=claude", "ATTN_SESSION_ID=" + id}})
				s.Launched(id)
				if got := launch.Crash(); got.Code == 0 {
					t.Fatal("crashed wrapper exited successfully")
				}
				if restartDaemon {
					s.Stop()
					s.Start()
					s.App()
				}
				cli := s.Client()
				switch action {
				case "wake":
					next, err := cli.CrewWake("keel", "claude")
					if err != nil || next.AlreadyAwake || next.SessionID == id {
						t.Fatalf("wake after crash = %+v, %v", next, err)
					}
					s.Launched(next.SessionID)
				case "sleep":
					asleep, err := cli.CrewSleep("keel")
					if err != nil || !asleep.AlreadyAsleep {
						t.Fatalf("sleep after crash = %+v, %v", asleep, err)
					}
				case "restart":
					next, err := cli.CrewRestart("keel", uuid.NewString())
					if err != nil || next.Restart.State != protocol.CrewRestartStateCompleted || protocol.Deref(next.Restart.SuccessorSessionID) == id {
						t.Fatalf("restart after crash = %+v, %v", next, err)
					}
					s.Launched(protocol.Deref(next.Restart.SuccessorSessionID))
				case "bare":
					replacementID := uuid.NewString()
					replacement := s.LaunchInTerminal(testworld.Invocation{Args: []string{"--member", "keel"}, Dir: s.Dir, Env: []string{"ATTN_INSIDE_APP=1", "ATTN_AGENT=claude", "ATTN_SESSION_ID=" + replacementID}})
					agent := s.Launched(replacementID)
					awake, err := cli.CrewWake("keel", "claude")
					if err != nil || !awake.AlreadyAwake || awake.SessionID != replacementID {
						t.Fatalf("replacement bare wrapper = %+v, %v", awake, err)
					}
					agent.Exit(0)
					replacement.Wait()
				}
			})
		}
	}
}

func TestAReusedWrapperPIDCannotKeepACrewDayAlive(t *testing.T) {
	t.Parallel()
	for _, restartDaemon := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart-daemon=%t", restartDaemon), func(t *testing.T) {
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
			writeCharter(t, s, "keel")
			s.Start()
			token, err := procreap.StartToken(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			conn, err := s.DialUnix()
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			err = json.NewEncoder(conn).Encode(protocol.RegisterMessage{Cmd: protocol.CmdRegister, ID: id, Dir: s.Dir, WorkspaceID: "workspace-" + id, Agent: protocol.Ptr("claude"), Member: protocol.Ptr("keel"), ExternalProcess: &protocol.ExternalProcess{Pid: os.Getpid(), StartToken: token + "-previous-process"}})
			if err != nil {
				t.Fatal(err)
			}
			var result protocol.Response
			if err := json.NewDecoder(conn).Decode(&result); err != nil || !result.Ok {
				t.Fatalf("register = %+v, %v", result, err)
			}
			conn.Close()
			if restartDaemon {
				s.Stop()
				s.Start()
				s.App()
			}
			awake, err := s.Client().CrewWake("keel", "claude")
			if err != nil || awake.AlreadyAwake || awake.SessionID == id {
				t.Fatalf("wake with mismatched process identity = %+v, %v", awake, err)
			}
			s.Launched(awake.SessionID)
		})
	}
}
