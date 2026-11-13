use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    CommandUsage, Desktop, DesktopPane, GetCommandUsageResultMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, RecordCommandUsageResultMessage, Session,
    SettingsUpdatedMessage,
};
use attn_testkit::tree::{pane, row, tile};
use attn_testkit::{Daemon, Request, Target, World, key_event, message};
use rune::KeyPlatform;
use rune::motion_gate::{self, AllowRow, Job, Recording};
use serde_json::Value;

use super::queue::ago;

/// The sidebar beside the tile area, in points: a 1600-point window has 1360
/// for tiles.
const BESIDE_TILES: f32 = 240.0;

/// A window whose tile area is `area` points wide. A terminal column is 9 points, so the
/// narrowest width steps are 57 cols at 513px, 66 at 594, 76 at 684, 86 at 774, 100 at 900,
/// 120 at 1080 and 150 at 1350.
fn window(area: f32) -> (f32, f32) {
    (area + BESIDE_TILES, 900.0)
}

/// Two tiles fit at 57 and 66 cols, one from 76.
const TWO_FIT: f32 = 1348.0;
/// How far a tile shown alone sits in from each edge of the tile area, as the Tauri app's
/// focus mode pads it.
const ALONE_INSET: f32 = 10.0;
/// Three tiles fit at 57 and 66 cols, two at 76 to 100.
const THREE_FIT: f32 = 1848.0;
/// Four tiles fit at 57 cols, three at 66 and 76, two at 86 and 100, one from 120.
const FOUR_FIT: f32 = 2100.0;

fn agent(desktop: &str, id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        session_id: format!("s-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn session(id: &str) -> Session {
    Session {
        id: format!("s-{id}"),
        label: format!("Agent {id}"),
        directory: format!("/work/{id}"),
        profile_id: "work".into(),
        ..Session::default()
    }
}

/// A desktop on `slot` whose tree is `tree`, with an agent pane for each pane leaf.
fn desktop(slot: u32, active: &str, tree: Value) -> Desktop {
    let id = format!("work/desktop_{slot}");
    Desktop {
        id: id.clone(),
        profile_id: "work".into(),
        name: format!("Desktop {slot}"),
        shortcut_slot: Some(slot as i64),
        order_key: format!("{slot}"),
        panes: pane_ids(&tree).iter().map(|p| agent(&id, p)).collect(),
        tree_json: tree.to_string(),
        active_pane_id: active.into(),
        revision: 1,
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

/// Desktop 1 with `leaves` side by side, each a terminal pane unless it is a tree already.
fn tiles(active: &str, leaves: impl IntoIterator<Item = Value>) -> Desktop {
    desktop(1, active, row(leaves))
}

fn panes<'a>(ids: impl IntoIterator<Item = &'a str>) -> Vec<Value> {
    ids.into_iter().map(pane).collect()
}

/// A pane carrying a dragged width of `cols`, saved at a tile area of `area_cols` if given.
fn wanted(id: &str, cols: u32, area_cols: Option<u32>) -> Value {
    let mut leaf = pane(id);
    leaf["wanted_cols"] = cols.into();
    if let Some(area_cols) = area_cols {
        leaf["wanted_area_cols"] = area_cols.into();
    }
    leaf
}

/// A markdown document agent `owner` opened, which it asked to show `asked` more times.
fn document(id: &str, owner: &str, asked: u64) -> Value {
    let mut doc = tile(id, "markdown");
    doc["tile_session_id"] = format!("s-{owner}").into();
    doc["tile_params"] = format!("{{\"path\":\"/work/{id}.md\"}}").into();
    doc["show_requests"] = asked.into();
    doc
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

/// The app in a window whose tile area is `area` wide, with `narrowest` cols as the narrowest
/// tile width and the agents in `waiting` owed a turn. The daemon stores each setting the app
/// sets and tells it back, and answers the palette's history with none.
fn start_with(
    area: f32,
    narrowest: Option<&str>,
    desktops: Vec<Desktop>,
    waiting: &[&str],
) -> World {
    let mut world = World::start_sized(daemon(narrowest, desktops, waiting), window(area));
    world.open_desktop();
    world
}

fn daemon(narrowest: Option<&str>, desktops: Vec<Desktop>, waiting: &[&str]) -> Daemon {
    let sessions = desktops
        .iter()
        .flat_map(|d| d.panes.iter())
        .map(|p| {
            let id = p.pane_id.as_str();
            if waiting.contains(&id) {
                Session {
                    state: "waiting_input".into(),
                    turn_owed: Some(true),
                    turn_opened_at: Some(ago(5)),
                    ..session(id)
                }
            } else {
                session(id)
            }
        })
        .collect();
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile(&desktops[0].id)]),
        desktops: Some(desktops),
        sessions: Some(sessions),
        settings: narrowest
            .map(|cols| BTreeMap::from([("narrowest_tile_cols".to_owned(), cols.to_owned())])),
        ..InitialStateMessage::new()
    });
    daemon
        .on("set_setting", |r| {
            let setting = |k: &str| r[k].as_str().unwrap().to_owned();
            message(SettingsUpdatedMessage {
                settings: Some(BTreeMap::from([(setting("key"), setting("value"))])),
                ..SettingsUpdatedMessage::new()
            })
        })
        .on("get_command_usage", |r| {
            message(GetCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                profile_id: r["profile_id"].as_str().unwrap().into(),
                success: true,
                entries: Vec::<CommandUsage>::new(),
                ..GetCommandUsageResultMessage::new()
            })
        })
        .on("record_command_usage", |r| {
            message(RecordCommandUsageResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                ..RecordCommandUsageResultMessage::new()
            })
        });
    daemon
}

