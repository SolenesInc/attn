use std::cell::Cell;
use std::sync::{Arc, Mutex, OnceLock};
use std::time::Duration;

use attn_app::State;
use attn_app::screen::Screens;
use attn_client::{
    Clock, Connection, Frame, Link, LinkConfig, ManualClock, Parked, Pulse, Store, Subscription,
    TerminalEvent,
};
use rune::{Chord, Compositor, Env, Key, KeyEvent, Mods, Runtime, Tx};

use attn_app::automation::AppHooks;
use attn_automation::Attempt;
use serde_json::Value;

use crate::daemon::{CLIENT_TOKEN, Daemon, INSTANCE};
use crate::screen::{Screen, Target};

/// A wire test's settle takes milliseconds; one that has not gone quiet after this long is
/// stuck, and the test says where.
const STUCK: Duration = Duration::from_secs(10);
const VIEWPORT: (f32, f32) = (960.0, 640.0);
const FRAME: f64 = 1.0 / 60.0;

/// The wall clock when every world starts: 2026-11-12T09:00:00Z.
pub const WALL_START_MS: i64 = 1_794_474_000_000;

/// The app on a windowless `Runtime`, with its real link connected to a scripted daemon over
/// loopback. Time moves only when the test calls `advance`.
pub struct World {
    pub daemon: Daemon,
    /// The clipboard the app copies to and pastes from, in place of the system's.
    pub clipboard: rune::clipboard::MemoryClipboard,
    runtime: Runtime<State>,
    screens: Screens,
    covered: bool,
    terminal_frames: usize,
    link: Option<Link>,
    _window: Subscription,
    inbox: Arc<Mutex<Vec<attn_app::Update>>>,
    frames: Arc<Mutex<Vec<Frame>>>,
    terminals: attn_app::TerminalInbox,
    replies: attn_app::ReplyInbox,
    clock: ManualClock,
    pulse: Pulse,
    store: Store,
    orders: crate::tiles::Orders,
    tiles: Vec<crate::tiles::TileRuntime>,
}

type OnKey = fn(&mut State, &KeyEvent, &mut Tx) -> bool;

/// The app's own key handler, which [`heard`] stands in for.
static APP_ON_KEY: OnceLock<OnKey> = OnceLock::new();

thread_local! {
    /// The app's key handler left the last key it heard.
    static LEFT: Cell<bool> = const { Cell::new(false) };
}

/// The app's key handler, noting the keys it leaves: macOS's menu bar acts on a standard
/// item's key, such as Window > Close's ⌘W, only when the window leaves it.
fn heard(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let took = APP_ON_KEY.get().expect("the app's key handler")(state, ev, tx);
    LEFT.set(!took);
    took
}

/// What a gesture can do to the app.
pub struct Driver<'a> {
    pub runtime: &'a mut Runtime<State>,
}

