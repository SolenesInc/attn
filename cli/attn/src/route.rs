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
        None => hand_off_to_attnd(argv),
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