fn start(area: f32, narrowest: Option<&str>, desktops: Vec<Desktop>) -> World {
    start_with(area, narrowest, desktops, &[])
}

/// The daemon tells the app the profile's desktops and its current desktop.
fn arrange(world: &mut World, current: &str, desktops: Vec<Desktop>) {
    world.daemon.emit(arrangement(current, desktops));
    world.settle();
}

fn arrangement(current: &str, desktops: Vec<Desktop>) -> ProfileArrangementChangedMessage {
    ProfileArrangementChangedMessage {
        profile: profile(current),
        desktops,
        ..ProfileArrangementChangedMessage::new()
    }
}

/// The daemon changes desktop 1, the current desktop and the only one.
fn becomes(world: &mut World, desktop: Desktop) {
    arrange(world, "work/desktop_1", vec![desktop]);
}

fn press(world: &mut World, action: Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
}

fn click(world: &mut World, key: &str) {
    let key = key.to_owned();
    world.gesture(|app| app.click(Target::key(key)));
}

/// Each command `cmd` the app sent, without its request id or name. The first
/// `desktop_set_current` is `start` opening the current desktop from home.
fn sent(world: &World, cmd: &str) -> Vec<Value> {
    let opening = usize::from(cmd == "desktop_set_current");
    world
        .daemon
        .sent_of(cmd)
        .into_iter()
        .skip(opening)
        .map(|mut r: Request| {
            r.as_object_mut().unwrap().remove("request_id");
            r.as_object_mut().unwrap().remove("cmd");
            r
        })
        .collect()
}

fn json(text: &str) -> Value {
    serde_json::from_str(text).unwrap()
}

fn shown_leaf(world: &World) -> Vec<String> {
    sent(world, "desktop_show_leaf")
        .iter()
        .map(|r| r["leaf_id"].as_str().unwrap().to_owned())
        .collect()
}

fn settings_sent(world: &World) -> Vec<String> {
    sent(world, "set_setting")
        .iter()
        .map(|r| {
            assert_eq!(r["key"], "narrowest_tile_cols");
            r["value"].as_str().unwrap().to_owned()
        })
        .collect()
}

fn widths_cleared(world: &World) -> Vec<(String, u64)> {
    sent(world, "desktop_set_wanted_cols")
        .iter()
        .map(|r| {
            (
                r["leaf_id"].as_str().unwrap().to_owned(),
                r["cols"].as_u64().unwrap(),
            )
        })
        .collect()
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

fn shows(world: &World, leaf: &str) -> bool {
    let title = if leaf.starts_with("doc") {
        format!("{leaf}.md")
    } else {
        format!("Agent {leaf}")
    };
    world
        .screen()
        .find(Target::role("group").name(title))
        .is_some()
}

/// The leaves the screen shows, left to right.
fn screen(world: &World) -> Vec<String> {
    let mut shown: Vec<(f32, &str)> = ["a", "b", "c", "d", "e", "n", "doc", "late"]
        .into_iter()
        .filter(|leaf| shows(world, leaf))
        .map(|leaf| {
            let x = world
                .runtime()
                .presented(&format!("tile.{leaf}"))
                .unwrap_or_else(|| panic!("{leaf} is drawn"))
                .0[0];
            (x, leaf)
        })
        .collect();
    shown.sort_by(|a, b| a.0.total_cmp(&b.0));
    shown.into_iter().map(|(_, leaf)| leaf.to_owned()).collect()
}

fn chip(world: &World, leaf: &str) -> Option<String> {
    world
        .screen()
        .find(Target::key(format!("shelf.chip.{leaf}")))
        .map(|n| n.value.unwrap_or_default())
}

fn width_label(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("shelf.width.label"))
        .and_then(|n| n.name)
}

