use std::time::Duration;

use attn_app::{Action, Binding, Category, Keymap};
use attn_protocol::{
    Ask, Desktop, DesktopPane, InitialStateMessage, Profile, ProfileActionResultMessage,
    ProfileArrangementChangedMessage, Session, SettingsUpdatedMessage,
};
use attn_testkit::tree::{below, pane, row, tile};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

/// Wide enough for two tiles at the narrowest width, and too narrow for three.
const TWO_FIT: (f32, f32) = (1600.0, 900.0);
/// Wide enough for the four columns of `stacked`.
const FOUR_FIT: (f32, f32) = (3200.0, 900.0);
const ONE: &str = "work/desktop_1";
const TWO: &str = "work/desktop_2";
const THREE: &str = "work/desktop_3";

fn session(id: &str, state: &str) -> Session {
    Session {
        id: format!("s-{id}"),
        label: format!("Agent {id}"),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        state: state.into(),
        ..Session::default()
    }
}

fn pane_ids(tree: &Value) -> Vec<String> {
    match tree["type"].as_str() {
        Some("pane") => vec![tree["pane_id"].as_str().unwrap().to_owned()],
        Some("split") => tree["children"]
            .as_array()
            .unwrap()
            .iter()
            .flat_map(pane_ids)
            .collect(),
        _ => vec![],
    }
}

/// A desktop on `slot`, with an agent in each pane of `tree`.
fn desktop(slot: u32, active: &str, tree: Option<Value>) -> Desktop {
    let id = format!("work/desktop_{slot}");
    let panes = tree.as_ref().map(pane_ids).unwrap_or_default();
    Desktop {
        id: id.clone(),
        profile_id: "work".into(),
        name: format!("Desktop {slot}"),
        shortcut_slot: Some(slot.into()),
        order_key: format!("{slot}"),
        tree_json: tree.map(|t| t.to_string()).unwrap_or_default(),
        active_pane_id: active.into(),
        panes: panes
            .into_iter()
            .map(|pane| DesktopPane {
                desktop_id: id.clone(),
                runtime_id: format!("r-{pane}"),
                session_id: format!("s-{pane}"),
                title: format!("Agent {pane}"),
                kind: "agent".into(),
                pane_id: pane,
                ..DesktopPane::default()
            })
            .collect(),
        revision: 1,
    }
}

/// `a`, `b`, a stack of `c` over `d`, then `e`: two columns fit on screen.
fn stacked(active: &str) -> Desktop {
    let columns = [pane("a"), pane("b"), below(pane("c"), pane("d")), pane("e")];
    desktop(1, active, Some(row(columns)))
}

/// Desktop 1 with a stack off screen, desktop 2 with two agents and an empty desktop 3.
fn three_desktops() -> Vec<Desktop> {
    three_desktops_at("a")
}

fn three_desktops_at(active: &str) -> Vec<Desktop> {
    vec![
        stacked(active),
        desktop(2, "y", Some(row([pane("x"), pane("y")]))),
        desktop(3, "", None),
    ]
}

/// More columns than the shelf has room for.
fn many() -> Desktop {
    let ids: Vec<String> = (0..24).map(|i| format!("p{i:02}")).collect();
    desktop(1, "p00", Some(row(ids.iter().map(|id| pane(id)))))
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

/// With the overview opening while the walk chord is held.
fn initial(desktops: Vec<Desktop>) -> InitialStateMessage {
    InitialStateMessage {
        settings: Some([hold_opens("true")].into()),
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(&desktops[0].id)]),
        sessions: Some(
            desktops
                .iter()
                .flat_map(|d| &d.panes)
                .map(|p| session(&p.pane_id, "idle"))
                .collect(),
        ),
        desktops: Some(desktops),
        ..InitialStateMessage::new()
    }
}

fn hold_opens(value: &str) -> (String, String) {
    ("hold_opens_overview_enabled".into(), value.into())
}

fn question(session: &str) -> Ask {
    let id = format!("a-{session}");
    super::asks::ask(&id, session, "Which branch?", 5, vec![])
}

/// Every session of `state` waits on the user with an open ask.
fn all_ask(state: &mut InitialStateMessage) {
    let sessions = state.sessions.iter_mut().flatten();
    let asks = sessions.map(|session| {
        session.state = "waiting_input".into();
        question(&session.id)
    });
    state.asks = Some(asks.collect());
}

fn start_on(daemon: Daemon) -> World {
    let mut world = World::start_sized(daemon, TWO_FIT);
    world.open_desktop();
    world
}

fn start(desktops: Vec<Desktop>) -> World {
    start_on(Daemon::scripted().state(initial(desktops)))
}

fn arrange(world: &mut World, current: &str, desktops: Vec<Desktop>) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(current),
        desktops,
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

fn chord(action: &Action) -> KeyEvent {
    match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn press(world: &mut World, action: Action) {
    world.gesture(|app| app.key(chord(&action)));
}

fn key(world: &mut World, key: Key) {
    let mods = Mods::default();
    world.gesture(|app| app.key(KeyEvent { key, mods }));
}

fn shift_return(world: &mut World) {
    let mods = Mods {
        shift: true,
        ..Mods::default()
    };
    let key = Key::Enter;
    world.gesture(|app| app.key(KeyEvent { key, mods }));
}

fn digit(world: &mut World, n: &str) {
    key(world, Key::Text(n.into()));
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn open(world: &World) -> bool {
    has(world, "overview")
}

fn miniature(leaf: &str) -> String {
    format!("overview.miniature.{leaf}")
}

/// The leaf the highlight stands on, as the miniature says and as assistive tech hears it.
fn highlight(world: &World) -> Option<String> {
    let shown = highlighted(world);
    let heard = world.screen().active_option().and_then(|n| n.key);
    assert_eq!(heard, shown.as_deref().map(miniature));
    shown
}

/// The leaf the highlight stands on, as the miniature says.
fn highlighted(world: &World) -> Option<String> {
    let screen = world.screen();
    let shown: Vec<String> = screen
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.value
                .as_deref()
                .unwrap_or_default()
                .contains("highlighted")
        })
        .filter_map(|n| Some(n.key?.strip_prefix("overview.miniature.")?.to_owned()))
        .collect();
    assert!(shown.len() <= 1, "{shown:?} are highlighted");
    shown.into_iter().next()
}

/// The leaves the overview draws, in the row's order.
fn row_shown(world: &World) -> Vec<String> {
    let mut drawn: Vec<([f32; 4], String)> = world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter_map(|n| n.key)
        .filter_map(|key| {
            let leaf = key.strip_prefix("overview.miniature.")?.to_owned();
            Some((world.runtime().presented(&key)?.0, leaf))
        })
        .collect();
    drawn.sort_by(|(a, _), (b, _)| (a[1], a[0]).partial_cmp(&(b[1], b[0])).unwrap());
    drawn.into_iter().map(|(_, leaf)| leaf).collect()
}

/// Whether `leaf`'s miniature shows its icon and title in place of a picture.
fn shows_card(world: &World, leaf: &str) -> bool {
    let card = has(world, &format!("{}.card", miniature(leaf)));
    let picture = has(world, &format!("{}.picture", miniature(leaf)));
    assert_ne!(card, picture, "{leaf}'s miniature shows one of the two");
    card
}

/// What the app sent for `cmd`, without the ids of the requests. The first
/// `desktop_set_current` is the current desktop opened from home.
fn sent(world: &World, cmd: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .skip(usize::from(cmd == "desktop_set_current"))
        .map(|mut r: Request| {
            let fields = r.as_object_mut().unwrap();
            fields.remove("request_id");
            fields.remove("cmd");
            r
        })
        .collect()
}

/// Every command that shows, moves, sizes or attaches something, so far.
fn acts(world: &World) -> Vec<Value> {
    let acting = [
        "desktop_set_current",
        "desktop_show_leaf",
        "desktop_move_leaf",
        "desktop_create",
        "attach_session",
        "detach_session",
        "pty_resize",
    ];
    let sent = world.daemon.sent();
    sent.into_iter()
        .filter(|r| acting.contains(&r["cmd"].as_str().unwrap_or_default()))
        .collect()
}

