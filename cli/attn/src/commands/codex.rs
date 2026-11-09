use super::support::{
    SessionLookup, bool_flag, client, current_session, daemon_message, has_help_flag,
    parse_interspersed, string_flag,
};
use super::{Run, Stop, print_reply_field, print_stdout};
use crate::client::{Client, Reply, SendError};
use crate::paths::os_error_text;
use attn::go::duration;
use attn::go::flag::FlagSet;
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn::go::time::Time;
use attn_protocol::go_values::{
    SHARED_CODEX_RESTART_FAILED, SHARED_CODEX_RESTART_PENDING, SHARED_CODEX_RESTART_RESTARTING,
};
use attn_protocol::{
    CodexStatusMessage, CodexStatusResult, QueryMessage, RestartSharedCodexMessage,
};
use serde::Deserialize;
use std::collections::HashMap;
use std::fmt::Write as _;
use std::process::Command;

const HELP: &str = r"usage: attn codex <command>

  status [--profile <name|id>] [--json]
  update [--profile <name|id>]
  restart [--now] [--profile <name|id>]

status shows the profile's shared Codex: the Codex its app-server runs, the
installed and latest ones, a restart and what it waits on, and the sessions
whose terminals still run an older Codex.
update runs Codex's own update here, with the Codex executable attn launches,
then has attn read the installed Codex again. Shared Codex moves to it once
no turn runs.
restart restarts the app-server on the installed Codex once no turn runs,
also when it already runs that Codex. --now stops turns waiting on an
approval or a question, gives running turns a minute to finish, then stops
them.

Inside an attn session the profile is the session's; elsewhere pass
--profile when there is more than one.
";

const NANOS_PER_SECOND: i128 = 1_000_000_000;
const STATUS_FIELD: &str = "codex_status_result";

struct Codex<'a> {
    client: Client<'a>,
    action: String,
    caller: String,
    profile: String,
}

impl Codex<'_> {
    fn status(&self, run: &mut Run<'_>, refresh: bool) -> Result<Reply, Stop> {
        let mut message = CodexStatusMessage::new();
        message.refresh = Some(refresh);
        message.caller_session_id = (!self.caller.is_empty()).then(|| self.caller.clone());
        let profile = self.profile.trim();
        message.profile = (!profile.is_empty()).then(|| profile.to_string());
        self.client
            .call_field(
                &message,
                STATUS_FIELD,
                "daemon returned no shared Codex status",
            )
            .map_err(|error| self.failed(run, &error))
    }

    fn failed(&self, run: &mut Run<'_>, error: &SendError) -> Stop {
        run.refusal.clone_from(&error.refusal);
        eprintln!("codex {}: {}", self.action, daemon_message(error));
        Stop(1)
    }

    fn decode(&self, run: &mut Run<'_>, reply: &Reply) -> Result<CodexStatusResult, Stop> {
        reply
            .value(STATUS_FIELD)
            .map_err(|error| self.failed(run, &error))
    }

    fn session_names(&self) -> HashMap<String, String> {
        #[derive(Deserialize)]
        struct Named {
            id: String,
            #[serde(default)]
            label: String,
        }
        let Ok(reply) = self.client.call(&QueryMessage::new()) else {
            return HashMap::new();
        };
        reply
            .value::<Option<Vec<Named>>>("sessions")
            .ok()
            .flatten()
            .unwrap_or_default()
            .into_iter()
            .filter(|session| !session.label.is_empty())
            .map(|session| (session.id, session.label))
            .collect()
    }
}

fn flag_failure(action: &str, message: &str) -> Stop {
    eprintln!("codex {action}: {message}");
    Stop(2)
}

fn resolve_caller(run: &mut Run<'_>, action: &str) -> Result<String, Stop> {
    let cause = match current_session(run) {
        Ok(SessionLookup::Found(session)) => return Ok(session),
        Ok(SessionLookup::NotShowing(message)) => message,
        Err(error) => {
            run.refusal.clone_from(&error.refusal);
            error.to_string()
        }
    };
    eprintln!("codex {action}: resolve the current session: {cause}");
    Err(Stop(1))
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() || has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let action = args[0].clone();
    if matches!(action.as_str(), "status" | "update" | "restart") {
        run.path = format!("codex {action}");
    }
    let flag_name = format!("codex {action}");
    let mut flags = FlagSet::new(
        &flag_name,
        &[string_flag("profile"), bool_flag("json"), bool_flag("now")],
    );
    let positionals = parse_interspersed(run, &mut flags, &args[1..])
        .map_err(|error| flag_failure(&action, &error.to_string()))?;
    if let Some(first) = positionals.first() {
        return Err(flag_failure(
            &action,
            &format!("unexpected argument {}", quote(first.as_bytes())),
        ));
    }
    let json = flags.bool("json");
    let now = flags.bool("now");
    if json && action != "status" {
        return Err(flag_failure(&action, "--json is only for status"));
    }
    if now && action != "restart" {
        return Err(flag_failure(&action, "--now is only for restart"));
    }
    let caller = resolve_caller(run, &action)?;
    let codex = Codex {
        client: client(run),
        action: action.clone(),
        caller,
        profile: flags.string("profile").to_string(),
    };
    match action.as_str() {
        "status" => status(run, &codex, json),
        "update" => update(run, &codex),
        "restart" => restart(run, &codex, now),
        _ => {
            eprint!(
                "codex: unknown command {}\n\n{HELP}",
                quote(action.as_bytes())
            );
            Err(Stop(2))
        }
    }
}

