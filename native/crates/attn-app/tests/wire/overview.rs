use std::time::Duration;

use attn_app::{Action, Binding, Category, Keymap};
use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileActionResultMessage,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{below, pane, row, tile};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

/// Wide enough for two tiles at the narrowest width, and too narrow for three.
const TWO_FIT: (f32, f32) = (1600.0, 900.0);
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
    vec![
        stacked("a"),
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

fn initial(desktops: Vec<Desktop>) -> InitialStateMessage {
    InitialStateMessage {
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
    let heard = screen.active_option().and_then(|n| n.key);
    assert_eq!(heard, shown.first().map(|leaf| miniature(leaf)));
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

#[test]
fn the_shelf_s_current_entry_follows_the_highlight_and_goes_back_when_the_overview_closes() {
    let mut world = start(vec![stacked("a")]);
    let current = |world: &World| -> Vec<String> {
        ["a", "b", "c", "d", "e"]
            .into_iter()
            .filter(|leaf| {
                world
                    .screen()
                    .find(Target::key(format!("shelf.entry.{leaf}")))
                    .and_then(|n| n.value)
                    .is_some_and(|said| said.contains("current"))
            })
            .map(str::to_owned)
            .collect()
    };
    assert_eq!(current(&world), ["a"]);

    press(&mut world, Action::DesktopOverview);
    key(&mut world, Key::Right);
    assert_eq!(current(&world), ["b"]);
    key(&mut world, Key::Right);
    key(&mut world, Key::Down);
    let entry = current(&world);
    assert_eq!(entry.len(), 1, "{entry:?}");
    let path = format!("shelf.entry.{}/", entry[0]);
    let written: Vec<String> = world
        .runtime()
        .elements()
        .into_iter()
        .filter(|e| e.path.contains(&path) && e.visible[2] * e.visible[3] > 1.0)
        .filter_map(|e| e.text)
        .collect();
    assert_eq!(
        written,
        ["Agent d"],
        "the slice shows the highlighted member"
    );

    key(&mut world, Key::Escape);
    assert_eq!(current(&world), ["a"]);
    rest(&mut world);
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
fn a_tile_that_needs_the_user_says_so_on_its_miniature_and_on_a_line_with_what_it_asks() {
    let mut state = initial(three_desktops());
    for session in state.sessions.iter_mut().flatten() {
        match session.id.as_str() {
            "s-b" => session.state = "waiting_input".into(),
            "s-e" => session.state = "pending_approval".into(),
            "s-y" => session.state = "waiting_input".into(),
            _ => {}
        }
    }
    state.asks = Some(vec![super::asks::ask(
        "a-1",
        "s-b",
        "Which branch?",
        5,
        vec![],
    )]);
    let mut world = start_on(Daemon::scripted().state(state));
    press(&mut world, Action::DesktopOverview);

    let screen = world.screen();
    let value = |leaf: &str| {
        let node = screen.get(Target::key(miniature(leaf)));
        node.value.filter(|said| !said.is_empty())
    };
    assert_eq!(value("a").as_deref(), Some("highlighted, on screen"));
    assert_eq!(value("b").as_deref(), Some("on screen, needs you"));
    assert_eq!(value("c"), None);
    assert_eq!(value("e").as_deref(), Some("needs you"));
    let line = |leaf: &str| {
        let line = screen.find(Target::key(format!("overview.line.{leaf}")))?;
        line.name
    };
    assert_eq!(line("b").as_deref(), Some("Agent b: Which branch?"));
    assert_eq!(line("e").as_deref(), Some("Agent e: approve?"));
    assert_eq!(line("a"), None);
    assert_eq!(
        screen
            .get(Target::key(format!("overview.strip.{TWO}")))
            .value
            .as_deref(),
        Some("2 columns, 1 waits")
    );
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
fn in_the_smallest_window_the_row_keeps_its_room_and_the_lists_around_it_scroll() {
    let ids: Vec<String> = (0..10).map(|i| format!("p{i}")).collect();
    let shown = desktop(5, "p0", Some(row(ids.iter().map(|id| pane(id)))));
    let mut desktops: Vec<_> = (1..=9).map(|slot| desktop(slot, "", None)).collect();
    desktops[4] = shown.clone();
    let mut state = initial(desktops);
    state.profiles = Some(vec![profile(&shown.id)]);
    for session in state.sessions.iter_mut().flatten() {
        session.state = "waiting_input".into();
    }
    let mut world = World::start_sized(Daemon::scripted().state(state), SMALLEST);
    world.open_desktop();
    press(&mut world, Action::DesktopOverview);
    rest(&mut world);

    let rect = |world: &World, key: &str| world.runtime().presented(key).unwrap().0;
    let [_, top, _, height] = rect(&world, "overview");
    let [edge, legend, room, _] = rect(&world, "overview.legend");
    for said in ["on screen", "row", "close"] {
        let elements = world.runtime().elements();
        let shown = elements.iter().find(|e| {
            let inside = e.rect[1] >= legend && e.rect[0] >= edge - 1.0;
            inside && e.text.as_deref() == Some(said)
        });
        let [x, _, w, _] = shown.unwrap_or_else(|| panic!("no {said}")).rect;
        let end = x + w;
        assert!(
            end <= edge + room,
            "{said} ends at {end} of {}",
            edge + room
        );
        let needs = attn_ui::system::width_of(said.to_owned(), attn_ui::system::Type::Small);
        assert!(w >= needs - 1.0, "{said} has {w} points of {needs}");
    }
    assert_eq!(row_shown(&world), ids);
    let first = rect(&world, &miniature("p0"));
    let last = rect(&world, &miniature("p9"));
    let row_height = last[1] + last[3] - first[1];
    assert!(
        row_height >= height / 3.0,
        "the row is {row_height} points of {height}"
    );
    assert!(first[1] >= top && last[1] + last[3] <= legend);

    wheel_to_the_end(&mut world, "overview.below");
    let new = rect(&world, "overview.new");
    assert!(last[1] + last[3] <= new[1] && new[1] + new[3] <= legend);
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