fn keyboard(world: &mut World) -> Value {
    world.automate("state", json!({"of": "window"})).unwrap()["keyboard"]["pane"].clone()
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn each_way_in_opens_the_overview_over_the_row_and_escape_leaves_everything_as_it_was() {
    type WayIn = (&'static str, fn(&mut World));
    let ways: [WayIn; 4] = [
        ("the shortcut", |w| press(w, Action::DesktopOverview)),
        ("the palette", |w| {
            super::palette::run_command(w, "desktop overview")
        }),
        ("the current entry", |w| click(w, "shelf.entry.p00")),
        ("a count", |w| click(w, "shelf.count.after")),
    ];
    for (name, enter) in ways {
        let mut world = start_on(super::palette::daemon(initial(vec![many()]), vec![]));
        let held = keyboard(&mut world);
        let acted = acts(&world);

        enter(&mut world);
        assert!(open(&world), "{name} opens it");
        let layer = world.screen().get(Target::key("overview"));
        assert_eq!(layer.name.as_deref(), Some("Desktop overview"), "{name}");
        assert_eq!(highlight(&world).as_deref(), Some("p00"), "{name}");
        assert_eq!(row_shown(&world).len(), 24, "{name}");
        rest(&mut world);

        key(&mut world, Key::Escape);
        assert!(!open(&world), "{name}: Escape closes it");
        assert_eq!(acts(&world), acted, "{name}");
        assert_eq!(keyboard(&mut world), held, "{name}");
        rest(&mut world);
    }
}

#[test]
fn the_shortcut_the_current_entry_and_a_press_beside_the_row_close_an_open_overview() {
    type WayOut = (&'static str, fn(&mut World));
    let ways: [WayOut; 3] = [
        ("the shortcut", |w| press(w, Action::DesktopOverview)),
        ("the current entry", |w| click(w, "shelf.entry.a")),
        ("a press beside the row", |w| {
            let [x, y, _, _] = w.runtime().presented("overview").unwrap().0;
            w.gesture(|app| {
                app.runtime.pointer_down(x + 4.0, y + 4.0);
                app.runtime.pointer_up(x + 4.0, y + 4.0);
            })
        }),
    ];
    for (name, leave) in ways {
        let mut world = start(three_desktops());
        press(&mut world, Action::DesktopOverview);
        let acted = acts(&world);

        leave(&mut world);
        assert!(!open(&world), "{name} closes it");
        assert_eq!(acts(&world), acted, "{name}");
        rest(&mut world);
    }
}

#[test]
fn the_walk_s_keys_and_the_arrows_step_the_highlight_as_the_walk_steps_the_row() {
    use Action::{TerminalFocusDown, TerminalFocusLeft, TerminalFocusRight, TerminalFocusUp};
    let walk = [
        TerminalFocusRight,
        TerminalFocusRight,
        TerminalFocusDown,
        TerminalFocusRight,
        TerminalFocusRight,
        TerminalFocusLeft,
        TerminalFocusUp,
        TerminalFocusLeft,
        TerminalFocusLeft,
        TerminalFocusLeft,
    ];
    let mut world = start(vec![stacked("a")]);
    let mut active = "a".to_owned();
    let walked: Vec<String> = walk
        .iter()
        .map(|step| {
            let shows = sent(&world, "desktop_show_leaf").len();
            press(&mut world, step.clone());
            if let Some(show) = sent(&world, "desktop_show_leaf").get(shows) {
                active = show["leaf_id"].as_str().unwrap().to_owned();
                arrange(&mut world, ONE, vec![stacked(&active)]);
            }
            active.clone()
        })
        .collect();
    assert_eq!(walked, ["b", "c", "d", "e", "e", "d", "c", "b", "a", "a"]);

    for arrows in [false, true] {
        let mut world = start(vec![stacked("a")]);
        press(&mut world, Action::DesktopOverview);
        let acted = acts(&world);
        let highlighted: Vec<String> = walk
            .iter()
            .map(|step| {
                match (arrows, step) {
                    (false, step) => press(&mut world, step.clone()),
                    (true, TerminalFocusRight) => key(&mut world, Key::Right),
                    (true, TerminalFocusLeft) => key(&mut world, Key::Left),
                    (true, TerminalFocusUp) => key(&mut world, Key::Up),
                    (true, _) => key(&mut world, Key::Down),
                }
                highlight(&world).unwrap()
            })
            .collect();
        assert_eq!(highlighted, walked, "by arrows: {arrows}");
        assert_eq!(acts(&world), acted, "the highlight moves alone");
        rest(&mut world);
    }
}

#[test]
fn up_and_down_past_a_column_preview_the_desktop_above_and_below_and_stop_at_the_first_and_last() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    let acted = acts(&world);
    assert!(!has(&world, &format!("overview.strip.{ONE}")));
    assert!(has(&world, &format!("overview.strip.{TWO}")));

    key(&mut world, Key::Up);
    assert_eq!(highlight(&world).as_deref(), Some("a"), "nothing is above");
    key(&mut world, Key::Down);
    assert_eq!(row_shown(&world), ["x", "y"]);
    assert_eq!(highlight(&world).as_deref(), Some("y"), "its active tile");
    assert!(has(&world, &format!("overview.strip.{ONE}")));
    assert!(!has(&world, &format!("overview.strip.{TWO}")));

    key(&mut world, Key::Down);
    assert!(row_shown(&world).is_empty());
    assert!(has(&world, "overview.empty"));
    key(&mut world, Key::Down);
    assert!(has(&world, "overview.empty"), "nothing is below");
    key(&mut world, Key::Up);
    key(&mut world, Key::Up);
    assert_eq!(highlight(&world).as_deref(), Some("a"));
    assert_eq!(acts(&world), acted, "a preview shows nothing");
    rest(&mut world);
}

#[test]
fn return_and_a_press_land_on_the_tile_and_close() {
    type Landing = (&'static str, &'static str, &'static str, fn(&mut World));
    let landings: [Landing; 5] = [
        ("Return on this row", ONE, "c", |w| {
            key(w, Key::Right);
            key(w, Key::Right);
            key(w, Key::Enter);
        }),
        ("a press on a miniature", ONE, "e", |w| {
            click(w, &miniature("e"))
        }),
        ("a press on a line", ONE, "d", |w| {
            click(w, "overview.line.d")
        }),
        ("Return on another row", TWO, "x", |w| {
            key(w, Key::Down);
            key(w, Key::Left);
            key(w, Key::Enter);
        }),
        ("a strip, then a miniature", TWO, "x", |w| {
            click(w, &format!("overview.strip.{TWO}"));
            assert!(open(w), "a strip previews");
            click(w, &miniature("x"));
        }),
    ];
    for (name, on, leaf, land) in landings {
        let mut state = initial(three_desktops());
        for session in state.sessions.iter_mut().flatten() {
            if session.id == "s-d" {
                session.state = "waiting_input".into();
                state.asks = Some(vec![question("s-d")]);
            }
        }
        let mut world = start_on(Daemon::scripted().state(state));
        press(&mut world, Action::DesktopOverview);

        land(&mut world);
        assert!(!open(&world), "{name} closes it");
        assert_eq!(
            sent(&world, "desktop_show_leaf"),
            [json!({"desktop_id": on, "leaf_id": leaf})],
            "{name}"
        );
        assert_eq!(keyboard(&mut world), leaf, "{name}");
        rest(&mut world);
    }
}

#[test]
fn an_empty_desktop_lands_as_the_desktop_itself() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    click(&mut world, &format!("overview.strip.{THREE}"));
    key(&mut world, Key::Enter);

    assert!(!open(&world));
    let current = sent(&world, "desktop_set_current");
    assert_eq!(current.last().unwrap()["desktop_id"], THREE);
    assert!(sent(&world, "desktop_show_leaf").is_empty());
}

#[test]
fn a_digit_goes_to_its_desktop_and_closes_and_one_without_a_desktop_does_nothing() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    let acted = acts(&world);

    digit(&mut world, "7");
    assert!(open(&world));
    assert_eq!(acts(&world), acted);

    digit(&mut world, "2");
    assert!(!open(&world));
    let current = sent(&world, "desktop_set_current");
    assert_eq!(current.last().unwrap()["desktop_id"], TWO);
    rest(&mut world);
}

#[test]
fn shift_return_sends_the_tile_the_user_came_from_to_the_previewed_desktop() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Right);

    shift_return(&mut world);
    assert!(
        open(&world),
        "there is nowhere to send a tile of this desktop"
    );
    assert!(sent(&world, "desktop_move_leaf").is_empty());

    key(&mut world, Key::Down);
    key(&mut world, Key::Down);
    key(&mut world, Key::Down);
    shift_return(&mut world);
    assert!(!open(&world));
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json!({"source_desktop_id": ONE, "target_desktop_id": THREE, "leaf_id": "a"})]
    );
}