fn keyboard(world: &mut World) -> Value {
    world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap()["keyboard"]["pane"]
        .clone()
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn fewer_and_more_tiles_step_to_each_width_that_changes_how_many_tiles_fit_and_the_label_marks_the_count_reached()
 {
    let mut world = start(
        FOUR_FIT,
        Some("66"),
        vec![tiles("a", panes(["a", "b", "c", "d", "e"]))],
    );
    assert_eq!(screen(&world), ["a", "b", "c"]);

    press(&mut world, Action::ShelfFewerTiles);
    assert_eq!(settings_sent(&world), ["86"]);
    assert_eq!(screen(&world), ["a", "b"]);
    assert_eq!(width_label(&world).as_deref(), Some("2 tiles"));
    let label = world.screen().to_string();
    assert!(label.contains("at least 86 cols each"), "{label}");
    assert!(
        label.contains("1 [2] 3 4"),
        "the steps reach 1 to 4 tiles and 2 are on screen: {label}"
    );

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(settings_sent(&world), ["86", "76"]);
    assert_eq!(screen(&world), ["a", "b", "c"]);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(settings_sent(&world), ["86", "76", "57"]);
    assert_eq!(screen(&world), ["a", "b", "c", "d"]);
    rest(&mut world);
}

#[test]
fn more_tiles_names_the_width_in_force_when_the_setting_is_not_a_step() {
    let mut world = start(
        FOUR_FIT,
        Some("40"),
        vec![tiles("a", panes(["a", "b", "c", "d", "e"]))],
    );

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(
        notice(&world).as_deref(),
        Some(
            "5 tiles at the narrowest width (40 cols, 480px) need 2400px. Tiles have 2100px here."
        )
    );
    assert!(settings_sent(&world).is_empty());
    rest(&mut world);
}

#[test]
fn fewer_tiles_from_a_width_between_steps_lands_on_a_step_and_the_label_marks_the_tiles_on_screen()
{
    let mut world = start(
        FOUR_FIT,
        Some("70"),
        vec![tiles("a", panes(["a", "b", "c", "d", "e"]))],
    );
    assert_eq!(screen(&world).len(), 3);

    press(&mut world, Action::ShelfFewerTiles);
    assert_eq!(settings_sent(&world), ["86"]);
    assert_eq!(screen(&world), ["a", "b"]);
    assert_eq!(width_label(&world).as_deref(), Some("2 tiles"));
    let label = world.screen().to_string();
    assert!(label.contains("at least 86 cols each"), "{label}");
    assert!(label.contains("[2]"), "{label}");
    rest(&mut world);
}

#[test]
fn a_dragged_width_wider_than_the_tile_area_gives_way_and_returns_when_the_window_grows() {
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles("a", [wanted("a", 220, None), pane("b"), pane("c")])],
    );
    assert_eq!(
        screen(&world),
        ["a", "b", "c"],
        "1980px does not fit in 1848px"
    );

    world.resize(window(FOUR_FIT));
    assert_eq!(screen(&world), ["a"]);
    world.resize(window(THREE_FIT));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    world.resize(window(FOUR_FIT));
    assert_eq!(screen(&world), ["a"]);
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());
    rest(&mut world);
}

#[test]
fn a_width_saved_with_its_area_hides_neighbours_only_at_that_area_or_larger() {
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles(
            "a",
            [wanted("a", 200, Some(233)), pane("b"), pane("c")],
        )],
    );
    assert_eq!(
        screen(&world),
        ["a", "b", "c"],
        "205 cols of area is less than the 233 it was saved at"
    );

    world.resize(window(FOUR_FIT));
    assert_eq!(screen(&world), ["a"]);
    world.resize(window(THREE_FIT));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());
    rest(&mut world);
}

#[test]
fn a_width_saved_without_an_area_hides_neighbours_wherever_it_fits() {
    let mut world = start(
        FOUR_FIT,
        None,
        vec![tiles("a", [wanted("a", 180, None), pane("b"), pane("c")])],
    );
    assert_eq!(screen(&world), ["a"]);

    world.resize(window(THREE_FIT));
    assert_eq!(screen(&world), ["a"], "1620px still fits in 1848px");
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());
    rest(&mut world);
}

#[test]
fn more_tiles_clears_the_dragged_width_of_the_tile_it_brings_back_too() {
    let mut world = start(
        FOUR_FIT,
        None,
        vec![tiles(
            "a",
            [wanted("a", 233, None), wanted("b", 233, None), pane("c")],
        )],
    );
    assert_eq!(screen(&world), ["a"]);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(
        widths_cleared(&world),
        [("a".to_owned(), 0), ("b".to_owned(), 0)]
    );
    assert!(settings_sent(&world).is_empty());
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert!(
        world
            .screen()
            .to_string()
            .contains("Cleared 2 dragged widths")
    );
    rest(&mut world);
}

#[test]
fn more_tiles_steps_narrower_too_when_clearing_a_dragged_width_is_not_enough() {
    let mut world = start(
        2200.0,
        Some("150"),
        vec![tiles("a", [wanted("a", 244, None), pane("b"), pane("c")])],
    );
    assert_eq!(screen(&world), ["a"]);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(widths_cleared(&world), [("a".to_owned(), 0)]);
    assert_eq!(settings_sent(&world), ["120"]);
    assert_eq!(screen(&world), ["a", "b"]);
    rest(&mut world);
}

#[test]
fn more_tiles_keeps_a_dragged_width_when_a_narrower_step_alone_brings_the_next_tile_back() {
    let mut world = start(
        2200.0,
        Some("86"),
        vec![tiles("a", [wanted("a", 90, None), pane("b"), pane("c")])],
    );
    assert_eq!(screen(&world), ["a", "b"]);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(settings_sent(&world), ["76"]);
    assert!(widths_cleared(&world).is_empty());
    assert_eq!(screen(&world), ["a", "b", "c"]);
    rest(&mut world);
}

fn run_command(world: &mut World, query: &str, command: &str) {
    press(world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text(query));
    click(world, &format!("palette.row.command:{command}"));
}

#[test]
fn the_palette_steps_the_width_and_moves_tiles_as_the_keys_do_and_the_shortcut_help_lists_the_moves()
 {
    let mut world = start(
        FOUR_FIT,
        Some("66"),
        vec![tiles("a", panes(["a", "b", "c", "d", "e"]))],
    );

    run_command(&mut world, "wider tiles", "shelf-fewer-tiles");
    assert_eq!(settings_sent(&world), ["86"]);

    run_command(&mut world, "carry tile right", "shelf-carry-right");
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"a",
                "anchor_id":"b","edge":"right","whole_column":true}"#
        )]
    );

    run_command(&mut world, "send tile to the shelf", "shelf-send-to-shelf");
    let send = &sent(&world, "desktop_move_leaf")[1];
    assert_eq!(send["leaf_id"], "a");
    assert_eq!(send["activate"], false);

    press(&mut world, Action::UiShowShortcuts);
    for action in [
        Action::ShelfCarryLeft,
        Action::ShelfCarryRight,
        Action::ShelfSendToShelf,
    ] {
        let listed = world
            .screen()
            .find(Target::key(format!("shortcut.{}", action.id())))
            .and_then(|n| n.name);
        assert!(
            listed
                .as_deref()
                .is_some_and(|l| l.starts_with(action.label())),
            "{listed:?}"
        );
    }
    rest(&mut world);
}

