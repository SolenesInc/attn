use attn_app::Action;
use attn_protocol::{
    InitialStateMessage, OutpostInfo, OutpostsUpdatedMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::{Target, World};
use rune::{Key, KeyEvent, Mods};
use serde_json::json;

use super::queue::{changes, queue, sent_ids, snoozed, turn};
use super::sidebar::{chord, desktop, pane, state};

const OUTPOST: &str = "studio-id";

fn studio(state: &str, reach: &str) -> OutpostInfo {
    OutpostInfo {
        id: OUTPOST.into(),
        name: "studio".into(),
        destination: "studio:2200".into(),
        state: state.into(),
        reach: reach.into(),
        state_since: "2026-10-27T10:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn reachable() -> OutpostInfo {
    studio("connected", "reachable")
}

fn lost() -> OutpostInfo {
    studio("lost", "unreachable")
}

fn on_studio(session: Session) -> Session {
    Session {
        host: Some(OUTPOST.into()),
        ..session
    }
}

/// The agent `remote` on studio owes the older turn and `local` the newer, each alone on its
/// desktop, in queue flow. Showing an agent makes its desktop current, as the daemon does.
fn start_with(outpost: OutpostInfo, remote: Session) -> World {
    let local = turn("local", 10);
    let with_terminal = |p: attn_protocol::DesktopPane| attn_protocol::DesktopPane {
        runtime_id: format!("rt-{}", p.session_id),
        ..p
    };
    let desktops = vec![
        desktop(
            "d1",
            "a",
            "",
            vec![with_terminal(pane("d1", "p-remote", "remote"))],
        ),
        desktop(
            "d2",
            "b",
            "",
            vec![with_terminal(pane("d2", "p-local", "local"))],
        ),
    ];
    let arranged = desktops.clone();
    let message: InitialStateMessage = queue(state(vec![remote, local], desktops));
    let mut message = message;
    message
        .settings
        .get_or_insert_default()
        .insert("outposts_enabled".into(), "true".into());
    message.outposts = Some(vec![outpost]);
    let daemon = super::palette::daemon(message, vec![]);
    daemon.on("desktop_show_session", move |r| {
        let current = if r["session_id"] == "remote" {
            "d1"
        } else {
            "d2"
        };
        attn_testkit::message(ProfileArrangementChangedMessage {
            profile: Profile {
                id: "work".into(),
                name: "Work".into(),
                current_desktop_id: current.into(),
                ..Profile::default()
            },
            desktops: arranged.clone(),
            ..ProfileArrangementChangedMessage::new()
        })
    });
    World::start(daemon)
}

fn start(outpost: OutpostInfo) -> World {
    start_with(outpost, on_studio(turn("remote", 60)))
}

fn outposts(world: &mut World, outposts: Vec<OutpostInfo>) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

fn name(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn offline(world: &World, row: &str) -> bool {
    name(world, &format!("{row}.state")).as_deref() == Some("offline")
}

fn press(world: &mut World, action: Action) {
    world.gesture(|app| app.key(chord(action)));
}

fn shown(world: &World) -> Vec<String> {
    sent_ids(world, "desktop_show_session")
}

/// The queue's turns, top to bottom.
fn turns(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("sidebar.queue"))
        .into_iter()
        .filter_map(|n| n.key?.strip_prefix("queue-turn.").map(str::to_owned))
        .collect()
}

fn hints(world: &World) -> Vec<String> {
    (0..)
        .map_while(|i| name(world, &format!("palette.hint.{i}")))
        .collect()
}

fn palette_offers(world: &World, verb: &str) -> bool {
    hints(world).iter().any(|h| h.ends_with(verb))
}

fn search_agents(world: &mut World, query: &str) {
    for _ in 0..12 {
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Backspace,
                mods: Mods::default(),
            })
        });
    }
    world.gesture(|app| app.type_text(query));
}

#[test]
fn a_stalled_or_connecting_agent_says_so_in_its_tile_and_stays_counted_and_unmarked() {
    for (state, reach, notice) in [
        ("stalled", "reachable", "studio not responding"),
        (
            "connecting",
            "connecting",
            "Connecting to studio · typing is not sent",
        ),
    ] {
        let mut world = start(reachable());
        world.gesture(|app| app.click(Target::key("queue-turn.remote")));
        outposts(&mut world, vec![studio(state, reach)]);

        assert_eq!(
            name(&world, "tile.rt-remote.reach").as_deref(),
            Some(notice)
        );
        assert!(!offline(&world, "tile.p-remote"), "{state}");
        assert!(!offline(&world, "queue-turn.remote"), "{state}");
        assert_eq!(
            name(&world, "queue.waiting-head").as_deref(),
            Some("2 waiting"),
            "{state}"
        );
        outposts(&mut world, vec![reachable()]);
        assert!(!has(&world, "tile.rt-remote.reach"));
    }
}

