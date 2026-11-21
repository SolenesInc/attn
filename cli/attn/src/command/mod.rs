use crate::client::{Client, Reply, SendError};
use crate::commands::{Run, Stop};
use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet, Value as FlagValue};
use attn::go::quote::quote;
use attn_protocol::{CommandRefusal, RunCommandMessage};
use serde_json::{Map, Value as Json};
use std::io::Read;

pub enum Cli {
    Pos,
    Rest,
    Flag(&'static str),
    Choice(&'static [(&'static str, &'static str)]),
    SplitRest,
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
    pub usage: &'static [&'static str],
    pub args: &'static [Arg],
}

pub const CODE_USAGE: &str = "usage";

pub trait Family {
    fn group(&self) -> &'static str;
    fn help(&self, verb: &str) -> &'static str;
    fn usage_error(&self, verb: &str, message: &str) -> Stop;
    fn fail(&self, verb: &str, message: &str) -> Stop {
        eprintln!("{} {verb}: {message}", self.group());
        Stop(1)
    }
    fn answered_here(&self, cmd: &Command) -> Option<&'static str>;
    fn decode_file(&self, cmd: &Command, arg: &Arg, bytes: &[u8]) -> Result<Json, String>;
    fn read_stdin_error(&self, arg: &Arg) -> &'static str;
    fn choice_conflict(&self, cmd: &Command, arg: &Arg) -> String;
    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop>;
    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop>;
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

pub fn flags_of(cmd: &Command) -> Vec<Flag<'static>> {
    let mut flags = vec![
        Flag {
            name: "profile",
            default: FlagDefault::String(""),
            usage: "",
        },
        Flag {
            name: "session",
            default: FlagDefault::String(""),
            usage: "",
        },
    ];
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
    if let Some(rest) = cmd.args.iter().find(|arg| matches!(arg.cli, Cli::Rest)) {
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
    if ["profile", "session", "json"].contains(&name) {
        return Some(flag_mention(name));
    }
    let arg = cmd.args.iter().find(|arg| arg.name == name)?;
    match arg.cli {
        Cli::Flag(flag) => Some(flag_mention(flag)),
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

fn render_mentions(cmd: &Command, text: &str) -> String {
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
    let definitions = flags_of(cmd);
    let mut fs = FlagSet::new("", &definitions);
    let positionals = split_positionals(&mut fs, argv)?;
    let mut values = positional_values(cmd, &positionals)?;
    flag_values(family, cmd, &fs, &mut values)?;
    read_inputs(family, cmd, &mut values)?;
    split_text(cmd, &mut values);
    Ok(Parsed {
        args: values,
        json: cmd.result && fs.bool("json"),
        profile: fs.string("profile").trim().to_string(),
        session: fs.string("session").trim().to_string(),
    })
}

fn split_positionals(fs: &mut FlagSet<'_>, argv: &[String]) -> Result<Vec<String>, ParseStop> {
    let mut positionals = Vec::new();
    let mut rest = argv.to_vec();
    loop {
        match fs.parse(&rest) {
            Ok(()) => {}
            Err(FlagError::Help) => return Err(ParseStop::Help),
            Err(FlagError::Invalid(message)) => return Err(ParseStop::Flag(message)),
        }
        rest = fs.args().to_vec();
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
    if let Some(arg) = cmd.args.iter().find(|arg| matches!(arg.cli, Cli::Rest)) {
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
    Ok(values)
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
            Source::File => {
                let path = given.unwrap_or_default();
                let path = path.trim();
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

pub fn run(
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
    let mut message = RunCommandMessage::new();
    message.command = format!("{} {}", family.group(), cmd.name);
    message.args = Some(parsed.args.clone().into_iter().collect());
    message.session_id = (!parsed.session.is_empty()).then(|| parsed.session.clone());
    message.profile_id = (!parsed.profile.is_empty()).then(|| parsed.profile.clone());
    let label = format!("{} {}", family.group(), cmd.name);
    let reply = Client::new(run.paths, "", &run.terminal)
        .call_command(&message)
        .map_err(|error| run.fail_send(&label, &error))?;
    if let Some(refusal) = refusal_of(&reply) {
        return Err(refuse(run, family, cmd, &label, &reply, &refusal));
    }
    family.present(run, cmd, &parsed, &reply)
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
    label: &str,
    reply: &Reply,
    refusal: &CommandRefusal,
) -> Stop {
    let message = render_mentions(cmd, &refusal.message);
    if refusal.code == CODE_USAGE || cmd.usage.contains(&refusal.code.as_str()) {
        return family.usage_error(cmd.name, &message);
    }
    if reply.present("error") {
        let error = SendError::refused(
            format!("daemon error: {}", reply.optional_string("error")),
            reply.optional_string("refusal"),
        );
        return run.fail_send(label, &error);
    }
    family.fail(cmd.name, &message)
}

pub fn result(reply: &Reply) -> Reply {
    reply.field("command_result")
}
