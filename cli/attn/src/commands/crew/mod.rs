mod help_generated;
mod table_generated;

use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::go_values::{AGENT_MSG_STATUS_QUEUED, CREW_KEY_REF_PREFIX};
use attn_protocol::session_id::short;
use attn_protocol::{
    CrewApplyResult, CrewCloseResult, CrewListResult, CrewMember, CrewMoveHomeResult,
    CrewPrimeResult, CrewRenameResult, CrewRestoreResult, CrewRetireResult, CrewStartResult,
    LaunchDesktopSetting,
};
use serde::de::DeserializeOwned;
use std::fmt::Write as _;

struct Crew;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut args = args.to_vec();
    match args.first().map(String::as_str) {
        Some("wake") => return Err(renamed("wake", "start", &args[1..])),
        Some("sleep") => return Err(renamed("sleep", "close", &args[1..])),
        Some("ls") => {
            args[0] = "list".to_string();
            let result = command::run_group(run, &Crew, &args);
            run.path = "crew ls".to_string();
            return result;
        }
        _ => {}
    }
    command::run_group(run, &Crew, &args)
}

fn renamed(old: &str, new: &str, args: &[String]) -> Stop {
    let mut command = format!("attn crew {new}");
    for arg in args {
        command.push(' ');
        command.push_str(&shell_word(arg));
    }
    eprintln!("crew {old}: `{old}` is now `{new}`: {command}");
    Stop(2)
}

fn decode<T: DeserializeOwned>(run: &mut Run<'_>, label: &str, result: &Reply) -> Result<T, Stop> {
    result.whole().map_err(|error| run.fail_send(label, &error))
}

impl Family for Crew {
    fn group(&self) -> &'static str {
        "crew"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        help_generated::GROUP_HELP
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let path: &[&str] = if cmd.name == "list" {
            &["members"]
        } else {
            &[]
        };
        let raw = result
            .raw_field(path)
            .map_err(|error| run.fail_send(&format!("crew {}", cmd.name), &error))?;
        print_json(raw.as_bytes());
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let label = format!("crew {}", cmd.name);
        let out = match cmd.name {
            "list" => roster(&decode::<CrewListResult>(run, &label, &result)?.members),
            "create" => created(&decode(run, &label, &result)?),
            "start" => start_lines(&decode(run, &label, &result)?),
            "close" => format!("{}\n", close_outcome(&decode(run, &label, &result)?)),
            "prime" => {
                let primed: CrewPrimeResult = decode(run, &label, &result)?;
                if primed.member.is_none() {
                    eprintln!("crew prime: this session is not a crew member's; nothing to prime");
                    return Err(Stop(1));
                }
                format!("{}\n", primed.guidance.unwrap_or_default())
            }
            "apply" => format!(
                "{}.\n",
                decode::<CrewApplyResult>(run, &label, &result)?.detail
            ),
            "set" => set_lines(&decode(run, &label, &result)?),
            "rename" => rename_line(&decode(run, &label, &result)?),
            "move-home" => {
                let moved: CrewMoveHomeResult = decode(run, &label, &result)?;
                format!(
                    "{}'s home is now {} (it was {}).\n",
                    moved.member.name, moved.member.home_dir, moved.previous_home
                )
            }
            "retire" => retirement(&decode::<CrewRetireResult>(run, &label, &result)?),
            "restore" => restored(&decode(run, &label, &result)?),
            _ => unreachable!("crew {} has no renderer", cmd.name),
        };
        print_stdout(&out);
        Ok(())
    }
}

fn created(member: &CrewMember) -> String {
    format!(
        "{name} joined profile {} with key {CREW_KEY_REF_PREFIX}{}.\nhome: {}\nTheir first session writes CHARTER.md with you: `attn crew start {name}`.\n",
        or_dash(member.profile_name.as_deref().unwrap_or("")),
        member.id,
        member.home_dir,
        name = member.name
    )
}

