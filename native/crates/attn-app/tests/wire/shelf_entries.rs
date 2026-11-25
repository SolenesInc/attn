//! What the title bar's shelf draws for the columns of a row: their entries, a stack's slice and
//! its dots, the frame around the ones on screen, and what folds into the counts.

use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, InitialStateMessage, Profile, ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{below, pane, row};
use attn_testkit::{Daemon, Target, World};
use serde_json::Value;

const FRAME_REACH: f32 = 3.0;
const DOT: f32 = 4.0;
const DASH: f32 = 10.0;

fn agent(id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: "work/desktop_1".into(),
        pane_id: id.into(),
        runtime_id: format!("r-{id}"),
        session_id: format!("s-{id}"),
        title: id.into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(active: &str, ids: &[&str], tree: &Value) -> Desktop {
    Desktop {
        id: "work/desktop_1".into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        panes: ids.iter().copied().map(agent).collect(),
        tree_json: tree.to_string(),
        active_pane_id: active.into(),
        revision: 1,
    }
}

fn profile() -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: "work/desktop_1".into(),
        ..Profile::default()
    }
}

/// A desktop of agents `ids` arranged as `tree`, those in `waiting` waiting on the user.
struct Row {
    ids: Vec<&'static str>,
    tree: Value,
    waiting: Vec<&'static str>,
}

