use std::cell::RefCell;
use std::rc::Rc;
use std::time::Duration;

use attn_app::browser::{self, Alert, AlertKind, BrowserHost, Control, Heard, Placement};
use attn_app::{Action, Binding, Keymap};
use attn_client::TileKey;
use attn_protocol::{
    BrowserControlRequestMessage, CalloutShowRequestMessage, CalloutStep, Desktop, DesktopPane,
    InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Daemon, Target, World, key_event};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const TILE: &str = "tile-browser";
const START: &str = "https://start.example/";

/// What the window asked of the web engine, in order.
#[derive(Clone, Debug, PartialEq)]
enum Asked {
    Open(String),
    Navigate(String),
    Reload,
    Close,
    Place(Option<Placement>),
    Focus(bool),
    Control(Control),
}

#[derive(Clone, Default)]
struct Engine(Rc<RefCell<Vec<Asked>>>);

impl Engine {
    fn take(&self) -> Vec<Asked> {
        std::mem::take(&mut self.0.borrow_mut())
    }

    fn asked(&self) -> Vec<Asked> {
        self.0.borrow().clone()
    }

    /// The last place the window showed the page, or `None` while it is hidden.
    fn shown(&self) -> Option<Placement> {
        self.0.borrow().iter().rev().find_map(|asked| match asked {
            Asked::Place(at) => Some(*at),
            _ => None,
        })?
    }

    fn controls(&self) -> Vec<Control> {
        self.0
            .borrow()
            .iter()
            .filter_map(|asked| match asked {
                Asked::Control(control) => Some(control.clone()),
                _ => None,
            })
            .collect()
    }
}

struct Scripted(Engine);

impl BrowserHost for Scripted {
    fn open(&mut self, page: &TileKey, url: &str) {
        assert_eq!(page.tile, TILE);
        self.0.0.borrow_mut().push(Asked::Open(url.into()));
    }
    fn navigate(&mut self, _: &TileKey, url: &str) {
        self.0.0.borrow_mut().push(Asked::Navigate(url.into()));
    }
    fn reload(&mut self, _: &TileKey) {
        self.0.0.borrow_mut().push(Asked::Reload);
    }
    fn close(&mut self, _: &TileKey) {
        self.0.0.borrow_mut().push(Asked::Close);
    }
    fn place(&mut self, _: &TileKey, at: Option<Placement>) {
        self.0.0.borrow_mut().push(Asked::Place(at));
    }
    fn focus(&mut self, _: &TileKey, focused: bool) {
        self.0.0.borrow_mut().push(Asked::Focus(focused));
    }
    fn control(&mut self, _: &TileKey, request: Control) {
        self.0.0.borrow_mut().push(Asked::Control(request));
    }
}

fn page() -> TileKey {
    TileKey {
        desktop: DESKTOP.into(),
        tile: TILE.into(),
    }
}

fn browser_at(url: &str) -> Value {
    let mut browser = tile(TILE, "browser");
    browser["tile_params"] = json!(url);
    browser
}

fn desktop(tree: Value, revision: i64) -> Desktop {
    desktop_on(tree, revision, "a")
}

fn desktop_on(tree: Value, revision: i64, active: &str) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: active.into(),
        panes: vec![DesktopPane {
            desktop_id: DESKTOP.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "s-a".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision,
    }
}

fn with_browser(url: &str) -> Value {
    row(vec![pane("a"), browser_at(url)])
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

fn daemon(tree: Value) -> Daemon {
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile()]),
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "agent".into(),
            directory: "/work/a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        desktops: Some(vec![desktop(tree, 1)]),
        ..InitialStateMessage::new()
    });
    daemon.on("desktop_update_tile", |r| {
        json!({"event": "desktop_update_tile_result", "request_id": r["request_id"], "success": true})
    });
    daemon
}

/// The app with a scripted web engine, on a desktop whose browser tile is at `url`.
fn start(url: &str) -> (World, Engine) {
    start_on(daemon(with_browser(url)))
}

fn start_on(daemon: Daemon) -> (World, Engine) {
    let engine = Engine::default();
    browser::use_host(Box::new(Scripted(engine.clone())));
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    (world, engine)
}

fn lay_out(world: &mut World, tree: Value, revision: i64) {
    arrange(world, desktop(tree, revision));
}