fn restored(result: &CrewRestoreResult) -> String {
    if result.already_active {
        format!("{} was not retired; nothing changed.\n", result.member.name)
    } else {
        format!(
            "{} is back. Mail kept while retired is delivered now and can start a session for them.\n",
            result.member.name
        )
    }
}

fn shell_word(arg: &str) -> String {
    let plain = !arg.is_empty()
        && arg
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "-_.:/=@".contains(c));
    if plain {
        arg.to_string()
    } else {
        format!("'{}'", arg.replace('\'', "'\\''"))
    }
}

fn start_lines(result: &CrewStartResult) -> String {
    let session = short(&result.session_id);
    if result.already_in_session {
        return format!(
            "{} is already in session {session} — nothing was launched.\n",
            result.name
        );
    }
    let mut out = String::new();
    let released = result.released_session_id.as_deref().map_or("", str::trim);
    if !released.is_empty() {
        let _ = writeln!(
            out,
            "Previous session {} had exited; its binding was released.",
            short(released)
        );
    }
    let _ = writeln!(
        out,
        "{name} is in session {session}. `attn agent peek {name}` watches the session; the priming size is in the daemon log (grep `crew: priming`).",
        name = result.name
    );
    out
}

fn close_outcome(result: &CrewCloseResult) -> String {
    let name = &result.name;
    let detail = result.detail.trim();
    if result.already_closed {
        if !detail.is_empty() {
            return format!("{detail}.");
        }
        return format!("{name} is between sessions — no close request was sent.");
    }
    let session = short(result.session_id.as_deref().unwrap_or(""));
    if result.delivery_status.as_deref() == Some(AGENT_MSG_STATUS_QUEUED) {
        let detail = if detail.is_empty() {
            "the member is not taking input right now"
        } else {
            detail
        };
        return format!("Close request for {name} is queued in session {session} — {detail}");
    }
    format!(
        "Asked {name} in session {session} to write its handoff and file it with `attn handoff`."
    )
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

fn roster(members: &[CrewMember]) -> String {
    if members.is_empty() {
        return "No crew members in this profile. `attn crew create <name> --agent <harness>` adds one; `attn crew list --all` includes retired members.\n".to_string();
    }
    let mut out = format!(
        "{:<12}  {:<16}  {:<8}  {:<20}  {:<8}  {:<10}  {:<22}  {}\n",
        "MEMBER", "STATE", "AGENT", "MODEL", "EFFORT", "SESSION", "LAUNCH DESKTOP", "HOME"
    );
    for member in members {
        let binding = member.binding_session.as_deref().map_or("", str::trim);
        let (mut state, session) = if binding.is_empty() {
            ("between sessions", "-".into())
        } else {
            ("in session", short(binding))
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
            "{:<12}  {state:<16}  {:<8}  {:<20}  {:<8}  {session:<10}  {desktop:<22}  {}",
            member.name,
            or_dash(&member.resolved_agent),
            or_dash(member.resolved_model.as_deref().unwrap_or("")),
            or_dash(member.resolved_effort.as_deref().unwrap_or("")),
            member.home_dir
        );
    }
    out.push_str(
        "\nThe MEMBER or SESSION of a member in session works with `attn agent peek <target>`.\n",
    );
    out
}

fn rename_line(result: &CrewRenameResult) -> String {
    let (old, new) = (&result.previous_name, &result.name);
    let key = format!("{CREW_KEY_REF_PREFIX}{}", result.member);
    if let Some(home) = &result.home_dir {
        return format!(
            "{old} is now {new}, and its home moved to {home}; its key {key}, letters and mail stay.\n"
        );
    }
    if result.home_moves_at_start == Some(true) {
        return format!(
            "{old} is now {new}; its home keeps its folder while it is in session and moves to one named after {new} when its next session starts. Its key {key}, letters and mail stay.\n"
        );
    }
    format!("{old} is now {new}; its key {key}, home, letters and mail stay.\n")
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
    if let Some(closed) = &result.close {
        let _ = writeln!(out, "{}", close_outcome(closed));
    }
    let _ = writeln!(out, "`attn crew restore {name}` brings them back.");
    out
}