impl Driver<'_> {
    /// Changes the window's state as a handler would.
    pub fn update(&mut self, f: impl FnOnce(&mut State, &mut Tx)) {
        self.runtime.update(f);
    }

    /// Opens a link with the app, as the system hands one to `App::on_open_url`.
    pub fn open_link(&mut self, url: &str) {
        self.runtime
            .update(|state, tx| attn_app::open_link(state, url, tx));
    }

    /// Presses a key, as the window hears it from the keyboard.
    pub fn key(&mut self, ev: KeyEvent) {
        self.runtime.key(ev);
    }

    /// Presses `chord` as this platform's keyboard sends it: "mod+shift+k" is ⇧⌘K on macOS
    /// and Ctrl+Shift+K on Linux.
    pub fn press(&mut self, chord: &str) {
        let chord = Chord::parse(chord).unwrap_or_else(|why| panic!("{why}"));
        self.key(key_event(&chord));
    }

    /// Types `text` one character at a time.
    pub fn type_text(&mut self, text: &str) {
        for c in text.chars() {
            self.key(KeyEvent {
                key: Key::Text(c.to_string()),
                mods: Mods::default(),
            });
        }
    }

    /// Clicks the element `target` names.
    pub fn click(&mut self, target: Target) {
        let screen = Screen(self.runtime.accessibility_json());
        let key = screen
            .get(target.clone())
            .key
            .unwrap_or_else(|| panic!("{target:?} has no key to click by:\n{screen}"));
        let (x, y) = self
            .runtime
            .find(&key)
            .unwrap_or_else(|| panic!("{key} is not laid out:\n{screen}"));
        self.click_at((x, y));
    }

    /// Presses and lets go at `at` in window points.
    pub fn click_at(&mut self, at: (f32, f32)) {
        self.runtime.pointer_down(at.0, at.1);
        self.runtime.pointer_up(at.0, at.1);
    }

    /// Presses the element `target` names, drags to `to` in window points, and lets go there.
    pub fn drag(&mut self, target: Target, to: (f32, f32)) {
        self.drag_over(target, to);
        self.release(to);
    }

    /// Moves the held pointer to `at` in window points.
    pub fn move_to(&mut self, at: (f32, f32)) {
        self.runtime.pointer_move(at.0, at.1);
        self.runtime.frame(0.0);
    }

    /// Holds down `mods`, or lets go of the modifiers with `Mods::default()`.
    pub fn hold(&mut self, mods: rune::Mods) {
        self.runtime
            .physical_input(rune::PhysicalInput::Modifiers(mods));
    }

    /// Lets go of the pointer at `at`.
    pub fn release(&mut self, at: (f32, f32)) {
        self.runtime.pointer_up(at.0, at.1);
    }

    /// Presses the element `target` names and drags to `to` in window points, still held.
    pub fn drag_over(&mut self, target: Target, to: (f32, f32)) {
        let screen = Screen(self.runtime.accessibility_json());
        let key = screen
            .get(target.clone())
            .key
            .unwrap_or_else(|| panic!("{target:?} has no key to drag by:\n{screen}"));
        let (x, y) = self
            .runtime
            .find(&key)
            .unwrap_or_else(|| panic!("{key} is not laid out:\n{screen}"));
        self.drag_from((x, y), to);
    }

    /// Presses at `from` and drags to `to`, both in window points, still held.
    pub fn drag_from(&mut self, from: (f32, f32), to: (f32, f32)) {
        let (x, y) = from;
        self.runtime.pointer_down(x, y);
        self.runtime.frame(0.0);
        let steps = 4;
        for i in 1..=steps {
            let f = i as f32 / steps as f32;
            self.runtime
                .pointer_move(x + (to.0 - x) * f, y + (to.1 - y) * f);
            self.runtime.frame(0.0);
        }
    }
}

/// The key event the keyboard sends for `chord` on this platform.
pub fn key_event(chord: &Chord) -> KeyEvent {
    attn_automation::key_event(chord).unwrap_or_else(|why| panic!("{why}"))
}

impl World {
    /// Starts the app against `daemon` and returns once both are quiet: after the handshake,
    /// or after the app gave up on the daemon.
    pub fn start(daemon: Daemon) -> World {
        World::start_sized(daemon, VIEWPORT)
    }

    /// [`World::start`] with a window `size` points wide and tall.
    pub fn start_sized(daemon: Daemon, size: (f32, f32)) -> World {
        World::start_with(daemon, size, attn_app::whats_new::seen_prefs())
    }

    /// [`World::start`] for someone whose app settings are `prefs`, such as a first launch's
    /// empty store or a file an earlier world saved to.
    pub fn start_with_prefs(daemon: Daemon, prefs: rune::prefs::Prefs) -> World {
        World::start_with(daemon, VIEWPORT, prefs)
    }