fn arrange(world: &mut World, desktop: Desktop) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(),
        desktops: vec![desktop],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

/// The page tells the window something, as WebKit's callbacks do between frames.
fn page_says(world: &mut World, heard: Heard) {
    browser::hear(heard);
    world.gesture(|app| app.update(|_, _| {}));
}

fn ask(world: &mut World, id: &str, action: &str, params: Value) {
    world.daemon.emit(BrowserControlRequestMessage {
        request_id: id.into(),
        desktop_id: DESKTOP.into(),
        tile_id: TILE.into(),
        action: action.into(),
        params: Some(params.to_string()),
        ..BrowserControlRequestMessage::new()
    });
    world.settle();
}

/// The window's answer to the request `id`, as the daemon read it.
fn result(world: &World, id: &str) -> Option<Value> {
    world
        .daemon
        .sent_of("browser_control_result")
        .into_iter()
        .find(|r| r["request_id"] == id)
}

fn answered(id: &str, data: &str) -> Value {
    json!({"cmd": "browser_control_result", "request_id": id, "success": true, "data": data})
}

fn failed(id: &str, error: &str) -> Value {
    json!({"cmd": "browser_control_result", "request_id": id, "success": false, "error": error})
}

fn stored(world: &World) -> Vec<String> {
    world
        .daemon
        .sent_of("desktop_update_tile")
        .iter()
        .map(|r| r["tile_params"].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn address(world: &World) -> String {
    let field = world
        .screen()
        .find(Target::key(format!("tile.{TILE}.address")))
        .expect("the browser tile shows its address field");
    field.value.unwrap_or_default()
}

fn page_rect(world: &World) -> [f32; 4] {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| !e.exiting && e.key.as_deref() == Some(&format!("tile.{TILE}.page")))
        .expect("the page area is laid out")
        .rect
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(2));
    assert!(world.runtime_is_at_rest());
}

/// The key `action` has by default on this platform.
fn chord_of(action: Action) -> KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{action:?} has no single chord: {other:?}"),
    }
}

fn press(world: &mut World, action: Action) {
    let ev = chord_of(action);
    world.gesture(|app| app.key(ev));
}

fn key(world: &mut World, key: Key, mods: Mods) {
    world.gesture(|app| app.key(KeyEvent { key, mods }));
}

#[test]
fn a_browser_tile_opens_its_page_at_the_stored_address_over_its_page_area() {
    let (mut world, engine) = start(START);

    assert_eq!(engine.asked().first(), Some(&Asked::Open(START.into())));
    let rect = page_rect(&world);
    let shown = engine.shown().expect("the page shows");
    assert_eq!(shown.frame, rect);
    assert_eq!(shown.visible, rect);
    assert_eq!(address(&world), START);
    rest(&mut world);
}

#[test]
fn the_tile_points_the_open_page_at_a_new_address_in_place_and_closes_it_with_the_tile() {
    let (mut world, engine) = start("https://first.example/");
    engine.take();

    lay_out(&mut world, with_browser("https://second.example/"), 2);
    let asked = engine.take();
    assert!(
        asked.contains(&Asked::Navigate("https://second.example/".into())),
        "{asked:?}"
    );
    assert!(
        !asked
            .iter()
            .any(|a| matches!(a, Asked::Open(_) | Asked::Close)),
        "{asked:?}"
    );
    assert_eq!(address(&world), "https://second.example/");

    lay_out(&mut world, pane("a"), 3);
    assert_eq!(engine.take(), vec![Asked::Close]);
    rest(&mut world);
}

#[test]
fn a_page_that_browses_elsewhere_stores_its_address_and_loads_nothing_when_the_daemon_stores_it() {
    let (mut world, engine) = start("https://first.example/");
    engine.take();

    let dashboard = "https://first.example/dashboard";
    page_says(
        &mut world,
        Heard::Located {
            page: page(),
            url: dashboard.into(),
        },
    );
    assert_eq!(stored(&world), vec![dashboard]);
    assert_eq!(address(&world), dashboard);

    lay_out(&mut world, with_browser(dashboard), 2);
    let asked = engine.take();
    assert!(
        !asked
            .iter()
            .any(|a| matches!(a, Asked::Open(_) | Asked::Navigate(_))),
        "{asked:?}"
    );
    rest(&mut world);
}