#[test]
fn the_new_desktop_strip_makes_a_desktop_and_goes_to_it() {
    let daemon = Daemon::scripted().state(initial(three_desktops()));
    daemon.on("desktop_create", |_| {
        message(ProfileActionResultMessage {
            success: true,
            desktops: Some(vec![desktop(4, "", None)]),
            ..ProfileActionResultMessage::new()
        })
    });
    let mut world = start_on(daemon);
    press(&mut world, Action::DesktopOverview);
    click(&mut world, "overview.new");

    assert_eq!(sent(&world, "desktop_create").len(), 1);
    let current = sent(&world, "desktop_set_current");
    assert_eq!(current.last().unwrap()["desktop_id"], "work/desktop_4");
    assert!(!open(&world));
}

#[test]
fn a_tile_that_closes_under_the_highlight_gives_it_to_the_next_in_its_column_else_the_nearest() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Right);
    key(&mut world, Key::Right);
    assert_eq!(highlight(&world).as_deref(), Some("c"));

    let without_c = row([pane("a"), pane("b"), pane("d"), pane("e")]);
    arrange(&mut world, ONE, vec![desktop(1, "a", Some(without_c))]);
    assert_eq!(highlight(&world).as_deref(), Some("d"), "below it");

    let without_d = row([pane("a"), pane("b"), pane("e")]);
    arrange(&mut world, ONE, vec![desktop(1, "a", Some(without_d))]);
    assert_eq!(highlight(&world).as_deref(), Some("e"), "the column after");

    let without_e = row([pane("a"), pane("b")]);
    arrange(&mut world, ONE, vec![desktop(1, "a", Some(without_e))]);
    assert_eq!(highlight(&world).as_deref(), Some("b"), "the column before");
    rest(&mut world);
}

#[test]
fn a_desktop_that_closes_while_previewed_goes_back_to_the_one_the_user_came_from() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Down);
    assert_eq!(highlight(&world).as_deref(), Some("y"));

    let mut left = three_desktops();
    left.remove(1);
    arrange(&mut world, ONE, left);
    assert!(open(&world));
    assert_eq!(highlight(&world).as_deref(), Some("a"));
    rest(&mut world);
}

#[test]
fn opening_over_laid_out_shelved_and_never_shown_terminals_attaches_and_sizes_none() {
    let daemon = Daemon::scripted().state(initial(three_desktops()));
    daemon.on("get_screen_snapshot", |r: &Request| {
        json!({
            "event": "get_screen_snapshot_result", "id": r["id"], "success": true,
            "screen_snapshot": "aGVsbG8=", "screen_cols": 80, "screen_rows": 24,
        })
    });
    let mut world = start_on(daemon);
    press(&mut world, Action::TerminalFocusRight);
    press(&mut world, Action::TerminalFocusRight);
    arrange(&mut world, ONE, {
        let mut desktops = three_desktops();
        desktops[0] = stacked("e");
        desktops
    });
    world.settle();
    assert!(!has(&world, "tile.a"), "a is shelved");
    let acted = acts(&world);

    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Down);
    for _ in 0..3 {
        world.advance(Duration::from_secs(1));
    }
    click(&mut world, &format!("overview.strip.{ONE}"));
    key(&mut world, Key::Escape);
    world.advance(Duration::from_secs(1));
    assert_eq!(acts(&world), acted);
    rest(&mut world);
}

#[test]
fn a_terminal_no_tile_shows_is_asked_for_its_screen_once_an_opening_and_one_refused_shows_a_card() {
    let daemon = Daemon::scripted().state(initial(three_desktops()));
    daemon.on("get_screen_snapshot", |r: &Request| {
        let refused = r["id"] == "r-c";
        json!({
            "event": "get_screen_snapshot_result", "id": r["id"], "success": !refused,
            "screen_snapshot": (!refused).then_some("aGVsbG8="),
            "screen_cols": 80, "screen_rows": 24,
        })
    });
    let mut world = start_on(daemon);
    let asked = |world: &World| -> Vec<String> {
        let sent = sent(world, "get_screen_snapshot");
        sent.iter()
            .map(|r| r["id"].as_str().unwrap().to_owned())
            .collect()
    };

    press(&mut world, Action::DesktopOverview);
    assert_eq!(asked(&world), ["r-c", "r-d", "r-e"]);
    assert!(shows_card(&world, "c"), "a refused screen leaves the card");
    for leaf in ["a", "b", "d", "e"] {
        assert!(!shows_card(&world, leaf), "{leaf} shows its screen");
    }

    key(&mut world, Key::Down);
    key(&mut world, Key::Up);
    key(&mut world, Key::Down);
    assert_eq!(asked(&world), ["r-c", "r-d", "r-e", "r-x", "r-y"]);
    assert!(!shows_card(&world, "x"));

    key(&mut world, Key::Escape);
    press(&mut world, Action::DesktopOverview);
    assert_eq!(
        asked(&world)[5..],
        ["r-c", "r-d", "r-e"],
        "opening asks anew"
    );
    rest(&mut world);
}

#[test]
fn a_terminal_whose_screen_was_asked_for_and_not_answered_is_not_asked_again() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    assert_eq!(sent(&world, "get_screen_snapshot").len(), 3);
    key(&mut world, Key::Escape);
    press(&mut world, Action::DesktopOverview);
    assert_eq!(sent(&world, "get_screen_snapshot").len(), 3);
    assert!(shows_card(&world, "e"));
}

/// The columns the shelf has an entry for, by their first leaf, as `said` describes them.
fn entries(world: &World, said: &str) -> Vec<String> {
    let screen = world.screen();
    screen
        .all(Target::role("button"))
        .into_iter()
        .filter(|n| n.value.as_deref().unwrap_or_default().contains(said))
        .filter_map(|n| Some(n.key?.strip_prefix("shelf.entry.")?.to_owned()))
        .collect()
}

/// The words on the shelf's entry of `column`.
fn written_on(world: &World, column: &str) -> Vec<String> {
    let path = format!("shelf.entry.{column}/");
    let elements = world.runtime().elements();
    let seen = elements
        .into_iter()
        .filter(|e| e.path.contains(&path) && e.visible[2] * e.visible[3] > 1.0);
    seen.filter_map(|e| e.text).collect()
}

/// The columns whose entries the shelf's frame stands behind.
fn framed(world: &World) -> Vec<String> {
    let [x, _, w, _] = world.runtime().presented("shelf.frame").unwrap().0;
    let mut framed = entries(world, "");
    framed.retain(|column| {
        let key = format!("shelf.entry.{column}");
        let [left, _, width, _] = world.runtime().presented(&key).unwrap().0;
        x <= left && left + width <= x + w
    });
    framed
}

#[test]
fn the_shelf_s_current_entry_follows_the_highlight_and_goes_back_when_the_overview_closes() {
    let mut world = start(vec![stacked("a")]);
    assert_eq!(entries(&world, "current"), ["a"]);

    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Right);
    assert_eq!(entries(&world, "current"), ["b"]);
    key(&mut world, Key::Right);
    key(&mut world, Key::Down);
    assert_eq!(entries(&world, "current"), ["c"]);
    assert_eq!(
        written_on(&world, "c"),
        ["Agent d"],
        "the slice shows the highlighted member"
    );

    key(&mut world, Key::Escape);
    assert_eq!(entries(&world, "current"), ["a"]);
    rest(&mut world);
}

