package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func crewClient(profile string) *client.Client {
	return client.New("").WithRequester(profile, currentSessionOrExit())
}
func runCrew() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeCrewHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "list", "ls":
		runCrewList(os.Args[3:])
	case "wake":
		runCrewWake(os.Args[3:])
	case "sleep":
		runCrewSleep(os.Args[3:])
	case "restart":
		runCrewRestart(os.Args[3:])
	case "create":
		runCrewCreate(os.Args[3:])
	case "retire", "restore":
		runCrewRetirement(os.Args[2], os.Args[3:])
	case "prime":
		runCrewPrime(os.Args[3:])
	case "rename":
		runCrewRename(os.Args[3:])
	case "set":
		runCrewSet(os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "crew: unknown command %q\n", os.Args[2])
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeCrewHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn crew <command>

Manage the Crew. Members' charters and handoffs persist across sessions
in the active instance's crew directory. Every member belongs to a profile
and wakes only there. Names work within your profile, ignoring case.
Outside attn, use --profile <name|id> when several profiles exist.
Use member:<key> to address a member by permanent key.
Run crew commands on the home daemon; outposts report which home to use.

commands:
  create <name> [--agent <name>] [--model <name>] [--effort <level>]
                [--cwd <dir>] [--launch-desktop <own|desktop>]
                [--desktop-name <name>] [--profile <name|id>] [--json]
        Create a member with their own home and launch desktop. Wake them
        with attn crew wake <name> to begin their first day.

  retire <member> [--profile <name|id>] [--json]
        Take a member out of service, release claims and remove watches.
        Keep their name, home and unread mail; ask an awake member to sleep.

  restore <member> [--profile <name|id>] [--json]
        Return a retired member to service. Unread mail can wake them.

  prime
        Print your member identity, charter, letters and claimed seeds.

  list [--all] [--json]
        Show members in service and their sessions. --all includes retired members.

  wake <member> [--agent <name>] [--json]
        Start a session using the member's saved launch settings, on its chosen
        desktop in the member's profile without moving your focus. Asked from an agent of another profile, it
        refuses.
        Include its charter location, latest handoff, home instructions,
        held seeds with handoff notes, and ready counts for their plots.
        --agent overrides the harness for this session.
        If already awake, return the existing session.

  rename <member> <name> [--json]
        Rename a member. Their key, home and mail stay the same.

  sleep <member> [--json]
        Ask the member to write a handoff and close with attn handoff --sleep.
        The member closes its own session. Do nothing if already asleep.

  restart <member> [--request-id <id>] [--json]
        ask an awake member to finish its work, write its own handoff and start
        a fresh day. An asleep member wakes directly. The durable result says
        queued, requested, failed or completed; delivery alone is not completion.
        Reuse --request-id when retrying a request whose result was not received.

  set <member> [--cwd <dir>] [--agent <name>] [--model <name>] [--effort <level>]
               [--launch-desktop <own|desktop>] [--awareness-dir <dir>]...
        Save launch settings without changing the member's markdown files.
        --launch-desktop selects own, an empty slot (5–9), or a desktop digit, name or id.
        --cwd sets the working directory; --model selects the model.
        --agent accepts claude, codex, or an installed plugin driver.
        --agent "" restores the crew default; --model "" the harness default.
        --effort selects effort; the harness reports its levels in Settings.
        --effort "" restores the harness default.
        --awareness-dir sets context dirs. Repeat to replace the saved list.
        Use --awareness-dir "" to clear it.