#[test]
fn home_keeps_an_unreachable_turn_in_its_band_and_counts_only_reachable_turns() {
    let mut world = start(reachable());
    assert!(!offline(&world, "home.session.remote"));

    outposts(&mut world, vec![lost()]);
    assert!(offline(&world, "home.session.remote"));
    assert_eq!(
        name(&world, "home.group.turns.apart").as_deref(),
        Some("1 unreachable")
    );
    assert_eq!(name(&world, "home.group.turns.count").as_deref(), Some("1"));

    changes(
        &mut world,
        Session {
            turn_owed: Some(false),
            ..turn("local", 10)
        },
    );
    assert!(!has(&world, "home.all-settled"), "a turn is still owed");
    assert!(offline(&world, "home.session.remote"));
    assert_eq!(name(&world, "home.group.turns.count").as_deref(), Some("0"));

    outposts(&mut world, vec![reachable()]);
    assert!(!offline(&world, "home.session.remote"));
    assert!(!has(&world, "home.group.turns.apart"));
    assert_eq!(name(&world, "home.group.turns.count").as_deref(), Some("1"));
}

#[test]
fn the_queue_head_and_the_bar_count_unreachable_turns_apart_and_list_them_last() {
    let mut world = start(reachable());
    let owed = |world: &mut World| {
        let head = name(world, "queue.waiting-head").unwrap();
        world.gesture(|app| app.click(Target::role("button").name("Collapse sidebar")));
        let pill = name(world, "queue-bar.pill").unwrap();
        world.gesture(|app| app.click(Target::role("button").name("Show sidebar")));
        (head, pill)
    };
    assert_eq!(turns(&world), ["remote", "local"]);

    outposts(&mut world, vec![lost()]);
    let (head, pill) = owed(&mut world);
    assert_eq!(head, "1 waiting · 1 unreachable");
    assert!(pill.starts_with("1 waiting · 1 unreachable · "), "{pill}");
    assert_eq!(turns(&world), ["local", "remote"]);

    changes(
        &mut world,
        Session {
            turn_owed: Some(false),
            ..turn("local", 10)
        },
    );
    let (head, pill) = owed(&mut world);
    assert_eq!(head, "1 unreachable");
    assert!(pill.starts_with("1 unreachable · "), "{pill}");
    assert_eq!(turns(&world), ["remote"]);

    outposts(&mut world, vec![reachable()]);
    let (head, pill) = owed(&mut world);
    assert_eq!(head, "1 waiting");
    assert!(pill.starts_with("1 waiting · "), "{pill}");
}

#[test]
fn the_palette_offers_no_turn_keys_for_an_offline_agent_until_it_is_back() {
    let mut world = start(reachable());
    press(&mut world, Action::UiActionMenu);
    search_agents(&mut world, "remote");
    assert!(palette_offers(&world, "settle") && palette_offers(&world, "snooze"));

    outposts(&mut world, vec![lost()]);
    assert_eq!(
        name(&world, "palette.row.agent:remote.state").as_deref(),
        Some("unreachable")
    );
    assert!(!palette_offers(&world, "settle") && !palette_offers(&world, "snooze"));
    press(&mut world, Action::SessionSettle);
    press(&mut world, Action::SessionSnooze);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Snooze remote"))
            .is_none()
    );
    assert!(sent_ids(&world, "settle_turn").is_empty());
    assert!(sent_ids(&world, "snooze_turn").is_empty());

    search_agents(&mut world, "local");
    assert!(palette_offers(&world, "settle") && palette_offers(&world, "snooze"));

    search_agents(&mut world, "remote");
    outposts(&mut world, vec![reachable()]);
    assert!(palette_offers(&world, "settle") && palette_offers(&world, "snooze"));
    press(&mut world, Action::SessionSettle);
    assert_eq!(sent_ids(&world, "settle_turn"), ["remote"]);
}

#[test]
fn the_palette_leaves_the_snooze_choices_once_the_agents_outpost_goes_offline() {
    let mut world = start(reachable());
    press(&mut world, Action::UiActionMenu);
    search_agents(&mut world, "remote");
    press(&mut world, Action::SessionSnooze);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Snooze remote"))
            .is_some()
    );

    outposts(&mut world, vec![lost()]);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Snooze remote"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Agents"))
            .is_some()
    );
    assert!(sent_ids(&world, "snooze_turn").is_empty());
}

