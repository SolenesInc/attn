use crate::client::{Client, Reply, SendError};
use crate::commands::{Run, Stop};
use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet, Value as FlagValue};
use attn::go::quote::quote;
use attn_protocol::{CommandRefusal, RunCommandMessage};
use serde_json::{Map, Value as Json};
use std::io::Read;
use std::sync::Once;
use std::time::Instant;

static STALE_PROTOCOL_PROBE: Once = Once::new();

pub enum Cli {
    Pos,
    Rest,
    Flag(&'static str),
    Choice(&'static [(&'static str, &'static str)]),
    SplitRest,
    Words(&'static [&'static str]),
    Internal,
    Shares(&'static str, &'static str),
    AfterDashes,
    FlagWords(&'static str),
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Value {
    Text,
    Int,
    Bool,
    Json,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Source {
    Given,
    Stdin,
    File,
    FileOrStdin,
}

pub struct Arg {
    pub name: &'static str,
    pub cli: Cli,
    pub value: Value,
    pub source: Source,
    pub required: bool,
    pub list: bool,
}

pub struct Command {
    pub name: &'static str,
    pub result: bool,
    pub runs_without_daemon: bool,
    pub usage: &'static [&'static str],
    pub args: &'static [Arg],
}

pub const CODE_USAGE: &str = "usage";
const CODE_NO_SESSION: &str = "no_session";

pub fn unresolved_session(message: &str) -> Stop {
    let shown = message.split(';').next().unwrap_or(message);
    eprintln!("attn: resolve current session: {shown}");
    Stop(1)
}
const CODE_TIMEOUT: &str = "timeout";
const EXIT_TIMEOUT: u8 = 124;

pub trait Family {
    fn group(&self) -> &'static str;
    fn flags(&self) -> &'static [&'static str];
    fn commands(&self) -> &'static [Command];
    fn group_help(&self) -> &'static str;
    fn help(&self, verb: &str) -> &'static str;
    fn usage(&self, verb: &str) -> &'static str;
    fn label(&self, verb: &str) -> String {
        if verb.is_empty() {
            self.group().to_string()
        } else {
            format!("{} {verb}", self.group())
        }
    }
    fn usage_error(&self, verb: &str, message: &str) -> Stop {
        let label = self.label(verb);
        eprintln!("{label}: {message}");
        eprintln!(
            "usage: attn {} {} (attn {label} --help)",
            self.group(),
            self.usage(verb)
        );
        Stop(2)
    }
    fn fail(&self, verb: &str, message: &str) -> Stop {
        eprintln!("{}: {message}", self.label(verb));
        Stop(1)
    }
    fn error_exit(&self) -> Stop {
        Stop(1)
    }
    fn refused_line(&self, verb: &str, message: &str) -> String {
        format!("{}: daemon error: {message}", self.label(verb))
    }
    fn no_session(&self, verb: &str, message: &str) -> Stop {
        self.fail(verb, message)
    }
    fn answered_here(&self, _cmd: &Command) -> Option<&'static str> {
        None
    }
    fn decode_file(&self, _cmd: &Command, _arg: &Arg, bytes: &[u8]) -> Result<Json, String> {
        serde_json::from_slice(bytes).map_err(|error| error.to_string())
    }
    fn read_stdin_error(&self, _arg: &Arg) -> &'static str {
        "reading stdin"
    }
    fn choice_conflict(&self, _cmd: &Command, arg: &Arg) -> String {
        format!("{} takes one flag", arg.name)
    }
    fn before_send(&self, _cmd: &Command, _parsed: &mut Parsed) -> Result<(), Stop> {
        Ok(())
    }
    fn deadline(&self, _cmd: &Command, _parsed: &Parsed) -> Option<Instant> {
        None
    }
    fn exchange(
        &self,
        _run: &mut Run<'_>,
        _cmd: &Command,
        _parsed: &Parsed,
    ) -> Option<Result<(), Stop>> {
        None
    }
    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop>;
    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        print_result(run, self, cmd, &result(reply))
    }
}

