use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    CommandUsage, Desktop, DesktopPane, GetCommandUsageResultMessage, InitialStateMessage, Profile,
    ProfileActionResultMessage, ProfileArrangementChangedMessage, RecordCommandUsageResultMessage,
    Session,
};
use attn_testkit::tree::{below, pane, row};
use attn_testkit::{Answer, Daemon, Request, Target, World, key_event, message};
use rune::KeyPlatform;
use serde_json::Value;

/// The sidebar beside the tile area, in points: a 1600-point window has 1360
/// for tiles.
const BESIDE_TILES: f32 = 240.0;
/// The title bar above the tile area, in points: a 900-point window
/// has tiles 868 tall.
const ABOVE_TILES: f32 = 32.0;

/// A window whose tile area is `area` points wide and 868 tall. A terminal column is 9
/// points, so the narrowest width steps are 57 cols at 513px, 66 at 594, 76 at 684, 86 at
/// 774, 100 at 900, 120 at 1080 and 150 at 1350.
fn window(area: f32) -> (f32, f32) {
    (area + BESIDE_TILES, 900.0)
}

/// Two tiles fit at 57 and 66 cols, one from 76.
const TWO_FIT: f32 = 1348.0;
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

fn four(active: &str) -> Desktop {
    tiles(active, panes(["a", "b", "c", "d"]))
}

/// A pane carrying a dragged width of `cols`, saved at a tile area of `area_cols` if given.
fn wanted(id: &str, cols: u64, area_cols: Option<u64>) -> Value {
    let mut leaf = pane(id);
    leaf["wanted_cols"] = cols.into();
    if let Some(area_cols) = area_cols {
        leaf["wanted_area_cols"] = area_cols.into();
    }
    leaf
}

