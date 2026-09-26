package daemon

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func TestGitStatusSchedulerCoalescesDirtyRefreshes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var inFlight atomic.Int32
		var overlapped atomic.Bool
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})

		previousGetGitStatus := getGitStatusForDaemon
		getGitStatusForDaemon = func(_ context.Context, _ gitExecutor, dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
			if inFlight.Add(1) > 1 {
				overlapped.Store(true)
			}
			defer inFlight.Add(-1)

			call := calls.Add(1)
			if call == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return testGitStatus(dir, fmt.Sprintf("file-%d.txt", call)), nil
		}
		defer func() {
			getGitStatusForDaemon = previousGetGitStatus
		}()

		d := &Daemon{}
		client := &wsClient{send: make(chan outboundMessage, 10)}
		d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
			Cmd:       protocol.CmdSubscribeGitStatus,
			Directory: "/repo",
		})
		t.Cleanup(client.stopGitStatusPoll)

		<-firstStarted
		for i := 0; i < 5; i++ {
			client.requestGitStatusRefresh(gitStatusRefreshRequest{reason: gitStatusRefreshReasonDirty})
		}
		close(releaseFirst)

		time.Sleep(gitStatusRefreshDebounce)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("git status calls = %d, want 2", got)
		}
		time.Sleep(5 * gitStatusRefreshDebounce)
		synctest.Wait()

		if got := calls.Load(); got != 2 {
			t.Fatalf("git status calls = %d, want 2", got)
		}
		if overlapped.Load() {
			t.Fatal("git status refreshes overlapped")
		}
	})
}

func TestGitStatusSchedulerDelaysSafetyRefreshAfterSlowRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		previousGetGitStatus := getGitStatusForDaemon
		getGitStatusForDaemon = func(_ context.Context, _ gitExecutor, dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
			call := calls.Add(1)
			time.Sleep(gitStatusSlowRefreshDuration + time.Second)
			return testGitStatus(dir, fmt.Sprintf("file-%d.txt", call)), nil
		}
		defer func() {
			getGitStatusForDaemon = previousGetGitStatus
		}()

		d := &Daemon{}
		client := &wsClient{send: make(chan outboundMessage, 10)}
		d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
			Cmd:       protocol.CmdSubscribeGitStatus,
			Directory: "/repo",
		})
		t.Cleanup(client.stopGitStatusPoll)

		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("git status calls after subscribing = %d, want 1", got)
		}

		time.Sleep(2 * gitStatusSafetyInterval)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("git status calls = %d, want 1 slow run without normal safety refresh", got)
		}

		time.Sleep(gitStatusSlowSafetyInterval)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("git status calls after the slow safety interval = %d, want 2", got)
		}
	})
}

func TestGitStatusSchedulerUsesTrackedOnlyAfterLimitedRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		modes := make(chan gitStatusMode, 2)
		previousGetGitStatus := getGitStatusForDaemon
		getGitStatusForDaemon = func(_ context.Context, _ gitExecutor, dir string, mode gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
			modes <- mode
			call := calls.Add(1)
			status := testGitStatus(dir, fmt.Sprintf("file-%d.txt", call))
			if call == 1 {
				status.Limited = protocol.Ptr(true)
				status.Mode = protocol.Ptr(string(gitStatusModeTrackedOnly))
			}
			return status, nil
		}
		defer func() {
			getGitStatusForDaemon = previousGetGitStatus
		}()

		d := &Daemon{}
		client := &wsClient{send: make(chan outboundMessage, 10)}
		d.handleSubscribeGitStatus(client, &protocol.SubscribeGitStatusMessage{
			Cmd:       protocol.CmdSubscribeGitStatus,
			Directory: "/repo",
		})
		t.Cleanup(client.stopGitStatusPoll)

		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("git status calls after subscribing = %d, want 1", got)
		}
		client.requestGitStatusRefresh(gitStatusRefreshRequest{reason: gitStatusRefreshReasonDirty})
		time.Sleep(gitStatusRefreshDebounce)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("git status calls after the dirty refresh = %d, want 2", got)
		}

		first := <-modes
		second := <-modes
		if first != gitStatusModeFull {
			t.Fatalf("first mode = %q, want %q", first, gitStatusModeFull)
		}
		if second != gitStatusModeTrackedOnly {
			t.Fatalf("second mode = %q, want %q", second, gitStatusModeTrackedOnly)
		}
	})
}

func TestGitStatusCoordinatorSharesInFlightStatusForRepoAndMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		previousGetGitStatus := getGitStatusForDaemon
		getGitStatusForDaemon = func(_ context.Context, _ gitExecutor, dir string, _ gitStatusMode) (*protocol.GitStatusUpdateMessage, error) {
			call := calls.Add(1)
			if call == 1 {
				close(started)
				<-release
			}
			return testGitStatus(dir, "src/shared.ts"), nil
		}
		defer func() {
			getGitStatusForDaemon = previousGetGitStatus
		}()

		d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig)}
		results := make(chan *protocol.GitStatusUpdateMessage, 2)
		for i := 0; i < 2; i++ {
			go func() {
				status, _, err := d.statusReader().Status(context.Background(), "/repo", gitStatusModeFull)
				if err != nil {
					t.Errorf("Status failed: %v", err)
				}
				results <- status
			}()
		}

		<-started
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("git status calls while first refresh is in flight = %d, want 1", got)
		}
		close(release)

		for i := 0; i < 2; i++ {
			status := <-results
			if status == nil || len(status.Unstaged) != 1 || status.Unstaged[0].Path != "src/shared.ts" {
				t.Fatalf("status = %+v, want shared status result", status)
			}
		}
	})
}