    fn start_with(daemon: Daemon, size: (f32, f32), prefs: rune::prefs::Prefs) -> World {
        attn_app::use_prefs(prefs);
        let store = Store::default();
        let pulse = daemon.pulse();
        let inbox: Arc<Mutex<Vec<attn_app::Update>>> = Arc::default();
        let frames: Arc<Mutex<Vec<Frame>>> = Arc::default();
        let clock = ManualClock::default();
        let received = frames.clone();
        let terminals = attn_app::TerminalInbox::default();
        let to_app = {
            let pulse = pulse.clone();
            terminals.sink(move || pulse.beat())
        };
        let replies = attn_app::ReplyInbox::default();
        let answers = {
            let pulse = pulse.clone();
            replies.sink(move || pulse.beat())
        };
        let link = Link::start(
            LinkConfig {
                daemon: daemon.address().to_string(),
                client_token: CLIENT_TOKEN.into(),
                host_token: None,
                instance: INSTANCE.into(),
                profile_id: None,
                client_id: "wire-client".into(),
                app_pid: std::process::id(),
                clock: Clock::Manual(clock.clone()),
                pulse: pulse.clone(),
                terminals: Box::new(move |event| {
                    if let TerminalEvent::Frame(frame) = &event {
                        received.lock().unwrap().push(frame.clone());
                    }
                    to_app(event);
                }),
                replies: answers,
                start_daemon: Some(daemon.starter()),
                migration_marker: Some(daemon.migration_marker()),
            },
            store.clone(),
        );
        let window = {
            let (inbox, pulse) = (inbox.clone(), pulse.clone());
            attn_app::bind(
                &store,
                link.commands(),
                terminals.clone(),
                replies.clone(),
                WALL_START_MS,
                move |update| {
                    inbox.lock().unwrap().push(update);
                    pulse.beat();
                },
            )
        };
        let env = Env {
            system_dark: true,
            scale: 2.0,
        };
        let mut runtime = attn_app::app().headless_runtime(size, env);
        APP_ON_KEY.get_or_init(|| runtime.on_key.expect("the app hears keys"));
        runtime.on_key = Some(heard);
        let clipboard = rune::clipboard::memory();
        runtime.use_clipboard(&clipboard);
        let orders = crate::tiles::Orders {
            list: Arc::default(),
            pulse: pulse.clone(),
        };
        {
            let opener = Arc::new(crate::tiles::HeadlessOpener(orders.clone()));
            let (inbox, pulse) = (inbox.clone(), pulse.clone());
            let main: attn_app::desktop::Deliver = Arc::new(move |update| {
                inbox.lock().unwrap().push(update);
                pulse.beat();
            });
            runtime.update(move |state, _| state.open_tile_windows(opener, main));
        }
        let mut world = World {
            daemon,
            clipboard,
            runtime,
            screens: Screens::default(),
            covered: false,
            terminal_frames: 0,
            link: Some(link),
            _window: window,
            inbox,
            frames,
            terminals,
            replies,
            clock,
            pulse,
            store,
            orders,
            tiles: Vec::new(),
        };
        world.settle();
        world
    }

    /// The person resizes the window to `size` points wide and tall, then settles.
    pub fn resize(&mut self, size: (f32, f32)) {
        let scale = self.runtime.env.scale;
        self.runtime.set_viewport(size, scale);
        self.settle();
    }

    /// Presses `ev` as macOS's menu bar hands the window the key of a standard item, such as
    /// Window > Close's ⌘W or Edit > Redo's ⇧⌘Z, then settles. True when the app left the key,
    /// so the item acts: Close closes the window, and with the only window the app quits.
    pub fn press_menu_key(&mut self, ev: KeyEvent) -> bool {
        LEFT.set(false);
        self.runtime.key(ev);
        self.settle();
        LEFT.get()
    }

    /// Runs a user action, then returns once the app and the daemon have stopped exchanging
    /// messages.
    pub fn gesture(&mut self, f: impl FnOnce(&mut Driver)) {
        f(&mut Driver {
            runtime: &mut self.runtime,
        });
        self.settle();
    }

    /// Returns once nothing is in flight: every message each side sent has been handled, the
    /// window has drawn what the link delivered with its motion landed, and the link waits on
    /// the socket or on a time the clock has not reached.
    pub fn settle(&mut self) {
        self.exchange(None);
    }

    /// Runs `cause`, a user action or a change the daemon sends, and records the window for the
    /// motion gate: the app and the daemon finish their exchange before the app's clock moves,
    /// then `secs` of motion play at `hz` frames a second, and the window settles.
    pub fn record(
        &mut self,
        hz: f64,
        secs: f64,
        cause: impl FnOnce(&Daemon, &mut Driver),
    ) -> rune::motion_gate::Recording {
        self.settle();
        let mut sampler = rune::motion_gate::Sampler::new(&mut self.runtime, hz);
        self.sample(&mut sampler, 0.0);
        sampler.input("gesture");
        cause(
            &self.daemon,
            &mut Driver {
                runtime: &mut self.runtime,
            },
        );
        self.exchange(Some(&mut sampler));
        for _ in 0..(secs * hz).round() as usize {
            self.sample(&mut sampler, 1.0 / hz);
        }
        self.settle();
        sampler.rec
    }

