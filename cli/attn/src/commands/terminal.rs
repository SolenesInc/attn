use super::support::{client, current_session_or_exit, daemon_message};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, USAGE_UNKNOWN_FLAG, print_stdout};
use crate::client::{Client, Reply, SendError};
use attn::go::quote::quote;
use attn::go::{duration, tabwriter};
use attn_protocol::{
    TerminalCloseMessage, TerminalEntry, TerminalInputMessage, TerminalInputResult,
    TerminalListMessage, TerminalMoveMessage, TerminalMoveResult, TerminalOpenMessage,
    TerminalOpenResult, TerminalReadMessage, TerminalReadResult, TerminalWaitMessage,
    TerminalWaitResult,
};
use serde::Serialize;
use serde::de::DeserializeOwned;
use std::fmt::Write as _;
use std::time::{Duration, Instant};

const HELP: &str = r#"usage: attn terminal <command>

  ls [--desktop <ref> | --all] [--json]
  open [--host <outpost|home>] [--down] [--desktop <ref>] [--cwd <dir>] [-- <command>]
  run <terminal> "<command>" [--wait] [--timeout <seconds>] [--desktop <ref>]
  send <terminal> (--text "<text>" | --keys C-c Escape Enter ...) [--desktop <ref>]
  read <terminal> [--last | --scrollback [--lines <n>]]
  wait <terminal> --until prompt|exit [--timeout <seconds>]
  move [<terminal>] <desktop> [--json]
  close <terminal> [--desktop <ref>]

Run inside an attn agent. A target is a terminal id or unique terminal-id
prefix printed by ls. A session id/prefix or the name of a crew member in session also
works when that session shows in exactly one terminal; otherwise choose a terminal id.
All targets belong to the caller's profile.
Reads and moves reach every desktop; typing and closing on another desktop
require --desktop. Any agent may type into or move a shell. Typing into or
moving agents requires self, a transitive delegation chain, or the profile's
chief. Close agents with attn agent close instead.

Move defaults to your session. <desktop> is a shortcut digit, its name as shown
(case-insensitive), or its id. A missing numbered desktop is created. The
terminal keeps running beside the destination's active tile. The user's current
desktop stays put; a moved active tile becomes active at its destination only
when that desktop is not current. Move yourself and a shell with two calls:
  attn terminal move 8
  attn terminal move <shell> 8

Open defaults to your session's machine and directory. --host selects an
outpost by name or id; --host home selects the home. When the machine changes,
omitting --cwd starts in that machine's home directory. --cwd names a directory
on the target machine.
Claude, Codex and shells run on outposts. An agent's attn commands reach the
home. Spawning chief, crew and plugin agents on outposts is unavailable.

Open preserves focus. A terminal goes to the end of the desktop's row and shows
if it fits. --down stacks it below you, or below the terminal you most recently
opened in your column. --desktop opens it on that desktop; with --down it stacks
below that desktop's active tile.
A missing numbered --desktop is created; ls, typing, and closing require an
existing desktop. Open with a command requires OSC 133: known unsupported
shells are refused before opening. First-prompt wait limit=30s (measured 9-176ms in fish/zsh/bash).
On timeout the terminal stays open, nothing is typed later; use send there.
Open without a command for shells without integration.
Commands run through the shell, preserving history. run requires a prompt;
--wait and wait --until prompt require OSC 133 shell integration. --wait reads
the next completed block and exits with its exit code. Avoid concurrent writers
to that shell when attributing output to a command. A timeout exits 124;
without --timeout the wait lasts until completion or the caller disconnects.

Keys: C-a through C-z, C-\\, C-], C-^, C-_, Escape, Enter, Tab, Backspace,
Space, Up, Down, Left, Right, Home, End, Delete, Insert, PageUp, PageDown,
F1 through F12. send writes bytes directly, as a keyboard does.
"#;

