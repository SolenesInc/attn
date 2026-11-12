mod action;
pub mod automation;
mod connection;
pub mod daemon;
pub mod desktop;
mod dispatch;
mod frame;
pub mod headless;
mod home;
pub mod instance;
mod keymap;
mod keys;
pub mod launch;
mod picker;
mod requests;
pub mod screen;
mod setting;
mod shell;
pub mod sidebar;
#[cfg(target_os = "macos")]
pub mod tauri_settings;
mod terminal;
pub mod window_host;

use std::sync::Arc;

use attn_client::{
    Commands, Facts, Link, LinkConfig, Pulse, ReplySink, Slices, Store, Subscription, TerminalSink,
};
use rune::{AppSpec, KeyPlatform, MenuItem, Shortcut, Titlebar, Tx};

pub use action::{Action, Category};
pub use dispatch::dispatch;
pub use keymap::{Binding, Keymap};
pub use requests::ReplyInbox;
pub use shell::{Drawer, Holder, Layer, View, Window};
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
    requests: requests::Requests,
    /// Wall-clock milliseconds when the runtime's clock read zero.
    epoch_ms: i64,
    /// The runtime time the window next wakes to move turn ages on.
    age_wake: Option<f64>,
}

impl State {
    fn wall_ms(&self, tx: &Tx) -> i64 {
        self.epoch_ms + (tx.clock() * 1000.0) as i64
    }

    /// Reads the wall clock into the window and wakes it when the next age would change.
    fn tick(&mut self, tx: &mut Tx) {
        self.window.now_ms = self.wall_ms(tx);
        self.age_wake = sidebar::next_change(self, self.window.now_ms)
            .map(|at| (at - self.epoch_ms) as f64 / 1000.0);
        if let Some(at) = self.age_wake {
            tx.wake_at(at);
        }
    }
}

/// The wall clock's milliseconds since 1970, for `bind` in the app: tests pass their own.
#[allow(
    clippy::disallowed_methods,
    reason = "the one read of the wall clock, at launch"
)]
pub fn wall_clock_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_millis() as i64)
}

const READS: Slices = Slices::ALL;

/// Hands the window every change to a slice it reads, through `deliver`: `AppHandle::update`
/// in the app, the world's inbox in a wire test. The window sends its commands on `daemon` and
/// takes what the link hears about terminals from `terminals`, and answers to its requests from
/// `replies`.
pub fn bind(
    store: &Store,
    daemon: Commands,
    terminals: TerminalInbox,
    replies: ReplyInbox,
    epoch_ms: i64,
    deliver: impl Fn(Update) + Send + Sync + 'static,
) -> Subscription {
    deliver(Box::new(move |state, _| {
        state.epoch_ms = epoch_ms;
        state.daemon = daemon;
        state.terminals.inbox = terminals;
        state.requests.inbox = replies;
    }));
    store.subscribe(READS, move |facts, changed| {
        let facts = facts.clone();
        let keymap = changed.intersects(Slices::SETTINGS).then(|| {
            let setting = facts.settings.get(keymap::SETTING).map(String::as_str);
            Arc::new(Keymap::from_setting(setting, KeyPlatform::current()))
        });
        deliver(Box::new(move |state, tx| {
            let connected = |facts: &Facts| facts.connection == attn_client::Connection::Connected;
            let (was, is) = (connected(&state.facts), connected(&facts));
            state.facts = facts;
            if was && !is {
                requests::forget(state, tx);
            }
            if !was && is {
                picker::reconnected(state, tx);
            }
            if let Some(keymap) = keymap {
                state.keymap = keymap;
            }
            state.window.follow(&state.facts);
            desktop::settle(state);
            let _ = picker::follow(state, tx);
            state.tick(tx);
            sidebar::snooze::follow(state);
            sidebar::advance(state, tx);
            let keyboard = state.window.keyboard_terminal(&state.facts);
            state
                .terminals
                .follow(&state.facts, keyboard, tx.clock(), &state.daemon);
        }));
    })
}

/// Connects a window to the daemon: the link starts from what `config` makes of the terminal
/// and reply sinks, which call `wake` for each terminal event and reply, and the store's changes
/// reach the state
/// through `deliver`. `changed` beats once each change the link hears is in the state, a
/// terminal's output once the window took it, so a request waiting for the screen wakes for it
/// even in a covered window, which draws no frame.
pub fn connect(
    store: &Store,
    config: impl FnOnce(TerminalSink, ReplySink) -> LinkConfig,
    epoch_ms: i64,
    changed: Pulse,
    wake: impl Fn() + Send + Sync + 'static,
    deliver: impl Fn(Update) + Send + Sync + 'static,
) -> (Link, Subscription) {
    let terminals = TerminalInbox::beating(changed.clone());
    let replies = ReplyInbox::default();
    let wake = Arc::new(wake);
    let (woken, replied) = (wake.clone(), wake);
    let link = Link::start(
        config(
            terminals.sink(move || woken()),
            replies.sink(move || replied()),
        ),
        store.clone(),
    );
    let window = bind(
        store,
        link.commands(),
        terminals,
        replies.clone(),
        epoch_ms,
        move |update| {
            let changed = changed.clone();
            deliver(Box::new(move |state, tx| {
                update(state, tx);
                changed.beat();
            }));
        },
    );
    (link, window)
}

fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    state.terminals.drain(tx, &state.daemon);
    let answered = requests::drain(state, tx);
    let browsed = picker::browse_due(state, tx);
    let launched = picker::follow(state, tx);
    let aged = state.age_wake.is_some_and(|at| now >= at);
    if aged {
        state.tick(tx);
    }
    shell::expire(state, now, tx) || aged || answered || browsed || launched
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

fn quit(state: &mut State, tx: &mut Tx) {
    frame::save(state, tx);
    instance::remove_app_pid();
}

/// The app with its window, reopened where it last closed in the prefs `bundle_id` names, and
/// the items it adds to the macOS menu bar. Each item runs an action; AppKit takes ⌘. before
/// any view, so Stop Countdown's item is how its keys arrive.
pub fn native_app(bundle_id: &str) -> rune::App<State> {
    let stop_countdown = keymap::default_chord(&Action::SessionCancelCountdown, KeyPlatform::Mac)
        .and_then(|chord| Shortcut::parse_on(chord, KeyPlatform::Mac).ok())
        .expect("Stop Countdown's Mac default is a menu key equivalent");
    let app = app()
        .compositor(screen::Screens::default())
        .playground(attn_ui::playground());
    frame::restored(app, bundle_id)
        .on_quit(quit)
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