`)
}

type crewListArgs struct {
	all     bool
	profile string
	json    bool
}

func parseCrewListArgs(args []string) (crewListArgs, error) {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	all := fs.Bool("all", false, "include retired members")
	if err := fs.Parse(args); err != nil {
		return crewListArgs{}, err
	}
	if fs.NArg() != 0 {
		return crewListArgs{}, errors.New("crew list takes no arguments")
	}
	return crewListArgs{profile: *profile, json: *jsonOut, all: *all}, nil
}

func runCrewList(args []string) {
	parsed, err := parseCrewListArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew list: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := crewClient(parsed.profile).CrewList(parsed.all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew list: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result.Members)
		return
	}
	printCrewList(os.Stdout, result.Members)
}

type crewWakeArgs struct {
	profile string
	member  string
	agent   string
	json    bool
}

func parseCrewWakeArgs(args []string) (crewWakeArgs, error) {
	fs := flag.NewFlagSet("crew wake", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	agent := fs.String("agent", "", "the harness to launch (default claude)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew wake")
	if err != nil {
		return crewWakeArgs{}, err
	}
	return crewWakeArgs{profile: *profile, member: member, agent: strings.TrimSpace(*agent), json: *jsonOut}, nil
}

func parseMemberAndFlags(fs *flag.FlagSet, args []string, verb string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", errors.New(verb + " takes one member name")
	}
	member, rest := fs.Arg(0), fs.Args()[1:]
	if len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return "", err
		}
		if fs.NArg() != 0 {
			return "", fmt.Errorf("%s takes one member name, not %q", verb, strings.Join(fs.Args(), " "))
		}
	}
	return member, nil
}

func runCrewWake(args []string) {
	parsed, err := parseCrewWakeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew wake: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := crewClient(parsed.profile).CrewWake(parsed.member, parsed.agent, currentSessionOrExit())
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew wake: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if result.AlreadyAwake {
		fmt.Printf("%s is already awake in session %s.\n", result.Name, agentShortID(string(result.SessionID)))
		return
	}
	if repair := crewWakeRepairLine(result); repair != "" {
		fmt.Fprintln(os.Stdout, repair)
	}
	fmt.Printf("%s is awake in session %s. View it with `attn agent peek %s`. For priming size, grep `crew: priming` in the daemon log.\n",
		result.Name, agentShortID(string(result.SessionID)), result.Name)
}

func crewWakeRepairLine(result *protocol.CrewWakeResult) string {
	released := protocol.TrimID(protocol.Deref(result.ReleasedSessionID))
	if released == "" {
		return ""
	}
	return fmt.Sprintf("Previous session %s had exited; its binding was released.", agentShortID(string(released)))
}

type crewSleepArgs struct {
	profile string
	member  string
	json    bool
}

func parseCrewSleepArgs(args []string) (crewSleepArgs, error) {
	fs := flag.NewFlagSet("crew sleep", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew sleep")
	if err != nil {
		return crewSleepArgs{}, err
	}
	return crewSleepArgs{profile: *profile, member: member, json: *jsonOut}, nil
}

func runCrewSleep(args []string) {
	parsed, err := parseCrewSleepArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew sleep: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := crewClient(parsed.profile).CrewSleep(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew sleep: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fmt.Fprintln(os.Stdout, crewSleepOutcomeLine(result))
}

func crewSleepOutcomeLine(result *protocol.CrewSleepResult) string {
	name := result.Name
	if result.AlreadyAsleep {
		if detail := strings.TrimSpace(result.Detail); detail != "" {
			return detail + "."
		}
		return fmt.Sprintf("%s is already asleep — no sleep request was sent.", name)
	}
	if result.DeliveryStatus != nil && *result.DeliveryStatus == protocol.AgentMsgStatusQueued {
		detail := strings.TrimSpace(result.Detail)
		if detail == "" {
			detail = "the member is not taking input right now"
		}
		return fmt.Sprintf("Sleep request for %s is queued in session %s — %s", name, agentShortID(string(protocol.Deref(result.SessionID))), detail)
	}
	return fmt.Sprintf("Asked %s in session %s to write its handoff and file it with `attn handoff --sleep`.", name, agentShortID(string(protocol.Deref(result.SessionID))))
}

type crewRestartArgs struct {
	profile   string
	member    string
	requestID string
	json      bool
}

func parseCrewRestartArgs(args []string) (crewRestartArgs, error) {
	fs := flag.NewFlagSet("crew restart", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	requestID := fs.String("request-id", "", "stable idempotency key")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew restart")
	if err != nil {
		return crewRestartArgs{}, err
	}
	requestIDSet := false
	fs.Visit(func(f *flag.Flag) { requestIDSet = requestIDSet || f.Name == "request-id" })
	stableRequestID := strings.TrimSpace(*requestID)
	if requestIDSet && stableRequestID == "" {
		return crewRestartArgs{}, errors.New("--request-id cannot be empty")
	}
	if stableRequestID == "" {
		stableRequestID = uuid.NewString()
	}
	return crewRestartArgs{profile: *profile, member: member, requestID: stableRequestID, json: *jsonOut}, nil
}

func writeCrewRestartReceipt(w io.Writer, parsed crewRestartArgs) error {
	if parsed.json {
		return json.NewEncoder(w).Encode(struct {
			RequestID string `json:"request_id"`
		}{RequestID: parsed.requestID})
	}
	_, err := fmt.Fprintf(w, "crew restart request: request_id=%s\n", parsed.requestID)
	return err
}

func runCrewRestart(args []string) {
	parsed, err := parseCrewRestartArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew restart: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	if err := writeCrewRestartReceipt(os.Stderr, parsed); err != nil {
		fmt.Fprintf(os.Stderr, "crew restart: write request receipt: %v\n", err)
		os.Exit(1)
	}
	result, err := crewClient(parsed.profile).CrewRestart(parsed.member, parsed.requestID)
	if err != nil {
		if parsed.json {
			_ = json.NewEncoder(os.Stderr).Encode(struct {
				RequestID string `json:"request_id"`
				Error     string `json:"error"`
			}{RequestID: parsed.requestID, Error: err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "crew restart: result not confirmed; inspect the roster or retry with --request-id %s: %v\n", parsed.requestID, err)
		}
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	line := fmt.Sprintf("Restart for %s is %s", result.Member.Name, result.Restart.State)
	if detail := strings.TrimSpace(protocol.Deref(result.Restart.Detail)); detail != "" {
		line += ": " + detail
	}
	fmt.Fprintln(os.Stdout, line+".")
}

type crewDirList struct {
	values []string
	set    bool
}

func (l *crewDirList) String() string { return strings.Join(l.values, ",") }

func (l *crewDirList) Set(value string) error {
	l.set = true
	if strings.TrimSpace(value) != "" {
		l.values = append(l.values, value)
	}
	return nil
}

type crewSetArgs struct {
	profile     string
	member      string
	cwd         *string
	agent       *string
	model       *string
	effort      *string
	desktop     *string
	desktopName *string
	awareness   []string
	json        bool
}

func parseCrewSetArgs(args []string) (crewSetArgs, error) {
	fs := flag.NewFlagSet("crew set", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	cwd := fs.String("cwd", "", "where the member's sessions launch")
	agent := fs.String("agent", "", "the harness the member's days run on; empty goes back to the default")
	model := fs.String("model", "", "the model the member's days run on; empty goes back to the configured default")
	effort := fs.String("effort", "", "the effort the member's days run on; empty goes back to the harness default")
	desktopName := fs.String("desktop-name", "", "name for the new desktop of own or an empty slot (defaults to the member name)")
	desktop := fs.String("launch-desktop", "", "own, an empty slot (5–9), or a desktop digit, name or id in this member profile")
	var dirs crewDirList
	fs.Var(&dirs, "awareness-dir", "a directory the member's charter is about; repeat for several")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	member, err := parseMemberAndFlags(fs, args, "crew set")
	if err != nil {
		return crewSetArgs{}, err
	}
	parsed := crewSetArgs{profile: *profile, member: member, json: *jsonOut}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "cwd":
			parsed.cwd = cwd
		case "agent":
			parsed.agent = agent
		case "model":
			parsed.model = model
		case "effort":
			parsed.effort = effort
		case "launch-desktop":
			parsed.desktop = desktop
		case "desktop-name":
			parsed.desktopName = desktopName
		}
	})
	if parsed.desktopName != nil && parsed.desktop == nil {
		return crewSetArgs{}, errors.New("--desktop-name needs --launch-desktop own or an empty slot")
	}
	if dirs.set {
		parsed.awareness = dirs.values
		if parsed.awareness == nil {
			parsed.awareness = []string{}
		}
	}
	if parsed.cwd == nil && parsed.agent == nil && parsed.model == nil && parsed.effort == nil && parsed.desktop == nil && !dirs.set {
		return crewSetArgs{}, errors.New("nothing to set — pass --cwd, --agent, --model, --effort, --launch-desktop, --awareness-dir, or any of them together")
	}
	return parsed, nil
}

func runCrewSet(args []string) {
	parsed, err := parseCrewSetArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew set: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := crewClient(parsed.profile).CrewSetWithNamedDesktop(parsed.member, parsed.cwd, parsed.agent, parsed.model, parsed.effort, parsed.awareness, parsed.desktop, parsed.desktopName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew set: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result.Member)
		return
	}
	if result.WokeSessionID != nil {
		fmt.Printf("%s woke in session %s\n", result.Member.Name, string(*result.WokeSessionID)[:8])
	}
	if result.WakeError != nil {
		fmt.Fprintf(os.Stderr, "settings saved; Chief did not start: %s\n", *result.WakeError)
	}
	record := result.Member
	fmt.Printf("%s launches in %s on %s, model %s, effort %s\n", record.Name, valueOrDash(protocol.Deref(record.Cwd)), valueOrDash(protocol.Deref(record.ResolvedAgent)), valueOrDash(protocol.Deref(record.ResolvedModel)), valueOrDash(protocol.Deref(record.ResolvedEffort)))
	fmt.Printf("profile: %s\nlaunch desktop: %s\n", protocol.Deref(record.ProfileName), launchDesktopText(record.LaunchDesktop))
	fmt.Printf("awareness dirs: %s\n", valueOrDash(strings.Join(record.AwarenessDirs, ", ")))
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func printCrewList(w io.Writer, members []protocol.CrewMember) {
	if len(members) == 0 {
		fmt.Fprintln(w, "No crew members are registered. `attn crew create <name>` creates one. A <name>/CHARTER.md home in the active instance's crew directory joins the roster at the daemon's next start.")
		return
	}
	fmt.Fprintf(w, "%-12s  %-8s  %-8s  %-20s  %-8s  %-10s  %-22s  %s\n", "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "LAUNCH DESKTOP", "HOME")
	for _, member := range members {
		state, session := "asleep", "-"
		if id := protocol.TrimID(protocol.Deref(member.BindingSession)); id != "" {
			state, session = "awake", agentShortID(string(id))
		}
		if member.Retired {
			state = "retired"
		}
		fmt.Fprintf(w, "%-12s  %-8s  %-8s  %-20s  %-8s  %-10s  %-22s  %s\n", member.Name, state, valueOrDash(protocol.Deref(member.ResolvedAgent)), valueOrDash(protocol.Deref(member.ResolvedModel)), valueOrDash(protocol.Deref(member.ResolvedEffort)), session, protocol.Deref(member.ProfileName)+" › "+launchDesktopText(member.LaunchDesktop), member.HomeDir)
	}
	fmt.Fprintf(w, "\nAn awake MEMBER or SESSION works with `attn agent peek <target>`.\n")
}

func launchDesktopText(setting *protocol.LaunchDesktopSetting) string {
	if setting == nil {
		return "-"
	}
	return protocol.Deref(setting.Label)
}

func runCrewRename(args []string) {
	fs := flag.NewFlagSet("crew rename", flag.ContinueOnError)
	profile := fs.String("profile", "", "resolve names in this profile")
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	names, err := parseInterspersedFlagArgs(fs, args)
	if err != nil || len(names) != 2 {
		fmt.Fprintln(os.Stderr, "usage: attn crew rename <member> <name> [--profile <name|id>] [--json]")
		os.Exit(2)
	}
	result, err := crewClient(*profile).CrewRename(names[0], names[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew rename: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(result)
		return
	}
	fmt.Printf("%s is now %s (member:%s)\n", result.PreviousName, result.Name, result.Member)
}

func runCrewCreate(args []string) {
	fs := flag.NewFlagSet("crew create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "create in this profile")
	agent := fs.String("agent", "", "the harness to launch")
	model := fs.String("model", "", "the model to launch")
	effort := fs.String("effort", "", "the model effort")
	cwd := fs.String("cwd", "", "working directory")
	desktop := fs.String("launch-desktop", "", "own or a desktop in this profile")
	desktopName := fs.String("desktop-name", "", "name for a new desktop")
	jsonOut := fs.Bool("json", false, "print JSON")
	name, err := parseMemberAndFlags(fs, args, "crew create")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew create: %v\n", err)
		os.Exit(2)
	}
	msg := protocol.CrewCreateMessage{Name: name, Agent: agent, Model: model, Effort: effort, Cwd: cwd}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "launch-desktop" {
			msg.LaunchDesktop = desktop
		}
		if f.Name == "desktop-name" {
			msg.LaunchDesktopName = desktopName
		}
	})
	if msg.LaunchDesktopName != nil && msg.LaunchDesktop == nil {
		fmt.Fprintln(os.Stderr, "crew create: --desktop-name needs --launch-desktop own or an empty slot")
		os.Exit(2)
	}
	result, err := crewClient(*profile).CrewCreate(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew create: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(result)
		return
	}
	m := result.Member
	fmt.Printf("Created %s in %s (member:%s).\nHome: %s\nStart their first day: attn crew wake %s\n", m.Name, protocol.Deref(m.ProfileName), m.Key, m.HomeDir, m.Name)
}

func runCrewRetirement(verb string, args []string) {
	fs := flag.NewFlagSet("crew "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "resolve in this profile")
	jsonOut := fs.Bool("json", false, "print JSON")
	member, err := parseMemberAndFlags(fs, args, "crew "+verb)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew %s: %v\n", verb, err)
		os.Exit(2)
	}
	cli := crewClient(*profile)
	if verb == "restore" {
		result, err := cli.CrewRestore(member)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crew restore: %v\n", err)
			os.Exit(1)
		}
		if *jsonOut {
			printJSON(result)
			return
		}
		if result.AlreadyActive {
			fmt.Printf("%s is already in service.\n", result.Member.Name)
		} else {
			fmt.Printf("%s is back in service; kept unread mail can wake them.\n", result.Member.Name)
		}
		return
	}
	result, err := cli.CrewRetire(member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew retire: %v\n", err)
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(result)
		return
	}
	if result.AlreadyRetired {
		fmt.Printf("%s was already retired.\n", result.Member.Name)
	} else {
		fmt.Printf("%s is retired.\n", result.Member.Name)
	}
	fmt.Printf("Released seeds: %s\nRemoved watches: %d\nUnread mail kept: %d\n", valueOrDash(strings.Join(result.ReleasedSeeds, ", ")), result.RemovedWatches, result.Unread)
	if result.Sleep != nil {
		fmt.Println(crewSleepOutcomeLine(result.Sleep))
	}
}

func runCrewPrime(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "crew prime: takes no arguments")
		os.Exit(2)
	}
	result, err := client.New("").CrewPrime(currentSessionOrExit())
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew prime: %v\n", err)
		os.Exit(1)
	}
	if result.Member == nil {
		fmt.Fprintln(os.Stderr, "this session is not a crew member's; nothing to prime")
		os.Exit(1)
	}
	fmt.Println(protocol.Deref(result.Guidance))
}
