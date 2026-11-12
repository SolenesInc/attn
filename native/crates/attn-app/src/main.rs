use std::process::ExitCode;

use attn_app::TerminalInbox;
use attn_app::instance::remove_app_pid;
use attn_client::{Link, Store};

fn main() -> ExitCode {
    rune_mac::launch();
    let launch = match attn_app::launch::prepare() {
        Ok(launch) => launch,
        Err(reason) => {
            eprintln!("attn-app: {reason}");
            return ExitCode::FAILURE;
        }
    };
    let store = Store::default();
    let app = attn_app::native_app(&launch.instance.bundle_id).on_quit(|_, _| remove_app_pid());
    let handle = app.handle();
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
