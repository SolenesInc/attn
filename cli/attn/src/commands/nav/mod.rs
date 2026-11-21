mod help_generated;
mod table_generated;

use super::{Run, Stop, absolute, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use crate::paths::os_error_text;
use attn::go::time::Time;
use attn_protocol::{
    CodeNavCheckResult, CodeNavDefineResult, CodeNavLinkResult, CodeNavStatusResult,
};
use serde_json::Value as Json;
use std::fmt::Write as _;

struct Nav;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Nav, args)
}

fn seconds_since(stamp: &str) -> Option<i128> {
    let at = Time::parse_rfc3339_nano(stamp).ok()?;
    Some(((Time::now_utc().unix_nanos() - at.unix_nanos()) / 1_000_000_000).max(0))
}

fn megabytes(bytes: i64) -> String {
    format!("{} MB", bytes / (1 << 20))
}

fn status(result: &CodeNavStatusResult) -> String {
    let mut out = String::new();
    let _ = writeln!(out, "linked programs: {}", result.bin_dir);
    if let Some(origin) = &result.origin {
        let _ = writeln!(out, "origin: {origin}");
    }
    if !result.languages.is_empty() {
        let _ = writeln!(out, "languages:");
    }
    for language in &result.languages {
        let found = match (&language.program, &language.missing) {
            (Some(program), _) => program.clone(),
            (None, Some(missing)) => format!("missing: {missing}"),
            (None, None) => String::new(),
        };
        let _ = writeln!(
            out,
            "  {:<12} {:<9} {found}",
            language.language, language.source
        );
        if let Some(key) = &language.setting_key {
            let _ = writeln!(
                out,
                "  {:<12} {:<9} {key} = {}",
                "", "", language.definition
            );
        }
    }
    if result.servers.is_empty() {
        let _ = writeln!(out, "no language server is running on this machine");
    } else {
        let _ = writeln!(out, "servers on this machine:");
    }
    for server in &result.servers {
        let mut line = format!(
            "  {} in {}: {}{}, pid {}",
            server.language,
            server.folder,
            server.state,
            server
                .started_at
                .as_deref()
                .and_then(seconds_since)
                .map(|seconds| format!(" {seconds}s"))
                .unwrap_or_default(),
            server.pid
        );
        if let Some(rss) = server.rss_bytes {
            let _ = write!(line, ", {}", megabytes(rss));
        }
        if let Some(detail) = &server.detail {
            let _ = write!(line, " ({detail})");
        }
        let _ = writeln!(out, "{line}");
        if server.needed_by.is_empty() {
            let _ = writeln!(out, "    needed by no shown review; stops when idle");
        }
        for document in &server.needed_by {
            let _ = writeln!(out, "    needed by {document}");
        }
    }
    out
}

fn checked(result: &CodeNavCheckResult) -> String {
    let location = result.location.clone().unwrap_or_default();
    format!(
        "{}: {} -> {}:{} in {} ms\n",
        result.program,
        result.name.as_deref().unwrap_or_default(),
        location.path,
        location.line,
        result.elapsed_ms
    )
}

fn defined(result: &CodeNavDefineResult, language: &str) -> String {
    match &result.definition {
        Some(definition) => format!(
            "{} = {definition}\nRun `attn nav check <path>:<line>` on a {language} file to try it.\n",
            result.key
        ),
        None => format!(
            "removed {}; the built-in definition applies, if there is one\n",
            result.key
        ),
    }
}

fn resolve(parsed: &mut Parsed, verb: &str, name: &str, default: Option<&str>) -> Result<(), Stop> {
    let given = parsed.text(name).trim().to_string();
    let path = match (given.is_empty(), default) {
        (true, Some(default)) => default.to_string(),
        (true, None) => return Ok(()),
        (false, _) => given,
    };
    let resolved = absolute(&path).map_err(|error| {
        eprintln!("nav {verb}: resolve path: {}", os_error_text(&error));
        Stop(1)
    })?;
    parsed.args.insert(name.to_string(), Json::from(resolved));
    Ok(())
}

impl Family for Nav {
    fn group(&self) -> &'static str {
        "nav"
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

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        match cmd.name {
            "link" => resolve(parsed, cmd.name, "program", None),
            "unlink" => Ok(()),
            "check" => {
                parsed.args.insert("wait".to_string(), Json::from(true));
                resolve(parsed, cmd.name, "repository", Some("."))
            }
            _ => resolve(parsed, cmd.name, "repository", Some(".")),
        }
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        if parsed.json {
            return command::print_result(run, self, cmd, &result);
        }
        let out = match cmd.name {
            "status" => status(&command::decode(run, self, cmd, &result)?),
            "check" => checked(&command::decode(run, self, cmd, &result)?),
            "link" => {
                let linked: CodeNavLinkResult = command::decode(run, self, cmd, &result)?;
                format!(
                    "linked {} -> {}\n",
                    linked.path,
                    linked.target.unwrap_or_default()
                )
            }
            "unlink" => {
                let unlinked: CodeNavLinkResult = command::decode(run, self, cmd, &result)?;
                format!("removed {}\n", unlinked.path)
            }
            _ => defined(
                &command::decode(run, self, cmd, &result)?,
                parsed.text("language"),
            ),
        };
        print_stdout(&out);
        Ok(())
    }
}
