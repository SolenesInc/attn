mod client;
mod commands;
mod hooks;
mod instance;
mod invocation;
mod paths;
mod route;

use std::process::ExitCode;

fn main() -> ExitCode {
    route::run(std::env::args_os().collect())
}