    fn still_frame(&mut self, sampler: &mut Option<&mut rune::motion_gate::Sampler>) {
        match sampler {
            Some(sampler) => self.sample(sampler, 0.0),
            None => {
                self.runtime.frame(0.0);
            }
        }
    }

    /// Lands the motion the last frame started, unless a sampler records it.
    fn land_motion(&mut self, sampler: &Option<&mut rune::motion_gate::Sampler>) {
        if sampler.is_none() && self.runtime.animating {
            self.runtime.settle_all();
            self.runtime.frame(0.0);
        }
    }

    fn sample(&mut self, sampler: &mut rune::motion_gate::Sampler, dt: f64) {
        sampler.before(&mut self.runtime);
        let awake = self.runtime.frame(dt);
        sampler.frame(&mut self.runtime, awake);
    }

    /// Delivers what each side sent until both are quiet, on frames that move no time. With no
    /// `sampler` the window lands its motion; with one, each frame is recorded and the motion
    /// keeps running.
    fn exchange(&mut self, mut sampler: Option<&mut rune::motion_gate::Sampler>) {
        loop {
            let seen = self.pulse.count();
            let updates = std::mem::take(&mut *self.inbox.lock().unwrap());
            for update in updates {
                self.runtime.update(update);
            }
            self.still_frame(&mut sampler);
            self.land_motion(&sampler);
            while self.runtime.has_pending_input() {
                self.still_frame(&mut sampler);
                self.land_motion(&sampler);
            }
            if !self.covered && self.screens.redraws(&self.runtime) {
                self.screens.draw_without_gpu(&mut self.runtime);
                self.terminal_frames += 1;
            }
            self.follow_orders();
            for tile in &mut self.tiles {
                tile.settle();
            }
            if self.quiet()
                && self.orders.list.lock().unwrap().is_empty()
                && self.tiles.iter().all(|t| t.idle())
                && self.inbox.lock().unwrap().is_empty()
                && self.terminals.is_empty()
                && self.replies.is_empty()
                && !self.runtime.state.pictures.decoding()
            {
                return;
            }
            assert!(
                self.pulse.wait_past(seen, STUCK),
                "the app and the daemon were still busy after {STUCK:?}: link {:?}, daemon {:?}, clock at {:?}",
                self.link().traffic(),
                self.daemon.traffic(),
                self.clock.now(),
            );
        }
    }

    /// Moves the app's clock and the link's together, then settles.
    pub fn advance(&mut self, by: Duration) {
        self.clock.advance(by);
        self.pulse.beat();
        self.runtime.frame(by.as_secs_f64());
        self.settle();
    }

    /// Hangs up, waits out the link's retry, and returns once the app has connected again and
    /// finished its handshake.
    pub fn reconnect(&mut self) {
        let before = self.daemon.connections();
        self.daemon.disconnect();
        self.settle();
        while self.daemon.connections() == before
            || self.runtime.state.facts.connection != Connection::Connected
        {
            let Parked::Until(at) = self.link().traffic().parked else {
                panic!(
                    "the link is not waiting to reconnect: {:?}\n{}",
                    self.link().traffic(),
                    self.screen()
                );
            };
            self.advance(at.saturating_sub(self.clock.now()));
        }
    }

    fn follow_orders(&mut self) {
        let orders = std::mem::take(&mut *self.orders.list.lock().unwrap());
        for order in orders {
            match order {
                crate::tiles::Order::Open { key, frame, setup } => {
                    let daemon = self.link().commands();
                    let tile = crate::tiles::TileRuntime::open(
                        key,
                        frame,
                        setup,
                        &self.store,
                        daemon,
                        &self.pulse,
                    );
                    self.tiles.push(tile);
                }
                crate::tiles::Order::Place { key, frame } => {
                    let tile = self.tile_mut(&key);
                    *tile.frame.lock().unwrap() = frame;
                    tile.shown = true;
                }
                crate::tiles::Order::Hide { key } => self.tile_mut(&key).shown = false,
                crate::tiles::Order::Update { key, update } => {
                    self.tile_mut(&key).inbox.lock().unwrap().push(update);
                }
            }
        }
    }