pub fn run_group(run: &mut Run<'_>, family: &dyn Family, args: &[String]) -> Result<(), Stop> {
    let group = family.group();
    let first = args.first().map(String::as_str);
    if first.is_some_and(|word| ["-h", "--help"].contains(&word)) {
        crate::commands::print_stdout(family.group_help());
        return Ok(());
    }
    let verb = first.and_then(|word| {
        family
            .commands()
            .iter()
            .find(|cmd| !cmd.name.is_empty() && cmd.name == word)
    });
    let bare = family.commands().iter().find(|cmd| cmd.name.is_empty());
    let (cmd, words) = match (verb, bare, first) {
        (Some(cmd), _, _) => (cmd, &args[1..]),
        (None, Some(cmd), _) => (cmd, args),
        (None, None, None) => {
            crate::commands::print_stdout(family.group_help());
            return Ok(());
        }
        (None, None, Some(word)) => {
            run.path = group.to_string();
            run.usage_error = Some(crate::commands::USAGE_UNKNOWN_COMMAND);
            eprintln!("{group}: unknown command {}", quote(word.as_bytes()));
            eprintln!("usage: attn {group} <command> (attn {group} --help)");
            return Err(Stop(2));
        }
    };
    run.path = family.label(cmd.name);
    run_command(run, family, cmd, words)
}

pub struct Parsed {
    pub args: Map<String, Json>,
    pub json: bool,
    pub profile: String,
    pub session: String,
}

impl Parsed {
    pub fn text(&self, name: &str) -> &str {
        self.args.get(name).and_then(Json::as_str).unwrap_or("")
    }

    pub fn on(&self, name: &str) -> bool {
        self.args.get(name).and_then(Json::as_bool).unwrap_or(false)
    }

    pub fn int(&self, name: &str) -> Option<i64> {
        self.args.get(name).and_then(Json::as_i64)
    }

    pub fn has(&self, name: &str) -> bool {
        self.args.contains_key(name)
    }
}

enum ParseStop {
    Help,
    Flag(String),
    Usage(String),
    Fail(String),
}

fn flag_default(arg: &Arg) -> FlagDefault<'static> {
    match (arg.list, arg.value) {
        (true, _) => FlagDefault::Repeated,
        (_, Value::Bool) => FlagDefault::Bool(false),
        (_, Value::Int) => FlagDefault::Int(0),
        _ => FlagDefault::String(""),
    }
}

