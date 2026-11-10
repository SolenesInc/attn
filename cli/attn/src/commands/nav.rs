use super::support::{client, has_help_flag, parse_interspersed, string_flag};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, absolute, print_stdout};
use crate::paths::os_error_text;
use attn::go::flag::FlagSet;
use attn::go::quote::quote;
use attn_protocol::{
    CodeNavCheckMessage, CodeNavCheckResult, CodeNavDefineMessage, CodeNavDefineResult,
    CodeNavLinkMessage, CodeNavLinkResult, CodeNavStatusMessage, CodeNavStatusResult,
    CodeNavUnlinkMessage,
};
use std::fmt::Write as _;

pub const HELP: &str = r#"usage: attn nav <command>

Code navigation in a review: definition, uses and type of a name, answered by
a language server that attn starts on the machine that holds the files and
stops once no shown review needs it.

commands:
  status
        the server definitions for the repository you are in, where each
        program was found or why it was not, and the servers running on this
        machine with their memory.

  check <path>:<line>
        ask for the definition of each name on the line, in order, and print
        the first that resolves. Starts the server when it is not running.
        Setup is done when this resolves a real definition.

  link <program> [--as <name>]
        link a server's program into this machine's code navigation folder,
        which attn searches before PATH. Use it for a server your editor
        installed outside PATH, such as Neovim's mason folder.

  unlink <name>
        remove a link.

  set <language> <definition>
        set how to launch the server for a language in the repositories with
        this repository's origin. Outposts with a clone use it too. The
        definition is JSON:
          {"command": ["jdtls"], "extensions": [".java"],
           "roots": ["MODULE.bazel"], "settings": {},
           "prepare": {"command": ["tools/ide/setup_classpath.py"],
                       "inputs": ["**/BUILD.bazel", "MODULE.bazel"]}}
        command and extensions are required. roots are files that mark a
        workspace folder. settings go to the server at start. prepare runs
        from the workspace folder before the server starts, and again before
        a start once a file its inputs match has changed.

  rm <language>
        remove the definition set for a language, so the built-in one
        applies again.

Built-in definitions: go (gopls), typescript (typescript-language-server
--stdio), rust (rust-analyzer), python (pyright-langserver --stdio).

Needs the experimental setting code_review_enabled.
"#;

fn usage_failure(verb: &str, message: &str) -> Stop {
    eprint!("nav {verb}: {message}\n{HELP}");
    Stop(2)
}

fn positionals(
    run: &mut Run<'_>,
    verb: &str,
    flags: &mut FlagSet<'_>,
    args: &[String],
    want: usize,
    shape: &str,
) -> Result<Vec<String>, Stop> {
    let positionals = parse_interspersed(run, flags, args)
        .map_err(|error| usage_failure(verb, &error.to_string()))?;
    if positionals.len() != want {
        return Err(usage_failure(verb, &format!("expected {shape}")));
    }
    Ok(positionals)
}

fn working_directory(verb: &str) -> Result<String, Stop> {
    absolute(".").map_err(|error| {
        eprintln!(
            "nav {verb}: resolve the working directory: {}",
            os_error_text(&error)
        );
        Stop(1)
    })
}

fn megabytes(bytes: i64) -> String {
    format!("{} MB", bytes / (1 << 20))
}

fn status(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("nav status", &[]);
    positionals(run, "status", &mut flags, args, 0, "no arguments")?;
    let mut message = CodeNavStatusMessage::new();
    message.repository = Some(working_directory("status")?);
    let field = "code_nav_status_result";
    let reply = client(run)
        .call_field(&message, field, "daemon returned no nav status")
        .map_err(|error| run.fail_send("nav status", &error))?;
    let result: CodeNavStatusResult = reply
        .value(field)
        .map_err(|error| run.fail_send("nav status", &error))?;
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
            "  {} in {}: {}, pid {}",
            server.language, server.folder, server.state, server.pid
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
    print_stdout(&out);
    Ok(())
}