    fn tile_mut(&mut self, key: &str) -> &mut crate::tiles::TileRuntime {
        self.tiles
            .iter_mut()
            .find(|t| t.key == key)
            .unwrap_or_else(|| panic!("the app has no tile window {key}"))
    }

    /// The tile windows the app opened and has not closed, oldest first.
    pub fn tile_windows(&self) -> Vec<crate::TileWindowView> {
        self.tiles.iter().map(|t| t.view()).collect()
    }

    /// What tile window `key` shows to assistive tech.
    pub fn tile_screen(&self, key: &str) -> Screen {
        self.tiles
            .iter()
            .find(|t| t.key == key)
            .unwrap_or_else(|| panic!("the app has no tile window {key}"))
            .screen()
    }

    /// Runs a user action in tile window `key`, then settles.
    pub fn tile_gesture(&mut self, key: &str, f: impl FnOnce(&mut Driver)) {
        f(&mut Driver {
            runtime: &mut self.tile_mut(key).runtime,
        });
        self.settle();
    }

    /// The person moves tile window `key` to `frame` and then works in another window.
    pub fn move_tile_window(&mut self, key: &str, frame: attn_app::desktop::layout::Frame) {
        let tile = self.tile_mut(key);
        tile.runtime.window_focus(true);
        *tile.frame.lock().unwrap() = frame;
        tile.runtime.window_focus(false);
        self.settle();
    }

    /// The person closes tile window `key` from its title bar.
    pub fn close_tile_window(&mut self, key: &str) {
        let at = self
            .tiles
            .iter()
            .position(|t| t.key == key)
            .unwrap_or_else(|| panic!("the app has no tile window {key}"));
        let mut tile = self.tiles.remove(at);
        tile.runtime.update(attn_app::desktop::tile_window_closed);
        self.settle();
    }

    /// The app quits with its tile windows open: each runs its quit, then closes.
    pub fn quit_tile_windows(&mut self) {
        for mut tile in std::mem::take(&mut self.tiles) {
            tile.runtime.update(attn_app::desktop::tile_window_quits);
            tile.runtime.update(attn_app::desktop::tile_window_closed);
        }
        self.settle();
    }

    /// Whether every tile window is idle: no frame requested and no wake deadline.
    pub fn tile_windows_are_at_rest(&self) -> bool {
        self.tiles
            .iter()
            .all(|t| !t.runtime.animating && t.runtime.wake_deadline().is_none())
    }

    pub fn now(&self) -> Duration {
        self.clock.now()
    }

    /// Answers one automation request on this world's runtime, as the app's server would,
    /// settling after each attempt. A request that waits again on a screen that did not
    /// change fails with what it waited for: nothing in a quiet world would move it.
    pub fn automate(&mut self, action: &str, payload: Value) -> Result<Value, String> {
        let mut last = None;
        loop {
            let attempt = attn_automation::act(&mut self.runtime, &AppHooks, action, &payload);
            self.settle();
            match attempt {
                Attempt::Done(value) => return Ok(value),
                Attempt::Fail(why) => return Err(why),
                Attempt::Wait(what) => {
                    let screen = self.runtime.accessibility_json();
                    if last.as_ref() == Some(&screen) {
                        return Err(format!(
                            "{action}: the world is quiet and still waits for {what}"
                        ));
                    }
                    last = Some(screen);
                }
            }
        }
    }

    /// Leaves home for the current desktop the way a person does, by its sidebar row, then
    /// takes the pointer out of the window and lets the row's hover fade out: the app opens on
    /// home, as the Tauri app does.
    pub fn open_desktop(&mut self) {
        let profiles = self
            .automate("state", serde_json::json!({"of": "profiles"}))
            .unwrap_or_else(|why| panic!("{why}"));
        let selected = profiles["selected"].as_str().unwrap_or_default();
        let desktop = profiles["all"]
            .as_array()
            .and_then(|all| all.iter().find(|p| p["id"] == selected))
            .and_then(|p| p["current_desktop_id"].as_str())
            .unwrap_or_else(|| panic!("the selected profile has no current desktop: {profiles}"))
            .to_owned();
        self.gesture(|app| {
            app.click(Target::key(format!("sidebar.desktop.{desktop}")));
            app.runtime.pointer_leave();
        });
        let mut waited = 0.0;
        while self.runtime.animating {
            assert!(
                waited < STUCK.as_secs_f64(),
                "the window still animated {waited} s after opening the desktop"
            );
            self.runtime.frame(FRAME);
            waited += FRAME;
        }
        self.settle();
    }

