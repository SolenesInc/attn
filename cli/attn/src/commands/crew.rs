const HELP: &str = r#"usage: attn crew <command>

Manage the Crew. Each member has a home folder with its charter and letters,
which persist across sessions: in attn's data folder, or in its profile's
Notebook at crew/<name>/. Every member belongs to a profile and wakes only
there.

A <member> is a name in your profile, or member:<key> for the permanent key
that never changes. Every profile has a Chief member, and chief reaches it
whatever its name; it sleeps but is never retired.
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
               [--launch-desktop <own|desktop>] [--awareness-dir <dir>]... [--home <dir>]
        Save launch settings without changing the member's markdown files.
        --launch-desktop selects own, a desktop digit (an empty slot gets a new desktop), name or id.
        --cwd sets the working directory; --model selects the model.
        --agent accepts claude, codex, or an installed plugin driver.
        --agent "" restores the crew default.
        --model "" restores default_model_<harness>, else fable on Claude;
        --model default runs on the harness's own default.
        --effort selects effort; the harness reports its levels in Settings.
        --effort "" restores the harness default.
        --awareness-dir sets context dirs. Repeat to replace the saved list.
        Use --awareness-dir "" to clear it.
        --home records where the member's home is after you moved or renamed
        its folder: a folder holding its CHARTER.md. Pass it without other
        settings; the member must be asleep. Outside attn's data folder the
        folder is renamed after the member. A working or awareness directory
        inside the old home follows, and so do saved sessions. To undo
        move-home, move the folder to crew/<key>/ in attn's data folder and
        point --home there.

  rename <member> <name> [--json]
        Give the member a new name, unique in its profile. Its key, letters,
        mail and seeds stay. A home in the Notebook moves to a folder named
        after the new name: at once when the member is asleep, at its next
        wake when it is awake.

  move-home <member> [--json]
        Move the member's home into its profile's current Notebook, at
        crew/<name>/ (crew/<name>-2/ when that folder is taken), from wherever
        it is: attn's data folder, an earlier Notebook or any other folder.
        Its charter, letters and other files move; a working or awareness
        directory inside the home follows. The member must be asleep.
        Changing the Notebook folder moves no home by itself; run this for
        each member.

  retire <member> [--json]
        Stop the member's work: its claimed seeds are replanted with a note,
        its seed and pull request watches and open asks are removed, and new
        mail is refused. Unread mail, its home and its name stay. An awake
        member is asked to sleep.

  restore <member> [--json]
        Bring a retired member back. Mail kept while retired is delivered,
        which can wake it.
"#;

use super::support::{
    bool_flag, client, current_session_or_exit, parse_flags, parse_interspersed, string_flag,
};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_reply_field, print_stdout};
use crate::client::{Client, Reply};
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::quote::quote;
use attn_protocol::go_values::{AGENT_MSG_STATUS_QUEUED, CREW_KEY_REF_PREFIX};
use attn_protocol::{
    CrewApplyMessage, CrewApplyResult, CrewCreateMessage, CrewCreateResult, CrewListMessage,
    CrewListResult, CrewMember, CrewMoveHomeMessage, CrewMoveHomeResult, CrewPrimeMessage,
    CrewPrimeResult, CrewRenameMessage, CrewRenameResult, CrewRestoreMessage, CrewRestoreResult,
    CrewRetireMessage, CrewRetireResult, CrewSetMessage, CrewSetResult, CrewSleepMessage,
    CrewSleepResult, CrewWakeMessage, CrewWakeResult, LaunchDesktopSetting,
};
use serde::Serialize;
use serde::de::DeserializeOwned;
use std::fmt::Write as _;

const SHORT_ID_LENGTH: usize = 8;
const NOTHING_TO_SET: &str = "nothing to set — pass --cwd, --agent, --model, --effort, --launch-desktop, --awareness-dir, --home, or any of them together";

const JSON: Flag<'static> = bool_flag("json");
const PROFILE: Flag<'static> = string_flag("profile");