#[test]
fn a_chip_whose_dragged_width_cannot_fit_beside_the_active_tile_shows_alone_where_it_is() {
    let row = || [pane("a"), pane("b"), pane("c"), wanted("d", 149, None)];
    let mut world = start(TWO_FIT, None, vec![tiles("a", row())]);
    assert_eq!(screen(&world), ["a", "b"]);

    click(&mut world, "shelf.chip.d");
    assert_eq!(shown_leaf(&world), ["d"]);
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    becomes(&mut world, tiles("d", row()));
    assert_eq!(screen(&world), ["d"]);
    assert!(sent(&world, "desktop_set_wanted_cols").is_empty());
    rest(&mut world);
}

#[test]
fn a_chip_with_one_tile_on_screen_shows_its_tile_where_it_is() {
    let mut world = start(
        TWO_FIT,
        Some("150"),
        vec![tiles("a", panes(["a", "b", "c"]))],
    );
    assert_eq!(screen(&world), ["a"]);

    click(&mut world, "shelf.chip.c");
    assert_eq!(shown_leaf(&world), ["c"]);
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    becomes(&mut world, tiles("c", panes(["a", "b", "c"])));
    assert_eq!(screen(&world), ["c"]);
    rest(&mut world);
}

fn typed(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("pty_input")
        .iter()
        .map(|r| {
            (
                r["id"].as_str().unwrap().to_owned(),
                r["data"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

#[test]
fn keys_typed_right_after_a_chip_click_reach_the_tile_it_brings_once_it_lands() {
    let mut world = start(TWO_FIT, None, vec![tiles("a", panes(["a", "b", "c", "d"]))]);

    click(&mut world, "shelf.chip.d");
    world.gesture(|app| app.type_text("x"));
    assert!(typed(&world).is_empty(), "{:?}", typed(&world));

    becomes(&mut world, tiles("d", panes(["a", "d", "b", "c"])));
    assert_eq!(
        super::desktops::ringing(&world),
        ["d"],
        "the brought tile rings"
    );
    assert_eq!(typed(&world), [("r-d".to_owned(), "x".to_owned())]);
    world.gesture(|app| app.type_text("y"));
    assert_eq!(
        typed(&world),
        [
            ("r-d".to_owned(), "x".to_owned()),
            ("r-d".to_owned(), "y".to_owned())
        ]
    );
    rest(&mut world);
}

#[test]
fn sending_the_last_tile_of_the_row_to_the_shelf_moves_it_to_the_row_start() {
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles("a", panes(["a", "b", "c", "d"]))],
    );
    becomes(&mut world, tiles("d", panes(["a", "b", "c", "d"])));
    assert_eq!(screen(&world), ["b", "c", "d"]);

    press(&mut world, Action::ShelfSendToShelf);
    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_1","leaf_id":"d",
                "anchor_id":"a","edge":"left","whole_column":true,"activate":false}"#
        )]
    );
    assert_eq!(shown_leaf(&world), ["c"]);
    becomes(&mut world, tiles("c", panes(["d", "a", "b", "c"])));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    rest(&mut world);
}

#[test]
fn a_held_column_is_skipped_by_arrows_and_its_chip_brings_it_back() {
    let abc = |active: &str| tiles(active, panes(["a", "b", "c"]));
    let mut world = start(THREE_FIT, None, vec![abc("a")]);
    becomes(&mut world, abc("b"));

    press(&mut world, Action::ShelfSendToShelf);
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    assert_eq!(shown_leaf(&world), ["c"]);
    becomes(&mut world, abc("c"));
    assert_eq!(screen(&world), ["a", "c"]);
    assert!(chip(&world, "b").is_some());

    press(&mut world, Action::TerminalFocusLeft);
    assert_eq!(shown_leaf(&world), ["c", "a"], "the arrow skips b");
    becomes(&mut world, abc("a"));

    click(&mut world, "shelf.chip.b");
    becomes(&mut world, abc("b"));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert!(world.screen().find(Target::key("shelf")).is_none());
    rest(&mut world);
}

#[test]
fn sending_to_the_shelf_holds_a_second_column_when_the_first_held_one_left_room() {
    let abc = |active: &str| tiles(active, panes(["a", "b", "c"]));
    let mut world = start(THREE_FIT, None, vec![abc("a")]);
    becomes(&mut world, abc("b"));
    press(&mut world, Action::ShelfSendToShelf);
    becomes(&mut world, abc("c"));

    press(&mut world, Action::ShelfSendToShelf);
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    assert_eq!(shown_leaf(&world), ["c", "a"]);
    becomes(&mut world, abc("a"));
    assert_eq!(screen(&world), ["a"]);
    assert!(chip(&world, "b").is_some() && chip(&world, "c").is_some());
    rest(&mut world);
}

