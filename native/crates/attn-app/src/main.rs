use std::hash::{BuildHasher, Hasher};
use std::path::PathBuf;
use std::process::ExitCode;

use attn_client::{Clock, Link, LinkConfig, Pulse, Store};

fn main() -> ExitCode {
    let config = match link_config() {
        Ok(config) => config,
        Err(why) => {
            eprintln!("attn-app: {why}");
            return ExitCode::FAILURE;
        }
    };
    let store = Store::default();
    let link = Link::start(config, store.clone());
    let app = attn_app::native_app();
    let handle = app.handle();
    let _window = attn_app::bind(&store, link.commands(), move |update| handle.update(update));
    app.run();
    ExitCode::SUCCESS
}

const USAGE: &str = "usage: attn-app --instance <name> --port <ws-port> --data-dir <dir>, for a named, non-production instance (attn instance resolve prints its port and data directory)";

/// The daemon this app talks to, named on the command line until the app carries its
/// instance's identity from the build.
fn link_config() -> Result<LinkConfig, String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let flag = |name: &str| {
        args.iter()
            .position(|a| a == name)
            .and_then(|i| args.get(i + 1))
            .cloned()
            .ok_or_else(|| format!("{name} is missing; {USAGE}"))
    };
    let instance = flag("--instance")?;
    if instance.is_empty() || instance == "default" {
        return Err(format!(
            "this build does not connect to the production daemon; {USAGE}"
        ));
    }
    let port = flag("--port")?;
    let token_path = PathBuf::from(flag("--data-dir")?).join("client-token");
    let client_token = std::fs::read_to_string(&token_path).map_err(|why| {
        format!(
            "cannot read the client token at {}: {why}",
            token_path.display()
        )
    })?;
    let process_nonce = std::collections::hash_map::RandomState::new()
        .build_hasher()
        .finish();
    Ok(LinkConfig {
        daemon: format!("127.0.0.1:{port}"),
        client_token: client_token.trim().to_owned(),
        instance,
        profile_id: None,
        client_id: format!("native-{}-{process_nonce:016x}", std::process::id()),
        app_pid: std::process::id(),
        clock: Clock::system(),
        pulse: Pulse::default(),
        frames: Box::new(|_| {}),
    })
}