fn usage(label: &str, message: &str) -> Stop {
    eprint!("{label}: {message}\n{HELP}");
    Stop(2)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().map(String::as_str) else {
        print_stdout(HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" {
        print_stdout(HELP);
        return Ok(());
    }
    let rest = &args[1..];
    if matches!(
        verb,
        "list"
            | "ls"
            | "wake"
            | "sleep"
            | "apply"
            | "set"
            | "rename"
            | "create"
            | "retire"
            | "restore"
            | "move-home"
            | "prime"
    ) {
        run.path = format!("crew {verb}");
    }
    match verb {
        "list" | "ls" => list(run, rest),
        "wake" => wake(run, rest),
        "sleep" => sleep(run, rest),
        "apply" => apply(run, rest),
        "set" => set(run, rest),
        "rename" => rename(run, rest),
        "create" => create(run, rest),
        "retire" => retire(run, rest),
        "restore" => restore(run, rest),
        "move-home" => move_home(run, rest),
        "prime" => prime(run, rest),
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("crew: unknown command {}\n{HELP}", quote(verb.as_bytes()));
            Err(Stop(2))
        }
    }
}

struct Request {
    label: &'static str,
    profile: String,
    json: bool,
}

struct Answer {
    field: &'static str,
    json_path: &'static [&'static str],
    missing: &'static str,
}

impl Request {
    fn from_flags(label: &'static str, flags: &FlagSet<'_>) -> Self {
        Self {
            label,
            profile: flags.string("profile").trim().to_string(),
            json: flags.bool("json"),
        }
    }

    fn client<'a>(&self, run: &mut Run<'a>) -> Result<Client<'a>, Stop> {
        let session = current_session_or_exit(run)?;
        Ok(client(run).with_garden_scope(&self.profile, &session))
    }

    fn send<M: Serialize>(
        &self,
        run: &mut Run<'_>,
        message: &M,
        answer: &Answer,
    ) -> Result<Reply, Stop> {
        self.client(run)?
            .call_field(message, answer.field, answer.missing)
            .map_err(|error| run.fail_send(self.label, &error))
    }

    fn respond<M: Serialize, T: DeserializeOwned>(
        &self,
        run: &mut Run<'_>,
        message: &M,
        answer: &Answer,
        render: impl FnOnce(T) -> String,
    ) -> Result<(), Stop> {
        let reply = self.send(run, message, answer)?;
        if self.json {
            return print_reply_field(&reply, answer.json_path)
                .map_err(|error| run.fail_send(self.label, &error));
        }
        let result = reply
            .value(answer.field)
            .map_err(|error| run.fail_send(self.label, &error))?;
        print_stdout(&render(result));
        Ok(())
    }
}

fn parse_member(
    run: &mut Run<'_>,
    flags: &mut FlagSet<'_>,
    args: &[String],
    label: &str,
) -> Result<String, Stop> {
    let parsed = member_positional(run, flags, args, label);
    parsed.map_err(|message| usage(label, &message))
}

fn member_positional(
    run: &mut Run<'_>,
    flags: &mut FlagSet<'_>,
    args: &[String],
    label: &str,
) -> Result<String, String> {
    parse_flags(run, flags, args).map_err(|error| error.to_string())?;
    let Some((member, rest)) = flags.args().split_first() else {
        return Err(format!("{label} takes one member name"));
    };
    let (member, rest) = (member.clone(), rest.to_vec());
    if !rest.is_empty() {
        parse_flags(run, flags, &rest).map_err(|error| error.to_string())?;
        if !flags.args().is_empty() {
            return Err(format!(
                "{label} takes one member name, not {}",
                quote(flags.args().join(" ").as_bytes())
            ));
        }
    }
    Ok(member)
}

fn member_request(
    run: &mut Run<'_>,
    label: &'static str,
    args: &[String],
) -> Result<(String, Request), Stop> {
    let mut flags = FlagSet::new(label, &[JSON, PROFILE]);
    let member = parse_member(run, &mut flags, args, label)?;
    Ok((member, Request::from_flags(label, &flags)))
}

fn list(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let label = "crew list";
    let mut flags = FlagSet::new(label, &[JSON, PROFILE, bool_flag("all")]);
    parse_flags(run, &mut flags, args).map_err(|error| usage(label, &error.to_string()))?;
    if !flags.args().is_empty() {
        return Err(usage(label, "crew list takes no arguments"));
    }
    let request = Request::from_flags(label, &flags);
    let mut message = CrewListMessage::new();
    if flags.bool("all") {
        message.include_retired = Some(true);
    }
    let answer = Answer {
        field: "crew_list_result",
        json_path: &["crew_list_result", "members"],
        missing: "the daemon answered without a roster",
    };
    request.respond(run, &message, &answer, |result: CrewListResult| {
        roster(&result.members)
    })
}