impl Row {
    fn of(ids: &[&'static str], tree: Value) -> Row {
        Row {
            ids: ids.to_vec(),
            tree,
            waiting: vec![],
        }
    }

    fn waiting(mut self, ids: &[&'static str]) -> Row {
        self.waiting = ids.to_vec();
        self
    }

    fn session(&self, id: &str) -> Session {
        Session {
            id: format!("s-{id}"),
            label: id.into(),
            directory: "/work".into(),
            profile_id: "work".into(),
            state: if self.waiting.contains(&id) {
                "waiting_input".into()
            } else {
                "idle".into()
            },
            ..Session::default()
        }
    }

    fn start(&self, active: &str, window: (f32, f32)) -> World {
        let state = InitialStateMessage {
            selected_profile_id: Some("work".into()),
            profiles: Some(vec![profile()]),
            desktops: Some(vec![desktop(active, &self.ids, &self.tree)]),
            sessions: Some(self.ids.iter().map(|id| self.session(id)).collect()),
            ..InitialStateMessage::new()
        };
        let mut world = World::start_sized(Daemon::scripted().state(state), window);
        world.open_desktop();
        world
    }

    fn arrangement(&self, active: &str) -> ProfileArrangementChangedMessage {
        ProfileArrangementChangedMessage {
            profile: profile(),
            desktops: vec![desktop(active, &self.ids, &self.tree)],
            ..ProfileArrangementChangedMessage::new()
        }
    }

    fn activate(&self, world: &mut World, active: &str) {
        world.daemon.emit(self.arrangement(active));
        world.settle();
    }
}

fn rect(world: &World, key: &str) -> [f32; 4] {
    world
        .runtime()
        .presented(key)
        .unwrap_or_else(|| panic!("{key} is drawn"))
        .0
}

fn entry(world: &World, column: &str) -> [f32; 4] {
    rect(world, &format!("shelf.entry.{column}"))
}

fn shows(world: &World, column: &str) -> bool {
    world
        .runtime()
        .presented(&format!("shelf.entry.{column}"))
        .is_some()
}

fn frames(world: &World, first: &str, last: &str) {
    let [fx, _, fw, _] = rect(world, "shelf.frame");
    let [start, ..] = entry(world, first);
    let [x, _, w, _] = entry(world, last);
    let near = |a: f32, b: f32| (a - b).abs() < 0.5;
    assert!(
        near(fx, start - FRAME_REACH) && near(fx + fw, x + w + FRAME_REACH),
        "the frame runs {fx} to {}, {first} to {last} run {start} to {}",
        fx + fw,
        x + w
    );
}

/// The widths of the dots on one edge of `column`'s pill, in order.
fn dots(world: &World, column: &str, edge: &str, members: usize) -> Vec<f32> {
    (0..members)
        .filter_map(|i| {
            let key = format!("shelf.entry.{column}.{edge}.{i}");
            world.runtime().presented(&key)
        })
        .map(|(r, _)| r[2])
        .filter(|w| *w > 0.0)
        .collect()
}

/// The member whose slice the pill of `column` shows.
fn slice(world: &World, column: &str, members: usize) -> usize {
    let [_, top, ..] = entry(world, column);
    (0..members)
        .find(|i| {
            let [_, y, ..] = rect(world, &format!("shelf.entry.{column}.slice.{i}"));
            (y - top).abs() < 0.5
        })
        .expect("the pill shows a slice whole")
}

const TWO_FIT: (f32, f32) = (1588.0, 900.0);

#[test]
fn the_frame_runs_around_the_entries_on_screen_and_follows_the_screen() {
    let ids = ["a", "b", "c", "d"];
    let of = Row::of(&ids, row(ids.map(pane)));
    let mut world = of.start("a", TWO_FIT);
    frames(&world, "a", "b");
    let before = entry(&world, "d");

    of.activate(&mut world, "d");
    frames(&world, "c", "d");
    assert_eq!(
        entry(&world, "d")[0],
        before[0],
        "the entries stay where they are"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_stacks_pill_shows_the_member_in_use_and_a_dot_for_each_before_and_after_it() {
    let ids = ["a", "b", "c", "d", "e"];
    let stack = below(pane("b"), below(pane("c"), below(pane("d"), pane("e"))));
    let of = Row::of(&ids, row([pane("a"), stack])).waiting(&["e"]);
    let mut world = of.start("b", TWO_FIT);
    assert_eq!(slice(&world, "b", 4), 0);
    assert_eq!(dots(&world, "b", "above", 4), [0.0f32; 0]);
    assert_eq!(dots(&world, "b", "below", 4), [DOT, DOT, DASH]);
    let width = entry(&world, "b")[2];

    of.activate(&mut world, "d");
    assert_eq!(slice(&world, "b", 4), 2);
    assert_eq!(dots(&world, "b", "above", 4), [DOT, DOT]);
    assert_eq!(dots(&world, "b", "below", 4), [DASH]);

    of.activate(&mut world, "a");
    assert_eq!(slice(&world, "b", 4), 2, "it keeps the one used last");

    of.activate(&mut world, "e");
    assert_eq!(slice(&world, "b", 4), 3);
    assert_eq!(dots(&world, "b", "below", 4), [0.0f32; 0]);
    assert_eq!(entry(&world, "b")[2], width, "the pill keeps its width");
    let pill = world.screen().get(Target::key("shelf.entry.b"));
    assert_eq!(pill.value.as_deref(), Some("current, needs you"));
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_slice_scrolls_to_the_next_member_while_its_dot_shrinks_away() {
    let ids = ["a", "b", "c"];
    let of = Row::of(&ids, row([pane("a"), below(pane("b"), pane("c"))]));
    let mut world = of.start("b", TWO_FIT);

    let walked = world.record(60.0, 0.5, |daemon, _| daemon.emit(of.arrangement("c")));
    let from = walked.inputs[0].0;
    let looks = |suffix: &str| -> Vec<[f32; 4]> {
        walked
            .tracks
            .iter()
            .find(|t| t.path.ends_with(suffix))
            .unwrap_or_else(|| panic!("{suffix} is drawn"))
            .frames
            .iter()
            .filter(|(k, _)| *k >= from)
            .map(|(_, s)| s.rect)
            .collect()
    };
    let tops: Vec<f32> = looks("/shelf.entry.b.slice.1")
        .iter()
        .map(|r| r[1])
        .collect();
    let shrinking: Vec<f32> = looks("/shelf.entry.b.below.1")
        .iter()
        .map(|r| r[2])
        .collect();
    let falls = |seen: &[f32]| {
        let steps: std::collections::BTreeSet<_> = seen.iter().map(|v| v.to_bits()).collect();
        seen.windows(2).all(|p| p[1] <= p[0] + 0.01) && steps.len() > 4
    };
    assert!(falls(&tops), "the slice scrolls up: {tops:?}");
    assert!(falls(&shrinking), "its dot shrinks: {shrinking:?}");
    assert_eq!(shrinking.last(), Some(&0.0));
    let still = &tops[16..];
    assert!(
        still.iter().all(|y| y == &still[0]),
        "it lands within 0.22 s: {tops:?}"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

const MANY: [&str; 24] = [
    "p00", "p01", "p02", "p03", "p04", "p05", "p06", "p07", "p08", "p09", "p10", "p11", "p12",
    "p13", "p14", "p15", "p16", "p17", "p18", "p19", "p20", "p21", "p22", "p23",
];

#[test]
fn the_entries_farthest_from_the_screen_fold_first_and_one_that_needs_you_folds_last() {
    let of = Row::of(&MANY, row(MANY.map(pane))).waiting(&["p22"]);
    let mut world = of.start("p00", TWO_FIT);
    let kept: Vec<&str> = MANY.into_iter().filter(|id| shows(&world, id)).collect();
    let folded = MANY.len() - kept.len();
    assert!(folded > 0, "the title bar cannot hold all 24");
    assert_eq!(kept.last(), Some(&"p22"), "the one that waits stays out");
    let nearest = kept.len() - 1;
    assert_eq!(kept[..nearest], MANY[..nearest], "the nearest stay out");
    let count = world.screen().get(Target::key("shelf.count.after"));
    let label = format!("{folded} more this way");
    assert_eq!(count.name.as_deref(), Some(label.as_str()));
    assert_eq!(count.value.as_deref(), Some(""));
    let plain = rect(&world, "shelf.count.after")[2];

    of.activate(&mut world, "p23");
    let kept: Vec<&str> = MANY.into_iter().filter(|id| shows(&world, id)).collect();
    assert_eq!(kept.last(), Some(&"p23"));
    assert!(
        world
            .screen()
            .find(Target::key("shelf.count.before"))
            .is_some()
    );
    frames(&world, "p22", "p23");

    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());

    let crowded = Row::of(&MANY, row(MANY.map(pane)))
        .waiting(&MANY[6..])
        .start("p00", (800.0, 600.0));
    let count = crowded.screen().get(Target::key("shelf.count.after"));
    assert_eq!(count.value.as_deref(), Some("needs you"));
    assert!(
        rect(&crowded, "shelf.count.after")[2] > plain + 16.0,
        "the count shows a mark of the ones that wait"
    );
}

#[test]
fn entries_on_screen_never_fold_and_their_names_are_cut_evenly_the_current_one_last() {
    let ids = [
        "MMMMMMMMMMMMMMMMMMMMMMM-1",
        "MMMMMMMMMMMMMMMMMMMMMMM-2",
        "MMMMMMMMMMMMMMMMMMMMMMM-3",
    ];
    let of = Row::of(&ids, row(ids.map(pane)));
    let roomy = of.start(ids[1], (2400.0, 900.0));
    let full = ids.map(|id| entry(&roomy, id)[2]);

    let mut message = super::queue::crowding_desktops(8);
    let desktops = message.desktops.as_mut().unwrap();
    desktops[0] = desktop(ids[1], &ids, &of.tree);
    message.profiles = Some(vec![profile()]);
    let narrowest = ("narrowest_tile_cols".to_owned(), "57".to_owned());
    message.settings.get_or_insert_default().extend([narrowest]);
    let sessions = message.sessions.as_mut().unwrap();
    sessions.extend(ids.iter().map(|id| of.session(id)));
    let mut world = World::start(Daemon::scripted().state(message));
    world.gesture(|app| app.click(Target::key("queue.desktop.1")));
    world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
    world.resize((1560.0, 900.0));

    let [first, current, last] = ids.map(|id| entry(&world, id)[2]);
    assert!(
        (first - last).abs() < 1.0 && first < full[0],
        "the two others are cut to the same width: {first} and {last}"
    );
    assert!(current > first, "the current one keeps more: {current}");
    let [sx, _, sw, _] = rect(&world, "shelf.slot");
    let [x, _, w, _] = entry(&world, ids[2]);
    assert!(
        x + w <= sx + sw + 0.5,
        "the entries end at {} in a slot that ends at {}",
        x + w,
        sx + sw
    );
}
