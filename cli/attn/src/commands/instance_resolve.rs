use super::{Run, Stop, USAGE_UNKNOWN_FLAG, print_json, print_stdout};
use crate::instance::normalize_instance_name;
use attn::go::{json, quote::quote};

const HELP: &str = r#"attn instance — inspect and resolve attn instances

An instance fully isolates attn's runtime: data dir, socket, websocket port,
installed app (a macOS bundle, a directory tree elsewhere), the app's local data
dir (Tauri's app_local_data_dir), and bundle identifier. ATTN_INSTANCE selects it
for every entrypoint (CLI, daemon, e2e, real-app harness, build).

Usage:
  attn instance                 status of the active instance (ATTN_INSTANCE)
  attn instance status          same
  attn instance resolve         resolved resources as JSON
  attn instance resolve --field wsPort      print one resolved value
  attn instance resolve --instance agent7   resolve a different instance
  attn instance tauri-config    Tauri --config overlay for the instance's build (--demo: titled attn)
  attn instance register-scheme Linux only: claim <scheme>:// for this instance's app in the desktop database
  attn instance clean <name>    reap workers + plugin drivers, stop daemon, quit app, remove its app, app local data, data dir, shared Codex sockets, and scheme handler
  attn instance clean <name> --keep-app
                                the same, but keep the installed app and scheme handler
  attn instance stop-app        quit the active instance's app (--instance <name> for another)
  attn instance list            every instance with data and/or an installed app
  attn instance list --json     same, machine-readable, with origin and what is running
  attn instance set-origin <name> [--worktree <dir>]
                                record the worktree an instance was installed from
  attn instance env <name>      alias of: attn instance-env <name>

Instance names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is the development
sibling (port 29849, ~/.attn-dev). `clean` refuses the default (production)
instance unless given --force.
"#;

const VALID_FIELDS: &str = "instance,label,dataDir,socket,dbPath,wsPort,bundleId,appName,appPath,appExecutable,appDaemon,appCli,appLocalDataDir,appLockPath,deepLinkScheme,desktopEntry,e2eDaemonPort,e2eVitePort,mockGitHubPort";

fn fatal(message: &str) -> Stop {
    eprintln!("attn instance: {message}");
    Stop(1)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut instance = run.paths.instance();
    let mut field = String::new();
    let mut rest = args.iter();
    while let Some(arg) = rest.next() {
        match arg.as_str() {
            "--json" => {}
            "--instance" => {
                let value = rest
                    .next()
                    .ok_or_else(|| fatal("--instance requires a value"))?;
                instance = normalize_instance_name(value).map_err(|error| fatal(&error))?;
            }
            "--field" => {
                let value = rest.next().ok_or_else(|| fatal("--field requires a key"))?;
                field.clone_from(value);
            }
            "-h" | "--help" => {
                print_stdout(HELP);
                return Ok(());
            }
            other => {
                run.usage_error = Some(USAGE_UNKNOWN_FLAG);
                return Err(fatal(&format!("unknown flag {}", quote(other.as_bytes()))));
            }
        }
    }
    let resolved = run.paths.resolve(&instance);
    if !field.is_empty() {
        let Some(value) = resolved.field(&field) else {
            return Err(fatal(&format!(
                "unknown field {} (valid: {VALID_FIELDS})",
                quote(field.as_bytes())
            )));
        };
        print_stdout(&format!("{value}\n"));
        return Ok(());
    }
    let compact = json::encode(&resolved).map_err(|error| fatal(&error.to_string()))?;
    print_json(&compact);
    Ok(())
}