fn wake(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let label = "crew wake";
    let mut flags = FlagSet::new(label, &[JSON, PROFILE, string_flag("agent")]);
    let member = parse_member(run, &mut flags, args, label)?;
    let request = Request::from_flags(label, &flags);
    let mut message = CrewWakeMessage::new();
    message.member = member;
    let agent = flags.string("agent").trim();
    if !agent.is_empty() {
        message.agent = Some(agent.to_string());
    }
    let answer = Answer {
        field: "crew_wake_result",
        json_path: &["crew_wake_result"],
        missing: "the daemon answered without a wake result",
    };
    request.respond(run, &message, &answer, |result: CrewWakeResult| {
        wake_lines(&result)
    })
}

fn wake_lines(result: &CrewWakeResult) -> String {
    let session = short_id(&result.session_id);
    if result.already_awake {
        return format!(
            "{} is already awake in session {session} — nothing was launched.\n",
            result.name
        );
    }
    let mut out = String::new();
    let released = result.released_session_id.as_deref().map_or("", str::trim);
    if !released.is_empty() {
        let _ = writeln!(
            out,
            "Previous session {} had exited; its binding was released.",
            short_id(released)
        );
    }
    let _ = writeln!(
        out,
        "{name} is awake in session {session}. `attn agent peek {name}` watches the day; the priming size is in the daemon log (grep `crew: priming`).",
        name = result.name
    );
    out
}

fn sleep(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (member, request) = member_request(run, "crew sleep", args)?;
    let mut message = CrewSleepMessage::new();
    message.member = member;
    let answer = Answer {
        field: "crew_sleep_result",
        json_path: &["crew_sleep_result"],
        missing: "the daemon answered without a sleep result",
    };
    request.respond(run, &message, &answer, |result: CrewSleepResult| {
        format!("{}\n", sleep_outcome(&result))
    })
}

fn sleep_outcome(result: &CrewSleepResult) -> String {
    let name = &result.name;
    let detail = result.detail.trim();
    if result.already_asleep {
        if !detail.is_empty() {
            return format!("{detail}.");
        }
        return format!("{name} is already asleep — no sleep request was sent.");
    }
    let session = short_id(result.session_id.as_deref().unwrap_or(""));
    if result.delivery_status.as_deref() == Some(AGENT_MSG_STATUS_QUEUED) {
        let detail = if detail.is_empty() {
            "the member is not taking input right now"
        } else {
            detail
        };
        return format!("Sleep request for {name} is queued in session {session} — {detail}");
    }
    format!(
        "Asked {name} in session {session} to write its handoff and file it with `attn handoff`."
    )
}

fn apply(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (member, request) = member_request(run, "crew apply", args)?;
    let mut message = CrewApplyMessage::new();
    message.member = member;
    let answer = Answer {
        field: "crew_apply_result",
        json_path: &["crew_apply_result"],
        missing: "the daemon answered without saying how the settings were applied",
    };
    request.respond(run, &message, &answer, |result: CrewApplyResult| {
        format!("{}.\n", result.detail)
    })
}

fn parse_set(
    run: &mut Run<'_>,
    flags: &mut FlagSet<'_>,
    args: &[String],
) -> Result<CrewSetMessage, String> {
    let mut message = CrewSetMessage::new();
    message.member = member_positional(run, flags, args, "crew set")?;
    let chosen = |name: &str| flags.is_set(name).then(|| flags.string(name).to_string());
    message.cwd = chosen("cwd");
    message.agent = chosen("agent");
    message.model = chosen("model");
    message.effort = chosen("effort");
    message.launch_desktop = chosen("launch-desktop");
    message.launch_desktop_name = chosen("desktop-name");
    message.home = chosen("home");
    if message.launch_desktop_name.is_some() && message.launch_desktop.is_none() {
        return Err("--desktop-name needs --launch-desktop own or an empty slot".to_string());
    }
    let dirs_set = flags.is_set("awareness-dir");
    if dirs_set {
        let dirs: Vec<String> = flags
            .repeated("awareness-dir")
            .iter()
            .filter(|dir| !dir.trim().is_empty())
            .cloned()
            .collect();
        if dirs.is_empty() {
            message.clear_awareness_dirs = Some(true);
        } else {
            message.awareness_dirs = Some(dirs);
        }
    }
    if let Some(home) = message.home.as_deref()
        && !home.trim().is_empty()
        && !home.starts_with('~')
    {
        let absolute = super::absolute(home).map_err(|error| format!("--home {home}: {error}"))?;
        message.home = Some(absolute);
    }
    let nothing_chosen = message.cwd.is_none()
        && message.agent.is_none()
        && message.model.is_none()
        && message.effort.is_none()
        && message.launch_desktop.is_none()
        && message.home.is_none()
        && !dirs_set;
    if nothing_chosen {
        return Err(NOTHING_TO_SET.to_string());
    }
    Ok(message)
}

