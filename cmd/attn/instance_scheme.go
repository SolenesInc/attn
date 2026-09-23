package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/desktopentry"
)

func desktopEntryPath(appName string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return desktopentry.Path(appName)
}

func runInstanceRegisterScheme(args []string) {
	instance := config.Instance()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--instance":
			if i+1 >= len(args) {
				instanceFatal("--instance requires a value")
			}
			i++
			p, err := config.NormalizeInstanceName(args[i])
			if err != nil {
				instanceFatal(err.Error())
			}
			instance = p
		case "-h", "--help":
			printInstanceHelp(os.Stdout)
			return
		default:
			instanceFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	if runtime.GOOS != "linux" {
		instanceFatal(fmt.Sprintf("register-scheme is Linux-only; on %s the installed app bundle already carries the scheme", runtime.GOOS))
	}

	r := resolveInstance(instance)
	report, err := desktopentry.Install(desktopentry.Entry{
		AppName: r.AppName,
		Exec:    r.AppExecutable,
		Scheme:  r.DeepLinkScheme,
	})
	if err != nil {
		instanceFatal(err.Error())
	}

	fmt.Printf(">>> Registered %s:// for %s\n", r.DeepLinkScheme, r.Label)
	fmt.Printf("  entry    %s\n", report.Path)
	fmt.Printf("  exec     %s\n", r.AppExecutable)
	if !fileExists(r.AppExecutable) {
		fmt.Printf("           ! nothing installed there yet; run make install%s\n", instanceSuffix(r.Instance))
	}
	if len(report.Ran) > 0 {
		fmt.Printf("  database %s\n", strings.Join(report.Ran, ", "))
	}
	if len(report.MissingTools) > 0 {
		fmt.Printf("  database ! %s missing (apt install desktop-file-utils xdg-utils); the entry is written, but %s:// links from other apps will not reach %s until they run\n",
			strings.Join(report.MissingTools, " and "), r.DeepLinkScheme, r.AppName)
	}
}

func instanceSuffix(instance string) string {
	if instance == "" {
		return ""
	}
	return " INSTANCE=" + instance
}
