use std::collections::BTreeMap;

use attn_app::{Action, BuiltInView};
use attn_protocol::{Desktop, InitialStateMessage, PR, Session};
use attn_testkit::tree::{below, pane, row, tile};
use attn_testkit::{Target, World};
use serde_json::Value;

use super::palette::chord;
use super::queue::{ago, changes, plain, sent_ids, turn};
use super::sidebar::session;

const DESKTOP: &str = "d1";

fn tile_id(view: BuiltInView) -> String {
    format!("tile-{}", view.name())
}

fn view_tile(view: BuiltInView) -> Value {
    let mut tile = tile(&tile_id(view), "markdown");
    tile["tile_params"] = view.params().into();
    tile
}

/// A terminal above attn, beside Sessions above Pull requests: two columns, which an ordinary
/// desktop's screen shows together.
fn beside_a_terminal() -> Value {
    row([
        below(pane("p0"), view_tile(BuiltInView::Attn)),
        below(
            view_tile(BuiltInView::Sessions),
            view_tile(BuiltInView::PullRequests),
        ),
    ])
}

fn desktop(tree: Value) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "a".into(),
        tree_json: tree.to_string(),
        active_pane_id: "p0".into(),
        panes: vec![super::sidebar::pane(DESKTOP, "p0", "busy")],
        revision: 1,
        ..Desktop::default()
    }
}

fn state(tree: Value, sessions: Vec<Session>, settings: &[(&str, &str)]) -> InitialStateMessage {
    let mut state = super::sidebar::state(sessions, vec![desktop(tree)]);
    state.settings = Some(
        settings
            .iter()
            .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
            .collect::<BTreeMap<_, _>>(),
    );
    state.prs = Some(vec![PR {
        id: "p1".into(),
        number: 1,
        role: "reviewer".into(),
        title: "review this".into(),
        repo: "victorarias/attn".into(),
        author: "someone".into(),
        reason: "review_requested".into(),
        url: "https://github.com/victorarias/attn/pull/1".into(),
        host: "github.com".into(),
        state: "waiting".into(),
        ..PR::default()
    }]);
    state
}

const VIEWS_AND_QUEUE: [(&str, &str); 2] =
    [("views_enabled", "true"), ("queue_mode_enabled", "true")];

fn start(mut state: InitialStateMessage) -> World {
    let settings = state.settings.replace(BTreeMap::new());
    let daemon = super::palette::daemon(state, vec![]);
    daemon.on("approve_pr", |r| {
        attn_testkit::message(attn_protocol::PRActionResultMessage {
            action: "approve".into(),
            id: r["id"].as_str().unwrap().into(),
            success: true,
            ..attn_protocol::PRActionResultMessage::new()
        })
    });
    let mut world = World::start_sized_with_prefs(daemon, (1600.0, 900.0), seen_the_tour());
    world.open_desktop();
    world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
        settings,
        ..attn_protocol::SettingsUpdatedMessage::new()
    });
    world.settle();
    world
}

/// Prefs in which this release's tour was seen, so it does not cover the desktop, and its
/// banner still shows.
fn seen_the_tour() -> rune::prefs::Prefs {
    let mut prefs = rune::prefs::Prefs::memory();
    prefs
        .set(
            attn_app::whats_new::SEEN_PREF,
            &attn_app::whats_new::RELEASE.to_owned(),
        )
        .expect("memory prefs keep every value");
    prefs
}

fn in_tile(world: &World, view: BuiltInView, key: &str) -> bool {
    world
        .screen()
        .descendants(Target::key(format!("tile.{}", tile_id(view))))
        .iter()
        .any(|n| n.key.as_deref() == Some(key))
}

