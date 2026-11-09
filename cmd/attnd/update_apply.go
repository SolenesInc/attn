package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/selfupdate"
)

type updateApply struct {
	run, kind, dir, root, instance string
	dataDir                        string
}

func runUpdateApply() {
	flags := flag.NewFlagSet("_update-apply", flag.ExitOnError)
	apply := updateApply{dataDir: config.DataDir()}
	flags.StringVar(&apply.run, "run", "", "")
	flags.StringVar(&apply.kind, "kind", "", "")
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

// installedBuild is how one kind of install holds its old build while the new
// one starts. restoreOld must be safe after a replace that failed part way.
type installedBuild interface {
	daemon() string
	keepOld() error
	replace() error
	restoreOld() error
	discardOld() error
}

func (a updateApply) installed() installedBuild {
	switch selfupdate.Kind(a.kind) {
	case selfupdate.ReleaseApp:
		return &appBundle{apply: a}
	case selfupdate.ReleaseBinary:
		return looseBinaries{apply: a}
	default:
		return appTree{apply: a}
	}
}

func (a updateApply) execute() error {
	if err := os.WriteFile(selfupdate.RestartMarkerPath(a.dataDir), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600); err != nil {
		return a.fail(fmt.Sprintf("could not write the restart marker: %v", err))
	}
	build := a.installed()
	if err := build.keepOld(); err != nil {
		return a.fail(err.Error())
	}
	if err := build.replace(); err != nil {
		return a.rollBack(build, err)
	}
	if output, err := exec.Command(build.daemon(), "daemon", "ensure").CombinedOutput(); err != nil {
		return a.rollBack(build, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))))
	}
	if err := build.discardOld(); err != nil {
		fmt.Fprintf(os.Stderr, "could not remove the old install: %v\n", err)
	}
	if err := a.relaunchApp(); err != nil {
		fmt.Fprintf(os.Stderr, "could not reopen the app: %v\n", err)
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
func (a updateApply) rollBack(build installedBuild, cause error) error {
	failure := fmt.Sprintf("the new build did not start, so attn went back to the old one: %v", cause)
	_ = a.fail(failure)
	if err := build.restoreOld(); err != nil {
		return a.fail(fmt.Sprintf("%s; restoring the old install also failed: %v", failure, err))
	}
	output, err := exec.Command(build.daemon(), "daemon", "ensure").CombinedOutput()
	if err != nil {
		return a.fail(fmt.Sprintf("%s; the old build then did not start either: %s. Restore a database backup with attn db restore", failure, strings.TrimSpace(string(output))))
	}
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

type appTree struct{ apply updateApply }

func (t appTree) daemon() string { return config.AppDaemonBinaryInTree(t.apply.root) }

func (t appTree) previous() string {
	return filepath.Join(filepath.Dir(t.apply.root), "."+filepath.Base(t.apply.root)+".previous")
}

func (t appTree) keepOld() error {
	if err := os.RemoveAll(t.previous()); err != nil {
		return fmt.Errorf("could not clear %s: %v", t.previous(), err)
	}
	if output, err := exec.Command("cp", "-a", t.apply.root, t.previous()).CombinedOutput(); err != nil {
		return fmt.Errorf("could not keep the old install: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (t appTree) replace() error {
	args := []string{"install-staged"}
	if t.apply.instance != "" {
		args = append(args, "INSTANCE="+t.apply.instance)
	}
	cmd := exec.Command("make", args...)
	cmd.Dir = t.apply.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make install-staged: %w", err)
	}
	return nil
}

func (t appTree) restoreOld() error {
	if err := os.RemoveAll(t.apply.root); err != nil {
		return err
	}
	return os.Rename(t.previous(), t.apply.root)
}

func (t appTree) discardOld() error { return os.RemoveAll(t.previous()) }

// appBundle exchanges the staged bundle with the installed one, so a complete
// bundle is at the app's path at every moment. After the exchange the staged
// path holds the old bundle.
type appBundle struct {
	apply     updateApply
	exchanged bool
}

func (b *appBundle) staged() string { return filepath.Join(b.apply.dir, selfupdate.BundleName) }

func (b *appBundle) daemon() string {
	return filepath.Join(b.apply.root, "Contents", "MacOS", "attnd")
}

func (b *appBundle) keepOld() error { return nil }

func (b *appBundle) replace() error {
	app := resolveInstance("")
	app.AppPath, app.AppExecutable = b.apply.root, config.AppExecutableInTree(b.apply.root)
	if _, err := stopInstanceApp(app); err != nil {
		return fmt.Errorf("could not quit the app: %w", err)
	}
	if err := selfupdate.ExchangePaths(b.staged(), b.apply.root); err != nil {
		return fmt.Errorf("could not swap in the new app: %w", err)
	}
	b.exchanged = true
	return nil
}

func (b *appBundle) restoreOld() error {
	if !b.exchanged {
		return nil
	}
	return selfupdate.ExchangePaths(b.staged(), b.apply.root)
}

func (b *appBundle) discardOld() error { return os.RemoveAll(b.apply.dir) }

// looseBinaries replaces only the named files beside the daemon. The directory
// they sit in, such as ~/.local/bin, belongs to the user.
type looseBinaries struct{ apply updateApply }

func (l looseBinaries) names() []string { return []string{ptyhost.BinaryName, "attnd", "attn"} }

func (l looseBinaries) daemon() string { return filepath.Join(l.apply.root, "attnd") }

func (l looseBinaries) keepOld() error {
	for _, name := range l.names() {
		if err := os.Remove(filepath.Join(l.apply.root, name+".previous")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("could not clear the old copy of %s: %v", name, err)
		}
	}
	return nil
}

func (l looseBinaries) replace() error {
	names := l.names()
	for _, name := range names {
		target := filepath.Join(l.apply.root, name)
		defer os.Remove(target + ".new")
		if output, err := exec.Command("cp", filepath.Join(l.apply.dir, name), target+".new").CombinedOutput(); err != nil {
			return fmt.Errorf("could not copy %s into %s: %v: %s", name, l.apply.root, err, strings.TrimSpace(string(output)))
		}
	}
	for _, name := range names {
		target := filepath.Join(l.apply.root, name)
		if err := replaceKeepingPrevious(target+".new", target); err != nil {
			return err
		}
	}
	return nil
}

func (l looseBinaries) restoreOld() error {
	for _, name := range l.names() {
		target := filepath.Join(l.apply.root, name)
		if err := os.Rename(target+".previous", target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (l looseBinaries) discardOld() error {
	for _, name := range l.names() {
		if err := os.Remove(filepath.Join(l.apply.root, name+".previous")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.RemoveAll(l.apply.dir)
}
