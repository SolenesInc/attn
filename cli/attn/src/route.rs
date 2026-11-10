use crate::commands::{
    Run, Stop, automation, browser, callout, codex, conversation, crew, doc, enrollment,
    instance_env, instance_resolve, journal, nav, open, pr, print_stdout, review, seed, send,
    settings, terminal, ticket, top_level, worktree,
};
use crate::hooks::{COMMANDS, Failure, Hook, terminal_from_env};
use crate::instance::Paths;
use crate::invocation::{self, Invocation};
use std::ffi::OsString;
use std::fs;
use std::io::{self, Write};
use std::os::unix::process::CommandExt;
use std::path::PathBuf;
use std::process::{Command, ExitCode};

const EXIT_NOT_FOUND: u8 = 127;

pub fn run(argv: Vec<OsString>) -> ExitCode {
    match argv
        .get(1)
        .and_then(|arg| arg.to_str())
        .filter(|name| COMMANDS.contains(name))
    {
        Some(hook) => run_hook(hook, &argv),
        None => route_command(argv),
    }
}

#[derive(Clone, Copy, PartialEq)]
enum Checks {
    InstanceAndRouting,
    InstanceOnly,
    Nothing,
}

type NativeRun = for<'a, 'b> fn(&'a mut Run<'b>, &'a [String]) -> Result<(), Stop>;

#[derive(Clone, Copy)]
struct Native {
    run: NativeRun,
    banner: bool,
    checks: Checks,
    arguments_from: usize,
    help: Option<&'static str>,
}

const fn native(run: NativeRun, banner: bool) -> Native {
    Native {
        run,
        banner,
        checks: Checks::InstanceAndRouting,
        arguments_from: 2,
        help: None,
    }
}

#[allow(clippy::unnecessary_wraps)]
fn run_help(_run: &mut Run<'_>, _args: &[String]) -> Result<(), Stop> {
    top_level::help();
    Ok(())
}

fn native_command(name: &str, verb: Option<&str>, argv: &[OsString]) -> Option<Native> {
    Some(match (name, verb) {
        ("help" | "-h" | "--help", _) => native(run_help, false),
        ("seed", _) => native(seed::run, true),
        ("ticket", _) => native(ticket::run, true),
        ("doc", Some("watch")) => return None,
        ("doc", _) => native(doc::run, true),
        ("pr", Some("wait-ready")) if !pr::asks_for_help(&argv[2..]) => return None,
        ("pr", _) => native(pr::run, false),
        ("settings", _) => native(settings::run, false),
        ("crew", _) => native(crew::run, true),
        ("automation", _) => Native {
            help: Some(automation::USAGE),
            ..native(automation::run, true)
        },
        ("callout", _) => native(callout::run, false),
        ("send", _) => native(send::run, true),
        ("conversation", _) => native(conversation::run, true),
        ("worktree", _) => native(worktree::run, true),
        ("codex", _) => native(codex::run, true),
        ("enrollment", _) => native(enrollment::run, true),
        ("instance", Some("resolve")) => Native {
            checks: Checks::InstanceOnly,
            arguments_from: 3,
            ..native(instance_resolve::run, false)
        },
        ("terminal", _) => native(terminal::run, true),
        ("journal", _) => native(journal::run, true),
        ("review", _) => Native {
            help: Some(review::HELP),
            ..native(review::run, true)
        },
        ("nav", _) => Native {
            help: Some(nav::HELP),
            ..native(nav::run, true)
        },
        ("open", _) => Native {
            help: Some(open::USAGE),
            ..native(open::run, true)
        },
        ("browser", _) => Native {
            help: Some(browser::USAGE),
            ..native(browser::run, true)
        },
        ("instance-env", _) => Native {
            checks: Checks::Nothing,
            ..native(instance_env::run, false)
        },
        ("instance", Some("env")) => Native {
            checks: Checks::InstanceOnly,
            arguments_from: 3,
            ..native(instance_env::run, false)
        },
        _ => return None,
    })
}

fn route_command(argv: Vec<OsString>) -> ExitCode {
    let Some(name) = argv.get(1).map(|name| name.to_string_lossy().into_owned()) else {
        return hand_off_to_attnd(argv);
    };
    let verb = argv.get(2).map(|verb| verb.to_string_lossy().into_owned());
    let command = native_command(&name, verb.as_deref(), &argv);
    if command.is_none() && top_level::attnd_owns(&name) {
        return hand_off_to_attnd(argv);
    }
    let from = command.map_or(2, |command| command.arguments_from);
    let arguments: Vec<String> = argv
        .iter()
        .skip(from)
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    let paths = Paths::new(&getenv);
    let terminal = terminal_from_env(&getenv).trim().to_string();
    let checks = command.map_or(Checks::InstanceAndRouting, |command| command.checks);
    let checked = match checks {
        Checks::InstanceAndRouting => paths
            .validate_instance()
            .and_then(|()| paths.validate_routing()),
        Checks::InstanceOnly => paths.validate_instance(),
        Checks::Nothing => Ok(()),
    };
    if let Err(message) = checked {
        let _ = writeln!(io::stderr(), "{message}");
        return finish(&paths, "attn", String::new(), None, 1, &terminal);
    }
    let mut run = Run {
        paths: &paths,
        terminal: terminal.clone(),
        path: if from == 3 {
            format!("{name} {}", verb.clone().unwrap_or_default())
        } else {
            name.clone()
        },
        usage_error: None,
        refusal: None,
        closed_session: None,
    };
    let code = if let Some(command) = command {
        run_native(command, &mut run, &arguments, &paths)
    } else {
        let Stop(code) = top_level::unknown(&mut run, &name);
        code
    };
    invocation::record(
        &paths,
        Invocation {
            path: &run.path,
            caller: String::new(),
            refusal: run.refusal.take(),
            usage_error: run.usage_error,
            session: run.closed_session.take(),
        },
        code,
        &terminal,
    );
    ExitCode::from(code)
}

fn run_native(command: Native, run: &mut Run<'_>, arguments: &[String], paths: &Paths<'_>) -> u8 {
    if let Some(help) = command.help
        && let [only] = arguments
        && (only == "-h" || only == "--help")
    {
        print_stdout(help);
        return 0;
    }
    if command.banner
        && let Some(banner) = paths.banner()
    {
        let _ = writeln!(io::stderr(), "{banner}");
    }
    match (command.run)(run, arguments) {
        Ok(()) => 0,
        Err(Stop(code)) => code,
    }
}

fn getenv(name: &str) -> String {
    std::env::var_os(name).map_or_else(String::new, |value| value.to_string_lossy().into_owned())
}

fn run_hook(name: &str, argv: &[OsString]) -> ExitCode {
    let arguments: Vec<String> = argv
        .iter()
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    let paths = Paths::new(&getenv);
    let env_terminal = terminal_from_env(&getenv).trim().to_string();

    let checked = paths
        .validate_instance()
        .and_then(|()| paths.validate_routing());
    if let Err(message) = checked {
        let _ = writeln!(io::stderr(), "{message}");
        return finish(&paths, "attn", String::new(), None, 1, &env_terminal);
    }

    let mut hook = Hook {
        argv: &arguments,
        paths: &paths,
        getenv: &getenv,
        caller: String::new(),
    };
    let (code, refusal) = match hook.run(name) {
        Ok(()) => (0, None),
        Err(Failure { refusal }) => (1, refusal),
    };
    let caller = hook.caller;
    finish(&paths, name, caller, refusal, code, &env_terminal)
}

fn finish(
    paths: &Paths<'_>,
    path: &str,
    caller: String,
    refusal: Option<String>,
    code: u8,
    env_terminal: &str,
) -> ExitCode {
    invocation::record(
        paths,
        Invocation {
            path,
            caller,
            refusal,
            usage_error: None,
            session: None,
        },
        code,
        env_terminal,
    );
    ExitCode::from(code)
}

fn hand_off_to_attnd(argv: Vec<OsString>) -> ExitCode {
    let attnd = match attnd_beside_this_binary() {
        Ok(path) => path,
        Err(error) => {
            eprintln!("attn: cannot locate this executable to find attnd beside it: {error}");
            return ExitCode::from(EXIT_NOT_FOUND);
        }
    };
    let mut forwarded = argv.into_iter();
    let mut command = Command::new(&attnd);
    if let Some(program_name) = forwarded.next() {
        command.arg0(program_name);
    }
    let error = command.args(forwarded).exec();
    eprintln!(
        "attn: cannot run attnd at {}: {error}; attn and attnd ship together, so reinstall attn",
        attnd.display()
    );
    ExitCode::from(EXIT_NOT_FOUND)
}

fn attnd_beside_this_binary() -> io::Result<PathBuf> {
    let resolved = fs::canonicalize(std::env::current_exe()?)?;
    Ok(resolved.with_file_name("attnd"))
}
