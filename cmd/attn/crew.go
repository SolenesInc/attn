package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

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
	case "apply":
		runCrewApply(os.Args[3:])
	case "set":
		runCrewSet(os.Args[3:])
	case "rename":
		runCrewRename(os.Args[3:])
	case "create":
		runCrewCreate(os.Args[3:])
	case "retire":
		runCrewRetire(os.Args[3:])
	case "restore":
		runCrewRestore(os.Args[3:])
	case "move-home":
		runCrewMoveHome(os.Args[3:])
	case "prime":
		runCrewPrime(os.Args[3:])
	default:
		recordUsageError("crew", usageErrorUnknownCommand)
		fmt.Fprintf(os.Stderr, "crew: unknown command %q\n", os.Args[2])
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeCrewHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn crew <command>

Manage the Crew. Each member has a home folder with its charter and letters,
which persist across sessions: in attn's data folder, or in its profile's
Notebook at crew/<name>/. Every member belongs to a profile and wakes only
there.

A <member> is a name in your profile, or member:<key> for the permanent key
that never changes. With crew_v2_enabled on, every profile has a Chief
member, and chief reaches it whatever its name; it sleeps but is never retired.
Inside attn, your session's profile applies; outside, pass --profile <name|id>
when several profiles exist.

commands:
  list [--all] [--json]
        Show your profile's members and their active sessions, if any.
        --all includes retired members.

  create <name> [--json]
        Add a member to your profile. Its home is crew/<name>/ in the profile's
        Notebook (crew/<name>-2/ when that folder is taken) and starts with an
        empty CHARTER.md, which the member writes with you in its first session.

  wake <member> [--agent <name>] [--json]
        Start a session using the member's saved launch settings, on its chosen
        desktop in the member's profile without moving your focus. Asked from an agent of another profile, it
        refuses.
        Include its charter location, latest handoff, home instructions,
        held seeds with handoff notes, and ready counts for their plots.
        --agent overrides the harness for this session.
        If already awake, return the existing session.

  sleep <member> [--json]
        Ask the member to write a handoff and close with attn handoff.
        The member closes its own session. Do nothing if already asleep.

  prime
        Print the orientation your crew session started with, read fresh:
        identity, charter, latest letter and held seeds. Run it when your
        conversation starts empty, as after /clear or /new.

  apply <member> [--json]
        Apply the member's saved launch settings and charter to its running
        session. On the same harness the session reloads in place and keeps
        its conversation. On a different harness the member is asked to file
        its letter with attn handoff, which ends the session and starts the
        next one on the saved harness. The member itself, an agent above it in
        the delegation chain, and the profile's chief may apply. Does nothing
        if the member is asleep; its next wake uses the saved settings.

  set <member> [--cwd <dir>] [--agent <name>] [--model <name>] [--effort <level>]
               [--launch-desktop <own|desktop>] [--awareness-dir <dir>]...
        Save launch settings without changing the member's markdown files.
        --launch-desktop selects own, a desktop digit (an empty slot gets a new desktop), name or id.
        --cwd sets the working directory; --model selects the model.
        --agent accepts claude, codex, or an installed plugin driver.
        --agent "" restores the crew default; --model "" the harness default.
        --effort selects effort; the harness reports its levels in Settings.
        --effort "" restores the harness default.
        --awareness-dir sets context dirs. Repeat to replace the saved list.
        Use --awareness-dir "" to clear it.

  rename <member> <name> [--json]
        Give the member a new name, unique in its profile. Its key, letters,
        mail and seeds stay. A home in the Notebook moves to a folder named
        after the new name, so the member must be asleep. Needs crew_v2_enabled.

  move-home <member> [--json]
        Move the member's home from attn's data folder into its profile's
        Notebook, at crew/<name>/ (crew/<name>-2/ when that folder is taken).
        Its charter, letters and other files move; a working or awareness
        directory inside the home follows. The member must be asleep. Later
        changes to the Notebook folder leave the home where it is.

  retire <member> [--json]
        Stop the member's work: its claimed seeds are replanted with a note,
        its seed and pull request watches and open asks are removed, and new
        mail is refused. Unread mail, its home and its name stay. An awake
        member is asked to sleep.

  restore <member> [--json]
        Bring a retired member back. Mail kept while retired is delivered,
        which can wake it.