/// Desktop 2 with agent x alone on it.
fn other() -> Desktop {
    desktop(2, "x", pane("x"))
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

/// The app in a window of `size` points, with `narrowest` cols as the narrowest tile width.
/// The daemon answers the palette's history with none.
fn start_sized(size: (f32, f32), narrowest: Option<&str>, desktops: Vec<Desktop>) -> World {
    let sessions = desktops
        .iter()
        .flat_map(|d| d.panes.iter())
        .map(|p| session(&p.pane_id))
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
    let mut world = World::start_sized(daemon, size);
    world.open_desktop();
    world
}

/// The app in a window whose tile area is `area` wide.
fn start(area: f32, narrowest: Option<&str>, desktops: Vec<Desktop>) -> World {
    start_sized(window(area), narrowest, desktops)
}

/// The daemon tells the app the profile's desktops and its current desktop.
fn arrange(world: &mut World, current: &str, desktops: Vec<Desktop>) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(current),
        desktops,
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
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

fn escape(world: &mut World) {
    world.gesture(|app| {
        app.key(rune::KeyEvent {
            key: rune::Key::Escape,
            mods: rune::Mods::default(),
        })
    });
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

/// Each move the app sent as its leaf, anchor, edge and whether it took the whole column.
fn moves(world: &World) -> Vec<(String, String, String, bool)> {
    sent(world, "desktop_move_leaf")
        .iter()
        .map(|r| {
            (
                r["leaf_id"].as_str().unwrap().to_owned(),
                r["anchor_id"].as_str().unwrap_or_default().to_owned(),
                r["edge"].as_str().unwrap().to_owned(),
                r["whole_column"] == true,
            )
        })
        .collect()
}

fn moved(
    leaf: &str,
    anchor: &str,
    edge: &str,
    whole_column: bool,
) -> (String, String, String, bool) {
    (leaf.into(), anchor.into(), edge.into(), whole_column)
}

fn widths_sent(world: &World) -> Vec<(String, u64)> {
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

/// The agents the screen shows, left to right.
fn screen(world: &World) -> Vec<String> {
    let mut shown: Vec<(f32, String)> = world
        .screen()
        .all(Target::role("group"))
        .into_iter()
        .filter_map(|n| n.name?.strip_prefix("Agent ").map(str::to_owned))
        .filter_map(|leaf| {
            let [x, ..] = world.runtime().presented(&format!("tile.{leaf}"))?.0;
            Some((x, leaf))
        })
        .collect();
    shown.sort_by(|a, b| a.0.total_cmp(&b.0));
    shown.dedup_by(|a, b| a.1 == b.1);
    shown.into_iter().map(|(_, leaf)| leaf).collect()
}

fn width(world: &World, leaf: &str) -> f32 {
    world
        .runtime()
        .presented(&format!("tile.{leaf}"))
        .unwrap_or_else(|| panic!("{leaf} is drawn"))
        .0[2]
}

fn keyboard(world: &mut World) -> Value {
    world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap()["keyboard"]["pane"]
        .clone()
}

/// A point in `leaf`'s tile at fractions `fx`, `fy` of its width and height.
fn in_tile(world: &World, leaf: &str, fx: f32, fy: f32) -> (f32, f32) {
    at(world, &format!("tile.{leaf}"), fx, fy)
}

/// A point in the element `key` names at fractions `fx`, `fy` of its width and height.
fn at(world: &World, key: &str, fx: f32, fy: f32) -> (f32, f32) {
    let [x, y, w, h] = world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0;
    (x + w * fx, y + h * fy)
}

fn drag(world: &mut World, from: &str, to: (f32, f32)) {
    let from = from.to_owned();
    world.gesture(|app| app.drag(Target::key(from), to));
}

/// Drags `tile`'s header and drops it at `share` of the width of the chip or cap `key`.
fn drop_on(world: &mut World, tile: &str, key: &str, share: f32) {
    let to = at(world, key, share, 0.5);
    drag(world, &format!("tile.{tile}.header"), to);
}

fn divider_at(world: &World, split: &str) -> (f32, f32) {
    at(world, &format!("divider.{split}"), 0.5, 0.5)
}

fn drag_divider(world: &mut World, split: &str, by: f32) {
    let (x, y) = divider_at(world, split);
    world.gesture(|app| {
        app.drag_from((x, y), (x + by, y));
        app.release((x + by, y));
    });
}

/// The answer to a command the daemon refused with `error`.
fn refused(r: &Request, error: &str) -> Value {
    message(ProfileActionResultMessage {
        action: r["cmd"].as_str().unwrap().into(),
        request_id: r["request_id"].as_str().unwrap().into(),
        success: false,
        error: Some(error.into()),
        ..ProfileActionResultMessage::new()
    })
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(10));
    assert!(world.runtime_is_at_rest());
}

const LEFT_HALF: f32 = 0.1;
const RIGHT_HALF: f32 = 0.9;

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
fn a_refused_bring_says_the_keys_typed_for_it_were_not_delivered_and_sends_none() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);
    world
        .daemon
        .answer_in_turn("desktop_move_leaf", [Answer::Hold]);

    world.gesture(|app| app.click(Target::key("shelf.chip.d")));
    world.gesture(|app| app.type_text("y"));
    let bring = world.daemon.sent_of("desktop_move_leaf")[0].clone();
    world.daemon.reply_to(&bring, refused(&bring, "refused"));
    world.settle();

    let said = notice(&world).unwrap_or_default();
    assert!(
        said.contains("Input typed while switching could not be delivered"),
        "{said:?}"
    );
    assert!(typed(&world).is_empty(), "{:?}", typed(&world));
    rest(&mut world);
}

#[test]
fn a_stacks_divider_whose_tile_closes_mid_drag_lets_go_so_the_next_header_drag_moves_its_tile() {
    let stack = [below(pane("a"), pane("c")), pane("b")];
    let mut world = start(TWO_FIT, None, vec![tiles("a", stack)]);
    let (x, y) = divider_at(&world, "horizontal:a");
    world.gesture(|app| app.drag_from((x, y), (x, y + 100.0)));
    becomes(&mut world, tiles("a", panes(["a", "b"])));
    world.gesture(|app| app.release((x, y + 100.0)));

    let to = in_tile(&world, "b", 0.5, 0.97);
    drag(&mut world, "tile.a.header", to);

    assert_eq!(moves(&world).len(), 1, "{:?}", moves(&world));
    rest(&mut world);
}

