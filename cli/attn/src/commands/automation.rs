use super::crew::launch_desktop_text;
use super::support::{client, current_session_or_exit, parse_flags};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, print_json, print_reply_field, print_stdout};
use crate::client::{Client, Reply, SendError};
use crate::paths::os_error_text;
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::quote::quote;
use attn_protocol::go_values::LAUNCH_DESKTOP_KIND_AUTOMATION;
use attn_protocol::{
    AutomationApplyMessage, AutomationCleanupMessage, AutomationDefinitionGetMessage,
    AutomationDefinitionResultMessage, AutomationDefinitionSummary,
    AutomationDefinitionsGetMessage, AutomationDeleteMessage, AutomationRunMessage,
    AutomationRunsGetMessage, AutomationSetEnabledMessage, AutomationValidateMessage,
    LaunchDesktopSetMessage,
};
use serde::Serialize;
use std::fmt::Write as _;
use std::fs::File;
use std::io::Read;

pub const USAGE: &str = "usage: attn automation <apply|set|validate|list|show|run|runs|enable|disable|delete|cleanup> [--profile <name|id>]\n";

const LABEL: &str = "automation";
const RUN_USAGE: &str = "usage: attn automation run <definition-id> [--input-file <file> | --pr-url <url>] [--request-id <id>]";
const DESKTOP_NAME_NEEDS_DESKTOP: &str =
    "--desktop-name needs --launch-desktop own or an empty slot";
const FAILED_RUN_STATE: &str = "failed";

const fn text_flag<'a>(name: &'a str, usage: &'a str) -> Flag<'a> {
    Flag {
        name,
        default: FlagDefault::String(""),
        usage,
    }
}

const DESKTOP_NAME_FLAG: Flag<'static> = text_flag(
    "desktop-name",
    "name for the new desktop of own or an empty slot (defaults to the automation name)",
);
const FILE_FLAG: Flag<'static> = text_flag("file", "definition YAML");

fn usage_stop() -> Stop {
    eprint!("{USAGE}");
    Stop(2)
}

fn fail(message: &str) -> Stop {
    eprintln!("{LABEL}: {message}");
    Stop(1)
}

fn parse(run: &mut Run<'_>, flags: &mut FlagSet<'_>, args: &[String]) -> Result<(), Stop> {
    parse_flags(run, flags, args).map_err(|_| {
        eprint!("{}", flags.output());
        Stop(2)
    })
}

fn strip_profile(args: &[String]) -> Result<(String, Vec<String>), Stop> {
    let mut args = args.to_vec();
    let mut profile = String::new();
    let mut index = 1;
    while index < args.len() {
        if args[index] == "--profile" {
            if index + 1 == args.len() {
                eprintln!("automation: --profile needs a name or id");
                return Err(Stop(2));
            }
            profile = args[index + 1].trim().to_string();
            args.drain(index..index + 2);
        } else if let Some(value) = args[index].strip_prefix("--profile=") {
            profile = value.trim().to_string();
            args.remove(index);
        } else {
            index += 1;
        }
    }
    Ok((profile, args))
}

fn read_file(path: &str) -> Result<String, Stop> {
    let mut file = File::open(path)
        .map_err(|error| fail(&format!("open {path}: {}", os_error_text(&error))))?;
    let mut raw = Vec::new();
    file.read_to_end(&mut raw)
        .map_err(|error| fail(&format!("read {path}: {}", os_error_text(&error))))?;
    Ok(String::from_utf8_lossy(&raw).into_owned())
}

fn new_request_id() -> Result<String, Stop> {
    let mut bytes = [0_u8; 16];
    File::open("/dev/urandom")
        .and_then(|mut random| random.read_exact(&mut bytes))
        .map_err(|error| fail(&format!("generate request id: {}", os_error_text(&error))))?;
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    let mut id = String::with_capacity(36);
    for (index, byte) in bytes.iter().enumerate() {
        if matches!(index, 4 | 6 | 8 | 10) {
            id.push('-');
        }
        let _ = write!(id, "{byte:02x}");
    }
    Ok(id)
}

struct Automation<'a> {
    client: Client<'a>,
    args: Vec<String>,
    id: i64,
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() {
        return Err(usage_stop());
    }
    let (profile, args) = strip_profile(args)?;
    let session = current_session_or_exit(run)?;
    let scoped = client(run).with_garden_scope(&profile, &session);
    let verb = args[0].clone();
    let mut id = 0;
    if matches!(
        verb.as_str(),
        "set" | "show" | "run" | "runs" | "enable" | "disable" | "delete" | "cleanup"
    ) {
        run.path = format!("automation {verb}");
        let Some(raw) = args.get(1) else {
            return Err(usage_stop());
        };
        id = raw.parse().ok().filter(|id| *id > 0).ok_or_else(|| {
            eprintln!("automation: ID must be a positive number");
            Stop(2)
        })?;
    }
    if matches!(verb.as_str(), "apply" | "validate" | "list") {
        run.path = format!("automation {verb}");
    }
    let automation = Automation {
        client: scoped,
        args,
        id,
    };
    automation.dispatch(run, &verb)
}

