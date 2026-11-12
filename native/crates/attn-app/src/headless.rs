use std::path::PathBuf;
use std::sync::Arc;

use attn_automation::{Headless, Host, Server, Work};
use attn_client::{Clock, LinkConfig, Pulse, Store, TerminalSink};
use rune::Env;

use crate::State;
use crate::automation::AppHooks;

/// The window's size for a windowless run: what a scenario's parked window shows.
const VIEWPORT: (f32, f32) = (1200.0, 800.0);

/// `attn-app --automation-headless --manifest PATH`: the app with no window and no GPU,
/// against the daemon the environment names (`ATTN_WS_PORT`, `ATTN_DATA_DIR`, and
/// `ATTN_INSTANCE`), driven over the automation protocol. A stack test starts the daemon; this
/// mode never starts one. Prints `ready` once the manifest is written.
pub fn run(args: &[String]) -> Result<(), String> {
    let manifest = args
        .iter()
        .position(|a| a == "--manifest")
        .and_then(|i| args.get(i + 1))
        .map(PathBuf::from)
        .ok_or("--automation-headless needs --manifest PATH")?;
    let store = Store::default();
    let (host, inbox) = Headless::<State>::new();
    let (wake, updates) = (host.updates(), host.updates());
    let (link, _window) = crate::connect(
        &store,
        link_config()?,
        host.frames().clone(),
        move || {
            let _ = wake.send(Work::Update(Box::new(|_, _| {})));
        },
        move |update| {
            let _ = updates.send(Work::Update(update));
        },
    );
    let host = Arc::new(host);
    let server = Server::start(host.clone(), Arc::new(AppHooks), &manifest)?;
    println!("ready port={}", server.manifest.port);
    let env = Env {
        system_dark: true,
        scale: 2.0,
    };
    host.run(crate::app().headless_runtime(VIEWPORT, env), inbox);
    drop(link);
    unreachable!("the windowless host runs for the process's life")
}

fn link_config() -> Result<impl FnOnce(TerminalSink) -> LinkConfig, String> {
    let var =
        |name: &str| std::env::var(name).map_err(|_| format!("--automation-headless needs {name}"));
    let port = var("ATTN_WS_PORT")?;
    let data_dir = PathBuf::from(var("ATTN_DATA_DIR")?);
    let read = |file: &str| {
        let path = data_dir.join(file);
        std::fs::read_to_string(&path)
            .map(|s| s.trim().to_owned())
            .map_err(|e| format!("read {}: {e}", path.display()))
    };
    let instance = std::env::var("ATTN_INSTANCE")
        .ok()
        .filter(|i| !i.is_empty());
    let (client_token, host_token) = (
        read("client-token")?,
        crate::instance::ensure_host_token(&data_dir)?,
    );
    Ok(move |terminals| LinkConfig {
        daemon: format!("127.0.0.1:{port}"),
        client_token,
        host_token: Some(host_token),
        instance: instance.unwrap_or_else(|| "default".into()),
        profile_id: None,
        client_id: format!("native-headless-{}", std::process::id()),
        app_pid: std::process::id(),
        clock: Clock::system(),
        pulse: Pulse::default(),
        terminals,
    })
}