#[test]
fn a_chip_dropped_on_a_tiles_bottom_edge_stacks_its_column_there() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);

    let to = in_tile(&world, "b", 0.5, 0.97);
    drag(&mut world, "shelf.chip.d", to);

    assert_eq!(moves(&world), [moved("d", "b", "bottom", true)]);
    assert_eq!(notice(&world), None);
    rest(&mut world);
}

#[test]
fn a_stack_chip_dropped_on_a_tiles_top_edge_takes_the_share_that_keeps_every_tile_at_the_minimum_height()
 {
    for (name, lower_share, tiles_high, y, share) in [
        ("an even stack", 0.5, 1200.0, 0.02, 640.0 / 1200.0),
        (
            "a stack with a short tile",
            0.8,
            2000.0,
            0.02,
            1600.0 / 2000.0,
        ),
        ("the band the pointer chose fits", 0.5, 4000.0, 0.2, 0.2),
    ] {
        let mut stack = below(pane("d"), pane("e"));
        stack["ratio"] = lower_share.into();
        let size = (TWO_FIT + BESIDE_TILES, tiles_high + ABOVE_TILES);
        let mut world = start_sized(
            size,
            None,
            vec![tiles("a", [pane("a"), pane("b"), pane("c"), stack])],
        );

        let to = in_tile(&world, "b", 0.5, y);
        drag(&mut world, "shelf.chip.d", to);

        assert_eq!(moves(&world), [moved("d", "b", "top", true)], "{name}");
        let sent_share = sent(&world, "desktop_move_leaf")[0]["leaf_share"]
            .as_f64()
            .unwrap();
        assert!(
            (sent_share - share).abs() < 1e-3,
            "{name}: {sent_share} is not {share}"
        );
        rest(&mut world);
    }
}

#[test]
fn a_chip_dropped_on_itself_moves_nothing_and_brings_nothing() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);

    let (x, y) = at(&world, "shelf.chip.d", 0.5, 0.5);
    world.gesture(|app| {
        app.drag_from((x, y), (x + 10.0, y));
        app.release((x + 10.0, y));
    });

    assert!(moves(&world).is_empty(), "{:?}", moves(&world));
    assert!(shown_leaf(&world).is_empty(), "{:?}", shown_leaf(&world));
    assert_eq!(screen(&world), ["a", "b"]);
    rest(&mut world);
}

#[test]
fn a_tile_dropped_on_either_half_of_a_chip_shows_beside_that_chip_whether_it_was_active_or_not() {
    for (tile, chip, share) in [
        ("a", "c", LEFT_HALF),
        ("a", "c", RIGHT_HALF),
        ("a", "d", LEFT_HALF),
        ("a", "d", RIGHT_HALF),
        ("b", "c", LEFT_HALF),
        ("b", "c", RIGHT_HALF),
        ("b", "d", LEFT_HALF),
        ("b", "d", RIGHT_HALF),
    ] {
        let case = format!("{tile} on {chip} at {share}");
        let mut world = start(THREE_FIT, Some("86"), vec![four("a")]);
        assert_eq!(screen(&world), ["a", "b"]);

        drop_on(&mut world, tile, &format!("shelf.chip.{chip}"), share);
        let edge = if share == LEFT_HALF { "left" } else { "right" };
        assert_eq!(moves(&world), [moved(tile, chip, edge, false)], "{case}");
        let mut order: Vec<&str> = ["a", "b", "c", "d"]
            .into_iter()
            .filter(|l| *l != tile)
            .collect();
        let at = order.iter().position(|l| *l == chip).unwrap() + usize::from(share == RIGHT_HALF);
        order.insert(at, tile);
        becomes(&mut world, tiles(tile, panes(order.iter().copied())));

        let pair: Vec<&str> = order
            .into_iter()
            .filter(|l| *l == tile || *l == chip)
            .collect();
        assert_eq!(screen(&world), pair, "{case}");
        assert_eq!(keyboard(&mut world), tile, "{case}");
        rest(&mut world);
    }
}