fn checked(world: &World, key: &str) -> bool {
    world
        .screen()
        .get(Target::key(key))
        .states
        .iter()
        .any(|s| s == "checked")
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn tab_to(world: &mut World, key: &str) {
    for _ in 0..200 {
        if focused(world).as_deref() == Some(key) {
            return;
        }
        world.gesture(|app| app.key(plain(rune::Key::Tab)));
    }
    panic!(
        "Tab never reached {key}; focus ended on {:?}",
        focused(world)
    );
}

#[test]
fn each_view_draws_in_its_tile_beside_a_terminal_with_what_home_draws_of_it() {
    let mut world = start(state(
        beside_a_terminal(),
        vec![session("busy", "working")],
        &VIEWS_AND_QUEUE,
    ));
    world.daemon.emit(attn_protocol::RateLimitedMessage {
        rate_limit_resource: "core".into(),
        rate_limit_reset_at: ago(-5),
        ..attn_protocol::RateLimitedMessage::new()
    });
    world.settle();

    for (view, title) in [
        (BuiltInView::Attn, "attn"),
        (BuiltInView::Sessions, "Sessions"),
        (BuiltInView::PullRequests, "Pull requests"),
    ] {
        let frame = world
            .screen()
            .get(Target::key(format!("tile.{}", tile_id(view))));
        assert_eq!(
            frame.name.as_deref(),
            Some(title),
            "the {view:?} tile's name"
        );
    }
    for (view, key) in [
        (BuiltInView::Attn, "home.logo"),
        (BuiltInView::Attn, "home.settings"),
        (BuiltInView::Attn, "home.intro-banner"),
        (BuiltInView::Sessions, "home.all-settled"),
        (BuiltInView::Sessions, "home.follow"),
        (BuiltInView::Sessions, "home.session.busy"),
        (BuiltInView::PullRequests, "home.rate-limit"),
        (BuiltInView::PullRequests, "home.pr.p1.approve"),
    ] {
        assert!(
            in_tile(&world, view, key),
            "{key} draws in the {view:?} tile"
        );
    }
    assert!(
        world.screen().find(Target::key("home")).is_none(),
        "the desktop shows, not Home"
    );
}

#[test]
fn the_controls_of_each_view_work_in_its_tile() {
    let mut world = start(state(
        beside_a_terminal(),
        vec![session("busy", "working")],
        &VIEWS_AND_QUEUE,
    ));
    assert!(
        world.runtime_is_at_rest(),
        "the desktop with the three views draws nothing while nothing changes"
    );

    world.gesture(|app| app.click(Target::key("home.pr.p1.approve")));
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);
    assert_eq!(
        world
            .screen()
            .get(Target::key("home.pr.p1.approve"))
            .name
            .as_deref(),
        Some("Approved")
    );

    world.gesture(|app| app.click(Target::key("home.follow")));
    assert!(checked(&world, "home.follow"));

    world.gesture(|app| app.click(Target::key("home.intro-banner.dismiss")));
    assert!(!in_tile(&world, BuiltInView::Attn, "home.intro-banner"));

    world.gesture(|app| app.click(Target::key("home.settings")));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Settings"))
            .is_some()
    );
}

#[test]
fn tab_reaches_a_views_controls_in_its_tile_and_its_keys_press_them() {
    let mut world = start(state(
        beside_a_terminal(),
        vec![session("busy", "working")],
        &VIEWS_AND_QUEUE,
    ));

    tab_to(&mut world, "home.follow");
    world.gesture(|app| app.key(plain(rune::Key::Text(" ".into()))));
    assert!(checked(&world, "home.follow"));

    tab_to(&mut world, "home.pr.p1.approve");
    world.gesture(|app| app.key(plain(rune::Key::Text(" ".into()))));
    assert_eq!(world.daemon.sent_of("approve_pr").len(), 1);
}