fn check(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("nav check", &[]);
    let target = positionals(run, "check", &mut flags, args, 1, "one <path>:<line>")?;
    let Some((path, line)) = target[0].rsplit_once(':') else {
        return Err(usage_failure("check", "expected <path>:<line>"));
    };
    let Ok(line) = line.parse::<i64>() else {
        return Err(usage_failure(
            "check",
            &format!("{line} is not a line number"),
        ));
    };
    let mut message = CodeNavCheckMessage::new();
    message.repository = working_directory("check")?;
    message.path = path.to_string();
    message.line = line;
    let field = "code_nav_check_result";
    let reply = client(run)
        .call_field(&message, field, "daemon returned no nav check")
        .map_err(|error| run.fail_send("nav check", &error))?;
    let result: CodeNavCheckResult = reply
        .value(field)
        .map_err(|error| run.fail_send("nav check", &error))?;
    let name = result.name.unwrap_or_default();
    let location = result.location.unwrap_or_default();
    print_stdout(&format!(
        "{}: {name} -> {}:{} in {} ms\n",
        result.program, location.path, location.line, result.elapsed_ms
    ));
    Ok(())
}

fn link(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let definitions = [string_flag("as")];
    let mut flags = FlagSet::new("nav link", &definitions);
    let program = positionals(run, "link", &mut flags, args, 1, "one <program>")?;
    let mut message = CodeNavLinkMessage::new();
    message.target = absolute(program[0].trim()).map_err(|error| {
        eprintln!("nav link: resolve path: {}", os_error_text(&error));
        Stop(1)
    })?;
    let name = flags.string("as").trim().to_string();
    message.name = (!name.is_empty()).then_some(name);
    let field = "code_nav_link_result";
    let reply = client(run)
        .call_field(&message, field, "daemon returned no link")
        .map_err(|error| run.fail_send("nav link", &error))?;
    let result: CodeNavLinkResult = reply
        .value(field)
        .map_err(|error| run.fail_send("nav link", &error))?;
    print_stdout(&format!(
        "linked {} -> {}\n",
        result.path,
        result.target.unwrap_or_default()
    ));
    Ok(())
}

fn unlink(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new("nav unlink", &[]);
    let name = positionals(run, "unlink", &mut flags, args, 1, "one <name>")?;
    let mut message = CodeNavUnlinkMessage::new();
    message.name.clone_from(&name[0]);
    let field = "code_nav_link_result";
    let reply = client(run)
        .call_field(&message, field, "daemon returned no unlink")
        .map_err(|error| run.fail_send("nav unlink", &error))?;
    let result: CodeNavLinkResult = reply
        .value(field)
        .map_err(|error| run.fail_send("nav unlink", &error))?;
    print_stdout(&format!("removed {}\n", result.path));
    Ok(())
}

fn define(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    let mut flags = FlagSet::new(if verb == "set" { "nav set" } else { "nav rm" }, &[]);
    let (want, shape) = if verb == "set" {
        (2, "<language> <definition>")
    } else {
        (1, "one <language>")
    };
    let given = positionals(run, verb, &mut flags, args, want, shape)?;
    let mut message = CodeNavDefineMessage::new();
    message.repository = working_directory(verb)?;
    message.language.clone_from(&given[0]);
    message.definition = given.get(1).cloned();
    let label = format!("nav {verb}");
    let field = "code_nav_define_result";
    let reply = client(run)
        .call_field(&message, field, "daemon returned no definition")
        .map_err(|error| run.fail_send(&label, &error))?;
    let result: CodeNavDefineResult = reply
        .value(field)
        .map_err(|error| run.fail_send(&label, &error))?;
    match &result.definition {
        Some(definition) => print_stdout(&format!(
            "{} = {definition}\nRun `attn nav check <path>:<line>` on a {} file to try it.\n",
            result.key, given[0]
        )),
        None => print_stdout(&format!(
            "removed {}; the built-in definition applies, if there is one\n",
            result.key
        )),
    }
    Ok(())
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first() else {
        print_stdout(HELP);
        return Ok(());
    };
    if verb == "-h" || verb == "--help" || has_help_flag(&args[1..]) {
        print_stdout(HELP);
        return Ok(());
    }
    super::warn_if_stale(run.paths);
    let rest = &args[1..];
    run.path = format!("nav {verb}");
    match verb.as_str() {
        "status" => status(run, rest),
        "check" => check(run, rest),
        "link" => link(run, rest),
        "unlink" => unlink(run, rest),
        "set" | "rm" => define(run, verb, rest),
        _ => {
            run.path = "nav".to_string();
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!("nav: unknown command {}\n{HELP}", quote(verb.as_bytes()));
            Err(Stop(2))
        }
    }
}