#[test]
fn a_tile_dropped_beside_the_chip_already_next_to_it_shows_the_pair() {
    let mut world = start(THREE_FIT, Some("86"), vec![four("a")]);

    drop_on(&mut world, "b", "shelf.chip.c", LEFT_HALF);
    becomes(&mut world, four("b"));

    assert_eq!(screen(&world), ["b", "c"]);
    assert_eq!(keyboard(&mut world), "b");
    rest(&mut world);
}

#[test]
fn a_tile_dropped_beside_a_chip_whose_dragged_width_keeps_them_apart_clears_it_and_shows_the_pair()
{
    let row = || [wanted("a", 200, None), pane("b"), pane("c"), pane("d")];
    let mut world = start(THREE_FIT, None, vec![tiles("a", row())]);
    press(&mut world, Action::TerminalFocusRight);
    becomes(&mut world, tiles("b", row()));
    assert_eq!(screen(&world), ["b", "c", "d"]);

    drop_on(&mut world, "b", "shelf.chip.a", RIGHT_HALF);
    assert_eq!(moves(&world), [moved("b", "a", "right", false)]);
    becomes(&mut world, tiles("b", row()));

    assert_eq!(widths_sent(&world), [("a".to_owned(), 0)]);
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert_eq!(keyboard(&mut world), "b");
    rest(&mut world);
}

#[test]
fn a_tile_dropped_beside_a_chip_when_two_tiles_cannot_fit_shows_alone_and_says_the_numbers() {
    let mut world = start(TWO_FIT, Some("150"), vec![four("a")]);
    assert_eq!(screen(&world), ["a"]);

    drop_on(&mut world, "a", "shelf.chip.c", RIGHT_HALF);
    assert_eq!(moves(&world), [moved("a", "c", "right", false)]);
    let said = notice(&world).unwrap_or_default();
    assert!(
        said.starts_with("2 tiles side by side at the narrowest width (150 cols, ")
            && said.contains("Tiles have 1348px here, so the dropped tile shows alone."),
        "{said:?}"
    );
    becomes(&mut world, tiles("a", panes(["b", "c", "a", "d"])));

    assert_eq!(screen(&world), ["a"]);
    rest(&mut world);
}

/// Desktop 1 with 24 agents in a row, `p00` to `p23`, more than the title bar has chips for.
fn many(active: &str, order: &[String]) -> Desktop {
    tiles(active, order.iter().map(|id| pane(id)))
}

fn many_ids() -> Vec<String> {
    (0..24).map(|i| format!("p{i:02}")).collect()
}

#[test]
fn a_tile_dropped_on_the_trailing_cap_moves_past_the_rows_last_column_and_shows_beside_it() {
    let ids = many_ids();
    let mut world = start(TWO_FIT, None, vec![many("p00", &ids)]);
    assert!(
        world
            .screen()
            .find(Target::key("shelf.cap.after"))
            .is_some()
    );

    drop_on(&mut world, "p01", "shelf.cap.after", 0.5);
    assert_eq!(moves(&world), [moved("p01", "p23", "right", false)]);
    let mut order: Vec<String> = ids.into_iter().filter(|id| id != "p01").collect();
    order.push("p01".into());
    becomes(&mut world, many("p01", &order));

    assert_eq!(screen(&world), ["p23", "p01"]);
    rest(&mut world);
}

