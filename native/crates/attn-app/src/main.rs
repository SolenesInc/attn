use std::process::ExitCode;

use attn_app::automation::{AppHooks, enabled, manifest_path};
use attn_app::instance::remove_app_pid;
use attn_app::window_host::WindowHost;
use attn_automation::Server;
use attn_client::{Pulse, Store};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

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
    let drawn = Arc::new(AtomicU64::new(0));
    let app = attn_app::native_app(&launch.instance.bundle_id)
        .on_frame_encoded({
            let frames = frames.clone();
            move |_| frames.beat()
        })
        .on_frame_drawn({
            let (frames, drawn) = (frames.clone(), drawn.clone());
            move |rt| {
                drawn.store(rt.stats.commits, Ordering::Release);
                frames.beat();
            }
        });
    let handle = app.handle();
    let _automation = enabled(&launch.instance.name)
        .then(|| {
            let host = Arc::new(WindowHost {
                handle: handle.clone(),
                frames: frames.clone(),
                drawn,
            });
            let manifest = manifest_path(&launch.instance.bundle_id).ok_or("no home directory")?;
            Server::start(host, Arc::new(AppHooks), &manifest)
        })
        .transpose()
        .unwrap_or_else(|reason| {
            eprintln!("attn-app: automation is off: {reason}");
            None
        });
    let waker = handle.clone();
    let (_link, _window) = attn_app::connect(
        &store,
        |terminals, replies| launch.link_config(terminals, replies),
        attn_app::wall_clock_ms(),
        frames,
        move || waker.wake(),
        move |update| handle.update(update),
    );
    app.run();
    remove_app_pid();
    ExitCode::SUCCESS
}