#[test]
fn a_turn_command_the_daemon_refuses_says_why_before_the_outpost_reads_offline() {
    for cmd in [
        "settle_turn",
        "snooze_turn",
        "wake_turn",
        "cancel_countdown",
    ] {
        let mut world = start(reachable());
        world.daemon.emit(json!({
            "event": "command_error",
            "cmd": cmd,
            "success": false,
            "error": "outpost \"studio\" is unreachable",
        }));
        world.settle();
        assert_eq!(
            name(&world, "notice.text").as_deref(),
            Some("outpost \"studio\" is unreachable"),
            "{cmd}"
        );
    }
}

#[test]
fn an_unreachable_turn_keeps_its_row_but_leaves_the_count_and_the_jump_key_until_it_is_back() {
    let mut world = start(lost());
    assert!(offline(&world, "queue-turn.remote"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("queue.waiting-head"))
            .name
            .as_deref(),
        Some("1 waiting · 1 unreachable")
    );
    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown(&world), ["local"]);

    outposts(&mut world, vec![reachable()]);
    assert!(!offline(&world, "queue-turn.remote"));
    assert_eq!(
        name(&world, "queue.waiting-head").as_deref(),
        Some("2 waiting")
    );
    press(&mut world, Action::SessionJumpToWaiting);
    assert_eq!(shown(&world), ["local", "remote"]);
}

#[test]
fn an_offline_turn_offers_no_settle_or_snooze_while_rename_and_priority_remain() {
    let mut world = start(reachable());
    world.gesture(|app| app.click(Target::key("queue-turn.remote")));
    outposts(&mut world, vec![lost()]);

    assert!(!has(&world, "queue-turn.remote.settle"));
    assert!(!has(&world, "queue-turn.remote.snooze"));
    press(&mut world, Action::SessionSettle);
    press(&mut world, Action::SessionSnooze);
    assert!(sent_ids(&world, "settle_turn").is_empty());
    assert!(sent_ids(&world, "snooze_turn").is_empty());

    world.gesture(|app| app.click(Target::key("queue-turn.remote.actions")));
    let menu = world
        .screen()
        .find(Target::role("menu").name("Actions for remote"))
        .is_some();
    assert!(menu);
    assert!(has(&world, "session.menu.rename"));
    assert!(has(&world, "session.menu.priority"));
}

#[test]
fn next_and_the_advance_after_a_closed_turn_skip_an_unreachable_turn() {
    let mut world = start(lost());
    world.gesture(|app| app.click(Target::key("queue-turn.local")));
    press(&mut world, Action::SessionNext);
    assert_eq!(
        shown(&world),
        ["local", "local"],
        "Next stays on the one reachable turn"
    );

    changes(
        &mut world,
        Session {
            turn_owed: Some(false),
            ..turn("local", 10)
        },
    );
    assert!(!shown(&world).contains(&"remote".to_owned()));
    assert!(offline(&world, "queue-turn.remote"));
}

#[test]
fn an_offline_agents_countdown_stands_still_and_cannot_be_kept_until_it_is_back() {
    let remote = Session {
        auto_settle_fires_at: Some("2999-01-01T00:00:00Z".into()),
        ..on_studio(turn("remote", 60))
    };
    let mut world = start_with(reachable(), remote);
    world.gesture(|app| app.click(Target::key("queue-turn.remote")));
    outposts(&mut world, vec![lost()]);

    let chip = world.screen().get(Target::key("tile.p-remote.settling"));
    assert!(chip.is("disabled"));
    assert_eq!(
        chip.name.as_deref(),
        Some("Settling paused while the host is unreachable")
    );
    press(&mut world, Action::SessionCancelCountdown);
    assert!(sent_ids(&world, "cancel_countdown").is_empty());

    outposts(&mut world, vec![reachable()]);
    assert!(
        !world
            .screen()
            .get(Target::key("tile.p-remote.settling"))
            .is("disabled")
    );
}

#[test]
fn an_offline_snoozed_agent_offers_no_unsnooze() {
    let mut world = start_with(lost(), on_studio(snoozed("remote", 600)));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));

    assert!(has(&world, "queue-snoozed.remote"));
    assert!(offline(&world, "queue-snoozed.remote"));
    assert!(!has(&world, "queue-snoozed.remote.unsnooze"));
}

#[test]
fn closing_the_agent_on_screen_opens_no_turn_on_an_unreachable_outpost() {
    let mut world = start(lost());
    world.gesture(|app| app.click(Target::key("queue-turn.local")));
    world.gesture(|app| app.click(Target::key("queue-turn.local.actions")));
    world.gesture(|app| app.click(Target::key("session.menu.close")));

    assert_eq!(world.daemon.sent_of("unregister").len(), 1);
    assert_eq!(
        shown(&world),
        ["local"],
        "nothing reachable is owed, so the window stays"
    );
}