const EXIT_USAGE: u8 = 2;
const EXIT_TIMEOUT: u8 = 124;
const WAIT_FIELD: &str = "terminal_wait_result";
const INPUT_FIELD: &str = "terminal_input_result";
const ENTRIES_FIELD: &str = "terminal_entries";
const OPEN_FIELD: &str = "terminal_open_result";
const MOVE_FIELD: &str = "terminal_move_result";
const READ_FIELD: &str = "terminal_read_result";
const KEY_NAMES: &[(&str, &str)] = &[
    ("escape", "\x1b"),
    ("enter", "\r"),
    ("tab", "\t"),
    ("backspace", "\x7f"),
    ("space", " "),
    ("up", "\x1b[A"),
    ("down", "\x1b[B"),
    ("right", "\x1b[C"),
    ("left", "\x1b[D"),
    ("home", "\x1b[H"),
    ("end", "\x1b[F"),
    ("insert", "\x1b[2~"),
    ("delete", "\x1b[3~"),
    ("pageup", "\x1b[5~"),
    ("pagedown", "\x1b[6~"),
    ("f1", "\x1bOP"),
    ("f2", "\x1bOQ"),
    ("f3", "\x1bOR"),
    ("f4", "\x1bOS"),
    ("f5", "\x1b[15~"),
    ("f6", "\x1b[17~"),
    ("f7", "\x1b[18~"),
    ("f8", "\x1b[19~"),
    ("f9", "\x1b[20~"),
    ("f10", "\x1b[21~"),
    ("f11", "\x1b[23~"),
    ("f12", "\x1b[24~"),
];

fn action_flags(action: &str) -> Option<&'static str> {
    Some(match action {
        "ls" => "--desktop --all --json",
        "open" => "--host --down --desktop --cwd --json",
        "run" => "--wait --timeout --desktop --json",
        "send" => "--text --keys --desktop",
        "read" => "--last --scrollback --lines --json",
        "wait" => "--until --timeout --json",
        "close" => "--desktop",
        "move" => "--json",
        _ => return None,
    })
}

#[derive(Default)]
#[allow(clippy::struct_excessive_bools)]
struct Args {
    action: String,
    positional: Vec<String>,
    desktop: String,
    cwd: String,
    text: String,
    until: String,
    host: String,
    keys: Vec<String>,
    command: String,
    down: bool,
    all: bool,
    json: bool,
    wait: bool,
    last: bool,
    scrollback: bool,
    has_text: bool,
    has_keys: bool,
    lines: i64,
    timeout: Option<Duration>,
    seen: Vec<String>,
}

fn shell_command(words: &[String]) -> String {
    if let [only] = words {
        return only.clone();
    }
    words
        .iter()
        .map(|word| format!("'{}'", word.replace('\'', "'\\''")))
        .collect::<Vec<_>>()
        .join(" ")
}

fn parse_timeout(value: &str) -> Result<Duration, String> {
    match duration::parse(&format!("{value}s")) {
        Ok(nanos) if nanos > 0 => Ok(Duration::from_nanos(nanos.unsigned_abs())),
        _ => Err(format!(
            "--timeout must be positive seconds; got {}",
            quote(value.as_bytes())
        )),
    }
}

fn parse_lines(value: &str) -> Result<i64, String> {
    match value.parse::<i64>() {
        Ok(lines) if lines > 0 => Ok(lines),
        _ => Err(format!(
            "--lines must be positive; got {}",
            quote(value.as_bytes())
        )),
    }
}

impl Args {
    fn apply_value(&mut self, flag: &str, value: &str) -> Result<(), String> {
        match flag {
            "--host" => self.host = value.to_string(),
            "--desktop" => self.desktop = value.to_string(),
            "--cwd" => self.cwd = value.to_string(),
            "--text" => {
                self.text = value.to_string();
                self.has_text = true;
            }
            "--until" => self.until = value.to_string(),
            "--timeout" => self.timeout = Some(parse_timeout(value)?),
            _ => self.lines = parse_lines(value)?,
        }
        Ok(())
    }

    fn apply_switch(&mut self, flag: &str) -> bool {
        match flag {
            "--down" => self.down = true,
            "--all" => self.all = true,
            "--json" => self.json = true,
            "--wait" => self.wait = true,
            "--last" => self.last = true,
            "--scrollback" => self.scrollback = true,
            _ => return false,
        }
        true
    }