`)
}

func runCrewPrime(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "crew prime: takes no arguments")
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := client.New("").CrewPrime(currentSessionOrExit())
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew prime: %v\n", err)
		os.Exit(1)
	}
	if result.Member == nil {
		fmt.Fprintln(os.Stderr, "crew prime: this session is not a crew member's; nothing to prime")
		os.Exit(1)
	}
	fmt.Println(protocol.Deref(result.Guidance))
}

type crewListArgs struct {
	profile string
	json    bool
	all     bool
}

func parseCrewListArgs(args []string) (crewListArgs, error) {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	all := fs.Bool("all", false, "include retired members")
	profile := crewProfileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
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
	list := crewClient(parsed.profile).CrewList
	if parsed.all {
		list = crewClient(parsed.profile).CrewListAll
	}
	result, err := list()
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
	member  string
	agent   string
	profile string
	json    bool
}

func parseCrewWakeArgs(args []string) (crewWakeArgs, error) {
	fs := flag.NewFlagSet("crew wake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	agent := fs.String("agent", "", "the harness to launch (default claude)")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	profile := crewProfileFlag(fs)
	member, err := parseMemberAndFlags(fs, args, "crew wake")
	if err != nil {
		return crewWakeArgs{}, err
	}
	return crewWakeArgs{member: member, agent: strings.TrimSpace(*agent), profile: *profile, json: *jsonOut}, nil
}

func parseMemberAndFlags(fs *flag.FlagSet, args []string, verb string) (string, error) {
	if err := parseFlags(fs, args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", errors.New(verb + " takes one member name")
	}
	member, rest := fs.Arg(0), fs.Args()[1:]
	if len(rest) > 0 {
		if err := parseFlags(fs, rest); err != nil {
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
	result, err := crewClient(parsed.profile).CrewWake(parsed.member, parsed.agent, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew wake: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if result.AlreadyAwake {
		fmt.Printf("%s is already awake in session %s — nothing was launched.\n", result.Name, agentShortID(string(result.SessionID)))
		return
	}
	if repair := crewWakeRepairLine(result); repair != "" {
		fmt.Fprintln(os.Stdout, repair)
	}
	fmt.Printf("%s is awake in session %s. `attn agent peek %s` watches the day; the priming size is in the daemon log (grep `crew: priming`).\n",
		result.Name, agentShortID(string(result.SessionID)), result.Name)
}

func crewWakeRepairLine(result *protocol.CrewWakeResult) string {
	released := protocol.TrimID(protocol.Deref(result.ReleasedSessionID))
	if released == "" {
		return ""
	}
	return fmt.Sprintf("Previous session %s had exited; its binding was released.", agentShortID(string(released)))
}

func runCrewSleep(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew sleep", args)
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
	return fmt.Sprintf("Asked %s in session %s to write its handoff and file it with `attn handoff`.", name, agentShortID(string(protocol.Deref(result.SessionID))))
}

func runCrewApply(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew apply", args)
	result, err := crewClient(parsed.profile).CrewApply(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew apply: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fmt.Fprintln(os.Stdout, crewApplyOutcomeLine(result))
}

func crewApplyOutcomeLine(result *protocol.CrewApplyResult) string {
	return result.Detail + "."
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
	member      string
	cwd         *string
	agent       *string
	model       *string
	effort      *string
	desktop     *string
	desktopName *string
	awareness   []string
	profile     string
	json        bool
}

func parseCrewSetArgs(args []string) (crewSetArgs, error) {
	fs := flag.NewFlagSet("crew set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cwd := fs.String("cwd", "", "where the member's sessions launch")
	agent := fs.String("agent", "", "the harness the member's days run on; empty goes back to the default")
	model := fs.String("model", "", "the model the member's days run on; empty goes back to the configured default")
	effort := fs.String("effort", "", "the effort the member's days run on; empty goes back to the harness default")
	desktopName := fs.String("desktop-name", "", "name for the new desktop of own or an empty slot (defaults to the member name)")
	desktop := fs.String("launch-desktop", "", "own, a desktop digit (an empty slot gets a new desktop), name or id in this member profile")
	var dirs crewDirList
	fs.Var(&dirs, "awareness-dir", "a directory the member's charter is about; repeat for several")
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	profile := crewProfileFlag(fs)
	member, err := parseMemberAndFlags(fs, args, "crew set")
	if err != nil {
		return crewSetArgs{}, err
	}
	parsed := crewSetArgs{member: member, profile: *profile, json: *jsonOut}
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
	record := result.Member
	fmt.Printf("%s launches in %s on %s, model %s, effort %s\n", record.Name, valueOrDash(protocol.Deref(record.Cwd)), valueOrDash(record.ResolvedAgent), valueOrDash(protocol.Deref(record.ResolvedModel)), valueOrDash(protocol.Deref(record.ResolvedEffort)))
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
		fmt.Fprintln(w, "No crew members in this profile. `attn crew create <name>` adds one; `attn crew list --all` includes retired members.")
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
		fmt.Fprintf(w, "%-12s  %-8s  %-8s  %-20s  %-8s  %-10s  %-22s  %s\n", member.Name, state, valueOrDash(member.ResolvedAgent), valueOrDash(protocol.Deref(member.ResolvedModel)), valueOrDash(protocol.Deref(member.ResolvedEffort)), session, protocol.Deref(member.ProfileName)+" › "+launchDesktopText(member.LaunchDesktop), member.HomeDir)
	}
	fmt.Fprintf(w, "\nAn awake MEMBER or SESSION works with `attn agent peek <target>`.\n")
}

func launchDesktopText(setting *protocol.LaunchDesktopSetting) string {
	if setting == nil {
		return "-"
	}
	return protocol.Deref(setting.Label)
}

func crewProfileFlag(fs *flag.FlagSet) *string {
	return fs.String("profile", "", "the profile whose crew to address, by name or id; inside attn, your session's profile")
}

func crewClient(profile string) *client.Client {
	return client.New("").WithGardenProfile(strings.TrimSpace(profile), currentSessionOrExit())
}

type crewRenameArgs struct {
	member  string
	name    string
	profile string
	json    bool
}

func parseCrewRenameArgs(args []string) (crewRenameArgs, error) {
	fs := flag.NewFlagSet("crew rename", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	profile := crewProfileFlag(fs)
	var positionals []string
	for {
		if err := parseFlags(fs, args); err != nil {
			return crewRenameArgs{}, err
		}
		if fs.NArg() == 0 {
			break
		}
		positionals = append(positionals, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positionals) != 2 {
		return crewRenameArgs{}, fmt.Errorf("crew rename takes a member and its new name, not %q", strings.Join(positionals, " "))
	}
	return crewRenameArgs{member: positionals[0], name: positionals[1], profile: *profile, json: *jsonOut}, nil
}

func runCrewRename(args []string) {
	parsed, err := parseCrewRenameArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew rename: %v\n", err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	result, err := crewClient(parsed.profile).CrewRename(parsed.member, parsed.name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew rename: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if result.HomeDir != nil {
		fmt.Printf("%s is now %s, and its home moved to %s; its key %s, letters and mail stay.\n", result.PreviousName, result.Name, *result.HomeDir, crew.KeyRefPrefix+result.Member)
		return
	}
	fmt.Printf("%s is now %s; its key %s, home, letters and mail stay.\n", result.PreviousName, result.Name, crew.KeyRefPrefix+result.Member)
}

func runCrewMoveHome(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew move-home", args)
	result, err := crewClient(parsed.profile).CrewMoveHome(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew move-home: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	fmt.Printf("%s's home is now %s (it was %s).\n", result.Member.Name, result.Member.HomeDir, result.PreviousHome)
}

type crewMemberArgs struct {
	member  string
	profile string
	json    bool
}

func parseCrewMemberArgs(verb string, args []string) (crewMemberArgs, error) {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print the machine result as JSON")
	profile := crewProfileFlag(fs)
	member, err := parseMemberAndFlags(fs, args, verb)
	if err != nil {
		return crewMemberArgs{}, err
	}
	return crewMemberArgs{member: member, profile: *profile, json: *jsonOut}, nil
}

func parsedCrewMemberArgsOrExit(verb string, args []string) crewMemberArgs {
	parsed, err := parseCrewMemberArgs(verb, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", verb, err)
		writeCrewHelp(os.Stderr)
		os.Exit(2)
	}
	return parsed
}

func runCrewCreate(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew create", args)
	result, err := crewClient(parsed.profile).CrewCreate(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew create: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result.Member)
		return
	}
	member := result.Member
	fmt.Printf("%s joined profile %s with key %s.\nhome: %s\nTheir first wake writes CHARTER.md with you: `attn crew wake %s`.\n",
		member.Name, valueOrDash(protocol.Deref(member.ProfileName)), crew.KeyRefPrefix+member.ID, member.HomeDir, member.Name)
}

func runCrewRetire(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew retire", args)
	result, err := crewClient(parsed.profile).CrewRetire(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew retire: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	printCrewRetirement(os.Stdout, result)
}

func printCrewRetirement(w io.Writer, result *protocol.CrewRetireResult) {
	name := result.Member.Name
	if result.AlreadyRetired {
		fmt.Fprintf(w, "%s was already retired.\n", name)
	} else {
		fmt.Fprintf(w, "%s is retired.\n", name)
	}
	fmt.Fprintf(w, "released seeds: %s\n", valueOrDash(strings.Join(result.ReleasedSeeds, ", ")))
	fmt.Fprintf(w, "removed watches: %d\n", result.RemovedWatches)
	fmt.Fprintf(w, "closed asks: %s\n", valueOrDash(strings.Join(result.ClosedAsks, ", ")))
	fmt.Fprintf(w, "unread mail kept: %d\n", result.Unread)
	if result.Sleep != nil {
		fmt.Fprintln(w, crewSleepOutcomeLine(result.Sleep))
	}
	fmt.Fprintf(w, "`attn crew restore %s` brings them back.\n", name)
}

func runCrewRestore(args []string) {
	parsed := parsedCrewMemberArgsOrExit("crew restore", args)
	result, err := crewClient(parsed.profile).CrewRestore(parsed.member)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crew restore: %v\n", err)
		os.Exit(1)
	}
	if parsed.json {
		printJSON(result)
		return
	}
	if result.AlreadyActive {
		fmt.Printf("%s was not retired; nothing changed.\n", result.Member.Name)
		return
	}
	fmt.Printf("%s is back. Mail kept while retired is delivered now and can wake them.\n", result.Member.Name)
}