func TestTrackedOnlyStatusResultStaysLimited(t *testing.T) {
	previousRunGitStatusCommand := runGitStatusCommandForDaemon
	runGitStatusCommandForDaemon = func(_ context.Context, _ *attngit.Client, _ string, _ time.Duration, args ...string) ([]byte, error) {
		if !containsArg(args, "--untracked-files=no") {
			t.Fatalf("args = %v, want tracked-only status", args)
		}
		return []byte(" M tracked.txt\x00"), nil
	}
	defer func() {
		runGitStatusCommandForDaemon = previousRunGitStatusCommand
	}()

	status, err := getGitStatusWithOptionsAdmitted(context.Background(), attngit.NewClient(), "/repo", gitStatusOptions{
		mode: gitStatusModeTrackedOnly,
	})
	if err != nil {
		t.Fatalf("getGitStatusWithOptionsAdmitted failed: %v", err)
	}
	if !protocol.Deref(status.Limited) {
		t.Fatal("tracked-only status limited = false, want true")
	}
	if protocol.Deref(status.LimitedReason) == "" {
		t.Fatal("tracked-only status missing limited reason")
	}
}

func TestGetGitStatusWithOptionsFallsBackToTrackedOnlyAfterFullTimeout(t *testing.T) {
	var calls atomic.Int32
	argsSeen := make(chan []string, 2)
	previousRunGitStatusCommand := runGitStatusCommandForDaemon
	runGitStatusCommandForDaemon = func(_ context.Context, _ *attngit.Client, _ string, _ time.Duration, args ...string) ([]byte, error) {
		argsSeen <- append([]string(nil), args...)
		if calls.Add(1) == 1 {
			return nil, fmt.Errorf("git status timed out after 5s: git status")
		}
		return []byte(" M tracked.txt\x00"), nil
	}
	defer func() {
		runGitStatusCommandForDaemon = previousRunGitStatusCommand
	}()

	status, err := getGitStatusWithOptionsAdmitted(context.Background(), attngit.NewClient(), "/repo", gitStatusOptions{
		mode:        gitStatusModeFull,
		fullTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("getGitStatusWithOptionsAdmitted failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("status command calls = %d, want 2", calls.Load())
	}
	firstArgs := <-argsSeen
	secondArgs := <-argsSeen
	if !containsArg(firstArgs, "--untracked-files=all") {
		t.Fatalf("first args = %v, want full untracked status", firstArgs)
	}
	if !containsArg(secondArgs, "--untracked-files=no") {
		t.Fatalf("second args = %v, want tracked-only status", secondArgs)
	}
	if !protocol.Deref(status.Limited) {
		t.Fatal("status limited = false, want true")
	}
	if got := protocol.Deref(status.Mode); got != string(gitStatusModeTrackedOnly) {
		t.Fatalf("status mode = %q, want %q", got, gitStatusModeTrackedOnly)
	}
	if len(status.Untracked) != 0 {
		t.Fatalf("untracked = %v, want none in tracked-only fallback", status.Untracked)
	}
}

func TestGitCoordinatorSharesInFlightFileDiff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		previousReadFileDiff := readFileDiffForDaemon
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		readFileDiffForDaemon = func(_ context.Context, _ gitExecutor, _ fileDiffCacheKey) (fileDiffContent, error) {
			call := calls.Add(1)
			if call == 1 {
				close(started)
				<-release
			}
			return fileDiffContent{original: "before", modified: "after"}, nil
		}
		defer func() {
			readFileDiffForDaemon = previousReadFileDiff
		}()

		d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig)}
		results := make(chan fileDiffContent, 2)
		for i := 0; i < 2; i++ {
			go func() {
				content, err := d.diffReader().FileDiff(context.Background(), "/repo", "src/file.ts", "HEAD", "", false)
				if err != nil {
					t.Errorf("FileDiff failed: %v", err)
				}
				results <- content
			}()
		}

		<-started
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("file diff calls while first refresh is in flight = %d, want 1", got)
		}
		close(release)

		for i := 0; i < 2; i++ {
			content := <-results
			if content.original != "before" || content.modified != "after" {
				t.Fatalf("file diff content = %+v, want shared content", content)
			}
		}
	})
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func testGitStatus(dir, path string) *protocol.GitStatusUpdateMessage {
	return &protocol.GitStatusUpdateMessage{
		Event:     protocol.EventGitStatusUpdate,
		Directory: dir,
		Staged:    []protocol.GitFileChange{},
		Unstaged:  []protocol.GitFileChange{{Path: path, Status: "modified"}},
		Untracked: []protocol.GitFileChange{},
	}
}
