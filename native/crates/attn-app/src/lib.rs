mod action;
pub mod automation;
mod connection;
pub mod daemon;
mod dispatch;
mod frame;
pub mod headless;
pub mod instance;
mod keymap;
mod keys;
pub mod launch;
mod shell;
#[cfg(target_os = "macos")]
pub mod tauri_settings;
mod terminal;

use std::sync::Arc;

use attn_client::{Commands, Facts, Slices, Store, Subscription};
use rune::{AppSpec, KeyPlatform, MenuItem, Shortcut, Titlebar, Tx};

pub use action::{Action, Category};
pub use dispatch::dispatch;
pub use keymap::{Binding, Keymap};
pub use shell::{Drawer, Holder, Layer, Window};
pub use terminal::{TerminalInbox, Terminals};

/// A change for a window's state, made on the link's thread and run on the UI thread.
pub type Update = Box<dyn FnOnce(&mut State, &mut Tx) + Send>;

#[derive(Default)]
pub struct State {
    pub facts: Facts,
    pub keymap: Arc<Keymap>,
    pub window: Window,
    pub terminals: Terminals,
    daemon: Commands,
}

const READS: Slices = Slices::ALL;

/// Hands the window every change to a slice it reads, through `deliver`: `AppHandle::update`
/// in the app, the world's inbox in a wire test. The window sends its commands on `daemon` and
/// takes what the link hears about terminals from `terminals`.
pub fn bind(
    store: &Store,
    daemon: Commands,
    terminals: TerminalInbox,
    deliver: impl Fn(Update) + Send + Sync + 'static,
) -> Subscription {
    deliver(Box::new(move |state, _| {
        state.daemon = daemon;
        state.terminals.inbox = terminals;
    }));
    store.subscribe(READS, move |facts, changed| {
        let facts = facts.clone();
        let keymap = changed.intersects(Slices::SETTINGS).then(|| {
            let setting = facts.settings.get(keymap::SETTING).map(String::as_str);
            Arc::new(Keymap::from_setting(setting, KeyPlatform::current()))
        });
        deliver(Box::new(move |state, tx| {
            state.facts = facts;
            if let Some(keymap) = keymap {
                state.keymap = keymap;
            }
            state.window.follow(&state.facts);
            let keyboard = state.window.keyboard_terminal(&state.facts);
            state
                .terminals
                .follow(&state.facts, keyboard, tx.clock(), &state.daemon);
        }));
    })
}

fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    state.terminals.drain(tx, &state.daemon);
    shell::expire(state, now, tx)
}

/// The app as every window runs it, and as `App::headless_runtime` runs it in tests.
pub fn app() -> rune::App<State> {
    rune::App::new(AppSpec {
        on_key: Some(keys::on_key),
        poll: Some(poll),
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        min_size: Some((800.0, 600.0)),
        ..AppSpec::new("attn", (1200.0, 800.0), State::default, shell::view)
    })
    .titlebar(Titlebar::new(shell::TITLE_BAR_HEIGHT))
    .on_ime(keys::on_ime)
}

/// The app with its window, reopened where it last closed in the prefs `bundle_id` names, and
/// the items it adds to the macOS menu bar. Each item runs an action; AppKit takes ⌘. before
/// any view, so Stop Countdown's item is how its keys arrive.
pub fn native_app(bundle_id: &str) -> rune::App<State> {
    let stop_countdown = Shortcut::parse_on("mod+.", KeyPlatform::Mac).expect("⌘. parses");
    let app = app().playground(attn_ui::playground());
    frame::restored(app, bundle_id)
        .menu_item(
            MenuItem::new("Stop Countdown", |state, tx| {
                dispatch(state, Action::SessionCancelCountdown, tx)
            })
            .id(Action::SessionCancelCountdown.id())
            .menu("File")
            .key_equivalent(stop_countdown)
            .keys_first(),
        )
        .menu_item(
            MenuItem::new("Close Pane", |state, tx| {
                dispatch(state, Action::SessionClose, tx)
            })
            .id(Action::SessionClose.id())
            .menu("File"),
        )
        .menu_item(
            MenuItem::new("Pop Out or Bring Back Tile", |state, tx| {
                dispatch(state, Action::TilePopOut, tx)
            })
            .id(Action::TilePopOut.id())
            .menu("File"),
        )
        .menu_item(
            MenuItem::new("Quick Capture", |_, _| {})
                .id("quick-capture")
                .menu("File")
                .enabled(false),
        )
}
