mod help_generated;
mod table_generated;

use super::{Run, Stop, print_compact_line, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::quote::quote;
use attn::go::time::Time;
use attn_protocol::session_id::short;
use attn_protocol::{ActorView, Ask as OpenAsk, AskCloseResult, AskListResult, AskShowResult};
use serde_json::Value as Json;
use std::fmt::Write as _;
use std::io::Read;

struct Ask;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Ask, args)
}

fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

fn read_ask(parsed: &Parsed) -> Result<(String, String), String> {
    let file = parsed.text("markdown");
    if !file.is_empty() {
        let bytes = std::fs::read(file)
            .map_err(|error| format!("open {file}: {}", crate::paths::os_error_text(&error)))?;
        let absolute =
            super::absolute(file).map_err(|error| crate::paths::os_error_text(&error))?;
        let folder = std::path::Path::new(&absolute)
            .parent()
            .map_or_else(String::new, |folder| folder.to_string_lossy().into_owned());
        return Ok((String::from_utf8_lossy(&bytes).into_owned(), folder));
    }
    let mut raw = Vec::new();
    std::io::stdin()
        .read_to_end(&mut raw)
        .map_err(|error| crate::paths::os_error_text(&error))?;
    let markdown = String::from_utf8_lossy(&raw).into_owned();
    if markdown.trim().is_empty() {
        return Err("pipe the markdown on stdin, or pass --file".to_string());
    }
    let folder = super::absolute(".").map_err(|error| crate::paths::os_error_text(&error))?;
    Ok((markdown, folder))
}

fn seed_name(ask: &OpenAsk) -> &str {
    match ask.seed_slug.as_deref() {
        Some(slug) if !slug.is_empty() => slug,
        _ => ask.seed_id.as_deref().unwrap_or(""),
    }
}

fn row(ask: &OpenAsk) -> String {
    if ask.seed_id.is_none() {
        format!("{}  {}", ask.id, ask.subject)
    } else {
        format!("{}  {}  {}", ask.id, seed_name(ask), ask.subject)
    }
}

fn asker(from: &ActorView) -> String {
    match from.r#ref.strip_prefix("session:") {
        Some(session) if short(session) != from.name => {
            format!("{} ({})", from.name, short(session))
        }
        _ => from.name.clone(),
    }
}

fn age(created_at: &str) -> String {
    let Ok(at) = Time::parse_rfc3339_nano(created_at) else {
        return created_at.to_string();
    };
    let seconds = (Time::now_utc().unix_nanos() - at.unix_nanos()) / 1_000_000_000;
    match seconds {
        s if s < 60 => format!("{s}s"),
        s if s < 3600 => format!("{}m", s / 60),
        s if s < 48 * 3600 => format!("{}h", s / 3600),
        s => format!("{}d", s / 86400),
    }
}

fn opened(ask: &OpenAsk) -> String {
    if ask.seed_id.is_none() {
        return format!(
            "Asked {}: {}. attn types the answer into this conversation as the user's message.\n",
            ask.id,
            quote(ask.subject.as_bytes())
        );
    }
    format!(
        "Asked {} about {}: {}. End your turn: your session stays open so the user can find you, and attn types the answer into this conversation. The seed cannot be harvested until the user answers.\n",
        ask.id,
        seed_name(ask),
        quote(ask.subject.as_bytes())
    )
}

fn listed(result: &AskListResult) -> String {
    let mut out = String::new();
    if !result.enabled {
        out.push_str("Asks are off for this session.\n");
    }
    if result.asks.is_empty() && result.overseen.is_empty() {
        if result.enabled {
            out.push_str("No open asks.\n");
        }
        return out;
    }
    if result.asks.is_empty() && result.enabled {
        out.push_str("You have no open asks.\n");
    }
    for ask in &result.asks {
        let _ = writeln!(out, "{}", row(ask));
    }
    if result.overseen.is_empty() {
        return out;
    }
    if !result.asks.is_empty() || result.enabled {
        out.push('\n');
    }
    out.push_str("Asked by agents you oversee. These are not your asks, and their answers go to the askers.\n");
    out.push_str("Read one: attn ask show <id>. Withdraw one: attn ask close <id> -m \"<why>\".\n");
    for overseen in &result.overseen {
        let _ = writeln!(
            out,
            "{}  · from {}",
            row(&overseen.ask),
            asker(&overseen.from)
        );
    }
    out
}

fn shown(result: &AskShowResult) -> String {
    let ask = &result.ask;
    let mut out = String::new();
    let _ = writeln!(out, "{}  {}", ask.id, ask.subject);
    let _ = writeln!(out, "asked by  {}", asker(&result.from));
    if ask.seed_id.is_some() {
        let _ = writeln!(out, "seed      {}", seed_name(ask));
    }
    let _ = writeln!(out, "open      {}", age(&ask.created_at));
    if !result.held {
        let _ = writeln!(
            out,
            "Not your ask. The user answers it, and the answer goes to its asker. Withdraw it with attn ask close {} -m \"<why>\".",
            ask.id
        );
    }
    let _ = writeln!(out, "\n{}", result.markdown.trim_end_matches('\n'));
    for part in &ask.parts {
        if let (Some(at), Some(markdown)) = (&part.amended_at, &part.markdown) {
            let _ = writeln!(
                out,
                "\nUpdated {}:\n{}",
                at,
                markdown.trim_end_matches('\n')
            );
        }
    }
    out
}

fn amended(ask: &OpenAsk) -> String {
    format!(
        "Amended {}: {}. The user sees the update in the ask, and keeps its place and their answer so far.\n",
        ask.id,
        quote(ask.subject.as_bytes())
    )
}

impl Family for Ask {
    fn group(&self) -> &'static str {
        "ask"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        static HELP: std::sync::OnceLock<String> = std::sync::OnceLock::new();
        HELP.get_or_init(|| {
            format!(
                "{}\nattn {}",
                help_generated::GROUP_HELP,
                help_generated::help("")
            )
        })
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn usage_error(&self, verb: &str, message: &str) -> Stop {
        eprintln!("{}: {message}", self.label(verb));
        Stop(2)
    }

    fn refused_line(&self, verb: &str, message: &str) -> String {
        format!("{}: {message}", self.label(verb))
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if parsed.session.is_empty() && crate::hooks::terminal_from_env(&env).trim().is_empty() {
            return Err(self.usage_error(
                "",
                "no session — pass --session <id>, or run inside an attn session",
            ));
        }
        if cmd.name.is_empty() || cmd.name == "amend" {
            let (markdown, folder) = read_ask(parsed).map_err(|message| self.fail("", &message))?;
            parsed
                .args
                .insert("markdown".to_string(), Json::from(markdown));
            parsed
                .args
                .insert("base_dir".to_string(), Json::from(folder));
        }
        Ok(())
    }

    fn no_session(&self, _verb: &str, message: &str) -> Stop {
        command::unresolved_session(message)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let raw = result
            .raw_field(&[])
            .map_err(|error| run.fail_send(&self.label(cmd.name), &error))?;
        print_compact_line(raw);
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
        let text = match cmd.name {
            "list" => listed(&command::decode(run, self, cmd, &result)?),
            "show" => shown(&command::decode(run, self, cmd, &result)?),
            "amend" => amended(&command::decode(run, self, cmd, &result)?),
            "close" => {
                let closed: AskCloseResult = command::decode(run, self, cmd, &result)?;
                format!("Withdrew {}.\n", closed.id)
            }
            _ => opened(&command::decode(run, self, cmd, &result)?),
        };
        print_stdout(&text);
        Ok(())
    }
}