#[test]
fn a_tile_dropped_on_the_leading_cap_moves_before_the_rows_first_column_and_shows_beside_it() {
    let ids = many_ids();
    let mut world = start(TWO_FIT, None, vec![many("p00", &ids)]);
    becomes(&mut world, many("p23", &ids));
    assert_eq!(screen(&world), ["p22", "p23"]);
    assert!(
        world
            .screen()
            .find(Target::key("shelf.cap.before"))
            .is_some()
    );

    drop_on(&mut world, "p22", "shelf.cap.before", 0.5);
    assert_eq!(moves(&world), [moved("p22", "p00", "left", false)]);
    let mut order: Vec<String> = ids.into_iter().filter(|id| id != "p22").collect();
    order.insert(0, "p22".into());
    becomes(&mut world, many("p22", &order));

    assert_eq!(screen(&world), ["p22", "p00"]);
    rest(&mut world);
}

fn with_stack(active: &str) -> Desktop {
    tiles(active, [pane("a"), below(pane("b"), pane("c")), pane("d")])
}

#[test]
fn a_stacked_tile_dropped_beside_a_chip_leaves_its_stack_alone_and_shows_beside_the_chip() {
    let mut world = start(THREE_FIT, Some("86"), vec![with_stack("a")]);
    assert_eq!(screen(&world), ["a", "b", "c"]);

    drop_on(&mut world, "c", "shelf.chip.d", RIGHT_HALF);
    assert_eq!(moves(&world), [moved("c", "d", "right", false)]);
    assert_eq!(shown_leaf(&world), ["c"]);
    becomes(&mut world, tiles("c", panes(["a", "b", "d", "c"])));

    assert_eq!(screen(&world), ["d", "c"]);
    rest(&mut world);
}

#[test]
fn the_active_stacked_tile_dropped_beside_its_neighbouring_chip_stays_active_beside_it() {
    let mut world = start(THREE_FIT, Some("86"), vec![with_stack("a")]);
    press(&mut world, Action::TerminalFocusRight);
    becomes(&mut world, with_stack("b"));
    press(&mut world, Action::TerminalFocusDown);
    becomes(&mut world, with_stack("c"));
    assert_eq!(screen(&world), ["a", "b", "c"]);
    assert_eq!(shown_leaf(&world), ["b", "c"]);

    drop_on(&mut world, "c", "shelf.chip.d", LEFT_HALF);
    assert_eq!(moves(&world), [moved("c", "d", "left", false)]);
    assert_eq!(shown_leaf(&world), ["b", "c"]);
    becomes(&mut world, four("c"));

    assert_eq!(screen(&world), ["c", "d"]);
    assert_eq!(keyboard(&mut world), "c");
    rest(&mut world);
}

#[test]
fn a_refused_shelf_drop_gives_the_tile_back_and_names_the_failure() {
    let mut world = start(THREE_FIT, None, vec![four("a")]);
    world
        .daemon
        .on("desktop_move_leaf", |r| refused(r, "refused"));
    assert_eq!(screen(&world), ["a", "b", "c"]);

    drop_on(&mut world, "c", "shelf.chip.d", 0.25);
    world.settle();

    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not move that tile: refused")
    );
    becomes(&mut world, four("c"));
    assert_eq!(
        screen(&world),
        ["a", "b", "c"],
        "the refused drop does not show its pair once the tile becomes active"
    );
    rest(&mut world);
}

#[test]
fn a_refused_swap_keeps_the_slots_tile_and_sends_no_second_move() {
    let mut world = start(THREE_FIT, None, vec![four("a")]);
    world
        .daemon
        .on("desktop_move_leaf", |r| refused(r, "refused"));

    let to = at(&world, "tile.a", 0.5, 0.5);
    drag(&mut world, "shelf.chip.d", to);
    world.settle();

    assert_eq!(sent(&world, "desktop_move_leaf").len(), 1);
    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not put that tile in the slot: refused")
    );
    assert_eq!(screen(&world), ["a", "b", "c"]);
    rest(&mut world);
}

fn in_sidebar(world: &World, slot: u32) -> (f32, f32) {
    at(
        world,
        &format!("sidebar.desktop.work/desktop_{slot}"),
        0.5,
        0.5,
    )
}