#[test]
fn sending_the_only_tile_on_screen_to_the_shelf_moves_it_to_the_row_end_and_shows_the_next() {
    let mut world = start(
        TWO_FIT,
        Some("150"),
        vec![tiles("a", panes(["a", "b", "c"]))],
    );
    assert_eq!(screen(&world), ["a"]);

    press(&mut world, Action::ShelfSendToShelf);
    let send = &sent(&world, "desktop_move_leaf")[0];
    assert_eq!(
        (&send["leaf_id"], &send["anchor_id"], &send["edge"]),
        (&"a".into(), &"c".into(), &"right".into())
    );
    assert_eq!(send["activate"], false);
    assert_eq!(shown_leaf(&world), ["b"]);
    becomes(&mut world, tiles("b", panes(["b", "c", "a"])));
    assert_eq!(screen(&world), ["b"]);
    rest(&mut world);
}

#[test]
fn a_split_beside_a_full_screen_slides_to_keep_the_anchor_and_flashes_the_chip_of_the_column_that_left()
 {
    let mut world = start(THREE_FIT, None, vec![tiles("a", panes(["a", "b", "c"]))]);
    becomes(&mut world, tiles("c", panes(["a", "b", "c"])));

    press(&mut world, Action::TerminalSplitVertical);
    let spawn = sent(&world, "spawn_session")[0].clone();
    assert_eq!(spawn["placement"]["anchor_pane_id"], "c");
    assert_eq!(spawn["placement"]["direction"], "vertical");

    let mut placed = tiles("n", panes(["a", "b", "c", "n"]));
    placed.panes[3].session_id = spawn["id"].as_str().unwrap().to_owned();
    becomes(&mut world, placed);
    assert_eq!(screen(&world), ["b", "c", "n"]);
    assert_eq!(chip(&world, "a").as_deref(), Some("arrived"));
    assert_eq!(
        super::desktops::ringing(&world),
        Vec::<String>::new(),
        "the split's own shell does not ring"
    );
    rest(&mut world);
}

#[test]
fn an_unseen_document_goes_before_a_waiting_agent_for_the_jump_and_closing_it_restores_the_screen()
{
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let mut world = start_with(TWO_FIT, None, vec![four("a")], &["d"]);
    becomes(&mut world, four("b"));
    becomes(&mut world, four("c"));
    assert_eq!(screen(&world), ["b", "c"]);

    let with_doc = |active: &str| {
        tiles(
            active,
            [
                pane("a"),
                document("doc", "a", 0),
                pane("b"),
                pane("c"),
                pane("d"),
            ],
        )
    };
    becomes(&mut world, with_doc("c"));
    assert_eq!(screen(&world), ["b", "c"]);
    assert_eq!(chip(&world, "doc").as_deref(), Some("needs you, arrived"));

    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown_leaf(&world), ["doc"], "the document goes before d");
    assert!(sent(&world, "desktop_move_leaf").is_empty());
    becomes(&mut world, with_doc("doc"));
    assert_eq!(screen(&world), ["doc", "b"]);

    press(&mut world, Action::SessionClose);
    assert_eq!(sent(&world, "desktop_remove_leaf")[0]["leaf_id"], "doc");
    becomes(&mut world, four("c"));
    assert_eq!(screen(&world), ["b", "c"]);
    rest(&mut world);
}

#[test]
fn closing_a_document_the_person_moved_to_restores_the_screen_and_keeps_the_tile_the_daemon_makes_active()
 {
    let five = |active: &str| tiles(active, panes(["a", "b", "c", "d", "e"]));
    let with_doc = |active: &str| {
        tiles(
            active,
            [
                pane("a"),
                pane("b"),
                pane("c"),
                document("doc", "c", 0),
                pane("d"),
                pane("e"),
            ],
        )
    };
    let mut world = start(THREE_FIT, None, vec![five("a")]);
    becomes(&mut world, five("b"));
    becomes(&mut world, with_doc("b"));
    assert_eq!(screen(&world), ["b", "c", "doc"]);
    for active in ["c", "doc"] {
        press(&mut world, Action::TerminalFocusRight);
        becomes(&mut world, with_doc(active));
    }
    assert_eq!(shown_leaf(&world), ["c", "doc"]);

    press(&mut world, Action::SessionClose);
    assert_eq!(sent(&world, "desktop_remove_leaf")[0]["leaf_id"], "doc");
    becomes(&mut world, five("c"));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert_eq!(
        shown_leaf(&world),
        ["c", "doc"],
        "the app leaves c, which the daemon made active, active"
    );
    rest(&mut world);
}

#[test]
fn a_document_from_an_agent_more_tiles_just_brought_on_screen_slides_into_view() {
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let mut world = start(FOUR_FIT, Some("86"), vec![four("a")]);
    becomes(&mut world, four("b"));
    assert_eq!(screen(&world), ["a", "b"]);

    press(&mut world, Action::ShelfMoreTiles);
    assert_eq!(screen(&world), ["a", "b", "c"]);
    becomes(
        &mut world,
        tiles(
            "b",
            [
                pane("a"),
                pane("b"),
                pane("c"),
                document("doc", "c", 0),
                pane("d"),
            ],
        ),
    );
    assert_eq!(screen(&world), ["b", "c", "doc"]);
    rest(&mut world);
}

/// Four agents in a window two fit in, with a document agent a opened beside it, asked to
/// show `asked` times, and `active` the active tile.
fn with_doc_by_a(active: &str, asked: u64) -> Desktop {
    tiles(
        active,
        [
            pane("a"),
            document("doc", "a", asked),
            pane("b"),
            pane("c"),
            pane("d"),
        ],
    )
}

fn other() -> Desktop {
    desktop(2, "x", pane("x"))
}