    pub fn screen(&self) -> Screen {
        Screen(self.runtime.accessibility_json())
    }

    /// No frame requested and no wake deadline: an idle window that draws nothing.
    pub fn runtime_is_at_rest(&self) -> bool {
        !self.runtime.animating && self.runtime.wake_deadline().is_none()
    }

    /// Covers the window or uncovers it: a covered window, such as a parked or occluded one,
    /// draws no terminal frames until it is uncovered.
    pub fn cover(&mut self, covered: bool) {
        self.covered = covered;
        self.settle();
    }

    pub fn runtime(&self) -> &Runtime<State> {
        &self.runtime
    }

    /// Terminal `id`'s `terminal-view` slice as the automation server reads it: what its screen
    /// holds now, whether or not a frame drew it.
    pub fn terminal_view(&mut self, id: &str) -> Option<serde_json::Value> {
        self.runtime.state.terminals.view_state(id)
    }

    /// The links the app asked the system to open since the last call, which a test run
    /// keeps instead of opening.
    pub fn opened_links(&mut self) -> Vec<String> {
        self.runtime.take_url_requests()
    }

    /// The paths the app asked the file manager to show since the last call, which a test run
    /// keeps instead of showing.
    pub fn revealed_paths(&mut self) -> Vec<String> {
        self.runtime.take_reveal_requests()
    }

    /// The file dialog the app shows, and its title.
    pub fn file_dialog(&self) -> Option<(rune::FileDialogKind, String)> {
        self.runtime
            .file_dialog()
            .map(|(kind, options)| (kind, options.title.clone()))
    }

    /// Answers the file dialog the app shows as the user would, choosing `path` or cancelling
    /// with `None`.
    pub fn answer_file_dialog(&mut self, path: Option<&str>) {
        self.runtime
            .resolve_file_dialog(path.map(std::path::PathBuf::from))
            .expect("a file dialog is open");
        self.runtime.frame(0.0);
        self.settle();
    }

    /// How many settles left a terminal screen the renderer had not yet taken, which a
    /// window asked for a frame by a key or a timer would present: a key whose echo has not
    /// come back leaves none.
    pub fn terminal_frames(&self) -> usize {
        self.terminal_frames
    }

    /// The text on a terminal's screen, by its runtime id, row by row.
    pub fn terminal_text(&mut self, runtime_id: &str) -> Vec<String> {
        self.runtime
            .state
            .terminals
            .screen_text(runtime_id)
            .unwrap_or_else(|| panic!("no terminal {runtime_id} is drawn"))
    }

    /// The size of a terminal's own screen, which follows its PTY.
    pub fn terminal_size(&mut self, runtime_id: &str) -> (u16, u16) {
        self.runtime
            .state
            .terminals
            .screen_size(runtime_id)
            .unwrap_or_else(|| panic!("no terminal {runtime_id} is drawn"))
    }

    /// The binary frames the link has handed on, oldest first, emptying the list.
    pub fn take_frames(&mut self) -> Vec<Frame> {
        std::mem::take(&mut *self.frames.lock().unwrap())
    }

    pub fn link(&self) -> &Link {
        self.link
            .as_ref()
            .expect("the link runs until the world drops")
    }

    /// Messages in flight on a connection that has ended are lost with it, so the counts are
    /// compared only while the link reads the daemon's newest connection.
    fn quiet(&self) -> bool {
        let link = self.link().traffic();
        let daemon = self.daemon.traffic();
        match link.parked {
            Parked::Reading => {
                link.connection == daemon.connection as u64
                    && link.sent == daemon.received
                    && link.received == daemon.queued
            }
            Parked::Stopped => true,
            Parked::Until(at) => at > self.clock.now(),
            Parked::Busy => false,
        }
    }
}

impl Drop for World {
    fn drop(&mut self) {
        self.link.take();
        let violations = self.daemon.violations();
        if !violations.is_empty() && !std::thread::panicking() {
            panic!(
                "the app broke the daemon's script:\n{}",
                violations.join("\n")
            );
        }
    }
}