    fn parse_words(&mut self, run: &mut Run<'_>, args: &[String]) -> Result<(), String> {
        let mut index = 1;
        while index < args.len() {
            let arg = &args[index];
            if arg == "--" {
                if self.action != "open" {
                    return Err("-- command is only for open".to_string());
                }
                self.command = shell_command(&args[index + 1..]);
                break;
            }
            if !arg.starts_with("--") {
                self.positional.push(arg.clone());
                index += 1;
                continue;
            }
            self.seen.push(arg.clone());
            if !self.apply_switch(arg) {
                self.parse_flag(run, args, &mut index)?;
            }
            index += 1;
        }
        Ok(())
    }

    fn parse_flag(
        &mut self,
        run: &mut Run<'_>,
        args: &[String],
        index: &mut usize,
    ) -> Result<(), String> {
        let arg = &args[*index];
        match arg.as_str() {
            "--host" | "--desktop" | "--cwd" | "--text" | "--until" | "--timeout" | "--lines" => {
                *index += 1;
                let value = args
                    .get(*index)
                    .ok_or_else(|| format!("{arg} needs a value"))?;
                self.apply_value(arg, value)
            }
            "--keys" => {
                self.has_keys = true;
                while let Some(key) = args.get(*index + 1).filter(|key| !key.starts_with("--")) {
                    self.keys.push(key.clone());
                    *index += 1;
                }
                if self.keys.is_empty() {
                    return Err("--keys needs at least one key".to_string());
                }
                Ok(())
            }
            _ => {
                run.usage_error = Some(USAGE_UNKNOWN_FLAG);
                Err(format!("unknown flag {arg}"))
            }
        }
    }

    fn validate(&self) -> Result<(), String> {
        let valid = action_flags(&self.action).unwrap_or_default();
        if let Some(flag) = self
            .seen
            .iter()
            .find(|flag| !valid.split(' ').any(|known| known == flag.as_str()))
        {
            return Err(format!("{} does not accept {flag}", self.action));
        }
        let want = match self.action.as_str() {
            "ls" | "open" => 0,
            "run" => 2,
            "move" if self.positional.len() == 2 => 2,
            _ => 1,
        };
        if self.positional.len() != want {
            return Err(format!(
                "{} requires {want} positional arguments, got {}",
                self.action,
                self.positional.len()
            ));
        }
        if self.all && !self.desktop.is_empty() {
            return Err("choose --all or --desktop".to_string());
        }
        if self.action == "send" && self.has_text == self.has_keys {
            return Err("send needs exactly one of --text or --keys".to_string());
        }
        if self.last && self.scrollback {
            return Err("choose --last or --scrollback".to_string());
        }
        if self.lines > 0 && !self.scrollback {
            return Err("--lines requires --scrollback".to_string());
        }
        if self.action == "wait" && self.until != "prompt" && self.until != "exit" {
            return Err("--until must be prompt or exit".to_string());
        }
        Ok(())
    }
}

fn parse(run: &mut Run<'_>, args: &[String]) -> Result<Args, String> {
    let mut parsed = Args {
        action: args[0].clone(),
        ..Args::default()
    };
    if action_flags(&parsed.action).is_some() {
        run.path = format!("terminal {}", parsed.action);
    }
    parsed.parse_words(run, args)?;
    if action_flags(&parsed.action).is_none() {
        run.path = "terminal".to_string();
        run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
        return Err(format!(
            "unknown terminal command {}",
            quote(parsed.action.as_bytes())
        ));
    }
    parsed.validate()?;
    Ok(parsed)
}

fn key_bytes(keys: &[String]) -> Result<String, String> {
    let mut out = String::new();
    for key in keys {
        let lower = key.to_lowercase();
        if let Some((_, value)) = KEY_NAMES.iter().find(|(name, _)| *name == lower) {
            out.push_str(value);
            continue;
        }
        let bytes = lower.as_bytes();
        if bytes.len() == 3 && lower.starts_with("c-") {
            let control = bytes[2];
            if control.is_ascii_lowercase() {
                out.push(char::from(control - b'a' + 1));
                continue;
            }
            if (b'\\'..=b'_').contains(&control) {
                out.push(char::from(control & 31));
                continue;
            }
        }
        return Err(format!(
            "unknown key {}; attn terminal --help lists supported keys",
            quote(key.as_bytes())
        ));
    }
    Ok(out)
}

fn some_text(value: &str) -> Option<String> {
    (!value.is_empty()).then(|| value.to_string())
}