/// The person jumps to the document agent a opened off screen, then moves on to b and c and
/// back to b, which leaves the screen on b and c.
fn document_seen_then_left() -> World {
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let mut world = start(TWO_FIT, None, vec![four("a"), other()]);
    arrange(&mut world, "work/desktop_1", vec![four("c"), other()]);
    let at = |world: &mut World, active: &str| {
        arrange(
            world,
            "work/desktop_1",
            vec![with_doc_by_a(active, 0), other()],
        );
    };
    at(&mut world, "c");
    press(&mut world, Action::SessionJumpToWaiting);
    at(&mut world, "doc");
    assert_eq!(screen(&world), ["doc", "b"]);
    for active in ["b", "c", "b"] {
        at(&mut world, active);
    }
    assert_eq!(screen(&world), ["b", "c"]);
    world.advance(Duration::from_secs(1));
    assert_eq!(chip(&world, "doc").as_deref(), Some(""));
    world
}

#[test]
fn a_document_asked_for_again_after_it_was_seen_waits_unseen_once_for_the_jump() {
    let mut world = document_seen_then_left();
    let jumps = shown_leaf(&world).len();

    for asked in [1, 2] {
        arrange(
            &mut world,
            "work/desktop_1",
            vec![with_doc_by_a("b", asked), other()],
        );
    }
    assert_eq!(screen(&world), ["b", "c"]);
    assert_eq!(chip(&world, "doc").as_deref(), Some("needs you, arrived"));

    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown_leaf(&world)[jumps..], ["doc"]);
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with_doc_by_a("doc", 2), other()],
    );
    assert!(chip(&world, "doc").is_none());

    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(
        shown_leaf(&world)[jumps..],
        ["doc"],
        "two asks wait for one jump"
    );
    rest(&mut world);
}

#[test]
fn a_document_asked_for_again_by_an_agent_on_screen_slides_into_view_and_the_active_tile_stays() {
    let with_doc = |active: &str, asked: u64| {
        tiles(
            active,
            [
                pane("a"),
                pane("b"),
                pane("c"),
                pane("d"),
                document("doc", "d", asked),
            ],
        )
    };
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles("a", panes(["a", "b", "c", "d"]))],
    );
    becomes(&mut world, tiles("d", panes(["a", "b", "c", "d"])));
    becomes(&mut world, with_doc("d", 0));
    assert_eq!(screen(&world), ["c", "d", "doc"]);
    for active in ["c", "b", "c"] {
        becomes(&mut world, with_doc(active, 0));
    }
    assert_eq!(screen(&world), ["b", "c", "d"]);

    becomes(&mut world, with_doc("c", 1));
    assert_eq!(screen(&world), ["c", "d", "doc"]);
    assert!(chip(&world, "doc").is_none());
    rest(&mut world);
}

#[test]
fn a_document_asked_for_again_while_on_screen_changes_nothing_and_closing_it_restores_the_first_screen()
 {
    let five = |active: &str| tiles(active, panes(["a", "b", "c", "d", "e"]));
    let with_doc = |asked: u64| {
        tiles(
            "b",
            [
                pane("a"),
                pane("b"),
                pane("c"),
                document("doc", "c", asked),
                pane("d"),
                pane("e"),
            ],
        )
    };
    let mut world = start(THREE_FIT, None, vec![five("a")]);
    becomes(&mut world, five("b"));
    becomes(&mut world, with_doc(0));
    assert_eq!(screen(&world), ["b", "c", "doc"]);

    becomes(&mut world, with_doc(1));
    assert_eq!(screen(&world), ["b", "c", "doc"]);
    assert!(chip(&world, "doc").is_none());

    becomes(&mut world, five("b"));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    rest(&mut world);
}

#[test]
fn a_document_asked_for_again_while_another_desktop_is_current_waits_for_the_jump() {
    let mut world = document_seen_then_left();
    let jumps = shown_leaf(&world).len();
    arrange(
        &mut world,
        "work/desktop_2",
        vec![with_doc_by_a("b", 0), other()],
    );

    arrange(
        &mut world,
        "work/desktop_2",
        vec![with_doc_by_a("b", 1), other()],
    );
    arrange(
        &mut world,
        "work/desktop_1",
        vec![with_doc_by_a("b", 1), other()],
    );
    assert_eq!(screen(&world), ["b", "c"]);
    assert!(
        chip(&world, "doc").is_some_and(|v| v.starts_with("needs you")),
        "{:?}",
        chip(&world, "doc")
    );

    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown_leaf(&world)[jumps..], ["doc"]);
    rest(&mut world);
}

#[test]
fn a_document_opened_while_another_desktop_is_current_waits_for_the_jump() {
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let with_doc = tiles(
        "a",
        [
            pane("a"),
            pane("b"),
            pane("c"),
            pane("d"),
            document("doc", "d", 0),
        ],
    );
    let mut world = start(TWO_FIT, None, vec![four("a"), other()]);
    arrange(&mut world, "work/desktop_2", vec![four("a"), other()]);

    arrange(
        &mut world,
        "work/desktop_2",
        vec![with_doc.clone(), other()],
    );
    arrange(&mut world, "work/desktop_1", vec![with_doc, other()]);
    assert_eq!(screen(&world), ["a", "b"]);
    assert!(
        chip(&world, "doc").is_some_and(|v| v.starts_with("needs you")),
        "{:?}",
        chip(&world, "doc")
    );

    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown_leaf(&world), ["doc"]);
    rest(&mut world);
}