#[test]
fn a_page_that_moves_twice_before_the_daemon_stores_either_stays_where_it_went() {
    let (mut world, engine) = start(START);
    engine.take();

    let (pushed, replaced) = ("https://start.example/b", "https://start.example/c");
    for url in [pushed, replaced] {
        page_says(
            &mut world,
            Heard::Located {
                page: page(),
                url: url.into(),
            },
        );
    }
    lay_out(&mut world, with_browser(pushed), 2);
    lay_out(&mut world, with_browser(replaced), 3);

    let asked = engine.take();
    assert!(
        !asked.iter().any(|a| matches!(a, Asked::Navigate(_))),
        "{asked:?}"
    );
    assert_eq!(stored(&world), vec![pushed, replaced]);
    assert_eq!(address(&world), replaced);

    let elsewhere = "https://elsewhere.example/";
    lay_out(&mut world, with_browser(elsewhere), 4);
    assert_eq!(engine.take(), vec![Asked::Navigate(elsewhere.into())]);
    rest(&mut world);
}

#[test]
fn the_tile_stores_its_location_again_after_the_daemon_refused_to_store_it() {
    let daemon = daemon(with_browser(START));
    daemon.on("desktop_update_tile", |r| {
        let refused = r["tile_params"] == "https://second.example/";
        json!({"event": "desktop_update_tile_result", "request_id": r["request_id"],
               "success": !refused, "error": if refused { "persist failed" } else { "" }})
    });
    let (mut world, _engine) = start_on(daemon);

    for url in ["https://first.example/", "https://second.example/"] {
        page_says(
            &mut world,
            Heard::Located {
                page: page(),
                url: url.into(),
            },
        );
    }
    page_says(
        &mut world,
        Heard::Located {
            page: page(),
            url: "https://second.example/".into(),
        },
    );
    assert_eq!(
        stored(&world),
        vec![
            "https://first.example/",
            "https://second.example/",
            "https://second.example/"
        ]
    );
    rest(&mut world);
}

#[test]
fn the_tile_stores_its_location_once_while_the_daemon_has_not_answered_and_again_after_the_link_drops()
 {
    let daemon = daemon(with_browser(START));
    daemon.on("desktop_update_tile", |_| Value::Null);
    let (mut world, _engine) = start_on(daemon);
    let url = "https://first.example/";

    page_says(
        &mut world,
        Heard::Located {
            page: page(),
            url: url.into(),
        },
    );
    page_says(
        &mut world,
        Heard::Located {
            page: page(),
            url: url.into(),
        },
    );
    assert_eq!(stored(&world), vec![url]);

    world.reconnect();
    world.open_desktop();
    page_says(
        &mut world,
        Heard::Located {
            page: page(),
            url: url.into(),
        },
    );
    assert_eq!(stored(&world), vec![url, url]);
    rest(&mut world);
}

#[test]
fn the_page_hides_under_the_command_palette_and_the_settings_dialog_and_shows_again_after() {
    let (mut world, engine) = start(START);
    assert!(engine.shown().is_some());

    press(&mut world, Action::UiCommandPalette);
    assert_eq!(engine.shown(), None, "the palette covers the page");
    key(&mut world, Key::Escape, Mods::default());
    assert!(
        engine.shown().is_some(),
        "the page comes back when the palette closes"
    );

    press(&mut world, Action::UiOpenSettings);
    assert_eq!(engine.shown(), None, "the settings dialog covers the page");
    key(&mut world, Key::Escape, Mods::default());
    assert!(
        engine.shown().is_some(),
        "the page comes back when settings close"
    );
    rest(&mut world);
}