impl Automation<'_> {
    fn call<M: Serialize>(&self, run: &mut Run<'_>, message: &M) -> Result<Reply, Stop> {
        self.client
            .call_automation(message)
            .map_err(|error| refused(run, &error))
    }

    fn print_field<M: Serialize>(
        &self,
        run: &mut Run<'_>,
        message: &M,
        field: &str,
    ) -> Result<(), Stop> {
        let reply = self.call(run, message)?;
        print_reply_field(&reply, &[field]).map_err(|error| refused(run, &error))
    }

    fn require_only_id(&self, verb: &str) -> Result<(), Stop> {
        if self.args.len() != 2 {
            return Err(fail(&format!(
                "usage: attn automation {verb} <definition-id>"
            )));
        }
        Ok(())
    }

    fn dispatch(&self, run: &mut Run<'_>, verb: &str) -> Result<(), Stop> {
        match verb {
            "set" => self.set(run),
            "apply" => self.apply(run),
            "validate" => self.validate(run),
            "list" => self.print_field(run, &AutomationDefinitionsGetMessage::new(), "definitions"),
            "show" => self.show(run),
            "run" => self.start(run),
            "runs" => self.runs(run),
            "enable" => self.set_enabled(run, verb, true),
            "disable" => self.set_enabled(run, verb, false),
            "delete" => self.delete(run),
            "cleanup" => self.cleanup(run),
            _ => {
                run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
                Err(fail(&format!(
                    "unknown automation command {}",
                    quote(verb.as_bytes())
                )))
            }
        }
    }

    fn set(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        let mut flags = FlagSet::new(
            "automation set",
            &[
                DESKTOP_NAME_FLAG,
                text_flag(
                    "launch-desktop",
                    "own, a desktop digit (an empty slot gets a new desktop), name or id",
                ),
            ],
        );
        parse(run, &mut flags, &self.args[2..])?;
        let desktop = flags.string("launch-desktop");
        if desktop.is_empty() || !flags.args().is_empty() {
            return Err(fail("--launch-desktop is required"));
        }
        let mut message = LaunchDesktopSetMessage::new();
        message.kind = LAUNCH_DESKTOP_KIND_AUTOMATION.to_string();
        message.item_id = self.id.to_string();
        message.desktop_ref = Some(desktop.to_string());
        message.desktop_name = flags
            .is_set("desktop-name")
            .then(|| flags.string("desktop-name").to_string());
        self.print_field(run, &message, "item")
    }

    fn apply(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        let mut flags = FlagSet::new(
            "automation apply",
            &[
                DESKTOP_NAME_FLAG,
                text_flag(
                    "launch-desktop",
                    "own, a desktop digit (an empty slot gets a new desktop), name or id of the automation profile",
                ),
                FILE_FLAG,
            ],
        );
        parse(run, &mut flags, &self.args[1..])?;
        if flags.string("file").is_empty() {
            return Err(fail("--file is required"));
        }
        let mut message = AutomationApplyMessage::new();
        message.definition_yaml = read_file(flags.string("file"))?;
        let chosen = |name: &str| flags.is_set(name).then(|| flags.string(name).to_string());
        message.launch_desktop = chosen("launch-desktop");
        message.launch_desktop_name = chosen("desktop-name");
        if message.launch_desktop_name.is_some() && message.launch_desktop.is_none() {
            return Err(fail(DESKTOP_NAME_NEEDS_DESKTOP));
        }
        self.print_field(run, &message, "definition")
    }

    fn validate(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        let mut flags = FlagSet::new("automation validate", &[FILE_FLAG]);
        parse(run, &mut flags, &self.args[1..])?;
        if flags.string("file").is_empty() {
            return Err(fail("--file is required"));
        }
        let mut message = AutomationValidateMessage::new();
        message.definition_yaml = read_file(flags.string("file"))?;
        self.call(run, &message)?;
        print_json(br#"{"valid":true}"#);
        Ok(())
    }

    fn show(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        self.require_only_id("show")?;
        let mut message = AutomationDefinitionGetMessage::new();
        message.definition_id = self.id;
        let reply = self.call(run, &message)?;
        let result: AutomationDefinitionResultMessage =
            reply.whole().map_err(|error| refused(run, &error))?;
        if let Some(spec) = result.spec_yaml {
            let header = show_header(&result.definition.unwrap_or_default());
            print_stdout(&format!("{header}{spec}"));
        }
        Ok(())
    }

    fn start(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        let mut flags = FlagSet::new(
            "automation run",
            &[
                text_flag("input-file", "structured occurrence JSON"),
                text_flag(
                    "pr-url",
                    "resolve one GitHub pull request into structured occurrence input",
                ),
                text_flag(
                    "request-id",
                    "stable idempotency key to reuse when retrying an uncertain request",
                ),
            ],
        );
        parse(run, &mut flags, &self.args[2..])?;
        if !flags.args().is_empty() {
            return Err(fail(RUN_USAGE));
        }
        let (input_file, pr_url) = (flags.string("input-file"), flags.string("pr-url"));
        if !input_file.is_empty() && !pr_url.is_empty() {
            return Err(fail("--input-file and --pr-url are mutually exclusive"));
        }
        let input = if input_file.is_empty() {
            "{}".to_string()
        } else {
            read_file(input_file)?
        };
        let mut message = AutomationRunMessage::new();
        message.definition_id = self.id;
        message.request_id = match flags.string("request-id") {
            "" => new_request_id()?,
            given => given.to_string(),
        };
        if pr_url.is_empty() {
            message.input_json = Some(input);
        } else {
            message.pr_url = Some(pr_url.to_string());
        }
        self.print_field(run, &message, "run")
    }

    fn runs(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        self.require_only_id("runs")?;
        let mut message = AutomationRunsGetMessage::new();
        message.definition_id = self.id;
        self.print_field(run, &message, "runs")
    }

    fn set_enabled(&self, run: &mut Run<'_>, verb: &str, enabled: bool) -> Result<(), Stop> {
        self.require_only_id(verb)?;
        let mut message = AutomationSetEnabledMessage::new();
        message.definition_id = self.id;
        message.enabled = enabled;
        self.print_field(run, &message, "definition")
    }

    fn delete(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        self.require_only_id("delete")?;
        let mut message = AutomationDeleteMessage::new();
        message.definition_id = self.id;
        self.call(run, &message)?;
        print_json(format!(r#"{{"deleted":{}}}"#, self.id).as_bytes());
        Ok(())
    }

    fn cleanup(&self, run: &mut Run<'_>) -> Result<(), Stop> {
        self.require_only_id("cleanup")?;
        let mut message = AutomationCleanupMessage::new();
        message.definition_id = self.id;
        let reply = self.call(run, &message)?;
        let field = |name: &str| reply.raw_field(&[name]).map(str::to_string);
        let body = (|| -> Result<String, SendError> {
            Ok(format!(
                r#"{{"cleaned":{},"kept_active":{},"kept_dirty":{}}}"#,
                field("cleaned")?,
                field("kept_active")?,
                field("kept_dirty")?
            ))
        })()
        .map_err(|error| refused(run, &error))?;
        print_json(body.as_bytes());
        Ok(())
    }
}

fn refused(run: &mut Run<'_>, error: &SendError) -> Stop {
    run.fail_send(LABEL, error)
}

fn comment_lines(text: &str) -> String {
    text.replace('\n', "\n# ")
}

fn show_header(definition: &AutomationDefinitionSummary) -> String {
    let mut out = format!(
        "# Profile: {}\n# Launch desktop: {}\n",
        definition.profile_name.as_deref().unwrap_or(""),
        launch_desktop_text(definition.launch_desktop.as_ref())
    );
    if let Some(last) = &definition.last_run {
        let session = last.session_id.as_deref().unwrap_or("");
        if !session.is_empty() {
            let _ = write!(out, "# Last run session: {session}");
            let superseded = last
                .automation
                .as_ref()
                .is_some_and(|provenance| provenance.superseded == Some(true));
            if superseded {
                out.push_str(" (superseded; the next occurrence starts a new session)");
            }
            out.push('\n');
        }
        if last.state == FAILED_RUN_STATE {
            let _ = writeln!(
                out,
                "# Last run failed: {}",
                comment_lines(last.last_error.as_deref().unwrap_or(""))
            );
        }
    }
    if let Some(check) = &definition.guard_check {
        let _ = write!(out, "# Guard: {} at {}", check.outcome, check.checked_at);
        match check.detail.as_deref().filter(|detail| !detail.is_empty()) {
            Some(detail) => {
                let _ = write!(out, ": {}", comment_lines(detail));
            }
            None => {
                let _ = write!(
                    out,
                    " ({} checks since the last run)",
                    check.checks_since_run
                );
            }
        }
        out.push('\n');
    }
    out
}
