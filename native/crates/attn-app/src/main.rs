use std::process::ExitCode;

use attn_app::TerminalInbox;
use attn_app::automation::{AppHooks, enabled, manifest_path};
use attn_app::instance::remove_app_pid;
use attn_app::window_host::WindowHost;
use attn_automation::Server;
use attn_client::{Link, Pulse, Store};
use std::sync::Arc;

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|a| a == "--automation-headless") {
        return match attn_app::headless::run(&args) {
            Ok(()) => ExitCode::SUCCESS,
            Err(reason) => {
                eprintln!("attn-app: {reason}");
                ExitCode::FAILURE
            }
        };
    }
    rune_mac::launch();
    let launch = match attn_app::launch::prepare() {
        Ok(launch) => launch,
        Err(reason) => {
            eprintln!("attn-app: {reason}");
            return ExitCode::FAILURE;
        }
    };
    let store = Store::default();
    let frames = Pulse::default();
    let beat = frames.clone();
    let app = attn_app::native_app(&launch.instance.bundle_id)
        .on_quit(|_, _| remove_app_pid())
        .on_frame_encoded(move |_| beat.beat());
    let handle = app.handle();
    let _automation = enabled(&launch.instance.name)
        .then(|| {
            let host = Arc::new(WindowHost {
                handle: handle.clone(),
                frames,
            });
            let manifest = manifest_path(&launch.instance.bundle_id).ok_or("no home directory")?;
            Server::start(host, Arc::new(AppHooks), &manifest)
        })
        .transpose()
        .unwrap_or_else(|reason| {
            eprintln!("attn-app: automation is off: {reason}");
            None
        });
    let terminals = TerminalInbox::default();
    let waker = handle.clone();
    let link = Link::start(
        launch.link_config(terminals.sink(move || waker.wake())),
        store.clone(),
    );
    let _window = attn_app::bind(&store, link.commands(), terminals, move |update| {
        handle.update(update)
    });
    app.run();
    remove_app_pid();
    ExitCode::SUCCESS
}