#[test]
fn an_agents_action_runs_on_the_page_and_the_daemon_hears_its_answer() {
    let (mut world, engine) = start(START);
    world.daemon.emit(BrowserControlRequestMessage {
        request_id: "click-1".into(),
        desktop_id: DESKTOP.into(),
        tile_id: TILE.into(),
        action: "click".into(),
        selector: Some("#go".into()),
        params: Some(json!({"button": "left"}).to_string()),
        ..BrowserControlRequestMessage::new()
    });
    world.settle();

    let control = engine.controls().pop().expect("the page runs the action");
    assert_eq!(control.action, "click");
    assert_eq!(
        control.params,
        json!({"button": "left", "selector": "#go"})
            .as_object()
            .unwrap()
            .clone()
    );
    assert_eq!(result(&world, "click-1"), None);

    page_says(
        &mut world,
        Heard::Answered {
            request_id: control.request_id,
            result: Ok("true".into()),
        },
    );
    assert_eq!(result(&world, "click-1"), Some(answered("click-1", "true")));

    ask(&mut world, "text-1", "get_text", json!({}));
    let control = engine.controls().pop().unwrap();
    page_says(
        &mut world,
        Heard::Answered {
            request_id: control.request_id,
            result: Err("no element matches #go".into()),
        },
    );
    assert_eq!(
        result(&world, "text-1"),
        Some(failed("text-1", "no element matches #go"))
    );
    rest(&mut world);
}