/// Desktop 1 with `a` active, and desktop 2 with `x` and `y` on screen, then a stack of `p` over
/// `q`, then `r`.
fn two_rows() -> Vec<Desktop> {
    let columns = [pane("x"), pane("y"), below(pane("p"), pane("q")), pane("r")];
    vec![stacked("a"), desktop(2, "y", Some(row(columns)))]
}

/// The overview of `two_rows`, previewing desktop 2.
fn previewing() -> World {
    let mut world = start(two_rows());
    press(&mut world, Action::DesktopOverview);
    click(&mut world, &format!("overview.strip.{TWO}"));
    world
}

#[test]
fn the_shelf_shows_the_row_of_a_previewed_desktop_until_the_overview_closes() {
    type Preview = (&'static str, fn(&mut World));
    let previews: [Preview; 2] = [
        ("a strip", |w| click(w, &format!("overview.strip.{TWO}"))),
        ("an arrow", |w| key(w, Key::Down)),
    ];
    for (name, preview) in previews {
        let mut world = start(two_rows());
        press(&mut world, Action::DesktopOverview);
        let acted = acts(&world);

        preview(&mut world);
        assert_eq!(entries(&world, ""), ["x", "y", "p", "r"], "{name}");
        assert_eq!(entries(&world, "current"), ["y"], "{name}");
        assert_eq!(framed(&world), ["x", "y"], "{name}");

        key(&mut world, Key::Right);
        key(&mut world, Key::Down);
        assert_eq!(entries(&world, "current"), ["p"], "{name}");
        assert_eq!(written_on(&world, "p"), ["Agent q"], "{name}");
        assert_eq!(framed(&world), ["x", "y"], "{name}: the row does not move");
        assert_eq!(acts(&world), acted, "{name}: a preview shows nothing");
        rest(&mut world);

        key(&mut world, Key::Escape);
        assert!(!open(&world), "{name}");
        assert_eq!(entries(&world, ""), ["a", "b", "c", "e"], "{name}");
        assert_eq!(entries(&world, "current"), ["a"], "{name}");
        assert_eq!(framed(&world), ["a", "b"], "{name}");
        assert_eq!(acts(&world), acted, "{name}");
        rest(&mut world);
    }
}

/// Steps the highlight right to an entry off screen with an open ask, and the entries that changed
/// colour with it.
fn step_to_the_ask_of(world: &mut World, leaf: &str) -> Vec<String> {
    let step = chord(&Action::TerminalFocusRight);
    let rec = world.record(60.0, 0.5, |_, app| app.key(step));
    let key = format!("/shelf.entry.{leaf}.ask.0");
    let ask = rec.tracks.iter().find(|t| t.path.ends_with(&key));
    let ink = |at| ask.unwrap().paint_at(at).unwrap().colors;
    assert_ne!(ink(0), ink(rec.frames - 1), "the mark of {leaf}");
    super::shelf_entries::recolored_at_once(&rec)
}

#[test]
fn the_shelf_s_colours_change_at_once_as_the_highlight_moves_kept_held_and_on_a_previewed_desktop()
{
    let asking = |desktops| {
        let mut state = initial(desktops);
        state.asks = Some(vec![question("s-c"), question("s-e"), question("s-p")]);
        start_on(Daemon::scripted().state(state))
    };
    let mut kept = asking(two_rows());
    press(&mut kept, Action::DesktopOverview);
    let right = chord(&Action::TerminalFocusRight);
    let stepped = kept.record(60.0, 0.5, |_, app| app.key(right));
    let recolored = super::shelf_entries::recolored_at_once(&stepped);
    assert_eq!(recolored, ["a", "b"]);
    assert_eq!(step_to_the_ask_of(&mut kept, "c"), ["b", "c"]);

    let mut held = asking(three_desktops_at("b"));
    hold(&mut held);
    walk(&mut held, Action::TerminalFocusRight, "c");
    advance_ms(&mut held, 1000);
    assert!(open(&held));
    assert_eq!(step_to_the_ask_of(&mut held, "e"), ["c", "e"]);

    let mut previewed = asking(two_rows());
    press(&mut previewed, Action::DesktopOverview);
    click(&mut previewed, &format!("overview.strip.{TWO}"));
    assert_eq!(step_to_the_ask_of(&mut previewed, "p"), ["p", "y"]);
}

#[test]
fn an_entry_of_a_previewed_desktop_lands_there_and_the_current_one_closes_the_overview() {
    let entries = [("one on screen", "x"), ("one off screen", "r")];
    for (name, leaf) in entries {
        let mut world = previewing();
        click(&mut world, &format!("shelf.entry.{leaf}"));
        assert!(!open(&world), "{name}");
        assert_eq!(
            sent(&world, "desktop_show_leaf"),
            [json!({"desktop_id": TWO, "leaf_id": leaf})],
            "{name}"
        );
    }

    let highlights: [(&str, &[Key]); 2] = [("y", &[]), ("r", &[Key::Right, Key::Right])];
    for (current, steps) in highlights {
        let mut world = previewing();
        for step in steps {
            key(&mut world, step.clone());
        }
        assert_eq!(highlight(&world).as_deref(), Some(current));
        let acted = acts(&world);
        click(&mut world, &format!("shelf.entry.{current}"));
        assert!(!open(&world), "{current}");
        assert_eq!(acts(&world), acted, "{current}");
        rest(&mut world);
    }
}

#[test]
fn an_entry_dragged_from_a_previewed_desktop_moves_that_desktop_s_column() {
    let middle = |world: &World, key: &str, across: f32| {
        let [x, y, w, h] = world.runtime().presented(key).unwrap().0;
        (x + w * across, y + h / 2.0)
    };
    let drops = [
        (
            "shelf.entry.p",
            json!({"source_desktop_id": TWO, "target_desktop_id": TWO, "leaf_id": "r",
                "anchor_id": "p", "edge": "left", "whole_column": true, "activate": false}),
        ),
        (
            "sidebar.desktop.work/desktop_1",
            json!({"source_desktop_id": TWO, "target_desktop_id": ONE, "leaf_id": "r",
                "edge": "left", "leaf_share": 0.32, "whole_column": true}),
        ),
    ];
    for (on, moved) in drops {
        let mut world = previewing();
        let to = middle(&world, on, 0.25);
        world.gesture(|app| {
            app.drag(Target::key("shelf.entry.r"), to);
            app.release(to);
        });
        assert_eq!(sent(&world, "desktop_move_leaf"), [moved], "{on}");
    }
}

#[test]
fn the_shelf_of_a_previewed_desktop_peeks_at_no_tile_and_has_no_width_control() {
    let mut world = start(two_rows());
    let rest_on = |world: &mut World, key: &str| {
        let [x, y, w, h] = world.runtime().presented(key).unwrap().0;
        world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
        world.advance(Duration::from_millis(200));
    };
    let over_the_frame = |world: &mut World| {
        let [x, y, _, h] = world.runtime().presented("shelf.frame").unwrap().0;
        world.gesture(|app| app.move_to((x + 1.0, y + h / 2.0)));
    };
    press(&mut world, Action::DesktopOverview);
    over_the_frame(&mut world);
    assert!(has(&world, "shelf.width"));
    rest_on(&mut world, "shelf.entry.e");
    assert!(has(&world, "shelf.peek"));

    key(&mut world, Key::Down);
    assert_eq!(entries(&world, "current"), ["y"]);
    assert!(!has(&world, "shelf.peek"), "the peek goes with its row");
    rest_on(&mut world, "shelf.entry.r");
    assert!(!has(&world, "shelf.peek"));
    over_the_frame(&mut world);
    assert!(!has(&world, "shelf.width"));

    key(&mut world, Key::Escape);
    assert!(!open(&world));
    rest_on(&mut world, "shelf.entry.e");
    assert!(has(&world, "shelf.peek"));
    over_the_frame(&mut world);
    assert!(has(&world, "shelf.width"));
}

#[test]
fn an_entry_of_the_shelf_shows_its_tile_and_closes_the_overview() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    click(&mut world, "shelf.entry.e");

    assert!(!open(&world));
    let shown = sent(&world, "desktop_show_leaf");
    assert_eq!(shown.last().unwrap()["leaf_id"], "e");
}