fn status(run: &mut Run<'_>, codex: &Codex<'_>, json: bool) -> Result<(), Stop> {
    let reply = codex.status(run, false)?;
    if json {
        return print_reply_field(&reply, &[STATUS_FIELD])
            .map_err(|error| codex.failed(run, &error));
    }
    let result = codex.decode(run, &reply)?;
    print_stdout(&render_status(&result, &codex.session_names()));
    Ok(())
}

fn update(run: &mut Run<'_>, codex: &Codex<'_>) -> Result<(), Stop> {
    let before = codex.status(run, false)?;
    let before = codex.decode(run, &before)?;
    let exit = Command::new(&before.executable).arg("update").status();
    let code = match exit {
        Ok(status) => status.code().map_or(255, |code| code.to_le_bytes()[0]),
        Err(error) => {
            eprintln!(
                "codex update: run {} update: {}",
                before.executable,
                spawn_error_text(&before.executable, &error)
            );
            return Err(Stop(1));
        }
    };
    let after = codex.status(run, true)?;
    let after = codex.decode(run, &after)?;
    print_stdout(&format!(
        "\n{}",
        render_status(&after, &codex.session_names())
    ));
    if code == 0 {
        return Ok(());
    }
    Err(Stop(code))
}

fn spawn_error_text(executable: &str, error: &std::io::Error) -> String {
    if executable.contains('/') {
        format!("fork/exec {executable}: {}", os_error_text(error))
    } else {
        format!(
            "exec: {}: executable file not found in $PATH",
            quote(executable.as_bytes())
        )
    }
}

fn restart(run: &mut Run<'_>, codex: &Codex<'_>, now: bool) -> Result<(), Stop> {
    let reply = codex.status(run, false)?;
    let result = codex.decode(run, &reply)?;
    let mut message = RestartSharedCodexMessage::new();
    message.profile_id = result.profile_id;
    message.now = now;
    codex
        .client
        .send(&message)
        .map_err(|error| codex.failed(run, &error))?;
    print_stdout(&if now {
        format!(
            "Restarting shared Codex of profile {} now.\n",
            result.profile_name
        )
    } else {
        format!(
            "Shared Codex of profile {} restarts once no turn runs.\n",
            result.profile_name
        )
    });
    Ok(())
}

fn version_or<'a>(value: Option<&'a String>, missing: &'a str) -> &'a str {
    value
        .map(String::as_str)
        .filter(|value| !value.is_empty())
        .unwrap_or(missing)
}

fn time_until(stamp: &str) -> Option<String> {
    let at = Time::parse_rfc3339_nano(stamp).ok()?;
    Some(duration::format(round_to_second(
        at.unix_nanos() - Time::now_utc().unix_nanos(),
    )))
}

fn round_to_second(nanos: i128) -> i64 {
    let magnitude = nanos.abs();
    let rounded = (magnitude + NANOS_PER_SECOND / 2) / NANOS_PER_SECOND * NANOS_PER_SECOND;
    i64::try_from(if nanos < 0 { -rounded } else { rounded }).unwrap_or(i64::MAX)
}

fn render_status(result: &CodexStatusResult, names: &HashMap<String, String>) -> String {
    let name = |id: &str| names.get(id).cloned().unwrap_or_else(|| id.to_string());
    let mut table = format!(
        "profile\t{}\ncodex\t{}\n",
        result.profile_name, result.executable
    );
    let Some(state) = &result.shared_codex else {
        table.push_str("shared\toff; turn it on in Settings → Experimental\n");
        return tabwriter::format(&table);
    };
    let running = version_or(
        state.running_version.as_ref(),
        "not running; the next shared Codex terminal starts it",
    );
    let _ = writeln!(table, "server\t{running}");
    let _ = writeln!(
        table,
        "installed\t{}",
        version_or(state.installed_version.as_ref(), "unknown")
    );
    let _ = writeln!(
        table,
        "latest\t{}  (codex doctor, daily)",
        version_or(state.latest_version.as_ref(), "not checked yet")
    );
    let mut restart = state.restart.clone();
    if state.restart == SHARED_CODEX_RESTART_PENDING
        || state.restart == SHARED_CODEX_RESTART_RESTARTING
    {
        if let Some(remaining) = state.force_at.as_deref().and_then(time_until) {
            let _ = write!(restart, ", force stop in {remaining}");
        }
        let blockers = state.blockers.as_deref().unwrap_or(&[]);
        if !blockers.is_empty() {
            let waiting: Vec<String> = blockers
                .iter()
                .map(|blocker| format!("{} ({})", name(&blocker.session_id), blocker.reason))
                .collect();
            let _ = write!(restart, ", waiting on {}", waiting.join(", "));
        }
    } else if state.restart == SHARED_CODEX_RESTART_FAILED {
        let _ = write!(restart, ": {}", state.error.as_deref().unwrap_or(""));
    }
    let _ = writeln!(table, "restart\t{restart}");
    let outdated = state.stale_sessions.as_deref().unwrap_or(&[]);
    if !outdated.is_empty() {
        let names: Vec<String> = outdated.iter().map(|id| name(id)).collect();
        let _ = writeln!(table, "old tiles\t{}", names.join(", "));
    }
    tabwriter::format(&table)
}