#[test]
fn a_stacks_chip_dropped_on_a_sidebar_desktop_moves_its_whole_column_there() {
    let first = tiles(
        "a",
        [pane("a"), pane("b"), pane("c"), below(pane("d"), pane("e"))],
    );
    let mut world = start(THREE_FIT, None, vec![first, other()]);

    let to = in_sidebar(&world, 2);
    drag(&mut world, "shelf.chip.d", to);

    assert_eq!(
        sent(&world, "desktop_move_leaf"),
        [json(
            r#"{"source_desktop_id":"work/desktop_1","target_desktop_id":"work/desktop_2","leaf_id":"d",
                "edge":"left","leaf_share":0.32,"whole_column":true}"#
        )]
    );
    world.advance(Duration::from_secs(1));
    rest(&mut world);
}

#[test]
fn escape_cancels_a_chip_drag_and_a_header_drag_onto_a_chip_with_no_move_and_no_message() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);

    let to = in_tile(&world, "b", 0.5, 0.5);
    world.gesture(|app| app.drag_over(Target::key("shelf.chip.d"), to));
    assert!(world.screen().find(Target::key("drag.target")).is_some());
    escape(&mut world);
    world.gesture(|app| app.release(to));
    assert!(world.screen().find(Target::key("drag.target")).is_none());
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());

    let to = at(&world, "shelf.chip.d", 0.5, 0.5);
    world.gesture(|app| app.drag_over(Target::key("tile.b.header"), to));
    assert!(world.screen().find(Target::key("drag.ghost")).is_some());
    escape(&mut world);
    world.gesture(|app| app.release(to));
    assert!(world.screen().find(Target::key("drag.ghost")).is_none());

    assert!(moves(&world).is_empty(), "{:?}", moves(&world));
    assert_eq!(notice(&world), None);
    assert_eq!(screen(&world), ["a", "b"]);
    rest(&mut world);
}

#[test]
fn a_header_dropped_at_the_tile_areas_outer_edge_docks_against_the_tile_there() {
    let mut world = start(THREE_FIT, None, vec![four("a")]);

    let [x, y, _, h] = world.runtime().presented("tile.a").unwrap().0;
    drag(&mut world, "tile.b.header", (x + 1.0, y + h / 2.0));

    assert_eq!(moves(&world), [moved("b", "a", "left", false)]);
    rest(&mut world);
}

#[test]
fn a_divider_drag_moves_only_that_divider_when_every_dragged_width_is_scaled_to_fit() {
    let mut world = start(
        THREE_FIT,
        Some("57"),
        vec![tiles(
            "a",
            [
                wanted("a", 70, None),
                wanted("b", 65, None),
                wanted("c", 60, None),
            ],
        )],
    );
    assert_eq!(screen(&world), ["a", "b", "c"]);
    let (a, c) = (width(&world, "a"), width(&world, "c"));
    assert!(
        a + width(&world, "b") + c > 195.0 * 9.0,
        "195 cols of dragged widths grow to fill 205"
    );
    assert!(((a / c) - 70.0 / 60.0).abs() < 0.02, "{a} / {c}");

    drag_divider(&mut world, "row:a", 100.0);

    assert!(
        (width(&world, "a") - a - 100.0).abs() < 9.0,
        "{} after {a}",
        width(&world, "a")
    );
    assert!(
        (width(&world, "c") - c).abs() < 9.0,
        "{} after {c}",
        width(&world, "c")
    );
    let leaves: Vec<String> = widths_sent(&world).into_iter().map(|(l, _)| l).collect();
    assert_eq!(leaves, ["a", "b", "c"]);
    rest(&mut world);
}

/// The tree `desktop` has once the daemon saved each width the app sent, at the area it
/// sent with it.
fn with_sent_widths(world: &World, leaves: &[&str]) -> Vec<Value> {
    let saved = sent(world, "desktop_set_wanted_cols");
    leaves
        .iter()
        .map(
            |leaf| match saved.iter().rev().find(|w| w["leaf_id"] == *leaf) {
                Some(w) => wanted(
                    leaf,
                    w["cols"].as_u64().unwrap(),
                    Some(w["area_cols"].as_u64().unwrap()),
                ),
                None => pane(leaf),
            },
        )
        .collect()
}

