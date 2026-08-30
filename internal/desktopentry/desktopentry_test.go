package desktopentry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	cases := []struct {
		name    string
		entry   Entry
		want    string
		wantErr string
	}{
		{
			name:  "default profile",
			entry: Entry{AppName: "attn", Exec: "/home/u/.local/share/attn/bin/attn-app", Scheme: "attn"},
			want: `[Desktop Entry]
Type=Application
Name=attn
Exec="/home/u/.local/share/attn/bin/attn-app" %u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/attn;
`,
		},
		{
			name:  "named profile carries its own scheme",
			entry: Entry{AppName: "attn-lx", Exec: "/home/u/.local/share/attn-lx/bin/attn-app", Scheme: "attn-lx"},
			want: `[Desktop Entry]
Type=Application
Name=attn-lx
Exec="/home/u/.local/share/attn-lx/bin/attn-app" %u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/attn-lx;
`,
		},
		{
			name:    "relative executable",
			entry:   Entry{AppName: "attn", Exec: "bin/attn-app", Scheme: "attn"},
			wantErr: "absolute executable path",
		},
		{
			name:    "missing scheme",
			entry:   Entry{AppName: "attn", Exec: "/opt/attn/bin/attn-app"},
			wantErr: "needs a URL scheme",
		},
		{
			name:    "newline in the executable path",
			entry:   Entry{AppName: "attn", Exec: "/opt/attn/bin/attn-app\nExec=/bin/sh", Scheme: "attn"},
			wantErr: "cannot contain a newline or quote",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.entry.Render()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Render() error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Render() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestDirFollowsXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data/home")
	if got, want := Dir(), filepath.Join("/data/home", "applications"); got != want {
		t.Errorf("Dir() = %s, want %s", got, want)
	}
	if got, want := Path("attn-lx"), filepath.Join("/data/home", "applications", "attn-lx-handler.desktop"); got != want {
		t.Errorf("Path() = %s, want %s", got, want)
	}
}

func TestDirDefaultsToLocalShare(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	if got, want := Dir(), filepath.Join(home, ".local", "share", "applications"); got != want {
		t.Errorf("Dir() = %s, want %s", got, want)
	}
}

func TestInstallAndRemove(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	// PATH without the desktop tools: the entry still lands, and says what is missing.
	t.Setenv("PATH", t.TempDir())

	entry := Entry{AppName: "attn-lx", Exec: "/opt/attn-lx/bin/attn-app", Scheme: "attn-lx"}
	report, err := Install(entry)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := filepath.Join(dataHome, "applications", "attn-lx-handler.desktop")
	if report.Path != want {
		t.Errorf("Install() path = %s, want %s", report.Path, want)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read installed entry: %v", err)
	}
	if !strings.Contains(string(body), "MimeType=x-scheme-handler/attn-lx;") {
		t.Errorf("installed entry does not register the scheme:\n%s", body)
	}
	if len(report.MissingTools) != 2 {
		t.Errorf("MissingTools = %v, want update-desktop-database and xdg-mime", report.MissingTools)
	}

	if _, err := os.Stat(want + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file survived the install: %v", err)
	}

	removed, err := Remove(entry.AppName)
	if err != nil || !removed {
		t.Fatalf("Remove() = %v, %v; want true, nil", removed, err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("entry survived Remove(): %v", err)
	}

	removed, err = Remove(entry.AppName)
	if err != nil || removed {
		t.Fatalf("second Remove() = %v, %v; want false, nil", removed, err)
	}
}

func TestInstallOverwritesAnEarlierEntry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	if _, err := Install(Entry{AppName: "attn-lx", Exec: "/old/bin/attn-app", Scheme: "attn-lx"}); err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	report, err := Install(Entry{AppName: "attn-lx", Exec: "/new/bin/attn-app", Scheme: "attn-lx"})
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	body, err := os.ReadFile(report.Path)
	if err != nil {
		t.Fatalf("read installed entry: %v", err)
	}
	if !strings.Contains(string(body), `Exec="/new/bin/attn-app" %u`) || strings.Contains(string(body), "/old/bin") {
		t.Errorf("reinstall did not replace the exec line:\n%s", body)
	}
}
