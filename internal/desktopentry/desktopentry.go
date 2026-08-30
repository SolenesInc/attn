// Package desktopentry registers a profile's deep-link scheme with the Linux
// desktop database, the only route an attn:// link has from outside attn.
package desktopentry

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Entry struct {
	AppName string
	Exec    string
	Scheme  string
}

// Written but inert: the entry is on disk and the desktop will route the scheme
// once the named tools exist.
type Report struct {
	Path         string
	Ran          []string
	MissingTools []string
}

// A `-handler` suffix leaves `<appName>.desktop` free for a launcher entry.
func FileName(appName string) string {
	return appName + "-handler.desktop"
}

func Dir() string {
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "applications")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".local", "share", "applications")
}

func Path(appName string) string {
	return filepath.Join(Dir(), FileName(appName))
}

func (e Entry) validate() error {
	if strings.TrimSpace(e.AppName) == "" {
		return fmt.Errorf("desktop entry needs an app name")
	}
	if strings.TrimSpace(e.Scheme) == "" {
		return fmt.Errorf("desktop entry for %s needs a URL scheme", e.AppName)
	}
	if !filepath.IsAbs(e.Exec) {
		return fmt.Errorf("desktop entry for %s needs an absolute executable path, got %q", e.AppName, e.Exec)
	}
	for _, field := range []string{e.AppName, e.Scheme, e.Exec} {
		if strings.ContainsAny(field, "\n\r\"") {
			return fmt.Errorf("desktop entry for %s cannot contain a newline or quote: %q", e.AppName, field)
		}
	}
	return nil
}

func (e Entry) Render() (string, error) {
	if err := e.validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Exec="%s" %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`, e.AppName, e.Exec, e.Scheme), nil
}

func Install(e Entry) (Report, error) {
	body, err := e.Render()
	if err != nil {
		return Report{}, err
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Report{}, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName(e.AppName))
	// A torn write is a desktop entry the database rejects, so swap it in whole.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return Report{}, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return Report{}, fmt.Errorf("install %s: %w", path, err)
	}

	report := Report{Path: path}
	mimeType := "x-scheme-handler/" + e.Scheme
	commands := [][]string{
		{"update-desktop-database", dir},
		{"xdg-mime", "default", FileName(e.AppName), mimeType},
	}
	for _, args := range commands {
		if _, lookErr := exec.LookPath(args[0]); lookErr != nil {
			report.MissingTools = append(report.MissingTools, args[0])
			continue
		}
		if out, runErr := exec.Command(args[0], args[1:]...).CombinedOutput(); runErr != nil {
			return report, fmt.Errorf("%s: %w: %s", args[0], runErr, strings.TrimSpace(string(out)))
		}
		report.Ran = append(report.Ran, args[0])
	}
	return report, nil
}

// The mimeapps.list default xdg-mime wrote stays: it names a file that no longer
// exists, so nothing resolves through it.
func Remove(appName string) (bool, error) {
	path := Path(appName)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	if _, lookErr := exec.LookPath("update-desktop-database"); lookErr == nil {
		if out, runErr := exec.Command("update-desktop-database", Dir()).CombinedOutput(); runErr != nil {
			return true, fmt.Errorf("update-desktop-database: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
	}
	return true, nil
}