#[test]
fn a_dragged_width_stays_once_the_daemon_echoes_it() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);
    let before = width(&world, "a");

    drag_divider(&mut world, "row:a", 50.0);
    let dragged = width(&world, "a");
    assert!(dragged > before + 40.0, "{dragged} after {before}");
    let leaves: Vec<String> = widths_sent(&world).into_iter().map(|(l, _)| l).collect();
    assert_eq!(leaves, ["a", "b"]);

    let echoed = with_sent_widths(&world, &["a", "b", "c", "d"]);
    becomes(&mut world, tiles("a", echoed));
    rest(&mut world);
    assert_eq!(width(&world, "a"), dragged);
}

#[test]
fn a_divider_dragged_past_the_narrowest_saves_the_area_and_holds_there_and_gives_way_one_column_below()
 {
    let mut world = start(THREE_FIT, None, vec![tiles("a", panes(["a", "b"]))]);
    assert_eq!(screen(&world), ["a", "b"]);

    drag_divider(&mut world, "row:a", 1500.0);
    assert_eq!(screen(&world), ["a"]);
    let saved = sent(&world, "desktop_set_wanted_cols");
    assert_eq!(saved[0]["leaf_id"], "a");
    assert_eq!(saved[0]["area_cols"], saved[0]["cols"]);
    let area_cols = saved[0]["area_cols"].as_u64().unwrap() as f32;
    let echoed = with_sent_widths(&world, &["a", "b"]);
    becomes(&mut world, tiles("a", echoed));
    assert_eq!(screen(&world), ["a"]);

    world.resize(window((area_cols - 1.0) * 9.0 + 4.0));
    assert_eq!(screen(&world), ["a", "b"]);
    assert_eq!(sent(&world, "desktop_set_wanted_cols"), saved);

    world.resize(window(THREE_FIT));
    assert_eq!(screen(&world), ["a"]);
    rest(&mut world);
}

#[test]
fn a_divider_dragged_past_the_narrowest_at_a_smaller_area_than_saved_still_shelves_the_far_column()
{
    let mut world = start(
        THREE_FIT,
        None,
        vec![tiles("a", [wanted("a", 200, Some(233)), pane("b")])],
    );
    assert_eq!(screen(&world), ["a", "b"]);

    drag_divider(&mut world, "row:a", 1500.0);
    assert_eq!(screen(&world), ["a"]);
    let saved = sent(&world, "desktop_set_wanted_cols");
    assert_eq!(saved[0]["leaf_id"], "a");
    assert!(saved[0]["area_cols"].as_u64().unwrap() < 233, "{saved:?}");
    let echoed = with_sent_widths(&world, &["a", "b"]);
    becomes(&mut world, tiles("a", echoed));

    world.resize(window(FOUR_FIT));
    world.resize(window(THREE_FIT));
    assert_eq!(screen(&world), ["a"]);
    rest(&mut world);
}

#[test]
fn a_refused_width_goes_back_and_names_the_failure() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);
    world
        .daemon
        .on("desktop_set_wanted_cols", |r| refused(r, "refused"));
    let before = width(&world, "a");

    drag_divider(&mut world, "row:a", 50.0);
    world.settle();

    assert_eq!(
        notice(&world).as_deref(),
        Some("Could not save that width: refused")
    );
    rest(&mut world);
    assert_eq!(width(&world, "a"), before);
}

#[test]
fn a_double_click_on_a_divider_clears_the_dragged_widths_on_both_sides() {
    let mut world = start(TWO_FIT, None, vec![four("a")]);
    let before = width(&world, "a");
    drag_divider(&mut world, "row:a", 50.0);
    let echoed = with_sent_widths(&world, &["a", "b", "c", "d"]);
    becomes(&mut world, tiles("a", echoed));
    let dragged = widths_sent(&world).len();

    let at = divider_at(&world, "row:a");
    world.gesture(|app| {
        app.click_at(at);
        app.click_at(at);
    });

    assert_eq!(
        widths_sent(&world)[dragged..],
        [("a".to_owned(), 0), ("b".to_owned(), 0)]
    );
    rest(&mut world);
    assert_eq!(width(&world, "a"), before);
}

