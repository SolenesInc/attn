use super::{Stop, help_generated, table_generated};
use crate::client::{Client, Reply};
use crate::command::{self, Arg, Command, Family, Parsed};
use crate::commands::support::new_uuid;
use crate::commands::{Run, print_json, print_stdout, working_directory};
use attn_protocol::RunCommandMessage;
use attn_protocol::go_values::prompts::DELEGATION_STARTED;
use serde_json::Value as Json;

pub struct Delegate;

fn usage_error(verb: &str, message: &str) -> Stop {
    Family::usage_error(&Delegate, verb, message)
}

fn fail(verb: &str, message: &str) -> Stop {
    Family::fail(&Delegate, verb, message)
}

impl Family for Delegate {
    fn group(&self) -> &'static str {
        "delegate"
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

    fn decode_file(&self, _cmd: &Command, _arg: &Arg, bytes: &[u8]) -> Result<Json, String> {
        let mut values = serde_json::Deserializer::from_slice(bytes).into_iter::<Json>();
        let table = match values.next() {
            Some(Ok(table)) => table,
            Some(Err(error)) => return Err(format!("read the table: {error}; the table takes the fields attn delegate roles show --json prints")),
            None => return Err("read the table: it is empty; apply takes the one table attn delegate roles show --json prints".to_string()),
        };
        if values.next().is_some() {
            return Err("read the table: content follows the table; apply takes the one table attn delegate roles show --json prints".to_string());
        }
        Ok(table)
    }

    fn read_stdin_error(&self, _arg: &Arg) -> &'static str {
        "read the table from stdin"
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if cmd.name.is_empty() {
            return prepare_delegation(parsed);
        }
        if cmd.name == "status" {
            return Ok(());
        }
        if cmd.name == "roles show" && parsed.json && parsed.has("role") {
            return Err(usage_error(
                cmd.name,
                "--json prints the whole table; leave out the role",
            ));
        }
        if !parsed.json {
            parsed.args.insert("render".to_string(), Json::from(true));
        }
        Ok(())
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if cmd.name.is_empty() {
            return await_delegation(run, parsed, &result);
        }
        command::print_result(run, self, cmd, &result)
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let label = self.label(cmd.name);
        if cmd.name.is_empty() {
            return await_delegation(run, parsed, &result);
        }
        if cmd.name == "status" {
            return command::print_result(run, self, cmd, &result);
        }
        let text: String = result
            .value("text")
            .map_err(|error| run.fail_send(&label, &error))?;
        print_stdout(&text);
        Ok(())
    }
}

fn prepare_delegation(parsed: &mut Parsed) -> Result<(), Stop> {
    if let Some(source) = parsed.args.remove("source_session") {
        parsed.session = source.as_str().unwrap_or_default().trim().to_string();
    }
    if let Some(path) = parsed.args.remove("brief_file") {
        if !parsed.text("brief").trim().is_empty() {
            return Err(usage_error("", "pass only one of --brief or --brief-file"));
        }
        let path = path.as_str().unwrap_or_default().to_string();
        let brief = std::fs::read_to_string(&path).map_err(|error| {
            usage_error(
                "",
                &format!(
                    "read brief file: open {path}: {}",
                    crate::paths::os_error_text(&error)
                ),
            )
        })?;
        parsed
            .args
            .insert("brief".to_string(), Json::from(brief.trim()));
    }
    let base = working_directory().map_err(|error| {
        fail(
            "",
            &format!(
                "resolve caller working directory: {}",
                crate::paths::os_error_text(&error)
            ),
        )
    })?;
    parsed.args.insert("cwd_base".to_string(), Json::from(base));
    if parsed.text("request_id").trim().is_empty() {
        parsed
            .args
            .insert("request_id".to_string(), Json::from(new_uuid()));
    }
    eprintln!(
        "delegation request: request_id={}",
        parsed.text("request_id")
    );
    Ok(())
}

fn await_delegation(run: &mut Run<'_>, parsed: &Parsed, accepted: &Reply) -> Result<(), Stop> {
    let request_id = parsed.text("request_id").to_string();
    let field = |reply: &Reply, name: &str| reply.optional_string(name);
    eprintln!(
        "delegation accepted: request_id={} operation_id={} session_id={}",
        field(accepted, "request_id"),
        field(accepted, "operation_id"),
        field(accepted, "session_id")
    );
    let unknown = |message: &str| {
        fail(
            "",
            &format!("outcome unknown; inspect or retry with --request-id {request_id}: {message}"),
        )
    };
    let mut operation = accepted.clone();
    let mut last_progress = String::new();
    while matches!(
        field(&operation, "state").as_str(),
        "accepted" | "preparing"
    ) {
        let progress = field(&operation, "progress");
        if !progress.is_empty() && progress != last_progress {
            eprintln!("delegation progress: {progress}");
            last_progress = progress;
        }
        let mut message = RunCommandMessage::new();
        message.command = "delegate status".to_string();
        let mut args = std::collections::BTreeMap::new();
        args.insert(
            "id".to_string(),
            Json::from(field(&operation, "operation_id")),
        );
        args.insert(
            "wait_past".to_string(),
            Json::from(field(&operation, "updated_at")),
        );
        message.args = Some(args);
        message.session_id = (!parsed.session.is_empty()).then(|| parsed.session.clone());
        message.profile_id = (!parsed.profile.is_empty()).then(|| parsed.profile.clone());
        let reply = Client::new(run.paths, "", &run.terminal)
            .call_command(&message, None)
            .map_err(|error| unknown(&error.to_string()))?;
        if !reply.raw_field(&["ok"]).is_ok_and(|ok| ok == "true") {
            let refusal: Option<attn_protocol::CommandRefusal> =
                reply.value("command_refusal").ok().flatten();
            return Err(unknown(&refusal.map_or_default(|r| r.message)));
        }
        let next = command::result(&reply);
        if field(&next, "updated_at") == field(&operation, "updated_at")
            && field(&next, "state") == field(&operation, "state")
        {
            return Err(unknown(
                "the running daemon answered delegate status without waiting, so it predates this attn; restart it with `attn daemon ensure`",
            ));
        }
        operation = next;
    }
    if field(&operation, "state") == "failed" {
        let failure = operation.field("failure");
        let message = match failure.optional_string("message") {
            message if message.is_empty() && !failure.present("message") => {
                "unknown failure".to_string()
            }
            message => message,
        };
        return Err(fail(
            "",
            &format!("{message} (request_id={})", field(&operation, "request_id")),
        ));
    }
    if !operation.present("result") {
        return Err(fail("", "completed operation has no result"));
    }
    let result = operation.field("result");
    let unconfirmed = result.optional_string("first_turn_unconfirmed");
    if !unconfirmed.is_empty() {
        eprintln!("delegate: {unconfirmed}");
    }
    print_json(
        result
            .raw_field(&[])
            .map_err(|error| unknown(&error.to_string()))?
            .as_bytes(),
    );
    eprintln!("{DELEGATION_STARTED}");
    Ok(())
}
