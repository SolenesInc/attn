use std::ffi::OsString;
use std::fs;
use std::io;
use std::os::unix::process::CommandExt;
use std::path::PathBuf;
use std::process::{Command, ExitCode};

const EXIT_NOT_FOUND: u8 = 127;

pub fn run(argv: Vec<OsString>) -> ExitCode {
    hand_off_to_attnd(argv)
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