struct Terminal<'a> {
    client: Client<'a>,
    caller: String,
    deadline: Option<Instant>,
}

impl Terminal<'_> {
    fn send<M: Serialize>(&self, message: &M) -> Result<Reply, SendError> {
        self.client.call_until(message, self.deadline)
    }

    fn list(&self, args: &Args) -> Result<Reply, SendError> {
        let mut message = TerminalListMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.desktop = some_text(&args.desktop);
        message.all = Some(args.all);
        self.send(&message)
    }

    fn open(&self, args: &Args) -> Result<Reply, SendError> {
        let mut message = TerminalOpenMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.host = some_text(&args.host);
        message.desktop = some_text(&args.desktop);
        message.direction = Some(if args.down { "down" } else { "right" }.to_string());
        message.cwd = some_text(&args.cwd);
        message.command = some_text(&args.command);
        self.send(&message)
    }

    fn move_terminal(&self, args: &Args) -> Result<Reply, SendError> {
        let mut message = TerminalMoveMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.target = if args.positional.len() == 2 {
            args.positional[0].clone()
        } else {
            self.caller.clone()
        };
        message
            .desktop
            .clone_from(&args.positional[args.positional.len() - 1]);
        self.send(&message)
    }

    fn input(&self, args: &Args) -> Result<Reply, SendError> {
        let run = args.action == "run";
        let data = if run {
            format!("{}\r", args.positional[1])
        } else if args.has_keys {
            key_bytes(&args.keys).map_err(SendError::plain)?
        } else {
            args.text.clone()
        };
        let mut message = TerminalInputMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.target.clone_from(&args.positional[0]);
        message.desktop = some_text(&args.desktop);
        message.data = data;
        message.run = Some(run);
        message.wait = Some(args.wait);
        self.send(&message)
    }

    fn read(&self, args: &Args) -> Result<Reply, SendError> {
        let mut message = TerminalReadMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.target.clone_from(&args.positional[0]);
        message.mode = if args.scrollback {
            "scrollback"
        } else if args.last {
            "last"
        } else {
            "screen"
        }
        .to_string();
        message.lines = Some(args.lines);
        self.send(&message)
    }

    fn wait(&self, target: &str, until: &str, after: Option<String>) -> Result<Reply, SendError> {
        let mut message = TerminalWaitMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.target = target.to_string();
        message.until = until.to_string();
        message.after_block_id = after;
        self.send(&message)
    }

    fn close(&self, args: &Args) -> Result<Reply, SendError> {
        let mut message = TerminalCloseMessage::new();
        message.caller_session_id.clone_from(&self.caller);
        message.target.clone_from(&args.positional[0]);
        message.desktop = some_text(&args.desktop);
        self.send(&message)
    }

    fn dispatch(&self, args: &Args) -> Result<Reply, SendError> {
        let reply = match args.action.as_str() {
            "ls" => self.list(args),
            "open" => self.open(args),
            "move" => self.move_terminal(args),
            "send" | "run" => self.input(args),
            "read" => self.read(args),
            "wait" => self.wait(&args.positional[0], &args.until, None),
            _ => self.close(args),
        }?;
        if args.action != "run" || !args.wait {
            return Ok(reply);
        }
        let input = reply.value::<Option<TerminalInputResult>>(INPUT_FIELD)?;
        let Some((terminal, Some(block))) =
            input.map(|result| (result.terminal_id, result.last_block_id))
        else {
            return Err(SendError::plain(
                "daemon omitted last_block_id for run --wait".to_string(),
            ));
        };
        self.wait(&terminal, "prompt", Some(block))
    }
}

fn json_field(args: &Args) -> Option<&'static str> {
    Some(match args.action.as_str() {
        "ls" => ENTRIES_FIELD,
        "open" => OPEN_FIELD,
        "move" => MOVE_FIELD,
        "read" => READ_FIELD,
        "run" if !args.wait => INPUT_FIELD,
        "run" | "wait" => WAIT_FIELD,
        _ => return None,
    })
}

