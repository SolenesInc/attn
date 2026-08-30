package main

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Not a real test: re-exec'd so a process other than this one holds app.pid. It
// ignores SIGTERM, which is how an app that will not shut down is modelled.
func TestProfileAppStopHelperProcess(t *testing.T) {
	readyPath := os.Getenv("ATTN_APP_STOP_HELPER_READY")
	if readyPath == "" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("helper: write ready file: %v", err)
	}
	time.Sleep(time.Hour)
}

// The helper is this test binary, so it passes the ownership gate against an
// AppExecutable of os.Args[0] exactly as the installed app passes against its own.
func spawnStubbornApp(t *testing.T) int {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "helper-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileAppStopHelperProcess$")
	cmd.Env = append(os.Environ(), "ATTN_APP_STOP_HELPER_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn stubborn app helper: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			return cmd.Process.Pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("stubborn app helper never wrote %s", readyPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func spawnForeignProcess(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

// The waits are tripwires sized for a real app; a test that has to reach one waits
// for it, so shrink them to keep that honest and fast.
func shrinkAppStopWaits(t *testing.T) {
	t.Helper()
	quit, term, poll := appStopQuitWait, appStopSigtermWait, appStopPollInterval
	appStopQuitWait, appStopSigtermWait, appStopPollInterval = 200*time.Millisecond, 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		appStopQuitWait, appStopSigtermWait, appStopPollInterval = quit, term, poll
	})
}

func writeAppPID(t *testing.T, dataDir string, pid int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "app.pid"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A profile whose every path is disposable, so a clean can really run against it.
func sandboxedProfile(t *testing.T) profileResolved {
	t.Helper()
	root := t.TempDir()
	r := profileResolved{
		Label:         "sandbox",
		DataDir:       filepath.Join(root, "data"),
		AppPath:       filepath.Join(root, "install", "attn-sandbox"),
		AppExecutable: os.Args[0],
		AppLocalData:  filepath.Join(root, "app-local-data"),
		BundleID:      "com.attn.manager.sandbox-test",
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.AppLocalData, "debug.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStopProfileAppEscalatesWhenTheAppIgnoresTheQuitRequest(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedProfile(t)
	pid := spawnStubbornApp(t)
	writeAppPID(t, r.DataDir, pid)

	msg, err := stopProfileApp(r)
	if err != nil {
		t.Fatalf("stopProfileApp = %v, want the fence to escalate to SIGKILL", err)
	}
	if !strings.Contains(msg, "force-killed") {
		t.Fatalf("stopProfileApp = %q, want a force-killed note", msg)
	}
	if !processGone(pid) {
		t.Fatalf("pid %d is still alive after stopProfileApp reported %q", pid, msg)
	}
	if _, err := os.Stat(filepath.Join(r.DataDir, "app.pid")); !os.IsNotExist(err) {
		t.Fatalf("app.pid survived the stop: %v", err)
	}
}

func TestStopProfileAppLeavesAForeignPIDAlone(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedProfile(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)

	msg, err := stopProfileApp(r)
	if err == nil {
		t.Fatalf("stopProfileApp = %q, want a refusal: pid %d is not the profile's app", msg, pid)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(pid)) {
		t.Fatalf("stopProfileApp error = %v, want it to name pid %d", err, pid)
	}
	if processGone(pid) {
		t.Fatalf("pid %d was signalled; a pid file naming a stranger must never be", pid)
	}
}

// The whole point of the fence: a clean that could not stop the app removes nothing,
// or a WebKit writer that outlives the command recreates what was just deleted.
func TestCleanProfileRemovesNothingWhileTheAppIsUp(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedProfile(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)

	var out strings.Builder
	err := cleanProfile(&out, r)
	if err == nil {
		t.Fatalf("cleanProfile succeeded with the app still up; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "nothing was removed") || !strings.Contains(err.Error(), r.AppLocalData) {
		t.Fatalf("cleanProfile error = %v, want it to name the app local data dir and say nothing was removed", err)
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if !fileExists(dir) {
			t.Fatalf("%s was removed even though the app was never stopped", dir)
		}
	}
}

func TestCleanProfileRemovesTheAppLocalDataOnlyAfterTheAppExits(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedProfile(t)
	pid := spawnStubbornApp(t)
	writeAppPID(t, r.DataDir, pid)

	var out strings.Builder
	if err := cleanProfile(&out, r); err != nil {
		t.Fatalf("cleanProfile = %v; output:\n%s", err, out.String())
	}
	if !processGone(pid) {
		t.Fatalf("pid %d outlived the clean; output:\n%s", pid, out.String())
	}
	if !strings.Contains(out.String(), "force-killed") {
		t.Fatalf("cleanProfile output = %q, want the slow shutdown reported", out.String())
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if fileExists(dir) {
			t.Fatalf("%s survived a clean that did stop the app", dir)
		}
	}
}
