package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/desktopentry"
)

// macOS routes a deep link through the installed bundle's CFBundleURLTypes; on
// Linux nothing routes until a .desktop entry claims the scheme.
func desktopEntryPath(appName string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return desktopentry.Path(appName)
}

func runProfileRegisterScheme(args []string) {
	profile := config.Profile()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	if runtime.GOOS != "linux" {
		profileFatal(fmt.Sprintf("register-scheme is Linux-only; on %s the installed app bundle already carries the scheme", runtime.GOOS))
	}

	r := resolveProfile(profile)
	report, err := desktopentry.Install(desktopentry.Entry{
		AppName: r.AppName,
		Exec:    r.AppExecutable,
		Scheme:  r.DeepLinkScheme,
	})
	if err != nil {
		profileFatal(err.Error())
	}

	fmt.Printf(">>> Registered %s:// for %s\n", r.DeepLinkScheme, r.Label)
	fmt.Printf("  entry    %s\n", report.Path)
	fmt.Printf("  exec     %s\n", r.AppExecutable)
	if !fileExists(r.AppExecutable) {
		fmt.Printf("           ! nothing installed there yet; run make install%s\n", profileSuffix(r.Profile))
	}
	if len(report.Ran) > 0 {
		fmt.Printf("  database %s\n", strings.Join(report.Ran, ", "))
	}
	if len(report.MissingTools) > 0 {
		fmt.Printf("  database ! %s missing (apt install desktop-file-utils xdg-utils); the entry is written, but %s:// links from other apps will not reach %s until they run\n",
			strings.Join(report.MissingTools, " and "), r.DeepLinkScheme, r.AppName)
	}
}

func profileSuffix(profile string) string {
	if profile == "" {
		return ""
	}
	return " PROFILE=" + profile
}