pub fn flags_of(group_flags: &[&'static str], cmd: &Command) -> Vec<Flag<'static>> {
    let mut flags: Vec<Flag<'static>> = group_flags
        .iter()
        .map(|name| Flag {
            name,
            default: FlagDefault::String(""),
            usage: "",
        })
        .collect();
    if cmd.result {
        flags.push(Flag {
            name: "json",
            default: FlagDefault::Bool(false),
            usage: "",
        });
    }
    for arg in cmd.args {
        match arg.cli {
            Cli::Flag(name) => flags.push(Flag {
                name,
                default: flag_default(arg),
                usage: "",
            }),
            Cli::Choice(choices) => {
                for (name, _) in choices {
                    flags.push(Flag {
                        name,
                        default: FlagDefault::Bool(false),
                        usage: "",
                    });
                }
            }
            _ => {}
        }
    }
    flags
}

fn positive_number(raw: &str) -> Option<i64> {
    let digits = raw.strip_prefix('v').unwrap_or(raw);
    let unsigned = digits.strip_prefix(['+', '-']).unwrap_or(digits);
    if unsigned.is_empty() || !unsigned.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    digits.parse::<i64>().ok().filter(|number| *number >= 1)
}

fn wrong_args(cmd: &Command, positionals: &[String]) -> String {
    let got = if positionals.is_empty() {
        "none".to_string()
    } else {
        let quoted: Vec<String> = positionals
            .iter()
            .map(|word| quote(word.as_bytes()))
            .collect();
        format!("{}: {}", positionals.len(), quoted.join(" "))
    };
    if let Some(rest) = cmd
        .args
        .iter()
        .find(|arg| matches!(arg.cli, Cli::Rest | Cli::Words(_)))
    {
        return format!("needs {}, got {got}", rest.name);
    }
    let pos: Vec<&Arg> = cmd
        .args
        .iter()
        .filter(|arg| matches!(arg.cli, Cli::Pos))
        .collect();
    let required = pos.iter().filter(|arg| arg.required).count();
    let takes = match (required, pos.len()) {
        (_, 0) => return format!("takes no arguments, got {got}"),
        (1, 1) => "1 argument".to_string(),
        (0, 1) => "at most 1 argument".to_string(),
        (low, high) if low == high => format!("{high} arguments"),
        (low, high) if low + 1 == high => format!("{low} or {high} arguments"),
        (low, high) => format!("{low} to {high} arguments"),
    };
    let names: Vec<String> = pos
        .iter()
        .map(|arg| {
            if arg.required {
                arg.name.to_string()
            } else {
                format!("[{}]", arg.name)
            }
        })
        .collect();
    format!("takes {takes} ({}), got {got}", names.join(", "))
}

fn flag_mention(name: &str) -> String {
    if name.len() == 1 {
        format!("-{name}")
    } else {
        format!("--{name}")
    }
}

fn mention(cmd: &Command, name: &str) -> Option<String> {
    if name.starts_with("attn ") {
        return Some(name.to_string());
    }
    if ["profile", "session", "json"].contains(&name) {
        return Some(flag_mention(name));
    }
    let arg = cmd.args.iter().find(|arg| arg.name == name)?;
    match arg.cli {
        Cli::Flag(flag) | Cli::FlagWords(flag) => Some(flag_mention(flag)),
        Cli::Choice(choices) => Some(
            choices
                .iter()
                .map(|(flag, _)| flag_mention(flag))
                .collect::<Vec<_>>()
                .join(" or "),
        ),
        _ => None,
    }
}

pub fn render_mentions(cmd: &Command, text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(open) = rest.find('{') {
        out.push_str(&rest[..open]);
        let after = &rest[open + 1..];
        let rendered = after
            .find('}')
            .and_then(|close| Some((close, mention(cmd, &after[..close])?)));
        if let Some((close, flag)) = rendered {
            out.push_str(&flag);
            rest = &after[close + 1..];
        } else {
            out.push('{');
            rest = after;
        }
    }
    out.push_str(rest);
    out
}

fn read_stdin(what: &str) -> Result<Vec<u8>, String> {
    let mut raw = Vec::new();
    std::io::stdin()
        .read_to_end(&mut raw)
        .map_err(|error| format!("{what}: {}", crate::paths::os_error_text(&error)))?;
    Ok(raw)
}

fn parse(family: &dyn Family, cmd: &Command, argv: &[String]) -> Result<Parsed, ParseStop> {
    let definitions = flags_of(family.flags(), cmd);
    let mut fs = FlagSet::new("", &definitions);
    let (argv, taken) = take_word_args(cmd, &definitions, argv)?;
    let positional_slots = cmd
        .args
        .iter()
        .filter(|arg| matches!(arg.cli, Cli::Pos))
        .count();
    let positionals = split_positionals(&mut fs, &argv, positional_slots)?;
    let mut values = positional_values(cmd, &positionals)?;
    values.extend(taken);
    flag_values(family, cmd, &fs, &mut values)?;
    read_inputs(family, cmd, &mut values)?;
    split_text(cmd, &mut values);
    Ok(Parsed {
        args: values,
        json: cmd.result && fs.bool("json"),
        profile: group_flag(family, &fs, "profile"),
        session: [
            group_flag(family, &fs, "session"),
            group_flag(family, &fs, "source-session"),
        ]
        .into_iter()
        .find(|session| !session.is_empty())
        .unwrap_or_default(),
    })
}

fn takes_value(definitions: &[Flag<'_>], word: &str) -> bool {
    let name = word.trim_start_matches('-');
    word.starts_with('-')
        && !name.contains('=')
        && definitions
            .iter()
            .any(|flag| flag.name == name && !matches!(flag.default, FlagDefault::Bool(_)))
}

fn flag_values_at(definitions: &[Flag<'_>], argv: &[String]) -> Vec<bool> {
    let mut value = vec![false; argv.len()];
    let mut index = 0;
    while index < argv.len() {
        if argv[index] == "--" {
            break;
        }
        if takes_value(definitions, &argv[index]) && index + 1 < argv.len() {
            value[index + 1] = true;
            index += 1;
        }
        index += 1;
    }
    value
}

fn take_word_args(
    cmd: &Command,
    definitions: &[Flag<'_>],
    argv: &[String],
) -> Result<(Vec<String>, Map<String, Json>), ParseStop> {
    let mut taken = Map::new();
    let mut argv = argv.to_vec();
    let is_value = flag_values_at(definitions, &argv);
    if let Some(arg) = cmd
        .args
        .iter()
        .find(|arg| matches!(arg.cli, Cli::AfterDashes))
        && let Some(at) = argv
            .iter()
            .enumerate()
            .position(|(index, word)| word == "--" && !is_value[index])
    {
        let words: Vec<Json> = argv.split_off(at)[1..]
            .iter()
            .map(|word| Json::from(word.clone()))
            .collect();
        taken.insert(arg.name.to_string(), Json::from(words));
    }
    for arg in cmd.args {
        let Cli::FlagWords(flag) = arg.cli else {
            continue;
        };
        let spelled = format!("--{flag}");
        let is_value = flag_values_at(definitions, &argv);
        let mut words = Vec::new();
        let mut kept = Vec::new();
        let mut rest = argv.into_iter().enumerate().peekable();
        while let Some((index, word)) = rest.next() {
            if word != spelled || is_value[index] {
                kept.push(word);
                continue;
            }
            let before = words.len();
            while let Some((_, next)) = rest.next_if(|(_, next)| !next.starts_with("--")) {
                words.push(Json::from(next));
            }
            if words.len() == before {
                return Err(ParseStop::Usage(format!(
                    "{spelled} needs at least one word"
                )));
            }
        }
        argv = kept;
        if !words.is_empty() {
            taken.insert(arg.name.to_string(), Json::from(words));
        }
    }
    Ok((argv, taken))
}

fn group_flag(family: &dyn Family, fs: &FlagSet<'_>, name: &str) -> String {
    if family.flags().contains(&name) {
        fs.string(name).trim().to_string()
    } else {
        String::new()
    }
}

fn split_positionals(
    fs: &mut FlagSet<'_>,
    argv: &[String],
    slots: usize,
) -> Result<Vec<String>, ParseStop> {
    let mut positionals = Vec::new();
    let mut rest = argv.to_vec();
    loop {
        match fs.parse(&rest) {
            Ok(()) => {}
            Err(FlagError::Help) => return Err(ParseStop::Help),
            Err(FlagError::Invalid(message)) => return Err(ParseStop::Flag(message)),
        }
        let parsed = rest.len() - fs.args().len();
        let terminated = parsed > 0 && rest[parsed - 1] == "--";
        rest = fs.args().to_vec();
        if terminated {
            let taken = slots.saturating_sub(positionals.len()).min(rest.len());
            positionals.extend(rest.drain(..taken));
            continue;
        }
        if rest.is_empty() {
            return Ok(positionals);
        }
        positionals.push(rest.remove(0));
    }
}

fn positional_values(
    cmd: &Command,
    positionals: &[String],
) -> Result<Map<String, Json>, ParseStop> {
    let mut values = Map::new();
    if let Some((arg, fields)) = cmd.args.iter().find_map(|arg| match arg.cli {
        Cli::Words(fields) => Some((arg, fields)),
        _ => None,
    }) {
        values.insert(arg.name.to_string(), word_items(cmd, positionals, fields)?);
        return Ok(values);
    }
    if let Some(arg) = cmd.args.iter().find(|arg| matches!(arg.cli, Cli::Rest)) {
        if arg.list {
            if arg.required && positionals.is_empty() {
                return Err(ParseStop::Usage(wrong_args(cmd, positionals)));
            }
            values.insert(arg.name.to_string(), Json::from(positionals.to_vec()));
            return Ok(values);
        }
        let words = positionals.join(" ");
        if arg.required && words.trim().is_empty() {
            return Err(ParseStop::Usage(wrong_args(cmd, positionals)));
        }
        values.insert(arg.name.to_string(), Json::from(words));
        return Ok(values);
    }
    let pos: Vec<&Arg> = cmd
        .args
        .iter()
        .filter(|arg| matches!(arg.cli, Cli::Pos))
        .collect();
    let required = pos.iter().filter(|arg| arg.required).count();
    if positionals.len() < required || positionals.len() > pos.len() {
        return Err(ParseStop::Usage(wrong_args(cmd, positionals)));
    }
    for (arg, raw) in pos.iter().zip(positionals) {
        let value = if arg.value == Value::Int {
            let Some(number) = positive_number(raw) else {
                return Err(ParseStop::Usage(format!(
                    "{} {} must be a positive number, like 2 or v2",
                    arg.name,
                    quote(raw.as_bytes())
                )));
            };
            Json::from(number)
        } else {
            Json::from(raw.clone())
        };
        values.insert(arg.name.to_string(), value);
    }
    for arg in cmd.args {
        if let Cli::Shares(partner, prefix) = arg.cli
            && values
                .get(partner)
                .and_then(Json::as_str)
                .is_some_and(|word| word.trim().starts_with(prefix))
        {
            let word = values.remove(partner).expect("checked above");
            values.insert(arg.name.to_string(), word);
        }
    }
    Ok(values)
}

fn word_items(cmd: &Command, positionals: &[String], fields: &[&str]) -> Result<Json, ParseStop> {
    if positionals.is_empty() {
        return Err(ParseStop::Usage(wrong_args(cmd, positionals)));
    }
    if !positionals.len().is_multiple_of(fields.len()) {
        let last = &positionals[positionals.len() - 1];
        return Err(ParseStop::Usage(format!(
            "each {} needs its {}: {} is the last argument",
            fields[0],
            fields[fields.len() - 1],
            quote(last.as_bytes())
        )));
    }
    Ok(Json::from(
        positionals
            .chunks(fields.len())
            .map(|words| {
                fields
                    .iter()
                    .zip(words)
                    .map(|(field, word)| ((*field).to_string(), Json::from(word.clone())))
                    .collect::<Map<String, Json>>()
            })
            .collect::<Vec<_>>(),
    ))
}

fn flag_values(
    family: &dyn Family,
    cmd: &Command,
    fs: &FlagSet<'_>,
    values: &mut Map<String, Json>,
) -> Result<(), ParseStop> {
    for arg in cmd.args {
        match arg.cli {
            Cli::Flag(name) if fs.is_set(name) => {
                let value = match fs.value(name) {
                    FlagValue::Bool(false) => continue,
                    FlagValue::Bool(true) => Json::from(true),
                    FlagValue::Int(number) => Json::from(*number),
                    FlagValue::Repeated(list) => Json::from(list.clone()),
                    other => Json::from(other.to_string()),
                };
                values.insert(arg.name.to_string(), value);
            }
            Cli::Choice(choices) => {
                let chosen: Vec<&str> = choices
                    .iter()
                    .filter(|(flag, _)| fs.is_set(flag) && fs.bool(flag))
                    .map(|(_, value)| *value)
                    .collect();
                match chosen.as_slice() {
                    [] => {}
                    [only] => {
                        values.insert(arg.name.to_string(), Json::from(*only));
                    }
                    _ => return Err(ParseStop::Fail(family.choice_conflict(cmd, arg))),
                }
            }
            _ => {}
        }
    }
    Ok(())
}

fn read_inputs(
    family: &dyn Family,
    cmd: &Command,
    values: &mut Map<String, Json>,
) -> Result<(), ParseStop> {
    for arg in cmd.args {
        let given = values
            .get(arg.name)
            .and_then(Json::as_str)
            .map(str::to_string);
        match arg.source {
            Source::File | Source::FileOrStdin => {
                let path = given.unwrap_or_default();
                let path = path.trim();
                if path.is_empty() && matches!(arg.source, Source::File) {
                    continue;
                }
                let bytes = if path.is_empty() || path == "-" {
                    read_stdin(family.read_stdin_error(arg))
                } else {
                    read_file(path)
                }
                .map_err(ParseStop::Fail)?;
                let value = family
                    .decode_file(cmd, arg, &bytes)
                    .map_err(ParseStop::Fail)?;
                values.insert(arg.name.to_string(), value);
            }
            Source::Stdin if given.as_deref() == Some("-") => {
                let bytes = read_stdin("reading stdin").map_err(ParseStop::Fail)?;
                let text = String::from_utf8_lossy(&bytes).into_owned();
                if text.trim().is_empty() {
                    return Err(ParseStop::Usage(
                        "-m - read nothing from stdin; provide nonempty text".to_string(),
                    ));
                }
                values.insert(arg.name.to_string(), Json::from(text));
            }
            _ => {}
        }
    }
    Ok(())
}

fn split_text(cmd: &Command, values: &mut Map<String, Json>) {
    let Some(detail) = cmd
        .args
        .iter()
        .find(|arg| matches!(arg.cli, Cli::SplitRest))
    else {
        return;
    };
    let Some(head) = cmd
        .args
        .iter()
        .find(|arg| matches!(arg.cli, Cli::Flag("m")))
    else {
        return;
    };
    let Some(text) = values
        .get(head.name)
        .and_then(Json::as_str)
        .map(str::to_string)
    else {
        return;
    };
    if let Some((first, more)) = text.split_once('\n') {
        values.insert(head.name.to_string(), Json::from(first));
        values.insert(detail.name.to_string(), Json::from(more));
    }
}

fn read_file(path: &str) -> Result<Vec<u8>, String> {
    let fail = |op: &str, error: &std::io::Error| {
        format!("{op} {path}: {}", crate::paths::os_error_text(error))
    };
    let mut file = std::fs::File::open(path).map_err(|error| fail("open", &error))?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)
        .map_err(|error| fail("read", &error))?;
    Ok(bytes)
}

pub fn run_command(
    run: &mut Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    argv: &[String],
) -> Result<(), Stop> {
    let mut parsed = match parse(family, cmd, argv) {
        Ok(parsed) => parsed,
        Err(ParseStop::Help) => {
            crate::commands::print_stdout(family.help(cmd.name));
            return Err(Stop(0));
        }
        Err(ParseStop::Flag(message)) => {
            run.record_flag_error(&message);
            return Err(family.usage_error(cmd.name, &message));
        }
        Err(ParseStop::Usage(message)) => return Err(family.usage_error(cmd.name, &message)),
        Err(ParseStop::Fail(message)) => return Err(family.fail(cmd.name, &message)),
    };
    if let Some(text) = family.answered_here(cmd) {
        crate::commands::print_stdout(text);
        return Ok(());
    }
    if parsed.session.is_empty() && run.terminal.is_empty() {
        crate::hooks::server_caller_from_env()
            .map_err(|message| family.fail(cmd.name, &message))?;
    }
    family.before_send(cmd, &mut parsed)?;
    if let Some(done) = family.exchange(run, cmd, &parsed) {
        return done;
    }
    let reply = call(run, family, cmd, &parsed)?;
    if parsed.json {
        family.present_json(run, cmd, &parsed, &reply)
    } else {
        family.present(run, cmd, &parsed, &reply)
    }
}

pub enum Answer {
    Result(Reply),
    Refused(Reply, CommandRefusal),
}

pub fn send(
    run: &Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    parsed: &Parsed,
) -> Result<Reply, SendError> {
    let mut message = RunCommandMessage::new();
    message.command = family.label(cmd.name);
    message.args = Some(parsed.args.clone().into_iter().collect());
    message.session_id = (!parsed.session.is_empty()).then(|| parsed.session.clone());
    message.profile_id = (!parsed.profile.is_empty()).then(|| parsed.profile.clone());
    message.directory = crate::commands::working_directory().ok();
    let deadline = family.deadline(cmd, parsed);
    match Client::new(run.paths, "", &run.terminal).call_command(&message, deadline) {
        Err(error) if error.unconnected && cmd.runs_without_daemon => {
            crate::client::run_in_attnd(&message)
        }
        sent => sent,
    }
}

pub fn answer(run: &Run<'_>, reply: Reply) -> Answer {
    match refusal_of(&reply) {
        Some(refusal) => {
            STALE_PROTOCOL_PROBE.call_once(|| crate::commands::warn_if_stale(run.paths));
            Answer::Refused(reply, refusal)
        }
        None => Answer::Result(reply),
    }
}

pub fn ask(
    run: &mut Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    parsed: &Parsed,
) -> Result<Answer, Stop> {
    let reply =
        send(run, family, cmd, parsed).map_err(|error| send_failed(run, family, cmd, &error))?;
    Ok(answer(run, reply))
}

pub fn call(
    run: &mut Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    parsed: &Parsed,
) -> Result<Reply, Stop> {
    match ask(run, family, cmd, parsed)? {
        Answer::Result(reply) => Ok(reply),
        Answer::Refused(reply, refusal) => Err(refuse(run, family, cmd, &reply, &refusal)),
    }
}

fn refusal_of(reply: &Reply) -> Option<CommandRefusal> {
    if reply.raw_field(&["ok"]).is_ok_and(|ok| ok == "true") {
        return None;
    }
    Some(
        reply
            .value::<Option<CommandRefusal>>("command_refusal")
            .ok()
            .flatten()
            .unwrap_or_default(),
    )
}

fn refuse(
    run: &mut Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    reply: &Reply,
    refusal: &CommandRefusal,
) -> Stop {
    let message = render_mentions(cmd, &refusal.message);
    if refusal.code == CODE_USAGE || cmd.usage.contains(&refusal.code.as_str()) {
        return family.usage_error(cmd.name, &message);
    }
    if refusal.code == CODE_NO_SESSION {
        return family.no_session(cmd.name, &message);
    }
    if refusal.code == CODE_TIMEOUT {
        family.fail(cmd.name, &message);
        return Stop(EXIT_TIMEOUT);
    }
    if reply.present("error") {
        let error = SendError::refused(
            family.refused_line(cmd.name, &reply.optional_string("error")),
            reply.optional_string("refusal"),
        );
        run.refusal.clone_from(&error.refusal);
        eprintln!("{error}");
        return family.error_exit();
    }
    family.fail(cmd.name, &message)
}

pub fn send_failed<F: Family + ?Sized>(
    run: &mut Run<'_>,
    family: &F,
    cmd: &Command,
    error: &SendError,
) -> Stop {
    if error.deadline_exceeded() {
        family.fail(cmd.name, &error.to_string());
        return Stop(EXIT_TIMEOUT);
    }
    run.fail_send(&family.label(cmd.name), error);
    family.error_exit()
}

pub fn result(reply: &Reply) -> Reply {
    reply.field("command_result")
}

pub fn decode<T: serde::de::DeserializeOwned>(
    run: &mut Run<'_>,
    family: &dyn Family,
    cmd: &Command,
    result: &Reply,
) -> Result<T, Stop> {
    result
        .whole()
        .map_err(|error| send_failed(run, family, cmd, &error))
}

pub fn print_result<F: Family + ?Sized>(
    run: &mut Run<'_>,
    family: &F,
    cmd: &Command,
    result: &Reply,
) -> Result<(), Stop> {
    let raw = result
        .raw_field(&[])
        .map_err(|error| send_failed(run, family, cmd, &error))?;
    crate::commands::print_json(raw.as_bytes());
    Ok(())
}

pub fn outlive_close_of_own_session() {
    unsafe {
        libc::signal(libc::SIGTERM, libc::SIG_IGN);
        libc::signal(libc::SIGHUP, libc::SIG_IGN);
    }
}