fn render_table(entries: &[TerminalEntry]) -> String {
    let mut table = String::from("ID\tNAME\tAGENT\tSTATE\tDESKTOP\tPOSITION\tOPENED BY\n");
    for entry in entries {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}\t{}\t{}\t{}",
            entry.short_id,
            entry.name,
            entry.agent,
            entry.state,
            entry.desktop_label,
            entry.position.as_deref().unwrap_or_default(),
            entry.opened_by.as_deref().unwrap_or_default(),
        );
    }
    tabwriter::format(&table)
}

fn required<T: DeserializeOwned>(reply: &Reply, field: &str) -> Result<T, SendError> {
    reply
        .value::<Option<T>>(field)?
        .ok_or_else(|| SendError::plain(format!("daemon omitted {field}")))
}

fn render_plain(reply: &Reply, args: &Args) -> Result<(), SendError> {
    match args.action.as_str() {
        "ls" => {
            let entries = reply
                .value::<Option<Vec<TerminalEntry>>>(ENTRIES_FIELD)?
                .unwrap_or_default();
            print_stdout(&render_table(&entries));
        }
        "open" => {
            let result: TerminalOpenResult = required(reply, OPEN_FIELD)?;
            print_stdout(&format!("{}\n", result.terminal_id));
        }
        "move" => {
            let result: TerminalMoveResult = required(reply, MOVE_FIELD)?;
            let outcome = if result.unchanged.unwrap_or(false) {
                "already is on"
            } else {
                "moved to"
            };
            print_stdout(&format!(
                "{} {outcome} desktop {}\n",
                result.terminal_id, result.desktop_id
            ));
        }
        "read" => {
            let result: TerminalReadResult = required(reply, READ_FIELD)?;
            print_stdout(&format!("{}\n", result.text));
        }
        "wait" | "run" => {
            if let Some(TerminalWaitResult {
                block: Some(block), ..
            }) = reply.value::<Option<TerminalWaitResult>>(WAIT_FIELD)?
            {
                print_stdout(&format!("{}\n", block.output));
            }
        }
        _ => {}
    }
    Ok(())
}

fn exit_status(reply: &Reply) -> Result<i64, SendError> {
    let Some(result) = reply.value::<Option<TerminalWaitResult>>(WAIT_FIELD)? else {
        return Ok(0);
    };
    if let Some(block) = result.block {
        return block
            .exit_code
            .ok_or_else(|| SendError::plain("command finished without an exit code".to_string()));
    }
    if result.exited.unwrap_or(false) {
        return Ok(result.exit_code.unwrap_or(0));
    }
    Ok(0)
}

fn execute(terminal: &Terminal<'_>, args: &Args) -> Result<i64, SendError> {
    let reply = terminal.dispatch(args)?;
    if args.json {
        if let Some(field) = json_field(args) {
            print_stdout(&format!("{}\n", reply.raw_field(&[field])?));
        }
    } else {
        render_plain(&reply, args)?;
    }
    exit_status(&reply)
}

fn error_text(error: &SendError) -> String {
    let message = daemon_message(error);
    match message.strip_prefix("receive response: ") {
        Some(cause) => format!("terminal command: {cause}"),
        None => message,
    }
}

fn wants_help(args: &[String]) -> bool {
    let before_command = args.split(|arg| arg == "--").next().unwrap_or_default();
    args.is_empty()
        || matches!(args, [only] if only == "help")
        || before_command
            .iter()
            .any(|arg| arg == "--help" || arg == "-h")
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if wants_help(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let parsed = match parse(run, args) {
        Ok(parsed) => parsed,
        Err(message) => {
            eprint!("terminal: {message}\n{HELP}");
            return Err(Stop(EXIT_USAGE));
        }
    };
    let caller = current_session_or_exit(run)?;
    let terminal = Terminal {
        client: client(run),
        caller,
        deadline: parsed.timeout.map(|timeout| Instant::now() + timeout),
    };
    match execute(&terminal, &parsed) {
        Ok(0) => Ok(()),
        Ok(code) => Err(Stop(u8::try_from(code & 0xff).unwrap_or(1))),
        Err(error) => {
            run.refusal.clone_from(&error.refusal);
            eprintln!("terminal: {}", error_text(&error));
            let code = if error.deadline_exceeded() {
                EXIT_TIMEOUT
            } else {
                1
            };
            Err(Stop(code))
        }
    }
}
