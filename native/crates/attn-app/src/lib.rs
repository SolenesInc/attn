mod action;
pub mod annotations;
mod asks;
mod attention;
pub mod automation;
mod calendar;
mod callouts;
mod connection;
pub mod daemon;
mod deep_link;
pub mod desktop;
mod dispatch;
mod frame;
pub mod garden;
pub mod headless;
mod home;
pub mod instance;
mod keymap;
mod keys;
pub mod launch;
mod ledger;
mod markdown;
mod notifications;
mod opener;
mod palette;
mod picker;
mod profile_switcher;
pub mod prompts;
mod requests;
pub mod screen;
mod setting;
mod settings;
mod shell;
mod shortcut_editor;
pub mod sidebar;
#[cfg(target_os = "macos")]
pub mod tauri_settings;
mod terminal;
mod update_run;
pub mod window_host;

use std::sync::Arc;

use attn_client::{
    Commands, Facts, Link, LinkConfig, Pulse, ReplySink, Slices, Store, Subscription, TerminalSink,
};
use rune::{AppSpec, KeyPlatform, MenuItem, Shortcut, Titlebar, Tx};

pub use action::{Action, Category};
pub use deep_link::open_link;
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
    pub pictures: markdown::Pictures,
    pub drafts: markdown::Drafts,
    pub readings: markdown::Readings,
    pub opener: opener::Opener,
    daemon: Commands,
    requests: requests::Requests,
    /// Wall-clock milliseconds when the runtime's clock read zero.
    epoch_ms: i64,
    /// The runtime time the window next wakes to move turn ages on.
    age_wake: Option<f64>,
    /// Gives the menu bar's items the keys of each new keymap.
    menus: Option<Menus>,
}

/// Sets the menu bar's items for a keymap.
pub type Menus = Arc<dyn Fn(&Keymap) + Send + Sync>;

impl State {
    fn wall_ms(&self, tx: &Tx) -> i64 {
        self.epoch_ms + (tx.clock() * 1000.0) as i64
    }