fn clear_dragged_widths(world: &mut World) {
    press(world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text("clear dragged widths"));
    world.gesture(|app| {
        app.click(Target::key(
            "palette.row.command:shelf-clear-dragged-widths",
        ))
    });
}

#[test]
fn the_palette_clears_the_desktops_dragged_widths_and_says_when_there_are_none() {
    let mut world = start(
        TWO_FIT,
        None,
        vec![tiles(
            "a",
            [
                wanted("a", 80, None),
                wanted("b", 60, None),
                pane("c"),
                pane("d"),
            ],
        )],
    );

    clear_dragged_widths(&mut world);
    assert_eq!(
        widths_sent(&world),
        [("a".to_owned(), 0), ("b".to_owned(), 0)]
    );
    becomes(&mut world, four("a"));

    clear_dragged_widths(&mut world);
    assert_eq!(
        notice(&world).as_deref(),
        Some("This desktop has no dragged widths.")
    );
    rest(&mut world);
}

#[test]
fn a_stack_chip_carried_to_another_desktop_too_short_for_it_lands_beside_and_says_why() {
    let source = tiles(
        "a",
        [pane("a"), pane("b"), pane("c"), below(pane("d"), pane("e"))],
    );
    let size = (TWO_FIT + BESIDE_TILES, 800.0 + ABOVE_TILES);
    let mut world = start_sized(size, None, vec![source.clone(), other()]);

    let over = in_sidebar(&world, 2);
    world.gesture(|app| app.drag_over(Target::key("shelf.chip.d"), over));
    world.advance(Duration::from_millis(400));
    arrange(&mut world, "work/desktop_2", vec![source, other()]);
    let to = in_tile(&world, "x", 0.5, 0.02);
    world.gesture(|app| {
        app.move_to(to);
        app.release(to);
    });

    let sent_moves = sent(&world, "desktop_move_leaf");
    assert_eq!(sent_moves.len(), 1, "{sent_moves:?}");
    assert_eq!(sent_moves[0]["edge"], "right", "{sent_moves:?}");
    let said = notice(&world).unwrap_or_default();
    assert!(said.contains("Stacking here needs 960px"), "{said}");
    rest(&mut world);
}

#[test]
fn a_chip_carried_to_another_desktop_by_resting_on_it_lands_beside_a_tile_without_replacing_it() {
    let mut world = start(TWO_FIT, None, vec![four("a"), other()]);

    let over = in_sidebar(&world, 2);
    world.gesture(|app| app.drag_over(Target::key("shelf.chip.d"), over));
    world.advance(Duration::from_millis(400));
    assert_eq!(
        sent(&world, "desktop_set_current"),
        [json(
            r#"{"profile_id":"work","desktop_id":"work/desktop_2"}"#
        )]
    );
    arrange(&mut world, "work/desktop_2", vec![four("a"), other()]);
    assert_eq!(screen(&world), ["x"]);

    let to = in_tile(&world, "x", 0.5, 0.5);
    world.gesture(|app| {
        app.move_to(to);
        app.release(to);
    });

    let sent_moves = sent(&world, "desktop_move_leaf");
    assert_eq!(sent_moves.len(), 1, "{sent_moves:?}");
    assert_eq!(sent_moves[0]["source_desktop_id"], "work/desktop_1");
    assert_eq!(sent_moves[0]["target_desktop_id"], "work/desktop_2");
    assert_eq!(sent_moves[0]["leaf_id"], "d");
    assert_eq!(sent_moves[0]["anchor_id"], "x");
    assert_eq!(sent_moves[0]["whole_column"], true);
    rest(&mut world);
}