#[test]
fn a_document_opened_while_away_beside_an_agent_that_was_on_screen_shows_on_return() {
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let with_doc = tiles(
        "b",
        [
            pane("a"),
            pane("b"),
            document("doc", "b", 0),
            pane("c"),
            pane("d"),
        ],
    );
    let mut world = start(TWO_FIT, None, vec![four("a"), other()]);
    arrange(&mut world, "work/desktop_1", vec![four("b"), other()]);
    assert_eq!(screen(&world), ["a", "b"]);
    arrange(&mut world, "work/desktop_2", vec![four("b"), other()]);

    arrange(
        &mut world,
        "work/desktop_2",
        vec![with_doc.clone(), other()],
    );
    arrange(&mut world, "work/desktop_1", vec![with_doc, other()]);
    assert_eq!(screen(&world), ["b", "doc"]);
    assert_eq!(keyboard(&mut world), "b");
    rest(&mut world);
}

#[test]
fn a_terminal_an_agent_starts_off_screen_flashes_its_chip_and_moves_nothing() {
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles("a", panes(["a", "b", "c", "d", "e"]))],
    );

    let arrived = world.record(60.0, 1.0, |daemon, _| {
        daemon.emit(arrangement(
            "work/desktop_1",
            vec![tiles("a", panes(["a", "b", "c", "d", "e", "late"]))],
        ))
    });
    assert_eq!(screen(&world), ["a", "b", "c"]);
    let chip_shown = opacities(&arrived, |path| path.ends_with("/shelf.chip.late"));
    let flash: Vec<f32> = opacities(&arrived, |path| path.contains("/shelf.chip.late.flash."))
        .iter()
        .zip(&chip_shown)
        .map(|(flash, chip)| if *chip > 0.0 { flash / chip } else { 1.0 })
        .collect();
    let (held, fading) = flash.split_at(32);
    assert!(
        held.iter().all(|o| *o == 1.0),
        "the flash holds for 60%: {flash:?}"
    );
    assert!(
        fading.windows(2).all(|p| p[1] <= p[0]) && fading.iter().any(|o| *o < 1.0),
        "then fades: {flash:?}"
    );
    assert!(flash.len() <= 60, "over 0.9 s: {flash:?}");
    assert_eq!(chip(&world, "late").as_deref(), Some(""));
    assert!(shown_leaf(&world).is_empty());
    rest(&mut world);
}

#[test]
fn the_tile_that_slides_into_a_closed_tiles_slot_takes_the_keyboard() {
    let four = |active: &str| tiles(active, panes(["a", "b", "c", "d"]));
    let mut world = start(TWO_FIT, None, vec![four("a")]);
    for active in ["d", "c", "b"] {
        becomes(&mut world, four(active));
    }
    assert_eq!(screen(&world), ["b", "c"]);
    world.gesture(|app| app.click(Target::role("group").name("Agent b")));
    assert_eq!(keyboard(&mut world), "b");

    press(&mut world, Action::SessionClose);
    assert_eq!(sent(&world, "desktop_close_tile")[0]["tile_id"], "b");
    becomes(&mut world, tiles("c", panes(["a", "c", "d"])));
    assert_eq!(screen(&world), ["c", "d"]);
    assert_eq!(keyboard(&mut world), "c");
    rest(&mut world);
}

/// The tile area at `area` wide shows `before`, then the daemon changes desktop 1 to `after`:
/// what the window shows over the two seconds that follow, long enough for a chip's flash.
fn moving(area: f32, before: Desktop, after: Desktop) -> Recording {
    let mut world = start(area, None, vec![before]);
    world.record(60.0, 2.0, |daemon, _| {
        daemon.emit(arrangement("work/desktop_1", vec![after]));
    })
}

fn hiding_the_sidebar() -> Recording {
    let mut world = start(FOUR_FIT, None, vec![tiles("a", panes(["a", "b"]))]);
    world.record(60.0, 1.0, |_, app| {
        app.key(chord_of(Action::SessionToggleSidebar))
    })
}