#[test]
fn a_peek_s_open_shows_its_tile_and_closes_the_overview() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    let [x, y, w, h] = world.runtime().presented("shelf.entry.e").unwrap().0;
    world.gesture(|app| app.move_to((x + w / 2.0, y + h / 2.0)));
    world.advance(Duration::from_millis(200));
    click(&mut world, "shelf.peek.open");

    assert!(!open(&world));
    let shown = sent(&world, "desktop_show_leaf");
    assert_eq!(shown.last().unwrap()["leaf_id"], "e");
}

#[test]
fn a_tile_that_needs_the_user_says_so_on_its_miniature_and_only_its_question_on_a_line() {
    let mut state = initial(three_desktops());
    for session in state.sessions.iter_mut().flatten() {
        match session.id.as_str() {
            "s-b" => session.state = "waiting_input".into(),
            "s-e" => session.state = "pending_approval".into(),
            "s-y" => session.state = "waiting_input".into(),
            _ => {}
        }
    }
    let next = super::asks::ask("a-next", "s-b", "Which remote?", 2, vec![]);
    state.asks = Some(vec![question("s-b"), next]);
    let mut world = start_on(Daemon::scripted().state(state));
    press(&mut world, Action::DesktopOverview);

    let value = |world: &World, leaf: &str| {
        let node = world.screen().get(Target::key(miniature(leaf)));
        node.value.filter(|said| !said.is_empty())
    };
    let line = |world: &World, leaf: &str| {
        let key = format!("overview.line.{leaf}");
        world.screen().find(Target::key(key))?.name
    };
    let lines = |world: &World| {
        let asks = "Agent b: Which branch? and 1 more";
        assert_eq!(line(world, "b").as_deref(), Some(asks));
        for leaf in ["a", "c", "d", "e"] {
            assert_eq!(line(world, leaf), None);
        }
        assert_eq!(value(world, "e").as_deref(), Some("needs you"));
    };
    lines(&world);
    assert_eq!(value(&world, "c"), None);
    let highlighted = value(&world, "a");
    assert_eq!(highlighted.as_deref(), Some("highlighted, on screen"));
    let on_screen = value(&world, "b");
    assert_eq!(on_screen.as_deref(), Some("on screen, needs you"));
    key(&mut world, Key::Escape);

    walk_to_b(&mut world);
    press(&mut world, Action::TerminalFocusRight);
    assert!(open(&world));
    lines(&world);
}

#[test]
fn a_strip_shows_its_number_and_name_and_says_its_counts_to_a_screen_reader() {
    let mut state = initial(three_desktops());
    for session in state.sessions.iter_mut().flatten() {
        if session.id == "s-y" {
            session.state = "waiting_input".into();
        }
    }
    let mut world = start_on(Daemon::scripted().state(state));
    press(&mut world, Action::DesktopOverview);
    let strip = format!("overview.strip.{TWO}");
    let all = world.runtime().elements();
    let words: Vec<&str> = all
        .iter()
        .filter(|e| e.path.contains(&format!("/{strip}/")))
        .filter_map(|e| e.text.as_deref())
        .filter(|said| !said.is_empty())
        .collect();
    assert_eq!(words, ["2", "Desktop 2"]);
    let said = world.screen().get(Target::key(strip)).value;
    assert_eq!(said.as_deref(), Some("2 columns, 1 waits"));
}

#[test]
fn the_overview_holds_every_key_of_a_tile_but_the_walk_s() {
    let keymap = Keymap::from_setting(None, attn_testkit::platform());
    let walk = [
        Action::TerminalFocusLeft,
        Action::TerminalFocusRight,
        Action::TerminalFocusUp,
        Action::TerminalFocusDown,
    ];
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    let sent = world.daemon.sent().len();
    let mut pressed = 0;
    for action in Action::ALL.iter().filter(|a| {
        a.category() == Category::Panes
            && !walk.contains(a)
            && matches!(keymap.binding(a), Some(Binding::Chord(_)))
    }) {
        press(&mut world, action.clone());
        assert!(open(&world), "{} closed it", action.id());
        assert_eq!(
            world.daemon.sent().len(),
            sent,
            "{} acted behind it: {:?}",
            action.id(),
            world.daemon.sent().last()
        );
        pressed += 1;
    }
    assert!(pressed > 10, "only {pressed} keys pressed");
    rest(&mut world);
}

#[test]
fn a_document_of_the_current_desktop_shows_its_words_and_one_of_another_desktop_its_card() {
    let mut doc = tile("doc", "markdown");
    doc["tile_params"] = r#"{"path":"/work/plan.md"}"#.into();
    let mut other = doc.clone();
    other["tile_id"] = "other".into();
    let mut world = start(vec![
        desktop(1, "a", Some(row([pane("a"), doc, tile("web", "browser")]))),
        desktop(2, "x", Some(row([pane("x"), other]))),
    ]);
    for (on, id) in [(ONE, "doc"), (TWO, "other")] {
        world.daemon.emit(attn_protocol::DesktopTileContentMessage {
            desktop_id: on.into(),
            tile_id: id.into(),
            tile_kind: "markdown".into(),
            path: "/work/plan.md".into(),
            content: "# The plan\n\nBuild it.\n".into(),
            ..attn_protocol::DesktopTileContentMessage::new()
        });
    }
    world.settle();
    let words_in = |world: &World, leaf: &str| {
        let path = format!("{}.picture", miniature(leaf));
        world
            .runtime()
            .elements()
            .iter()
            .any(|e| e.path.contains(&path) && e.text.as_deref() == Some("Build it."))
    };

    press(&mut world, Action::DesktopOverview);
    assert!(words_in(&world, "doc"));
    assert!(shows_card(&world, "web"));
    rest(&mut world);
    key(&mut world, Key::Down);
    assert!(shows_card(&world, "other"));
    rest(&mut world);
}

#[test]
fn an_agent_shown_from_the_sidebar_closes_the_overview_over_it() {
    let mut world = start(three_desktops());
    press(&mut world, Action::DesktopOverview);
    click(&mut world, "sidebar.session.s-y");
    assert!(!open(&world));
    let shown = sent(&world, "desktop_show_session");
    assert_eq!(shown.last().unwrap()["session_id"], "s-y");
}

#[test]
fn the_row_stands_clear_of_an_open_drawer() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    click(&mut world, "titlebar.garden");
    world.settle();

    assert!(open(&world));
    let [drawer, ..] = world.runtime().presented("drawer").unwrap().0;
    for leaf in ["a", "b", "c", "d", "e"] {
        let [x, _, w, _] = world.runtime().presented(&miniature(leaf)).unwrap().0;
        assert!(x + w <= drawer, "{leaf} ends at {} under {drawer}", x + w);
    }
}

/// The smallest size the window takes.
const SMALLEST: (f32, f32) = (800.0, 600.0);
const LAPTOP: (f32, f32) = (1470.0, 956.0);

/// A wheel turn over `key` past the end of what it scrolls.
fn wheel_to_the_end(world: &mut World, key: &str) {
    let [x, y, w, h] = world.runtime().presented(key).unwrap().0;
    let at = (x + w / 2.0, y + h / 2.0);
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(0.0, -10_000.0).at(at.0, at.1))
    });
    rest(world);
}

#[test]
fn a_terminal_s_screen_stands_clear_of_every_edge_and_outline_over_it() {
    const HIGHLIGHT: f32 = 3.0;
    let daemon = Daemon::scripted().state(initial(vec![stacked("a")]));
    daemon.on("get_screen_snapshot", |r: &Request| {
        json!({
            "event": "get_screen_snapshot_result", "id": r["id"], "success": true,
            "screen_snapshot": "aGVsbG8=", "screen_cols": 80, "screen_rows": 24,
        })
    });
    let mut world = start_on(daemon);
    press(&mut world, Action::DesktopOverview);
    for leaf in ["a", "b", "c", "d", "e"] {
        let rect = |key: String| world.runtime().presented(&key).unwrap().0;
        let [x, y, w, h] = rect(miniature(leaf));
        let [sx, sy, sw, sh] = rect(format!("{}.screen", miniature(leaf)));
        assert!(sw > 0.0 && sh > 0.0, "{leaf}");
        assert!(
            sx >= x + HIGHLIGHT && sx + sw <= x + w - HIGHLIGHT,
            "{leaf}"
        );
        assert!(
            sy >= y + HIGHLIGHT && sy + sh <= y + h - HIGHLIGHT,
            "{leaf}"
        );
    }
}