#[test]
fn following_from_a_desktop_that_shows_sessions_opens_the_next_turn() {
    let mut state = state(
        beside_a_terminal(),
        vec![session("busy", "working"), session("other", "working")],
        &VIEWS_AND_QUEUE,
    );
    state.desktops.as_mut().unwrap().push(Desktop {
        id: "d2".into(),
        order_key: "b".into(),
        shortcut_slot: Some(2),
        tree_json: pane("p1").to_string(),
        active_pane_id: "p1".into(),
        panes: vec![super::sidebar::pane("d2", "p1", "other")],
        ..desktop(pane("p1"))
    });
    let mut world = start(state);

    changes(&mut world, turn("other", 0));
    assert_eq!(
        sent_ids(&world, "desktop_show_session"),
        Vec::<String>::new(),
        "nothing follows before the user asks"
    );

    changes(&mut world, session("other", "working"));
    world.gesture(|app| app.click(Target::key("home.follow")));
    changes(&mut world, turn("other", 0));
    assert_eq!(sent_ids(&world, "desktop_show_session"), ["other"]);
}

#[test]
fn a_view_tile_stays_in_its_desktop() {
    let mut state = state(
        beside_a_terminal(),
        vec![session("busy", "working")],
        &VIEWS_AND_QUEUE,
    );
    state.desktops.as_mut().unwrap()[0].active_pane_id = tile_id(BuiltInView::Sessions);
    let mut world = start(state);

    for view in BuiltInView::ALL {
        assert!(
            world
                .screen()
                .find(Target::key(format!("tile.{}.popout", tile_id(view))))
                .is_none(),
            "the {view:?} tile offers no pop-out"
        );
    }
    let ev = chord(Action::TilePopOut);
    world.gesture(|app| app.key(ev));
    assert_eq!(world.daemon.sent_of("desktop_pop_out_leaf").len(), 0);
}

fn run_palette_command(world: &mut World, text: &str) {
    let ev = chord(Action::UiCommandPalette);
    world.gesture(|app| app.key(ev));
    world.gesture(|app| app.type_text(text));
    world.gesture(|app| app.key(plain(rune::Key::Enter)));
}

#[test]
fn the_palette_puts_a_view_on_the_current_desktop_and_goes_to_it_once_there() {
    let mut world = start(state(
        row([pane("p0")]),
        vec![session("busy", "working")],
        &VIEWS_AND_QUEUE,
    ));
    world.daemon.on("desktop_dock_tile", |r| {
        serde_json::json!({"event": "profile_action_result", "action": r["cmd"], "request_id": r["request_id"], "success": true})
    });

    run_palette_command(&mut world, "Show Pull requests in a tile");
    let docks = world.daemon.sent_of("desktop_dock_tile");
    assert_eq!(docks.len(), 1);
    assert_eq!(docks[0]["desktop_id"], DESKTOP);
    assert_eq!(docks[0]["tile_kind"], "markdown");
    assert_eq!(
        BuiltInView::named_by(docks[0]["tile_params"].as_str().unwrap()),
        Some(BuiltInView::PullRequests)
    );

    let profile = super::sidebar::state(vec![], vec![desktop(row([pane("p0")]))])
        .profiles
        .unwrap()
        .remove(0);
    world
        .daemon
        .emit(attn_protocol::ProfileArrangementChangedMessage {
            profile,
            desktops: vec![Desktop {
                revision: 2,
                ..desktop(row([pane("p0"), view_tile(BuiltInView::PullRequests)]))
            }],
            ..attn_protocol::ProfileArrangementChangedMessage::new()
        });
    world.settle();
    run_palette_command(&mut world, "Show Pull requests in a tile");
    assert_eq!(
        world.daemon.sent_of("desktop_dock_tile").len(),
        1,
        "a view the desktop shows is gone to, not docked again"
    );
}

#[test]
fn the_palette_offers_no_view_while_views_are_off() {
    let mut world = start(state(
        row([pane("p0")]),
        vec![session("busy", "working")],
        &[],
    ));
    let ev = chord(Action::UiCommandPalette);
    world.gesture(|app| app.key(ev));
    world.gesture(|app| app.type_text("in a tile"));

    let titles = super::palette::titles(&world);
    assert!(
        !titles.iter().any(|t| t.starts_with("Show Sessions")),
        "views off, yet the palette offers {titles:?}"
    );
}