fn set(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let label = "crew set";
    let mut flags = FlagSet::new(
        label,
        &[
            JSON,
            PROFILE,
            string_flag("cwd"),
            string_flag("agent"),
            string_flag("model"),
            string_flag("effort"),
            string_flag("desktop-name"),
            string_flag("launch-desktop"),
            string_flag("home"),
            Flag {
                name: "awareness-dir",
                default: FlagDefault::Repeated,
                usage: "",
            },
        ],
    );
    let message = parse_set(run, &mut flags, args).map_err(|error| usage(label, &error))?;
    let request = Request::from_flags(label, &flags);
    let answer = Answer {
        field: "crew_set_result",
        json_path: &["crew_set_result", "member"],
        missing: "the daemon answered without a member",
    };
    request.respond(run, &message, &answer, |result: CrewSetResult| {
        set_lines(&result.member)
    })
}

fn set_lines(member: &CrewMember) -> String {
    format!(
        "{} launches in {} on {}, model {}, effort {}\nprofile: {}\nlaunch desktop: {}\nawareness dirs: {}\nhome: {}\n",
        member.name,
        or_dash(member.cwd.as_deref().unwrap_or("")),
        or_dash(&member.resolved_agent),
        or_dash(member.resolved_model.as_deref().unwrap_or("")),
        or_dash(member.resolved_effort.as_deref().unwrap_or("")),
        member.profile_name.as_deref().unwrap_or(""),
        launch_desktop_text(member.launch_desktop.as_ref()),
        or_dash(&member.awareness_dirs.join(", ")),
        member.home_dir
    )
}

fn or_dash(value: &str) -> &str {
    if value.trim().is_empty() { "-" } else { value }
}

pub fn launch_desktop_text(setting: Option<&LaunchDesktopSetting>) -> &str {
    setting.map_or("-", |setting| setting.label.as_deref().unwrap_or(""))
}

fn short_id(id: &str) -> String {
    let bytes = id.as_bytes();
    String::from_utf8_lossy(&bytes[..bytes.len().min(SHORT_ID_LENGTH)]).into_owned()
}

fn roster(members: &[CrewMember]) -> String {
    if members.is_empty() {
        return "No crew members in this profile. `attn crew create <name>` adds one; `attn crew list --all` includes retired members.\n".to_string();
    }
    let mut out = format!(
        "{:<12}  {:<8}  {:<8}  {:<20}  {:<8}  {:<10}  {:<22}  {}\n",
        "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "LAUNCH DESKTOP", "HOME"
    );
    for member in members {
        let binding = member.binding_session.as_deref().map_or("", str::trim);
        let (mut state, session) = if binding.is_empty() {
            ("asleep", "-".to_string())
        } else {
            ("awake", short_id(binding))
        };
        if member.retired {
            state = "retired";
        }
        let desktop = format!(
            "{} › {}",
            member.profile_name.as_deref().unwrap_or(""),
            launch_desktop_text(member.launch_desktop.as_ref())
        );
        let _ = writeln!(
            out,
            "{:<12}  {state:<8}  {:<8}  {:<20}  {:<8}  {session:<10}  {desktop:<22}  {}",
            member.name,
            or_dash(&member.resolved_agent),
            or_dash(member.resolved_model.as_deref().unwrap_or("")),
            or_dash(member.resolved_effort.as_deref().unwrap_or("")),
            member.home_dir
        );
    }
    out.push_str("\nAn awake MEMBER or SESSION works with `attn agent peek <target>`.\n");
    out
}

fn rename(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let label = "crew rename";
    let mut flags = FlagSet::new(label, &[JSON, PROFILE]);
    let positionals = parse_interspersed(run, &mut flags, args)
        .map_err(|error| usage(label, &error.to_string()))?;
    let [member, name] = positionals.as_slice() else {
        return Err(usage(
            label,
            &format!(
                "crew rename takes a member and its new name, not {}",
                quote(positionals.join(" ").as_bytes())
            ),
        ));
    };
    let request = Request::from_flags(label, &flags);
    let mut message = CrewRenameMessage::new();
    message.member.clone_from(member);
    message.name.clone_from(name);
    let answer = Answer {
        field: "crew_rename_result",
        json_path: &["crew_rename_result"],
        missing: "the daemon answered without a rename result",
    };
    request.respond(run, &message, &answer, |result: CrewRenameResult| {
        rename_line(&result)
    })
}