/// Whether each corner of the outline of `leaf` is round, top left first and clockwise.
fn rounded_corners(world: &World, leaf: &str) -> [bool; 4] {
    let rt = world.runtime();
    let key = format!("{}.outline", miniature(leaf));
    let pixels = rt.presented(&key).unwrap().0.map(|v| v * rt.env.scale);
    let near = |rect: &[f32; 4]| rect.iter().zip(pixels).all(|(a, b)| (a - b).abs() <= 1.0);
    rt.instances
        .iter()
        .find(|i| i.border[3] > 0.0 && i.params[0] >= 2.0 * rt.env.scale && near(&i.rect))
        .unwrap_or_else(|| panic!("{leaf} has an outline"))
        .radii
        .map(|radius| radius > 0.0)
}

#[test]
fn an_outline_in_a_stack_is_round_only_at_the_corners_of_its_column() {
    let stack = |ids: &[&str]| {
        let (last, over) = ids.split_last().unwrap();
        over.iter()
            .rev()
            .fold(pane(last), |under, id| below(pane(id), under))
    };
    let stacks: [&[&str]; 4] = [
        &["a"],
        &["b1", "b2"],
        &["c1", "c2", "c3"],
        &["d1", "d2", "d3", "d4"],
    ];
    let halves = row([pane("left"), pane("right")]);
    let over_halves = below(pane("over"), halves);
    let tree = row(stacks.map(stack).into_iter().chain([over_halves]));
    for waiting in [true, false] {
        let mut state = initial(vec![desktop(1, "a", Some(tree.clone()))]);
        for session in state.sessions.iter_mut().flatten().filter(|_| waiting) {
            session.state = "waiting_input".into();
        }
        let mut world = start_on(Daemon::scripted().state(state));
        press(&mut world, Action::DesktopOverview);
        rest(&mut world);
        for tiles in stacks {
            for (i, leaf) in tiles.iter().enumerate() {
                if !waiting {
                    assert_eq!(highlighted(&world).as_deref(), Some(*leaf));
                }
                let (top, bottom) = (i == 0, i + 1 == tiles.len());
                let round = [top, top, bottom, bottom];
                assert_eq!(rounded_corners(&world, leaf), round, "{leaf} of {tiles:?}");
                if !waiting && !bottom {
                    key(&mut world, Key::Down);
                    rest(&mut world);
                }
            }
            if !waiting {
                key(&mut world, Key::Right);
                rest(&mut world);
            }
        }
        if waiting {
            let corners = |leaf| rounded_corners(&world, leaf);
            assert_eq!(corners("over"), [true, true, false, false]);
            assert_eq!(corners("left"), [false, false, false, true]);
            assert_eq!(corners("right"), [false, false, true, false]);
        }
    }
}

#[test]
fn the_row_and_the_lists_take_the_whole_window_inside_its_padding() {
    for size in [SMALLEST, LAPTOP] {
        let mut state = initial(three_desktops());
        all_ask(&mut state);
        let mut world = World::start_sized(Daemon::scripted().state(state), size);
        world.open_desktop();
        let fills = |world: &World, way: &str| {
            let rect = |key: &str| world.runtime().presented(key).unwrap().0;
            let [_, top, _, height] = rect("overview");
            let end = top + height - attn_ui::blocks::Overview::PADDING;
            let [_, y, _, h] = rect("overview.below");
            assert!(
                (y + h - end).abs() < 0.5,
                "{way} at {size:?} ends at {}",
                y + h
            );
            for leaf in ["a", "b", "c", "d", "e"] {
                let [_, at, _, tall] = rect(&miniature(leaf));
                assert!(tall > 0.0 && at + tall <= y, "{way} at {size:?}: {leaf}");
            }
        };
        press(&mut world, Action::DesktopOverview);
        rest(&mut world);
        fills(&world, "kept");
        key(&mut world, Key::Escape);

        walk_to_b(&mut world);
        press(&mut world, Action::TerminalFocusRight);
        fills(&world, "held");
        let_go(&mut world);
        assert!(!open(&world));
    }
}

#[test]
fn in_the_smallest_window_the_row_keeps_its_room_and_the_lists_around_it_scroll() {
    let ids: Vec<String> = (0..10).map(|i| format!("p{i}")).collect();
    let shown = desktop(5, "p0", Some(row(ids.iter().map(|id| pane(id)))));
    let mut desktops: Vec<_> = (1..=9).map(|slot| desktop(slot, "", None)).collect();
    desktops[4] = shown.clone();
    let mut state = initial(desktops);
    state.profiles = Some(vec![profile(&shown.id)]);
    all_ask(&mut state);
    let mut world = World::start_sized(Daemon::scripted().state(state), SMALLEST);
    world.open_desktop();
    press(&mut world, Action::DesktopOverview);
    rest(&mut world);

    let rect = |world: &World, key: &str| world.runtime().presented(key).unwrap().0;
    let [_, top, _, height] = rect(&world, "overview");
    let end = top + height - attn_ui::blocks::Overview::PADDING;
    assert_eq!(row_shown(&world), ids);
    let first = rect(&world, &miniature("p0"));
    let last = rect(&world, &miniature("p9"));
    let row_height = last[1] + last[3] - first[1];
    assert!(
        row_height >= height / 3.0,
        "the row is {row_height} points of {height}"
    );
    assert!(first[1] >= top && last[1] + last[3] <= end);

    let [left, _, width, _] = rect(&world, "overview");
    let [_, over, _, over_height] = rect(&world, "overview.above");
    let [_, under, _, _] = rect(&world, "overview.below");
    let room_top = over + over_height;
    assert!(
        (under - room_top) * 2.0 >= end - over,
        "the row has {room_top} to {under} of {over} to {end}"
    );
    for id in &ids {
        let [x, y, w, h] = rect(&world, &miniature(id));
        assert!(w > 0.0 && h > 0.0, "{id} is {w} by {h}");
        assert!(x >= left && x + w <= left + width, "{id} at {x}");
        assert!(y >= room_top && y + h <= under, "{id} at {y}");
    }
    assert_eq!(highlight(&world).as_deref(), Some("p0"));
    let outline = rect(&world, &format!("{}.outline", miniature("p0")));
    assert_eq!(outline, first);
    let nearest = rect(&world, &format!("overview.strip.{}", "work/desktop_4"));
    assert!(nearest[1] >= over && nearest[1] + nearest[3] <= room_top);

    wheel_to_the_end(&mut world, "overview.below");
    let last_strip = rect(&world, &format!("overview.strip.{}", "work/desktop_9"));
    assert!(last_strip[1] >= under && last_strip[1] + last_strip[3] <= end);
    let new = rect(&world, "overview.new");
    assert!(last[1] + last[3] <= new[1] && new[1] + new[3] <= end);
    wheel_to_the_end(&mut world, "overview.above");
    let above = rect(&world, &format!("overview.strip.{}", "work/desktop_4"));
    assert!(above[1] >= top && above[1] + above[3] <= first[1]);
    assert!(open(&world));

    click(&mut world, &miniature("p9"));
    assert!(!open(&world));
    assert_eq!(
        sent(&world, "desktop_show_leaf").last().unwrap()["leaf_id"],
        json!("p9")
    );
}

#[test]
fn an_entry_dropped_on_the_overview_lands_on_no_tile_behind_it() {
    let mut world = start(vec![stacked("a")]);
    press(&mut world, Action::DesktopOverview);
    let [x, y, w, h] = world.runtime().presented("tile.b").unwrap().0;
    let to = (x + w / 2.0, y + h * 0.9);
    world.gesture(|app| {
        app.drag(Target::key("shelf.entry.e"), to);
        app.release(to);
    });

    assert!(sent(&world, "desktop_move_leaf").is_empty());
    assert!(open(&world));
}