    /// Reads the wall clock into the window and wakes it when the next turn age or activity
    /// staleness would change.
    fn tick(&mut self, tx: &mut Tx) {
        self.window.now_ms = self.wall_ms(tx);
        let now_ms = self.window.now_ms;
        self.age_wake = sidebar::next_change(self, now_ms)
            .into_iter()
            .chain(home::next_change(&self.facts, now_ms))
            .chain(palette::next_change(self, now_ms))
            .chain(asks::next_change(self, now_ms))
            .chain(settings::phones::next_change(self, now_ms))
            .min()
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
    let deliver = Arc::new(deliver);
    let woken = deliver.clone();
    let wake: rune::Waker = Arc::new(move || woken(Box::new(|_, _| {})));
    deliver(Box::new(move |state, _| {
        state.pictures.start(wake);
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
            if desktop::moves_tiles(state, &facts) {
                tx.animated();
            }
            let alone = desktop::shown_alone(state);
            let on_screen = desktop::arrival::on_screen(state);
            state.facts = facts;
            if was && !is {
                requests::forget(state, tx);
                settings::disconnected(state);
            }
            if !was && is {
                picker::reconnected(state, tx);
            }
            if let Some(keymap) = keymap {
                if let Some(menus) = &state.menus {
                    menus(&keymap);
                }
                state.keymap = keymap;
            }
            state.window.follow(&state.facts);
            if desktop::shown_alone(state) != alone {
                tx.instant();
            }
            desktop::settle(state, tx);
            desktop::arrival::follow(state, on_screen, tx);
            desktop::follow_windows(state);
            desktop::follow_window_title(state, tx);
            let _ = picker::follow(state, tx);
            deep_link::settle(state, tx);
            state.tick(tx);
            sidebar::snooze::follow(state);
            sidebar::session_menu::follow(state);
            sidebar::width::follow(state);
            palette::follow(state);
            asks::follow(state, tx);
            ledger::follow(state, was, tx);
            notifications::follow(state, was, tx);
            update_run::follow(state, tx);
            settings::follow(state, changed, tx);
            shortcut_editor::follow(state);
            sidebar::advance(state, tx);
            garden::follow(state, tx);
            if state.window.tile.is_none() {
                let keyboard = state.window.keyboard_terminal(&state.facts);
                state
                    .terminals
                    .follow(&state.facts, keyboard, tx.clock(), &state.daemon);
                annotations::draft::follow(state, tx);
            }
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
    let recounted = state.terminals.drain(tx, &state.daemon);
    let said = state.terminals.take_said();
    let told = said.is_some();
    if let Some(said) = said {
        state.window.notify(said, tx);
    }
    let answered = requests::drain(state, tx);
    annotations::draft::follow(state, tx);
    let expired = annotations::draft::expire(state, now) | annotations::draft::due(state, now, tx);
    let browsed = picker::browse_due(state, tx);
    let launched = picker::follow(state, tx);
    let browsed = browsed | opener::browse_due(state, tx);
    let pictured = markdown::follow_images(state, now, tx);
    let drafted = markdown::drafts::follow(state, now, tx)
        | markdown::annotating::expire(state, now)
        | markdown::sending::expire(state, now);
    let aged = state.age_wake.is_some_and(|at| now >= at);
    if aged {
        state.tick(tx);
    }
    let drains = sidebar::follow_countdowns(state, state.wall_ms(tx))
        | desktop::follow_settling(state, state.wall_ms(tx));
    let settings = settings::poll(state, now, tx);
    let updating = update_run::poll(state, now, tx) | attention::poll(state, now);
    let pointed = callouts::poll(state, tx);
    shell::expire(state, now, tx)
        || settings
        || updating
        || pointed
        || recounted
        || told
        || aged
        || answered
        || browsed
        || launched
        || pictured
        || drafted
        || drains
        || expired
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
    .on_field_key(keys::on_field_key)
    .on_open_url(open_link)
    .on_modifiers(keys::on_modifiers)
}

/// The app of a window that shows one popped-out tile, `size` points large;
/// `desktop::Setup` makes a bound window of it a tile window.
pub fn tile_window_app(size: (f32, f32)) -> rune::App<State> {
    rune::App::new(AppSpec {
        on_key: Some(keys::on_key),
        poll: Some(poll),
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        min_size: Some((320.0, 200.0)),
        ..AppSpec::new("attn", size, State::default, desktop::windows_view)
    })
    .on_ime(keys::on_ime)
    .on_field_key(keys::on_field_key)
    .on_focus(desktop::window_focus)
    .on_quit(desktop::tile_window_quits)
    .on_close(desktop::tile_window_closed)
}

impl State {
    /// Lets this main window open tile windows through `opener`; `main` hands it their changes.
    pub fn open_tile_windows(&mut self, opener: Arc<dyn desktop::Opener>, main: desktop::Deliver) {
        self.window.desk.windows.opener = Some(opener);
        self.window.desk.windows.main = Some(main);
        desktop::follow_windows(self);
    }

    /// Keeps the menu bar's key equivalents on the keys the keymap gives their actions.
    pub fn follow_menus(&mut self, menus: Menus) {
        menus(&self.keymap);
        self.menus = Some(menus);
    }
}

fn quit(state: &mut State, tx: &mut Tx) {
    frame::save(state, tx);
    instance::remove_app_pid();
}

/// The person closed the main window. On macOS it hides while a tile window shows, and comes
/// back when the app is reopened or the last tile window goes; otherwise the app quits.
fn main_closed(state: &mut State, tx: &mut Tx) {
    let windows = &mut state.window.desk.windows;
    if cfg!(target_os = "macos") && windows.shows_a_window() {
        windows.main_hidden = true;
    } else {
        tx.quit();
    }
}

/// The app was reopened (its Dock icon clicked, or opened again): the main window comes
/// forward, whether or not a tile window shows.
fn main_reopened(state: &mut State, _: bool, _: &mut Tx) -> bool {
    state.window.desk.windows.main_hidden = false;
    frame::open_main();
    true
}

/// The app with its window, reopened where it last closed in the prefs `bundle_id` names, and
/// the items it adds to the macOS menu bar.
pub fn native_app(bundle_id: &str) -> rune::App<State> {
    let app = app()
        .compositor(screen::Screens::default())
        .playground(attn_ui::playground());
    let app = frame::restored(app, bundle_id)
        .keep_running()
        .on_close(main_closed)
        .on_reopen(main_reopened)
        .on_quit(quit);
    menu_items(&Keymap::default())
        .into_iter()
        .fold(app, |app, item| app.menu_item(item))
}

/// The items attn adds to the macOS menu bar, each running an action, with the keys `keymap`
/// binds Stop Countdown to: AppKit takes ⌘. before any view, so its item is how its keys
/// arrive, and the window still hears them first.
pub fn menu_items(keymap: &Keymap) -> Vec<MenuItem<State>> {
    let stop = MenuItem::new("Stop Countdown", |state, tx| {
        dispatch(state, Action::SessionCancelCountdown, tx);
    })
    .id(Action::SessionCancelCountdown.id())
    .menu("File")
    .keys_first();
    let stop_keys = match keymap.binding(&Action::SessionCancelCountdown) {
        Some(Binding::Chord(chord)) => {
            Shortcut::parse_on(&chord.to_string(), KeyPlatform::Mac).ok()
        }
        _ => None,
    };
    vec![
        match stop_keys {
            Some(keys) => stop.key_equivalent(keys),
            None => stop,
        },
        MenuItem::new("Close Pane", |state, tx| {
            dispatch(state, Action::SessionClose, tx);
        })
        .id(Action::SessionClose.id())
        .menu("File"),
        MenuItem::new("Pop Out or Bring Back Tile", |state, tx| {
            dispatch(state, Action::TilePopOut, tx);
        })
        .id(Action::TilePopOut.id())
        .menu("File"),
        MenuItem::new("Quick Capture", |_, _| {})
            .id("quick-capture")
            .menu("File")
            .enabled(false),
    ]
}