fn rename_line(result: &CrewRenameResult) -> String {
    let (old, new) = (&result.previous_name, &result.name);
    let key = format!("{CREW_KEY_REF_PREFIX}{}", result.member);
    if let Some(home) = &result.home_dir {
        return format!(
            "{old} is now {new}, and its home moved to {home}; its key {key}, letters and mail stay.\n"
        );
    }
    if result.home_moves_at_wake == Some(true) {
        return format!(
            "{old} is now {new}; its home keeps its folder while it is awake and moves to one named after {new} at its next wake. Its key {key}, letters and mail stay.\n"
        );
    }
    format!("{old} is now {new}; its key {key}, home, letters and mail stay.\n")
}

fn move_home(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (member, request) = member_request(run, "crew move-home", args)?;
    let mut message = CrewMoveHomeMessage::new();
    message.member = member;
    let answer = Answer {
        field: "crew_move_home_result",
        json_path: &["crew_move_home_result"],
        missing: "the daemon answered without a move-home result",
    };
    request.respond(run, &message, &answer, |result: CrewMoveHomeResult| {
        format!(
            "{}'s home is now {} (it was {}).\n",
            result.member.name, result.member.home_dir, result.previous_home
        )
    })
}

fn create(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (name, request) = member_request(run, "crew create", args)?;
    let mut message = CrewCreateMessage::new();
    message.name = name;
    let answer = Answer {
        field: "crew_create_result",
        json_path: &["crew_create_result", "member"],
        missing: "the daemon answered without the new member",
    };
    request.respond(run, &message, &answer, |result: CrewCreateResult| {
        let member = result.member;
        format!(
            "{name} joined profile {} with key {CREW_KEY_REF_PREFIX}{}.\nhome: {}\nTheir first wake writes CHARTER.md with you: `attn crew wake {name}`.\n",
            or_dash(member.profile_name.as_deref().unwrap_or("")),
            member.id,
            member.home_dir,
            name = member.name
        )
    })
}

fn retire(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (member, request) = member_request(run, "crew retire", args)?;
    let mut message = CrewRetireMessage::new();
    message.member = member;
    let answer = Answer {
        field: "crew_retire_result",
        json_path: &["crew_retire_result"],
        missing: "the daemon answered without a retirement result",
    };
    request.respond(run, &message, &answer, |result: CrewRetireResult| {
        retirement(&result)
    })
}

fn retirement(result: &CrewRetireResult) -> String {
    let name = &result.member.name;
    let mut out = if result.already_retired {
        format!("{name} was already retired.\n")
    } else {
        format!("{name} is retired.\n")
    };
    let _ = writeln!(
        out,
        "released seeds: {}",
        or_dash(&result.released_seeds.join(", "))
    );
    let _ = writeln!(out, "removed watches: {}", result.removed_watches);
    let _ = writeln!(
        out,
        "closed asks: {}",
        or_dash(&result.closed_asks.join(", "))
    );
    let _ = writeln!(out, "unread mail kept: {}", result.unread);
    if let Some(sleep) = &result.sleep {
        let _ = writeln!(out, "{}", sleep_outcome(sleep));
    }
    let _ = writeln!(out, "`attn crew restore {name}` brings them back.");
    out
}

fn restore(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (member, request) = member_request(run, "crew restore", args)?;
    let mut message = CrewRestoreMessage::new();
    message.member = member;
    let answer = Answer {
        field: "crew_restore_result",
        json_path: &["crew_restore_result"],
        missing: "the daemon answered without a restore result",
    };
    request.respond(run, &message, &answer, |result: CrewRestoreResult| {
        if result.already_active {
            format!("{} was not retired; nothing changed.\n", result.member.name)
        } else {
            format!(
                "{} is back. Mail kept while retired is delivered now and can wake them.\n",
                result.member.name
            )
        }
    })
}

fn prime(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let label = "crew prime";
    if !args.is_empty() {
        return Err(usage(label, "takes no arguments"));
    }
    let mut message = CrewPrimeMessage::new();
    message.session_id = current_session_or_exit(run)?;
    let result: CrewPrimeResult = client(run)
        .call_field(
            &message,
            "crew_prime_result",
            "the daemon answered without a priming",
        )
        .and_then(|reply| reply.value("crew_prime_result"))
        .map_err(|error| run.fail_send(label, &error))?;
    if result.member.is_none() {
        eprintln!("crew prime: this session is not a crew member's; nothing to prime");
        return Err(Stop(1));
    }
    print_stdout(&format!("{}\n", result.guidance.unwrap_or_default()));
    Ok(())
}
