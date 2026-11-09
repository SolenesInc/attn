package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/selfupdate"
)

type updateApply struct {
	run, dir, root, instance string
	dataDir                  string
}

func runUpdateApply() {
	flags := flag.NewFlagSet("_update-apply", flag.ExitOnError)
	apply := updateApply{dataDir: config.DataDir()}
	flags.StringVar(&apply.run, "run", "", "")
	flags.StringVar(&apply.dir, "dir", "", "")
	flags.StringVar(&apply.root, "root", "", "")
	flags.StringVar(&apply.instance, "instance", "", "")
	_ = flags.Parse(os.Args[2:])
	if err := config.ValidateSessionHome(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err := apply.execute()
	pausepoint.At(pausepoint.UpdateApplyFinished)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a updateApply) previousRoot() string {
	return filepath.Join(filepath.Dir(a.root), "."+filepath.Base(a.root)+".previous")
}

func (a updateApply) execute() error {
	if err := os.WriteFile(selfupdate.RestartMarkerPath(a.dataDir), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600); err != nil {
		return a.fail(fmt.Sprintf("could not write the restart marker: %v", err))
	}
	if err := os.RemoveAll(a.previousRoot()); err != nil {
		return a.fail(fmt.Sprintf("could not clear %s: %v", a.previousRoot(), err))
	}
	if output, err := exec.Command("cp", "-a", a.root, a.previousRoot()).CombinedOutput(); err != nil {
		return a.fail(fmt.Sprintf("could not keep the old install: %v: %s", err, strings.TrimSpace(string(output))))
	}
	if err := a.install(); err != nil {
		return a.rollBack(err)
	}
	daemon := config.AppDaemonBinaryInTree(a.root)
	if _, err := daemonctl.Ensure(context.Background(), daemon); err != nil {
		return a.rollBack(err)
	}
	if err := os.RemoveAll(a.previousRoot()); err != nil {
		fmt.Fprintf(os.Stderr, "could not remove the old install %s: %v\n", a.previousRoot(), err)
	}
	if err := a.relaunchApp(); err != nil {
		fmt.Fprintf(os.Stderr, "could not reopen the app: %v\n", err)
	}
	return nil
}

func (a updateApply) install() error {
	args := []string{"install-staged"}
	if a.instance != "" {
		args = append(args, "INSTANCE="+a.instance)
	}
	cmd := exec.Command("make", args...)
	cmd.Dir = a.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make install-staged: %w", err)
	}
	return nil
}

func (a updateApply) fail(cause string) error {
	if err := selfupdate.WriteFailure(a.dataDir, selfupdate.Failure{Run: a.run, Cause: cause}); err != nil {
		fmt.Fprintf(os.Stderr, "could not record the failure: %v\n", err)
	}
	return fmt.Errorf("%s", cause)
}

// rollBack records the cause before starting the old build, because that start
// is what reads it.
func (a updateApply) rollBack(cause error) error {
	failure := fmt.Sprintf("the new build did not start, so attn went back to the old one: %v", cause)
	_ = a.fail(failure)
	if err := os.RemoveAll(a.root); err != nil {
		return a.fail(fmt.Sprintf("%s; removing the failed install also failed: %v", failure, err))
	}
	if err := os.Rename(a.previousRoot(), a.root); err != nil {
		return a.fail(fmt.Sprintf("%s; restoring the old install also failed: %v", failure, err))
	}
	output, err := exec.Command(config.AppDaemonBinaryInTree(a.root), "daemon", "ensure").CombinedOutput()
	if err != nil {
		return a.fail(fmt.Sprintf("%s; the old build then did not start either: %s. Restore a database backup with attn db restore", failure, strings.TrimSpace(string(output))))
	}
	return a.relaunchAfterRollback(failure)
}

func (a updateApply) relaunchAfterRollback(failure string) error {
	if err := a.relaunchApp(); err != nil {
		fmt.Fprintf(os.Stderr, "could not reopen the app: %v\n", err)
	}
	return fmt.Errorf("%s", failure)
}

func (a updateApply) relaunchApp() error {
	app := config.AppExecutableInTree(a.root)
	if _, err := os.Stat(app); err != nil {
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", a.root)
	} else {
		cmd = exec.Command("setsid", app)
	}
	return cmd.Start()
}