fn down(world: &mut World, mods: Mods) {
    world.gesture(|app| app.hold(mods));
}

fn let_go(world: &mut World) {
    down(world, Mods::default());
}

/// Holds the walk chord's modifiers.
fn hold(world: &mut World) {
    down(world, chord(&Action::TerminalFocusRight).mods);
}

/// Steps to `leaf`, as the daemon then shows.
fn walk(world: &mut World, step: Action, leaf: &str) {
    press(world, step);
    arrange(world, ONE, three_desktops_at(leaf));
}

/// Holds the walk chord's modifiers and steps right once, from `a` to `b`, which is on screen.
fn walk_to_b(world: &mut World) {
    hold(world);
    walk(world, Action::TerminalFocusRight, "b");
}

/// On `b`, the last tile on screen, holds the walk chord's modifiers and steps right once, to `c`.
fn walk_off_screen_to_c() -> World {
    let mut world = start(three_desktops_at("b"));
    hold(&mut world);
    walk(&mut world, Action::TerminalFocusRight, "c");
    world
}

fn advance_ms(world: &mut World, ms: u64) {
    world.advance(Duration::from_millis(ms));
}

fn stays_shut(world: &mut World) {
    assert!(!open(world));
    rest(world);
    assert!(!open(world));
}

/// Every way to let go of the chord: one of its modifiers at a time, and all of them.
fn ways_to_let_go() -> Vec<Mods> {
    let all = chord(&Action::TerminalFocusRight).mods;
    let primary = Mods {
        cmd: false,
        ctrl: false,
        logo: false,
        ..all
    };
    let alt = Mods { alt: false, ..all };
    let shift = Mods {
        shift: false,
        ..all
    };
    [primary, alt, shift, Mods::default()]
        .into_iter()
        .filter(|left| *left != all)
        .collect()
}

#[test]
fn steps_between_tiles_on_screen_never_open_the_overview_however_long_the_chord_is_held() {
    use Action::{TerminalFocusLeft, TerminalFocusRight};
    let mut world = start(three_desktops());
    walk_to_b(&mut world);
    stays_shut(&mut world);
    walk(&mut world, TerminalFocusLeft, "a");
    let ev = chord(&TerminalFocusRight);
    world.gesture(|app| app.runtime.repeat_key(ev));
    stays_shut(&mut world);

    let daemon = Daemon::scripted().state(initial(three_desktops()));
    let mut wide = World::start_sized(daemon, FOUR_FIT);
    wide.open_desktop();
    hold(&mut wide);
    for leaf in ["b", "c", "e"] {
        walk(&mut wide, TerminalFocusRight, leaf);
        stays_shut(&mut wide);
        assert_eq!(keyboard(&mut wide), leaf);
    }
}

#[test]
fn a_step_in_a_stack_and_one_back_on_screen_never_open_the_overview() {
    let mut stack = start(three_desktops_at("c"));
    hold(&mut stack);
    walk(&mut stack, Action::TerminalFocusDown, "d");
    stays_shut(&mut stack);

    let mut back = walk_off_screen_to_c();
    walk(&mut back, Action::TerminalFocusLeft, "b");
    stays_shut(&mut back);
}

#[test]
fn the_walk_chord_opens_the_overview_only_while_its_setting_is_on_and_it_is_off_by_default() {
    use Action::{TerminalFocusLeft, TerminalFocusRight};
    let walk_past_the_screen = |world: &mut World| {
        hold(world);
        for (step, leaf) in [(TerminalFocusRight, "c"), (TerminalFocusRight, "e")] {
            walk(world, step, leaf);
            stays_shut(world);
            assert_eq!(keyboard(world), leaf);
        }
        let_go(world);
        stays_shut(world);
    };
    let set = |world: &mut World, value: &str| {
        world.daemon.emit(SettingsUpdatedMessage {
            settings: Some([hold_opens(value)].into()),
            ..SettingsUpdatedMessage::new()
        });
        world.settle();
    };
    let mut state = initial(three_desktops_at("b"));
    state.settings = None;
    let mut world = start_on(Daemon::scripted().state(state));
    walk_past_the_screen(&mut world);
    press(&mut world, Action::DesktopOverview);
    assert!(open(&world), "its own key opens it");
    key(&mut world, Key::Escape);

    set(&mut world, "true");
    hold(&mut world);
    walk(&mut world, TerminalFocusLeft, "c");
    assert!(!open(&world));
    press(&mut world, TerminalFocusLeft);
    assert!(open(&world));
    let_go(&mut world);

    set(&mut world, "false");
    arrange(&mut world, ONE, three_desktops_at("b"));
    walk_past_the_screen(&mut world);
}

#[test]
fn a_single_quick_step_never_opens_the_overview_whichever_modifier_goes_first() {
    for left in ways_to_let_go() {
        let mut world = walk_off_screen_to_c();
        advance_ms(&mut world, 199);
        assert!(!open(&world), "a step is the walk's step");
        let acted = acts(&world);

        down(&mut world, left);
        advance_ms(&mut world, 1);
        assert!(!open(&world), "{left:?} still down");
        rest(&mut world);
        assert!(!open(&world));
        assert_eq!(acts(&world), acted);
        assert_eq!(
            sent(&world, "desktop_show_leaf"),
            [json!({"desktop_id": ONE, "leaf_id": "c"})]
        );
        assert_eq!(keyboard(&mut world), "c");
    }
}

#[test]
fn a_hold_past_the_screen_opens_the_overview_over_a_row_that_stays_and_letting_go_lands() {
    type WayIn = (&'static str, fn() -> World, &'static str, fn(&mut World));
    let to_b = || {
        let mut world = start(three_desktops());
        walk_to_b(&mut world);
        world
    };
    let ways: [WayIn; 4] = [
        ("200 ms", walk_off_screen_to_c, "c", |w| {
            advance_ms(w, 200);
        }),
        ("a second step off screen", walk_off_screen_to_c, "e", |w| {
            press(w, Action::TerminalFocusRight);
        }),
        ("a step off screen after one on it", to_b, "c", |w| {
            press(w, Action::TerminalFocusRight);
        }),
        ("a repeat of the held chord", to_b, "c", |w| {
            let ev = chord(&Action::TerminalFocusRight);
            w.gesture(|app| app.runtime.repeat_key(ev));
        }),
    ];
    for (i, (name, walked, at, enter)) in ways.into_iter().enumerate() {
        let lets_go = match i {
            0 => ways_to_let_go(),
            _ => vec![Mods::default()],
        };
        for left in lets_go {
            let mut world = walked();
            let acted = acts(&world);
            let on = keyboard(&mut world);

            enter(&mut world);
            assert!(open(&world), "{name} opens it");
            assert_eq!(highlighted(&world).as_deref(), Some(at), "{name}");
            assert_eq!(row_shown(&world), ["a", "b", "c", "e", "d"], "{name}");
            assert!(!has(&world, &format!("overview.strip.{TWO}")), "{name}");
            assert!(!has(&world, "overview.new"), "{name}");
            assert_eq!(acts(&world), acted, "{name} moves nothing");
            assert_eq!(keyboard(&mut world), on, "{name}");
            rest(&mut world);
            assert!(open(&world), "{name}: it stays while the chord is down");

            down(&mut world, left);
            assert!(!open(&world), "{name}: {left:?} still down");
            let shown = sent(&world, "desktop_show_leaf");
            assert_eq!(
                shown.last(),
                Some(&json!({"desktop_id": ONE, "leaf_id": at})),
                "{name}"
            );
            assert_eq!(keyboard(&mut world), at, "{name}");
            rest(&mut world);
        }
    }
}

