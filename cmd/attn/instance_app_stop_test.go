package main

import (
	"errors"
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

func TestInstanceAppStopHelperProcess(t *testing.T) {
	readyPath := os.Getenv("ATTN_APP_STOP_HELPER_READY")
	if readyPath == "" {
		return
	}
	relaunch := os.Getenv("ATTN_APP_STOP_HELPER_RELAUNCH")
	term := make(chan os.Signal, 1)
	if relaunch == "" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(term, syscall.SIGTERM)
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("helper: write ready file: %v", err)
	}
	if relaunch == "" {
		time.Sleep(time.Hour)
		return
	}
	<-term
	path, pid, _ := strings.Cut(relaunch, "|")
	if err := os.WriteFile(path, []byte(pid), 0o644); err != nil {
		t.Fatalf("helper: write relaunch marker: %v", err)
	}
}

func spawnStubbornApp(t *testing.T, extraEnv ...string) int {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "helper-ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstanceAppStopHelperProcess$")
	cmd.Env = append(os.Environ(), "ATTN_APP_STOP_HELPER_READY="+readyPath)
	cmd.Env = append(cmd.Env, extraEnv...)
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

func sandboxedInstance(t *testing.T) instanceResolved {
	t.Helper()
	root := t.TempDir()
	r := instanceResolved{
		Label:         "sandbox",
		DataDir:       filepath.Join(root, "data"),
		AppPath:       filepath.Join(root, "install", "attn-sandbox"),
		AppExecutable: os.Args[0],
		AppLocalData:  filepath.Join(root, "app-local-data"),
		AppLock:       filepath.Join(root, "locks", "app-sandbox.lock"),
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

func stoppedInstance(t *testing.T) instanceResolved {
	t.Helper()
	r := sandboxedInstance(t)
	if err := os.RemoveAll(r.AppPath); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStopInstanceAppEscalatesWhenTheAppIgnoresTheQuitRequest(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnStubbornApp(t)
	writeAppPID(t, r.DataDir, pid)

	msg, err := stopInstanceApp(r)
	if err != nil {
		t.Fatalf("stopInstanceApp = %v, want the fence to escalate to SIGKILL", err)
	}
	if !strings.Contains(msg, "force-killed") {
		t.Fatalf("stopInstanceApp = %q, want a force-killed note", msg)
	}
	if !processGone(pid) {
		t.Fatalf("pid %d is still alive after stopInstanceApp reported %q", pid, msg)
	}
	if _, err := os.Stat(filepath.Join(r.DataDir, "app.pid")); !os.IsNotExist(err) {
		t.Fatalf("app.pid survived the stop: %v", err)
	}
}

func TestStopInstanceAppLeavesAForeignPIDAlone(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)

	msg, err := stopInstanceApp(r)
	if err == nil {
		t.Fatalf("stopInstanceApp = %q, want a refusal: pid %d is not the instance's app", msg, pid)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(pid)) {
		t.Fatalf("stopInstanceApp error = %v, want it to name pid %d", err, pid)
	}
	if processGone(pid) {
		t.Fatalf("pid %d was signalled; a pid file naming a stranger must never be", pid)
	}
}

func TestCleanInstanceRemovesNothingWhileTheAppIsUp(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)

	var out strings.Builder
	err := cleanInstance(&out, r)
	if err == nil {
		t.Fatalf("cleanInstance succeeded with the app still up; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "nothing was removed") || !strings.Contains(err.Error(), r.AppLocalData) {
		t.Fatalf("cleanInstance error = %v, want it to name the app local data dir and say nothing was removed", err)
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if !fileExists(dir) {
			t.Fatalf("%s was removed even though the app was never stopped", dir)
		}
	}
}

func TestCleanInstanceRemovesTheAppLocalDataOnlyAfterTheAppExits(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnStubbornApp(t)
	writeAppPID(t, r.DataDir, pid)

	var out strings.Builder
	if err := cleanInstance(&out, r); err != nil {
		t.Fatalf("cleanInstance = %v; output:\n%s", err, out.String())
	}
	if !processGone(pid) {
		t.Fatalf("pid %d outlived the clean; output:\n%s", pid, out.String())
	}
	if !strings.Contains(out.String(), "force-killed") {
		t.Fatalf("cleanInstance output = %q, want the slow shutdown reported", out.String())
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if fileExists(dir) {
			t.Fatalf("%s survived a clean that did stop the app", dir)
		}
	}
}

func TestQuitAppPIDRevalidatesOwnershipBeforeSignalling(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)
	pidPath := filepath.Join(r.DataDir, "app.pid")

	msg, err := quitAppPID(r, pid, pidPath)
	if err != nil {
		t.Fatalf("quitAppPID = %v, want the app treated as gone once the pid is a stranger", err)
	}
	if !strings.Contains(msg, "another process") {
		t.Fatalf("quitAppPID = %q, want it to report the pid is another process now", msg)
	}
	if processGone(pid) {
		t.Fatalf("pid %d was signalled after ownership was lost", pid)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale app.pid survived: %v", err)
	}
}

func TestStopInstanceAppFailsClosedOnAnUnidentifiablePID(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	pid := spawnForeignProcess(t)
	writeAppPID(t, r.DataDir, pid)

	previous := lookupProcessExecutable
	lookupProcessExecutable = func(int) (string, error) { return "", errors.New("permission denied") }
	t.Cleanup(func() { lookupProcessExecutable = previous })

	var out strings.Builder
	err := cleanInstance(&out, r)
	if err == nil {
		t.Fatalf("cleanInstance succeeded with an unidentifiable live pid; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "could not be identified") {
		t.Fatalf("cleanInstance error = %v, want it to report the pid could not be identified", err)
	}
	if processGone(pid) {
		t.Fatalf("pid %d was signalled although it could not be identified", pid)
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if !fileExists(dir) {
			t.Fatalf("%s was removed while a live pid could not be identified", dir)
		}
	}
}

func TestCleanInstanceAbortsWhenTheAppRelaunchesWhileStopping(t *testing.T) {
	shrinkAppStopWaits(t)
	r := sandboxedInstance(t)
	relaunched := spawnForeignProcess(t)
	pidPath := filepath.Join(r.DataDir, "app.pid")
	pid := spawnStubbornApp(t, "ATTN_APP_STOP_HELPER_RELAUNCH="+pidPath+"|"+strconv.Itoa(relaunched))
	writeAppPID(t, r.DataDir, pid)

	var out strings.Builder
	err := cleanInstance(&out, r)
	if err == nil {
		t.Fatalf("cleanInstance succeeded across a relaunch; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "relaunched") {
		t.Fatalf("cleanInstance error = %v, want it to report the relaunch", err)
	}
	raw, readErr := os.ReadFile(pidPath)
	if readErr != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(relaunched) {
		t.Fatalf("app.pid = %q (err %v), want the relaunched pid %d left alone", raw, readErr, relaunched)
	}
	for _, dir := range []string{r.DataDir, r.AppPath, r.AppLocalData} {
		if !fileExists(dir) {
			t.Fatalf("%s was removed under a relaunched app", dir)
		}
	}
}

func TestCleanInstanceAbortsWhileAnAppHoldsTheLockAndProceedsOnceItReleases(t *testing.T) {
	shrinkAppStopWaits(t)
	r := stoppedInstance(t)
	if err := os.MkdirAll(filepath.Dir(r.AppLock), 0o700); err != nil {
		t.Fatal(err)
	}
	held := make([]*os.File, 2)
	for i := range held {
		f, err := os.OpenFile(r.AppLock, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
			t.Fatalf("app instance %d could not take the shared app lock: %v", i, err)
		}
		held[i] = f
	}

	var out strings.Builder
	if err := cleanInstance(&out, r); err == nil {
		t.Fatalf("cleanInstance succeeded while apps held %s shared; output:\n%s", r.AppLock, out.String())
	} else if !strings.Contains(err.Error(), r.AppLock) || !strings.Contains(err.Error(), "nothing was removed") {
		t.Fatalf("cleanInstance error = %v, want it to name the held lock and say nothing was removed", err)
	}
	for _, dir := range []string{r.DataDir, r.AppLocalData} {
		if !fileExists(dir) {
			t.Fatalf("%s was removed while the app lock was held", dir)
		}
	}
	if strings.Contains(out.String(), "daemon") {
		t.Fatalf("cleanInstance got as far as the daemon under a held lock; output:\n%s", out.String())
	}

	for _, f := range held {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := cleanInstance(&out, r); err != nil {
		t.Fatalf("cleanInstance = %v once every app released the lock; output:\n%s", err, out.String())
	}
	for _, dir := range []string{r.DataDir, r.AppLocalData} {
		if fileExists(dir) {
			t.Fatalf("%s survived the clean that ran after the apps released the lock", dir)
		}
	}
}

func TestCleanInstanceReleasesTheAppLockWhenItFinishes(t *testing.T) {
	shrinkAppStopWaits(t)
	r := stoppedInstance(t)

	var out strings.Builder
	if err := cleanInstance(&out, r); err != nil {
		t.Fatalf("cleanInstance = %v; output:\n%s", err, out.String())
	}
	f, err := os.OpenFile(r.AppLock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the app lock is still held after the clean finished: %v", err)
	}
}