fn chord_of(action: Action) -> rune::KeyEvent {
    match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn focusing(focused: bool) -> Recording {
    let mut world = start(TWO_FIT, None, vec![tiles("a", panes(["a", "b"]))]);
    if focused {
        press(&mut world, Action::TerminalToggleMaximize);
    }
    world.record(60.0, 1.0, |_, app| {
        app.key(chord_of(Action::TerminalToggleMaximize))
    })
}

const ALONE: &str = "a tile is shown alone|a tile shown alone returns";
const BY_DESIGN: &str = "showing a tile alone and returning it land at once, as in the Tauri app";

const LANDS_AT_ONCE: &[AllowRow] = &[
    AllowRow::new("jump", "tile.a", ALONE, BY_DESIGN).max(22),
    AllowRow::new("pop-in", "tile.", ALONE, BY_DESIGN).max(2),
    AllowRow::new("pop-out", "tile.", ALONE, BY_DESIGN).max(2),
    AllowRow::new("pop-in", "divider.", ALONE, BY_DESIGN).max(1),
    AllowRow::new("pop-out", "divider.", ALONE, BY_DESIGN).max(1),
    AllowRow::new(
        "text-swap",
        "titlebar.sidebar",
        "the sidebar hides",
        "the sidebar button's glyph swaps at once, its own unit's motion (s-bsjwvs)",
    )
    .max(1),
    AllowRow::new(
        "jump",
        "sidebar.session",
        "a tile closes",
        "the sidebar's rows land at once when the daemon removes one, its own unit's motion (s-bsjwvs)",
    )
    .max(1),
];

#[test]
fn tiles_move_join_and_leave_the_row_without_a_visible_glitch_and_focus_lands_at_once() {
    let cells: Vec<(&str, Job)> = vec![
        (
            "a tile joins the row",
            Box::new(|| {
                moving(
                    FOUR_FIT,
                    tiles("a", panes(["a", "b"])),
                    tiles("c", panes(["a", "b", "c"])),
                )
            }),
        ),
        (
            "a tile arrives on the shelf",
            Box::new(|| {
                moving(
                    TWO_FIT,
                    tiles("a", panes(["a", "b"])),
                    tiles("a", panes(["a", "b", "c"])),
                )
            }),
        ),
        (
            "a chip brings its tile in place of another",
            Box::new(|| {
                moving(
                    TWO_FIT,
                    tiles("a", panes(["a", "b", "c", "d"])),
                    tiles("d", panes(["a", "d", "b", "c"])),
                )
            }),
        ),
        (
            "a tile closes",
            Box::new(|| {
                moving(
                    FOUR_FIT,
                    tiles("a", panes(["a", "b", "c"])),
                    tiles("a", panes(["a", "c"])),
                )
            }),
        ),
        (
            "the row slides to the last tile",
            Box::new(|| {
                moving(
                    TWO_FIT,
                    tiles("a", panes(["a", "b", "c", "d"])),
                    tiles("d", panes(["a", "b", "c", "d"])),
                )
            }),
        ),
        ("the sidebar hides", Box::new(hiding_the_sidebar)),
        ("a tile is shown alone", Box::new(|| focusing(false))),
        ("a tile shown alone returns", Box::new(|| focusing(true))),
    ];
    motion_gate::gate_rows(
        cells.into_iter().map(|(n, j)| (n.to_owned(), j)).collect(),
        LANDS_AT_ONCE,
    );
}

/// How tile `leaf` looked on each frame `rec` drew it, from the frame the gesture landed on:
/// its rect and opacity.
fn looks_of(rec: &Recording, leaf: &str) -> Vec<([f32; 4], f32)> {
    let from = rec.inputs[0].0;
    let track = rec
        .tracks
        .iter()
        .find(|t| t.path.ends_with(&format!("/tile.{leaf}")))
        .unwrap_or_else(|| panic!("tile {leaf} is on screen"));
    track
        .frames
        .iter()
        .filter(|(k, _)| *k >= from)
        .map(|(_, s)| (s.rect, s.op))
        .collect()
}

/// The opacity, frame by frame from the gesture on, of the first element `rec` drew whose path
/// is `of`.
pub(crate) fn opacities(rec: &Recording, of: impl Fn(&str) -> bool) -> Vec<f32> {
    let from = rec.inputs[0].0;
    rec.tracks
        .iter()
        .find(|t| of(&t.path))
        .map(|t| {
            t.frames
                .iter()
                .filter(|(k, _)| *k >= from)
                .map(|(_, s)| s.op)
                .collect()
        })
        .unwrap_or_default()
}

fn lands_at_once(rec: &Recording, leaf: &str) -> bool {
    let looks = looks_of(rec, leaf);
    looks.iter().all(|l| *l == looks[0] && l.1 == 1.0)
}

#[test]
fn showing_a_tile_alone_and_returning_it_land_in_the_frame_of_the_key() {
    let alone = focusing(false);
    assert!(
        lands_at_once(&alone, "a") && looks_of(&alone, "a")[0].0[2] == TWO_FIT - 2.0 * ALONE_INSET
    );
    let returned = focusing(true);
    assert!(lands_at_once(&returned, "a") && looks_of(&returned, "a")[0].0[2] == TWO_FIT / 2.0);
}

#[test]
fn a_tile_opened_while_another_shows_alone_and_an_agent_opened_from_home_land_at_once() {
    let mut world = start(TWO_FIT, None, vec![tiles("a", panes(["a", "b", "c"]))]);
    press(&mut world, Action::TerminalToggleMaximize);
    let opened_tile = world.record(60.0, 1.0, |_, app| {
        app.update(|state, tx| {
            let open = Action::OpenTile {
                desktop: "work/desktop_1".into(),
                tile: "b".into(),
            };
            attn_app::dispatch(state, open, tx);
        })
    });
    assert!(
        lands_at_once(&opened_tile, "a"),
        "{:?}",
        looks_of(&opened_tile, "a")
    );

    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, Action::SessionGoToDashboard, tx);
        })
    });
    let opened = world.record(60.0, 1.0, |_, app| {
        app.click(Target::key("sidebar.session.s-b"))
    });
    assert!(lands_at_once(&opened, "b"), "{:?}", looks_of(&opened, "b"));
    rest(&mut world);
}

#[test]
fn one_shell_tile_in_a_1200_by_800_window_gets_the_grid_the_tauri_app_gives_it() {
    let mut shell = desktop(1, "a", pane("a"));
    shell.panes[0].kind = "shell".into();
    let mut world = World::start_sized(daemon(None, vec![shell], &[]), (1200.0, 800.0));
    world.open_desktop();
    let sized = world.daemon.sent_of("pty_resize");
    let last = sized.last().expect("the app sized the terminal");
    assert_eq!(
        (last["cols"].as_u64(), last["rows"].as_u64()),
        (Some(106), Some(34))
    );
    rest(&mut world);
}