#[test]
fn a_held_overview_steps_along_the_row_and_within_a_stack_and_stays_on_its_desktop() {
    use Action::{TerminalFocusDown, TerminalFocusLeft, TerminalFocusRight, TerminalFocusUp};
    let mut world = start(three_desktops());
    walk_to_b(&mut world);
    let acted = acts(&world);
    let steps = [
        (TerminalFocusRight, "c"),
        (TerminalFocusUp, "c"),
        (TerminalFocusDown, "d"),
        (TerminalFocusDown, "d"),
        (TerminalFocusRight, "e"),
        (TerminalFocusRight, "e"),
        (TerminalFocusLeft, "d"),
        (TerminalFocusLeft, "b"),
        (TerminalFocusLeft, "a"),
        (TerminalFocusLeft, "a"),
    ];
    for (i, (step, at)) in steps.into_iter().enumerate() {
        press(&mut world, step);
        assert_eq!(highlighted(&world).as_deref(), Some(at), "step {i}");
    }
    assert_eq!(acts(&world), acted);

    let_go(&mut world);
    assert_eq!(keyboard(&mut world), "a", "walking back undoes the walk");
    rest(&mut world);
}

#[test]
fn the_window_losing_focus_lands_a_held_overview_and_ends_a_hold_before_it_opens() {
    let mut world = start(three_desktops());
    walk_to_b(&mut world);
    press(&mut world, Action::TerminalFocusRight);
    world.gesture(|app| app.runtime.window_focus(false));
    assert!(!open(&world));
    assert_eq!(
        sent(&world, "desktop_show_leaf").last(),
        Some(&json!({"desktop_id": ONE, "leaf_id": "c"}))
    );

    let mut world = walk_off_screen_to_c();
    let acted = acts(&world);
    world.gesture(|app| app.runtime.window_focus(false));
    rest(&mut world);
    assert!(!open(&world));
    assert_eq!(acts(&world), acted);
}

#[test]
fn cancelling_a_held_overview_goes_back_to_the_tile_the_user_left_the_screen_from() {
    type WayOut = (&'static str, fn(&mut World));
    let mut ways: Vec<WayOut> = vec![("a press beside the row", |w| {
        let [x, y, _, _] = w.runtime().presented("overview").unwrap().0;
        w.gesture(|app| app.click_at((x + 4.0, y + 4.0)));
    })];
    if attn_testkit::platform() != rune::Platform::Mac {
        ways.push(("Escape", |w| {
            let mods = chord(&Action::TerminalFocusRight).mods;
            let key = Key::Escape;
            w.gesture(|app| app.key(KeyEvent { key, mods }));
        }));
    }
    let off_screen_at_once = || {
        let mut world = walk_off_screen_to_c();
        advance_ms(&mut world, 200);
        world
    };
    let after_a_step_on_screen = || {
        let mut world = start(three_desktops());
        walk_to_b(&mut world);
        press(&mut world, Action::TerminalFocusRight);
        world
    };
    let walks = [off_screen_at_once, after_a_step_on_screen as fn() -> World];
    for ((name, leave), walk) in ways
        .into_iter()
        .flat_map(|way| walks.map(|walk| (way, walk)))
    {
        let mut world = walk();
        assert!(open(&world));

        leave(&mut world);
        assert!(!open(&world), "{name} closes it");
        let shown = sent(&world, "desktop_show_leaf");
        assert_eq!(
            shown.last(),
            Some(&json!({"desktop_id": ONE, "leaf_id": "b"})),
            "{name}"
        );
        assert_eq!(keyboard(&mut world), "b", "{name}");

        let_go(&mut world);
        rest(&mut world);
        assert!(!open(&world), "{name}");
        assert_eq!(
            sent(&world, "desktop_show_leaf"),
            shown,
            "{name}: letting go of the chord then lands nothing"
        );
    }
}

#[test]
fn a_press_on_a_miniature_of_a_held_overview_lands_there_and_letting_go_lands_nothing_more() {
    let mut world = walk_off_screen_to_c();
    advance_ms(&mut world, 200);

    click(&mut world, &miniature("e"));
    assert!(!open(&world));
    let shown = sent(&world, "desktop_show_leaf");
    assert_eq!(
        shown.last(),
        Some(&json!({"desktop_id": ONE, "leaf_id": "e"}))
    );
    let_go(&mut world);
    rest(&mut world);
    assert_eq!(sent(&world, "desktop_show_leaf"), shown);
}

#[test]
fn a_step_that_leaves_the_row_and_the_repeats_after_it_never_open_the_overview() {
    let mut desktops = three_desktops();
    desktops[0] = stacked("e");
    let mut world = start(desktops.clone());
    down(&mut world, chord(&Action::TerminalFocusRight).mods);
    press(&mut world, Action::TerminalFocusRight);
    let current = sent(&world, "desktop_set_current");
    assert_eq!(
        current.last().unwrap()["desktop_id"],
        TWO,
        "it left the row"
    );
    advance_ms(&mut world, 200);
    assert!(!open(&world), "before the daemon switched desktops");
    desktops[1] = desktop(2, "y", Some(row([pane("x"), pane("y"), pane("z")])));
    arrange(&mut world, TWO, desktops);

    let ev = chord(&Action::TerminalFocusRight);
    world.gesture(|app| app.runtime.repeat_key(ev));
    assert_eq!(
        sent(&world, "desktop_show_leaf"),
        [json!({"desktop_id": TWO, "leaf_id": "z"})],
        "the repeat walks on as it does today"
    );
    rest(&mut world);
    assert!(!open(&world));
}

#[test]
fn a_step_with_nowhere_to_go_never_opens_the_overview() {
    let mut world = start(vec![stacked("e")]);
    down(&mut world, chord(&Action::TerminalFocusRight).mods);
    press(&mut world, Action::TerminalFocusRight);
    press(&mut world, Action::TerminalFocusRight);
    rest(&mut world);
    assert!(!open(&world));
    assert!(sent(&world, "desktop_show_leaf").is_empty());
}

#[test]
fn a_key_without_modifiers_a_sequence_and_the_palette_walk_without_a_hold() {
    let bare = r#"{"version":1,"overrides":{"terminal.focusRight":{"key":"F8"}}}"#;
    let sequence = r#"{"version":1,"overrides":{"terminal.focusRight":{"leader":{"key":"y","meta":true,"shift":true},"then":{"key":"j"}}}}"#;
    type Walk = (&'static str, Option<&'static str>, fn(&mut World));
    let walks: [Walk; 3] = [
        ("a bare key", Some(bare), |w| key(w, Key::Function(8))),
        ("a sequence", Some(sequence), |w| {
            let leader = key_event(&rune::Chord::parse("mod+shift+y").unwrap());
            down(w, leader.mods);
            w.gesture(|app| app.key(leader));
            w.gesture(|app| app.type_text("j"));
        }),
        ("the palette", None, |w| {
            down(w, chord(&Action::TerminalFocusRight).mods);
            w.gesture(|app| {
                app.update(|state, tx| {
                    attn_app::dispatch(state, Action::TerminalFocusRight, tx);
                })
            });
        }),
    ];
    for (name, keys, walk) in walks {
        let mut state = initial(three_desktops_at("b"));
        let bound = keys.map(|keys| ("keybindings_config".to_owned(), keys.to_owned()));
        state.settings.as_mut().unwrap().extend(bound);
        let mut world = start_on(Daemon::scripted().state(state));

        walk(&mut world);
        assert_eq!(
            sent(&world, "desktop_show_leaf"),
            [json!({"desktop_id": ONE, "leaf_id": "c"})],
            "{name} walks"
        );
        rest(&mut world);
        assert!(!open(&world), "{name} opens nothing");
    }
}

#[test]
fn a_layer_that_opens_before_the_hold_s_time_keeps_the_overview_shut() {
    let mut world = walk_off_screen_to_c();
    press(&mut world, Action::UiCommandPalette);
    rest(&mut world);
    assert!(!open(&world));
}

#[test]
fn a_tile_shown_another_way_before_the_hold_s_time_keeps_the_overview_shut() {
    let mut world = walk_off_screen_to_c();
    click(&mut world, "sidebar.session.s-a");
    arrange(&mut world, ONE, three_desktops());
    rest(&mut world);
    assert!(!open(&world));
    assert_eq!(keyboard(&mut world), "a");
}