#[test]
fn navigate_and_reload_move_the_page_in_the_window_and_refuse_an_address_that_is_not_the_web() {
    let (mut world, engine) = start(START);
    engine.take();

    ask(
        &mut world,
        "nav-1",
        "navigate",
        json!({"url": "https://next.example/"}),
    );
    assert_eq!(
        result(&world, "nav-1"),
        Some(answered("nav-1", r#"{"success":true}"#))
    );
    assert!(
        engine
            .take()
            .contains(&Asked::Navigate("https://next.example/".into()))
    );
    assert_eq!(address(&world), "https://next.example/");

    ask(
        &mut world,
        "nav-2",
        "navigate",
        json!({"url": "https://next.example/"}),
    );
    assert_eq!(
        result(&world, "nav-2"),
        Some(answered("nav-2", r#"{"success":true}"#))
    );
    assert!(
        !engine
            .take()
            .iter()
            .any(|a| matches!(a, Asked::Navigate(_))),
        "the page is there already"
    );

    ask(
        &mut world,
        "nav-3",
        "navigate",
        json!({"url": "file:///etc/passwd"}),
    );
    assert_eq!(
        result(&world, "nav-3"),
        Some(failed(
            "nav-3",
            "only http and https browser URLs are supported"
        ))
    );

    ask(&mut world, "reload-1", "reload", json!({}));
    assert_eq!(
        result(&world, "reload-1"),
        Some(answered("reload-1", r#"{"success":true}"#))
    );
    assert!(engine.take().contains(&Asked::Reload));

    ask(&mut world, "handle-1", "get_window_handles", json!({}));
    assert_eq!(
        result(&world, "handle-1"),
        Some(answered(
            "handle-1",
            r#"["browser-work-desktop_1-tile-browser"]"#
        ))
    );
    rest(&mut world);
}

#[test]
fn wait_for_asks_the_page_until_its_element_is_ready_or_its_time_runs_out() {
    let (mut world, engine) = start(START);

    ask(
        &mut world,
        "wait-1",
        "wait_for",
        json!({"selector": "#done", "timeout": 1000}),
    );
    let first = engine.controls().pop().expect("the page is asked at once");
    assert_eq!(first.action, "wait_for");
    page_says(
        &mut world,
        Heard::Answered {
            request_id: first.request_id.clone(),
            result: Ok(r#"{"ready":false,"value":null}"#.into()),
        },
    );
    assert_eq!(engine.controls().len(), 1, "the next probe waits its turn");

    world.advance(Duration::from_millis(50));
    let second = engine.controls().pop().unwrap();
    assert_eq!(engine.controls().len(), 2);
    page_says(
        &mut world,
        Heard::Answered {
            request_id: second.request_id,
            result: Ok(r#"{"ready":true,"value":{"found":1}}"#.into()),
        },
    );
    assert_eq!(
        result(&world, "wait-1"),
        Some(answered("wait-1", r#"{"found":1}"#))
    );

    ask(
        &mut world,
        "wait-2",
        "wait_for",
        json!({"selector": "#never", "timeout": 200, "state": "visible"}),
    );
    for _ in 0..10 {
        if let Some(probe) = engine
            .controls()
            .pop()
            .filter(|c| c.request_id.starts_with("wait-2"))
        {
            page_says(
                &mut world,
                Heard::Answered {
                    request_id: probe.request_id,
                    result: Ok(r#"{"ready":false}"#.into()),
                },
            );
        }
        world.advance(Duration::from_millis(50));
    }
    assert_eq!(
        result(&world, "wait-2"),
        Some(failed(
            "wait-2",
            "wait_for timed out waiting for visible (timeoutMs=200, hostTimeoutMs=200)"
        ))
    );

    ask(
        &mut world,
        "wait-3",
        "wait_for",
        json!({"selector": "#here", "timeout": 0}),
    );
    let once = engine
        .controls()
        .pop()
        .expect("a wait with no time asks the page once");
    assert_eq!(once.request_id, "wait-3");
    page_says(
        &mut world,
        Heard::Answered {
            request_id: once.request_id,
            result: Ok(r#"{"ready":true,"value":true}"#.into()),
        },
    );
    assert_eq!(result(&world, "wait-3"), Some(answered("wait-3", "true")));
    rest(&mut world);
}

#[test]
fn a_dialog_the_page_opens_during_an_agents_action_is_the_agents_to_read_type_into_and_close() {
    let (mut world, engine) = start(START);
    let closed: Rc<RefCell<Vec<Option<String>>>> = Rc::default();
    let prompt = |closed: &Rc<RefCell<Vec<Option<String>>>>| {
        let closed = closed.clone();
        Alert {
            kind: AlertKind::Prompt,
            message: "Your name?".into(),
            default_text: Some("Ada".into()),
            close: Box::new(move |answer| closed.borrow_mut().push(answer)),
        }
    };

    assert!(
        browser::page_alert(&page(), prompt(&closed)).is_some(),
        "with no action running the person answers"
    );

    ask(&mut world, "click-1", "click", json!({"selector": "#ask"}));
    let click = engine.controls().pop().unwrap();
    assert!(
        browser::page_alert(&page(), prompt(&closed)).is_none(),
        "the agent answers it"
    );
    world.gesture(|app| app.update(|_, _| {}));
    assert_eq!(
        result(&world, "click-1"),
        Some(answered("click-1", "null")),
        "the dialog ends the action"
    );
    page_says(
        &mut world,
        Heard::Answered {
            request_id: click.request_id,
            result: Ok("true".into()),
        },
    );
    assert_eq!(
        world.daemon.sent_of("browser_control_result").len(),
        1,
        "the late answer is dropped"
    );

    ask(&mut world, "text-1", "get_alert_text", json!({}));
    assert_eq!(
        result(&world, "text-1"),
        Some(answered("text-1", r#""Your name?""#))
    );
    ask(
        &mut world,
        "type-1",
        "send_alert_text",
        json!({"text": "Grace"}),
    );
    assert_eq!(result(&world, "type-1"), Some(answered("type-1", "null")));
    ask(&mut world, "accept-1", "accept_alert", json!({}));
    assert_eq!(
        result(&world, "accept-1"),
        Some(answered("accept-1", "null"))
    );
    assert_eq!(*closed.borrow(), vec![Some("Grace".to_owned())]);

    ask(&mut world, "dismiss-1", "dismiss_alert", json!({}));
    assert_eq!(
        result(&world, "dismiss-1"),
        Some(failed("dismiss-1", "no such alert"))
    );
    rest(&mut world);
}

#[test]
fn an_action_on_a_desktop_the_window_has_not_loaded_says_to_open_that_desktop() {
    let (mut world, _engine) = start(START);
    world.daemon.emit(BrowserControlRequestMessage {
        request_id: "far-1".into(),
        desktop_id: "work/desktop_9".into(),
        tile_id: TILE.into(),
        action: "get_text".into(),
        ..BrowserControlRequestMessage::new()
    });
    world.settle();
    assert_eq!(
        result(&world, "far-1"),
        Some(failed(
            "far-1",
            "the browser tile on desktop work/desktop_9 is not loaded: attn loads it only while its desktop is the current or the previous one. Ask the user to open that desktop, then retry."
        ))
    );
    rest(&mut world);
}

#[test]
fn the_address_field_takes_the_page_where_it_says_and_the_reload_buttons_load_it_again() {
    for (typed, url) in [
        ("localhost:3000/app", "http://localhost:3000/app"),
        ("127.0.0.1:8080", "http://127.0.0.1:8080"),
        ("example.com:8443/x", "https://example.com:8443/x"),
        ("http://plain.example", "http://plain.example"),
    ] {
        let (mut world, engine) = start(START);
        engine.take();
        world.gesture(|app| app.click(Target::key(format!("tile.{TILE}.address"))));
        world.gesture(|app| app.press("mod+a"));
        let typed = typed.to_owned();
        world.gesture(|app| app.type_text(&typed));
        key(&mut world, Key::Enter, Mods::default());
        assert!(
            engine.take().contains(&Asked::Navigate(url.into())),
            "{url}"
        );
        assert_eq!(address(&world), url);
    }

    let (mut world, engine) = start(START);
    engine.take();
    world.gesture(|app| app.click(Target::key(format!("tile.{TILE}.reload"))));
    assert!(engine.take().contains(&Asked::Reload));
    let sidebar = format!("sidebar.tile.{DESKTOP}.{TILE}.reload");
    assert!(
        world
            .screen()
            .get(Target::key(sidebar.clone()))
            .name
            .is_some_and(|name| name.starts_with("Reload ")),
    );
    world.gesture(|app| app.click(Target::key(sidebar)));
    assert!(engine.take().contains(&Asked::Reload));
    rest(&mut world);
}

#[test]
fn a_click_in_the_page_gives_it_the_keyboard_and_the_window_takes_back_only_the_keys_attn_binds() {
    let (mut world, engine) = start(START);
    engine.take();

    page_says(&mut world, Heard::Focused { page: page() });
    assert!(
        !engine.take().contains(&Asked::Focus(false)),
        "the page keeps the keyboard it took"
    );

    assert!(browser::claims_key(&chord_of(Action::SessionClose)));
    assert!(browser::claims_key(&chord_of(Action::UiCommandPalette)));
    for edit in ["mod+c", "mod+v", "mod+x", "mod+a", "mod+z", "mod+shift+z"] {
        let ev = key_event(&rune::Chord::parse(edit).unwrap());
        assert!(!browser::claims_key(&ev), "the page keeps {edit}");
    }
    assert!(!browser::claims_key(&KeyEvent {
        key: Key::Text("w".into()),
        mods: Mods::default()
    }));

    world.gesture(|app| app.click(Target::key("tile.a")));
    assert!(
        engine.take().contains(&Asked::Focus(false)),
        "a click elsewhere takes the keyboard back"
    );
    rest(&mut world);
}

#[test]
fn close_from_a_focused_page_closes_the_browser_tile_it_targets() {
    let (mut world, _engine) = start(START);
    page_says(&mut world, Heard::Focused { page: page() });
    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(
        shown.last().map(|s| s["leaf_id"].clone()),
        Some(json!(TILE)),
        "the page's tile becomes the active one"
    );
    arrange(&mut world, desktop_on(with_browser(START), 2, TILE));

    press(&mut world, Action::SessionClose);
    let removed = world.daemon.sent_of("desktop_remove_leaf");
    assert_eq!(removed.len(), 1, "{removed:?}");
    assert_eq!(removed[0]["leaf_id"], TILE);
    rest(&mut world);
}

#[test]
fn where_no_engine_runs_pages_the_tile_says_browser_tiles_are_unavailable() {
    let mut world = World::start_sized(daemon(with_browser(START)), (1600.0, 900.0));
    world.open_desktop();
    let said = world
        .screen()
        .descendants(Target::key(format!("tile.{TILE}.page")))
        .into_iter()
        .filter_map(|n| n.name)
        .collect::<Vec<_>>();
    assert_eq!(
        said,
        vec!["Browser tiles are not available in this app on this platform yet."]
    );
    rest(&mut world);
}

#[test]
fn the_page_hides_while_a_callout_shows_and_comes_back_when_it_ends() {
    let daemon = daemon(with_browser(START));
    daemon.on("callout_result", |_| Value::Null);
    let (mut world, engine) = start_on(daemon);

    world.daemon.emit(CalloutShowRequestMessage {
        request_id: "c-1".into(),
        profile_id: "work".into(),
        from: "Fern".into(),
        steps: vec![CalloutStep {
            anchor: "desktops".into(),
            text: "Your desktops".into(),
            ..CalloutStep::default()
        }],
        ..CalloutShowRequestMessage::new()
    });
    world.settle();
    assert_eq!(
        engine.shown(),
        None,
        "the callout's card and ring show over the page"
    );

    key(&mut world, Key::Escape, Mods::default());
    assert!(engine.shown().is_some());
    rest(&mut world);
}

#[test]
fn a_click_in_the_address_field_makes_close_act_on_its_browser_tile() {
    let (mut world, _engine) = start(START);
    world.gesture(|app| app.click(Target::key(format!("tile.{TILE}.address"))));
    press(&mut world, Action::SessionClose);
    let removed = world.daemon.sent_of("desktop_remove_leaf");
    assert_eq!(removed.len(), 1, "{removed:?}");
    assert_eq!(removed[0]["leaf_id"], TILE);
    rest(&mut world);
}

#[test]
fn an_answer_too_large_for_the_daemon_to_read_fails_and_says_why() {
    let (mut world, engine) = start(START);
    ask(&mut world, "html-1", "get_html", json!({}));
    let control = engine.controls().pop().unwrap();
    let huge = format!("\"{}\"", "x".repeat(24 * 1024 * 1024));
    let size = huge.len();
    page_says(
        &mut world,
        Heard::Answered {
            request_id: control.request_id,
            result: Ok(huge),
        },
    );
    assert_eq!(
        result(&world, "html-1"),
        Some(failed(
            "html-1",
            &format!(
                "serialized browser control result is {size} bytes; the maximum supported result is 25165824 bytes"
            )
        ))
    );
    rest(&mut world);
}

#[test]
fn a_popped_out_browser_tile_says_its_window_shows_no_page_and_agents_hear_why() {
    let mut popped = browser_at(START);
    popped["window"] = json!({"x": 10.0, "y": 20.0, "width": 500.0, "height": 400.0});
    let (mut world, engine) = start_on(daemon(row(vec![pane("a"), popped])));

    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1, "{windows:?}");
    let screen = world.tile_screen(&windows[0].key);
    let said = screen
        .descendants(Target::key(format!("tile.{TILE}.page")))
        .into_iter()
        .filter_map(|n| n.name)
        .collect::<Vec<_>>();
    assert_eq!(
        said,
        vec![
            "This window does not show browser pages yet. Bring the tile back to its desktop to see its page."
        ]
    );
    assert!(
        screen
            .find(Target::key(format!("tile.{TILE}.address")))
            .is_none()
    );
    assert!(
        screen
            .find(Target::key(format!("tile.{TILE}.reload")))
            .is_none()
    );
    assert!(
        !engine.asked().iter().any(|a| matches!(a, Asked::Open(_))),
        "{:?}",
        engine.asked()
    );

    ask(&mut world, "shot", "screenshot", json!({}));
    assert_eq!(
        result(&world, "shot"),
        Some(failed(
            "shot",
            "the browser tile is popped out into its own window, where this app shows no page yet. Ask the user to bring it back to its desktop, then retry."
        ))
    );
    world.advance(Duration::from_secs(2));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[test]
fn a_press_on_a_control_in_an_inactive_browser_tile_takes_the_tile_and_the_control_still_acts() {
    let (mut world, engine) = start(START);
    engine.take();

    world.gesture(|app| app.click(Target::key(format!("tile.{TILE}.reload"))));

    assert!(engine.take().contains(&Asked::Reload));
    let shown = world.daemon.sent_of("desktop_show_leaf");
    assert_eq!(
        shown.last().map(|s| s["leaf_id"].clone()),
        Some(json!(TILE))
    );
    assert_eq!(
        world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"],
        TILE
    );
    rest(&mut world);
}

#[test]
fn a_press_on_a_control_in_another_tile_takes_the_keys_off_the_address_field() {
    let (mut world, _engine) = start(START);
    world.gesture(|app| app.click(Target::key(format!("tile.{TILE}.address"))));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(format!("tile.{TILE}.address").as_str())
    );

    world.gesture(|app| app.click(Target::key("tile.a.id")));
    world.gesture(|app| app.type_text("zz"));

    assert_eq!(world.screen().focused().and_then(|n| n.key), None);
    assert_eq!(address(&world), START);
    assert_eq!(
        world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"],
        "a"
    );
    rest(&mut world);
}
