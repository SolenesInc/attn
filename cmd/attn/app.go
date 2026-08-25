package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

func runApp() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeAppHelp(os.Stdout)
		return
	}
	args := os.Args[3:]
	switch os.Args[2] {
	case "new":
		runAppNew(args)
	case "apply":
		runAppApply(args)
	case "rollback":
		runAppRollback(args)
	case "dev":
		runAppDev(args)
	case "list":
		runAppList(args)
	case "status":
		runAppStatus(args)
	case "enable":
		runAppSetEnabled(args, true)
	case "disable":
		runAppSetEnabled(args, false)
	case "remove":
		runAppRemove(args)
	case "logs":
		runAppLogs(args)
	case "runtime":
		runAppRuntime(args)
	default:
		fmt.Fprintf(os.Stderr, "app: unknown command %q\n", os.Args[2])
		writeAppHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeAppHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn app <command>

An app is an automation attn runs for you: it reacts to facts from the event
bus as the consumer app:<name>, and keeps its own documents under app/<name>.

commands:
  new <path> [--name <name>] [--description <text>]
        scaffold an app in <path>: manifest, entrypoint, generated types and an
        AGENTS.md (with a CLAUDE.md symlink) that is a complete brief on its own.
        The name defaults to the directory's. The result applies as-is — nothing
        has to be edited first. attn does not remember where the directory is.

  apply <path> [--json]
        build and install: parse the manifest, regenerate src/generated.ts, link
        the SDK into node_modules, typecheck, bundle, then record the version and
        point the app at it. It stops at the first failure with nothing installed,
        and it never runs your code — a module that throws at import still applies.

        A version is identified by the content it was built from, so applying
        byte-identical content again is the same version, not a new one.

  rollback <name> [version]
        point the app at a version it already has — the one you name, or with no
        version, one step back through the app's serving history: the version
        that was serving immediately before the current one. That is recorded
        history, not the next id down. If a broken version was rolled off before
        the current one was applied, the next id down is that broken version;
        what was serving is the one you kept running.

        Bare rollback again goes one step further back, and again, walking the
        history down until the oldest version on it; past that it refuses and
        lists the versions. Applying a version — or naming one here — starts the
        history again from where it lands, so the way back from a fix is
        whatever you were running when you applied it.

        Builds nothing: the artifact is still on disk.

  dev <path>
        apply on every change, and print every handler invocation as it runs.
        Shows apply results and build errors too, so one window is the whole
        edit-run-read loop.

  list [--json]
        every registered app: the version it runs, whether it is enabled, and
        how far behind the event log its consumer is.

  status <name> [--json]
        one app in full — its current version, its bus consumer, the ids of its
        recent versions (what rollback takes), where a bare rollback can still
        go, reconcile support and any rebuild it owes, and its most recent runs.
        Reports only what exists: an app with no consumer says so rather than
        showing a default.

  enable <name>
        resume delivery to the app, from wherever its consumer's cursor stands.

  disable <name>
        stop delivering facts to the app. Its cursor and unread backlog are
        preserved for as long as the app remains installed; enabling resumes
        delivery from that frozen cursor.

        This flips the app's bus consumer bit, which IS its enabled state. When
        the daemon is not running, attn bus disable app:<name> does the same
        thing straight against the database.

  remove <name>
        uninstall the app: stop and delete its bus consumer, delete its registry
        row. Version history, the invocation log and every document under
        app/<name> survive — deleting your data is a separate act, and this is
        not it.

  logs <name> [--lines N]
        what the app printed. Every app's handlers run in one shared process, so
        its output is tagged per app and this reads the tag back. The name
        "runtime" means the whole log, tags and all — that is where a runtime
        that will not start says why.

  runtime status [--json]
        the shared runtime every app's handlers run in: whether it is up, which
        binary it launches, and how many apps are installed and enabled.

  runtime restart
        kill the running runtime and start a fresh one. Also the way back from
        "parked", which is where a runtime that crash-looped ends up. There is
        one runtime for every app, so this takes no app name.
`)
}

func appClient() *client.Client { return client.New(config.SocketPath()) }

func appFail(verb string, err error) {
	fmt.Fprintf(os.Stderr, "app %s: %v\n", verb, err)
	os.Exit(1)
}

func appName(verb string, args []string) (string, []string) {
	rest := make([]string, 0, len(args))
	name := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			continue
		}
		if name != "" {
			appFail(verb, fmt.Errorf("takes one app name; got %q and %q", name, a))
		}
		name = a
	}
	if name == "" {
		fmt.Fprintf(os.Stderr, "usage: attn app %s <name>\n", verb)
		os.Exit(2)
	}
	if err := apps.ValidateName(name); err != nil {
		appFail(verb, err)
	}
	return name, rest
}

func runAppList(args []string) {
	asJSON := appOutputFlags("list", args)
	result, err := appClient().AppList()
	if err != nil {
		appFail("list", err)
	}
	if asJSON {
		writeJSON(result.Apps)
		return
	}
	if len(result.Apps) == 0 {
		fmt.Println("no apps installed")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tVERSION\tENABLED\tLAG\tCONSUMER")
	for _, app := range result.Apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			app.Name, appVersionCell(app), appEnabledCell(app), appLagCell(app), appConsumerCell(app))
	}
	w.Flush()
}

func appVersionCell(app protocol.AppSummary) string {
	if app.CurrentVersion == nil {
		return "none"
	}
	return fmt.Sprintf("%d", app.CurrentVersion.ID)
}

func appEnabledCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return "no consumer"
	}
	if app.Consumer.Enabled {
		return "yes"
	}
	return "no"
}

func appLagCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return "-"
	}
	return fmt.Sprintf("%d", app.Consumer.Lag)
}

func appConsumerCell(app protocol.AppSummary) string {
	if app.Consumer == nil {
		return apps.ConsumerName(app.Name) + " (not registered)"
	}
	return app.Consumer.Name
}

func runAppStatus(args []string) {
	name, rest := appName("status", args)
	asJSON := appOutputFlags("status", rest)
	result, err := appClient().AppStatus(name)
	if err != nil {
		appFail("status", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	app := result.App
	fmt.Printf("app %s\n", app.Name)
	if app.CurrentVersion != nil {
		fmt.Printf("  version:    %d (%s)\n", app.CurrentVersion.ID, app.CurrentVersion.ContentHash)
		fmt.Printf("  artifact:   %s\n", app.CurrentVersion.ArtifactPath)
	} else {
		fmt.Printf("  version:    none applied yet\n")
	}
	if app.Consumer != nil {
		state := "disabled"
		if app.Consumer.Enabled {
			state = "enabled"
		}
		filter := app.Consumer.Filter
		switch filter {
		case "":
			filter = "everything"
		case apps.NoSubscriptionsPattern:
			filter = "nothing — it declares no subscriptions"
		}
		fmt.Printf("  consumer:   %s — %s, cursor %d, %d event(s) behind\n",
			app.Consumer.Name, state, app.Consumer.Cursor, app.Consumer.Lag)
		fmt.Printf("  subscribes: %s\n", filter)
	} else {
		fmt.Printf("  consumer:   none — %s is not registered, so no facts are delivered to this app\n",
			apps.ConsumerName(app.Name))
	}
	fmt.Printf("  documents:  %s\n", apps.Namespace(app.Name))
	if len(app.Views) > 0 {
		fmt.Println("  views:")
		for _, v := range app.Views {
			line := fmt.Sprintf("              %s (%s) — %s", v.Name, v.Kind, v.Title)
			if v.ParamsLabel != nil {
				line += fmt.Sprintf("; asks for %q when docking", *v.ParamsLabel)
			}
			fmt.Println(line)
			fmt.Printf("              dock as %s\n", apps.ViewTileKind(app.Name, v.Name))
		}
	}
	if len(app.Commands) > 0 {
		fmt.Println("  commands:")
		for _, c := range app.Commands {
			line := fmt.Sprintf("              %s", c.Name)
			if c.Description != nil && *c.Description != "" {
				line += " — " + *c.Description
			}
			fmt.Println(line)
		}
	}
	fmt.Printf("  runtime:    %s\n", appRuntimeCell(result.Runtime))
	printAppReconcileStatus(result.Reconcile)
	if result.Stall != nil {
		if result.Stall.Kind == "reconcile" {
			fmt.Printf("  stalled:    reconcile request %s since %s, %d attempt(s)\n",
				optionalIntCell(result.Stall.ThroughRequestID), result.Stall.Since, result.Stall.Attempts)
		} else {
			fmt.Printf("  stalled:    on event %s (%s) since %s, %d attempt(s)\n",
				optionalIntCell(result.Stall.EventSeq), protocol.Deref(result.Stall.EventName), result.Stall.Since, result.Stall.Attempts)
		}
		fmt.Print(indentBlock("              ", result.Stall.LastError))
		fmt.Printf("              disables itself at %s unless it succeeds first\n", result.Stall.DisablesAt)
	}
	fmt.Printf("  history:    %d version(s), %d invocation(s)\n", result.Versions, result.Invocations)
	if len(result.RecentVersions) > 0 {
		fmt.Printf("  versions:   %s\n", versionIDList(result.RecentVersions, app.CurrentVersion))
		if result.Versions > len(result.RecentVersions) {
			fmt.Printf("              newest %d of %d; older ones are named by a rollback refusal\n",
				len(result.RecentVersions), result.Versions)
		}
	}
	fmt.Printf("  rollback:   %s\n", rollbackPath(result))
	if len(result.Recent) == 0 {
		fmt.Println("  recent:     no invocations recorded")
		return
	}
	fmt.Println("  recent:")
	w := tabwriter.NewWriter(os.Stdout, 4, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tSTARTED\tVERSION\tKIND\tWORK\tHANDLER\tSTATUS\tMS\tERROR")
	for _, inv := range result.Recent {
		fmt.Fprintf(w, "\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			inv.StartedAt, inv.VersionID, inv.Kind, appInvocationWork(inv), inv.Handler,
			inv.Status, optionalIntCell(inv.DurationMs), firstErrorLine(protocol.Deref(inv.Error)))
	}
	w.Flush()
}

func printAppReconcileStatus(status protocol.AppReconcileStatus) {
	fmt.Printf("  reconcile:  %s\n", appReconcileStatusCell(status))
	if status.State == "running" && status.CurrentAttempt != nil {
		fmt.Printf("              attempt %d started %s (request %s)\n",
			status.CurrentAttempt.ID, status.CurrentAttempt.StartedAt,
			optionalIntCell(status.CurrentAttempt.ThroughRequestID))
	}
	if status.LastError != nil && *status.LastError != "" {
		fmt.Println("              last error:")
		fmt.Print(indentBlock("                ", *status.LastError))
	}
}

func appReconcileStatusCell(status protocol.AppReconcileStatus) string {
	switch status.State {
	case "not_needed":
		return "not needed — the serving version declares no subscriptions"
	case "unsupported":
		return "unsupported — version moves and a discovered gap are refused until the app declares and implements reconcile"
	case "idle":
		return "supported, no rebuild owed"
	case "owed", "running":
		line := status.State
		if status.Reason != nil {
			line += " " + appReconcileReasonCell(*status.Reason)
		}
		return line
	default:
		return status.State
	}
}

func appReconcileReasonCell(reason protocol.AppReconcileReasonInfo) string {
	causes := strings.Join(reason.Causes, ", ")
	if causes == "" {
		causes = "unknown cause"
	}
	return fmt.Sprintf("through seq %d (%s)", reason.ThroughSeq, causes)
}

func appInvocationWork(inv protocol.AppInvocationInfo) string {
	if inv.Reconcile != nil {
		return appReconcileReasonCell(*inv.Reconcile)
	}
	event := protocol.Deref(inv.EventName)
	subject := protocol.Deref(inv.EventSubject)
	seq := ""
	if inv.EventSeq != nil {
		seq = fmt.Sprintf("seq %d", *inv.EventSeq)
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{seq, event, subject} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func optionalIntCell(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func firstErrorLine(text string) string {
	line, rest, found := strings.Cut(strings.TrimRight(text, "\n"), "\n")
	if found && strings.TrimSpace(rest) != "" {
		return line + " …"
	}
	return line
}

func indentBlock(prefix, text string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func rollbackPath(result *protocol.AppStatusResult) string {
	if len(result.ServingHistory) < 2 {
		return "nothing further back — name a version to move onto one the walk went past"
	}
	path := result.ServingHistory[1:]
	parts := make([]string, 0, len(path))
	for _, v := range path {
		parts = append(parts, fmt.Sprintf("%d (%s)", v.ID, appbuild.ShortHash(v.ContentHash)))
	}
	line := "walks back to " + strings.Join(parts, ", then ")
	if steps := protocol.Deref(result.ServingHistorySteps); steps > len(result.ServingHistory) {
		line += fmt.Sprintf(", then %d older step(s)", steps-len(result.ServingHistory))
	}
	return line
}

func versionIDList(versions []protocol.AppVersionInfo, current *protocol.AppVersionInfo) string {
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		label := fmt.Sprintf("%d (%s)", v.ID, appbuild.ShortHash(v.ContentHash))
		if current != nil && current.ID == v.ID {
			label += " ← serving"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

const appRuntimeNeverStarted = "not started — nothing has run one on this daemon yet; `attn app runtime status` says whether it can be started"

func appRuntimeCell(info *protocol.AppRuntimeInfo) string {
	if info == nil {
		return appRuntimeNeverStarted
	}
	switch {
	case info.Phase == "parked":
		return "PARKED — it crash-looped and attn stopped restarting it, so no app's handlers run. `attn app runtime restart`"
	case info.Connected:
		return fmt.Sprintf("running (%s), generation %d", info.Phase, info.Generation)
	default:
		return fmt.Sprintf("%s — not connected yet", info.Phase)
	}
}

func runAppSetEnabled(args []string, enabled bool) {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	name, rest := appName(verb, args)
	appOutputFlags(verb, rest)
	result, err := appClient().AppSetEnabled(name, enabled)
	if err != nil {
		appFail(verb, err)
	}
	if enabled {
		fmt.Printf("app %s enabled: %s resumes from its cursor\n", result.Name, result.Consumer)
		return
	}
	fmt.Printf("app %s disabled: %s stops receiving facts; its unread backlog stays retained until enable or uninstall\n",
		result.Name, result.Consumer)
}

func runAppRemove(args []string) {
	name, rest := appName("remove", args)
	appOutputFlags("remove", rest)
	result, err := appClient().AppRemove(name)
	if err != nil {
		appFail("remove", err)
	}
	consumer := "it had no bus consumer"
	if result.ConsumerRemoved {
		consumer = "stopped and deleted its bus consumer " + apps.ConsumerName(result.Name)
	}
	fmt.Printf("removed app %s: %s\n", result.Name, consumer)
	fmt.Printf("kept: %d version(s), %d invocation(s), and every document under %s\n",
		result.VersionsKept, result.InvocationsKept, result.NamespaceKept)
}

func appOutputFlags(verb string, args []string) bool {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			if verb != "list" && verb != "status" {
				appFail(verb, fmt.Errorf("--json is only for list and status; %s reports what it did", verb))
			}
			asJSON = true
		case "-h", "--help":
			writeAppHelp(os.Stdout)
			os.Exit(0)
		default:
			appFail(verb, fmt.Errorf("unknown flag %q", a))
		}
	}
	return asJSON
}
