use attn_app::Action;
use attn_protocol::{
    CalloutAnchorsRequestMessage, CalloutShowRequestMessage, CalloutStep, InitialStateMessage,
};
use attn_testkit::{Target, World};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::queue::{one_desktop, plain, queue};
use super::sidebar::{chord, desktop, pane, session, state};

const CARD: &str = "callout";
const RING: &str = "callout.ring";
const ON_SCREEN: [&str; 7] = [
    "sidebar",
    "home",
    "commands",
    "garden",
    "asks",
    "desktops",
    "settings-button",
];

fn start(state: InitialStateMessage) -> World {
    let daemon = super::palette::daemon(state, vec![]);
    daemon.on("callout_result", |_| ());
    World::start(daemon)
}

fn two() -> InitialStateMessage {
    one_desktop(vec![session("first", "idle"), session("second", "idle")])
}

fn two_desktops() -> InitialStateMessage {
    state(
        vec![session("first", "idle"), session("second", "idle")],
        vec![
            desktop("d1", "a", "One", vec![pane("d1", "p1", "first")]),
            desktop("d2", "b", "Two", vec![pane("d2", "p2", "second")]),
        ],
    )
}

fn step(anchor: &str, text: &str) -> CalloutStep {
    CalloutStep {
        anchor: anchor.into(),
        text: text.into(),
        ..CalloutStep::default()
    }
}

fn agent(session: &str, text: &str) -> CalloutStep {
    CalloutStep {
        session_id: Some(session.into()),
        label: Some(format!("Agent {session}")),
        ..step("agent", text)
    }
}

fn desktop_step(id: &str, text: &str) -> CalloutStep {
    CalloutStep {
        desktop_id: Some(id.into()),
        label: Some(format!("Desktop {id}")),
        ..step("desktop", text)
    }
}

fn request(id: &str, profile: &str, steps: Vec<CalloutStep>) -> CalloutShowRequestMessage {
    CalloutShowRequestMessage {
        request_id: id.into(),
        profile_id: profile.into(),
        from: "Fern".into(),
        steps,
        ..CalloutShowRequestMessage::new()
    }
}

fn last_result(world: &World) -> Value {
    world
        .daemon
        .sent_of("callout_result")
        .pop()
        .expect("the window answered")
}

fn show(world: &mut World, id: &str, steps: Vec<CalloutStep>) -> Value {
    world.daemon.emit(request(id, "work", steps));
    world.settle();
    last_result(world)
}

fn anchors(world: &mut World, id: &str, profile: &str) -> Value {
    world.daemon.emit(CalloutAnchorsRequestMessage {
        request_id: id.into(),
        profile_id: profile.into(),
        ..CalloutAnchorsRequestMessage::new()
    });
    world.settle();
    last_result(world)
}

fn answered(id: &str, on_screen: &[&str]) -> Value {
    json!({"cmd": "callout_result", "request_id": id, "on_screen": on_screen})
}

fn refused(id: &str, on_screen: &[&str], missing: &[&str]) -> Value {
    json!({"cmd": "callout_result", "request_id": id, "on_screen": on_screen, "missing": missing})
}

/// What the card says, or nothing when no card shows.
fn card(world: &World) -> Option<String> {
    let screen = world.screen();
    screen.find(Target::key(CARD))?;
    screen
        .find(Target::key("callout.text"))
        .and_then(|n| n.name.or(n.value))
}

/// Where the ring is drawn; it is decoration, so it is not in the accessibility tree.
fn ring(world: &World) -> Option<[f32; 4]> {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| !e.exiting && e.key.as_deref() == Some(RING))
        .map(|e| e.rect)
}

fn has(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn next_name(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("callout.next"))
        .and_then(|n| n.name)
}

fn key(world: &mut World, key: Key) {
    world.gesture(|app| app.key(plain(key)));
}

fn click(world: &mut World, key: &str) {
    world.gesture(|app| app.click(Target::key(key)));
}

/// A press on the window's empty floor: home's background, away from every control.
fn press_elsewhere(world: &mut World) {
    let window = world.screen().bounds(Target::key("window")).unwrap();
    world.gesture(|app| app.click_at((window[2] - 40.0, window[3] - 40.0)));
}

fn shows(world: &World, cmd: &str, field: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|m| m[field].as_str().unwrap_or_default().to_owned())
        .collect()
}

#[test]
fn the_window_answers_with_the_anchors_it_shows_and_draws_the_card_below_a_title_bar_target() {
    let mut world = start(two());

    assert_eq!(
        anchors(&mut world, "list", "work"),
        answered("list", &ON_SCREEN)
    );
    assert_eq!(
        show(&mut world, "c1", vec![step("garden", "Work lives here")]),
        answered("c1", &ON_SCREEN)
    );

    assert_eq!(card(&world).as_deref(), Some("Work lives here"));
    let card = world.screen().get(Target::key(CARD));
    assert_eq!(card.role, "dialog");
    assert_eq!(card.name.as_deref(), Some("Callout from Fern"));
    assert!(card.is("focused"), "the card takes the keyboard");
    let screen = world.screen();
    let target = screen.bounds(Target::key("titlebar.garden")).unwrap();
    let ring = ring(&world).unwrap();
    assert_eq!(
        ring,
        [
            target[0] - 3.0,
            target[1] - 3.0,
            target[2] + 6.0,
            target[3] + 6.0
        ]
    );
    let drawn = screen.bounds(Target::key(CARD)).unwrap();
    assert!(
        drawn[1] >= target[1] + target[3],
        "the card sits below the title bar"
    );
}

#[test]
fn unmodified_enter_and_right_step_through_and_escape_or_a_press_elsewhere_end_it() {
    let mut world = start(two());
    show(
        &mut world,
        "tour",
        vec![
            step("sidebar", "First"),
            step("garden", "Second"),
            step("home", "Last"),
        ],
    );
    assert_eq!(next_name(&world).as_deref(), Some("↵ Next"));

    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Enter,
            mods: Mods {
                shift: true,
                ..Mods::default()
            },
        })
    });
    assert_eq!(card(&world).as_deref(), Some("First"));
    key(&mut world, Key::Enter);
    assert_eq!(card(&world).as_deref(), Some("Second"));
    key(&mut world, Key::Right);
    assert_eq!(card(&world).as_deref(), Some("Last"));
    assert_eq!(next_name(&world).as_deref(), Some("↵ Done"));
    key(&mut world, Key::Escape);
    assert_eq!(card(&world), None);
    assert!(!ring(&world).is_some());
    assert_eq!(world.screen().focused().and_then(|n| n.key), None);

    show(&mut world, "again", vec![step("sidebar", "Again")]);
    press_elsewhere(&mut world);
    assert_eq!(
        card(&world),
        None,
        "a press elsewhere ends a one-step callout"
    );

    show(&mut world, "finish", vec![step("sidebar", "Finish")]);
    key(&mut world, Key::Right);
    assert_eq!(card(&world), None);

    show(&mut world, "closed", vec![step("sidebar", "Closed")]);
    click(&mut world, "callout.end");
    assert_eq!(card(&world), None);
}

#[test]
fn a_tour_waits_under_another_layer_and_gives_keys_back_after_a_press_elsewhere() {
    let mut world = start(two());
    show(
        &mut world,
        "tour",
        vec![step("commands", "First"), step("garden", "Second")],
    );

    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    assert_eq!(card(&world), None);
    key(&mut world, Key::Escape);
    assert_eq!(card(&world).as_deref(), Some("First"));
    assert!(world.screen().get(Target::key(CARD)).is("focused"));

    press_elsewhere(&mut world);
    assert_eq!(card(&world).as_deref(), Some("First"));
    key(&mut world, Key::Enter);
    key(&mut world, Key::Escape);
    assert_eq!(
        card(&world).as_deref(),
        Some("First"),
        "keys went to the window"
    );

    click(&mut world, "callout.text");
    key(&mut world, Key::Enter);
    assert_eq!(card(&world).as_deref(), Some("Second"));

    show(
        &mut world,
        "next",
        vec![
            step("commands", "One"),
            step("garden", "Two"),
            step("home", "Three"),
        ],
    );
    press_elsewhere(&mut world);
    click(&mut world, "callout.next");
    assert_eq!(card(&world).as_deref(), Some("Two"));
    key(&mut world, Key::Enter);
    assert_eq!(
        card(&world).as_deref(),
        Some("Three"),
        "the Next button gives the card the keys back"
    );

    show(&mut world, "single", vec![step("sidebar", "Only")]);
    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    key(&mut world, Key::Escape);
    assert_eq!(card(&world), None, "a one-step callout gives way for good");
}

#[test]
fn a_request_for_another_profile_is_refused_without_naming_anchors_or_replacing_the_card() {
    let mut world = start(two());
    let stale = vec![
        step("garden", "Private text"),
        agent("stranger", "Private agent"),
    ];

    world.daemon.emit(request("stale", "other", stale.clone()));
    world.settle();
    assert_eq!(
        last_result(&world),
        refused("stale", &[], &["garden", "agent"])
    );
    assert_eq!(card(&world), None);

    show(&mut world, "keep", vec![step("home", "Keep me")]);
    world.daemon.emit(request("stale-again", "other", stale));
    world.settle();
    assert_eq!(
        last_result(&world),
        refused("stale-again", &[], &["garden", "agent"])
    );
    assert_eq!(
        anchors(&mut world, "stale-anchors", "other"),
        answered("stale-anchors", &[])
    );
    assert_eq!(card(&world).as_deref(), Some("Keep me"));
}

#[test]
fn a_missing_fixed_anchor_refuses_the_whole_callout_and_keeps_the_one_showing() {
    let mut world = start(two());
    show(&mut world, "keep", vec![step("garden", "Keep me")]);

    assert_eq!(
        show(
            &mut world,
            "refused",
            vec![step("garden", "Not accepted"), step("queue", "Missing")]
        ),
        refused("refused", &ON_SCREEN, &["queue"])
    );
    assert_eq!(card(&world).as_deref(), Some("Keep me"));

    show(&mut world, "new", vec![step("commands", "Replacement")]);
    assert_eq!(card(&world).as_deref(), Some("Replacement"));
    assert_eq!(world.screen().all(Target::key(CARD)).len(), 1);
}

#[test]
fn an_agent_step_in_queue_flow_opens_the_agent_list_and_enter_shows_the_agent() {
    let mut world = start(queue(two()));
    assert!(!has(&world, "queue-settled.second"));

    show(&mut world, "agent", vec![agent("second", "Needs you")]);
    assert!(has(&world, "queue-settled.second"), "the agent list opened");
    let row = world
        .screen()
        .bounds(Target::key("queue-settled.second"))
        .unwrap();
    let ring = ring(&world).unwrap();
    assert_eq!(ring[1], row[1] - 3.0);
    assert_eq!(next_name(&world).as_deref(), Some("↵ Go there"));

    key(&mut world, Key::Enter);
    assert_eq!(
        shows(&world, "desktop_show_session", "session_id"),
        ["second"]
    );
    assert_eq!(card(&world), None);
}

#[test]
fn a_tour_mixes_agent_and_desktop_steps_and_goes_there_only_from_the_last() {
    let mut world = start(two_desktops());
    show(
        &mut world,
        "mixed",
        vec![
            step("sidebar", "One"),
            agent("second", "Two"),
            step("garden", "Three"),
            desktop_step("d1", "Four"),
            agent("second", "Five"),
        ],
    );

    key(&mut world, Key::Right);
    assert_eq!(card(&world).as_deref(), Some("Two"));
    assert!(ring(&world).is_some());
    assert_eq!(next_name(&world).as_deref(), Some("↵ Next"));
    key(&mut world, Key::Enter);
    assert_eq!(card(&world).as_deref(), Some("Three"));
    key(&mut world, Key::Enter);
    assert_eq!(card(&world).as_deref(), Some("Four"));
    key(&mut world, Key::Right);
    assert_eq!(card(&world).as_deref(), Some("Five"));
    assert!(world.daemon.sent_of("desktop_show_session").is_empty());
    assert!(world.daemon.sent_of("desktop_set_current").is_empty());
    assert_eq!(next_name(&world).as_deref(), Some("↵ Go there"));

    key(&mut world, Key::Right);
    assert_eq!(
        shows(&world, "desktop_show_session", "session_id"),
        ["second"]
    );
    assert_eq!(card(&world), None);
}

#[test]
fn an_agent_off_screen_shows_the_card_without_a_ring_and_enter_still_goes_there() {
    let mut world = start(queue(two()));
    world.gesture(|app| app.key(chord(Action::SessionToggleSidebar)));

    show(&mut world, "hidden", vec![agent("second", "Needs you")]);
    assert_eq!(card(&world).as_deref(), Some("Needs you"));
    let note = world
        .screen()
        .find(Target::key("callout.unanchored"))
        .unwrap();
    assert_eq!(
        note.name.or(note.value).as_deref(),
        Some("Agent second isn't on screen")
    );
    assert!(!ring(&world).is_some());

    key(&mut world, Key::Enter);
    assert_eq!(
        shows(&world, "desktop_show_session", "session_id"),
        ["second"]
    );
}

#[test]
fn a_shortcut_that_opens_the_palette_ends_a_one_step_callout() {
    let mut world = start(two());
    show(&mut world, "hello", vec![step("sidebar", "Hello")]);

    world.gesture(|app| app.key(chord(Action::UiCommandPalette)));
    assert_eq!(card(&world), None);
    assert!(has(&world, "palette.field"));
}

#[test]
fn a_request_while_settings_are_open_is_refused_for_every_step() {
    let mut world = start(two_desktops());
    world.gesture(|app| app.key(chord(Action::UiOpenSettings)));

    assert_eq!(
        show(
            &mut world,
            "blocked",
            vec![
                step("sidebar", "Blocked"),
                agent("second", "Blocked agent"),
                desktop_step("d2", "Blocked desktop"),
            ]
        ),
        refused("blocked", &[], &["sidebar", "agent", "desktop"])
    );
    world.gesture(|app| app.key(plain(Key::Escape)));
    assert_eq!(card(&world), None);
}

#[test]
fn a_callout_shows_beside_an_open_drawer() {
    let mut world = start(two());
    world.gesture(|app| app.key(chord(Action::DockAttention)));

    assert_eq!(
        show(
            &mut world,
            "beside",
            vec![step("sidebar", "Alongside a drawer")]
        ),
        answered("beside", &ON_SCREEN)
    );
    assert_eq!(card(&world).as_deref(), Some("Alongside a drawer"));
}

#[test]
fn the_ring_follows_its_target_when_the_window_grows() {
    let mut world = start(two());
    show(&mut world, "grow", vec![step("sidebar", "Growing target")]);
    let before = ring(&world).unwrap();

    world.resize((1200.0, 1000.0));
    world.settle();
    let sidebar = world.screen().bounds(Target::key("sidebar")).unwrap();
    let after = ring(&world).unwrap();
    assert!(after[3] > before[3]);
    assert_eq!(after[3], sidebar[3] + 6.0);
}

#[test]
fn the_anchors_follow_the_sidebar_shape() {
    for (queued, collapsed, expected) in [
        (false, false, &ON_SCREEN[..]),
        (false, true, &ON_SCREEN[..]),
        (
            true,
            false,
            &[
                "sidebar",
                "home",
                "commands",
                "garden",
                "asks",
                "desktops",
                "queue",
                "settings-button",
            ][..],
        ),
        (
            true,
            true,
            &[
                "sidebar",
                "commands",
                "garden",
                "asks",
                "desktops",
                "queue",
                "settings-button",
            ][..],
        ),
    ] {
        let mut world = start(if queued { queue(two()) } else { two() });
        if collapsed {
            world.gesture(|app| app.key(chord(Action::SessionToggleSidebar)));
        }
        assert_eq!(
            anchors(&mut world, "shape", "work"),
            answered("shape", expected),
            "queue {queued}, collapsed {collapsed}"
        );
    }
}

#[test]
fn settings_on_hidden_home_is_missing_and_a_desktop_step_goes_to_the_desktop() {
    let mut world = start(two_desktops());
    world.open_desktop();

    let result = show(
        &mut world,
        "hidden",
        vec![step("settings-button", "Hidden")],
    );
    assert_eq!(result["missing"], json!(["settings-button"]));
    assert_eq!(card(&world), None);

    let opened = world.daemon.sent_of("desktop_set_current").len();
    show(&mut world, "go", vec![desktop_step("d2", "Go here")]);
    assert!(ring(&world).is_some());
    key(&mut world, Key::Enter);
    assert_eq!(
        shows(&world, "desktop_set_current", "desktop_id")[opened..],
        ["d2"]
    );
    assert_eq!(card(&world), None);
}

#[test]
fn the_desktops_anchor_points_at_the_queue_bar_when_every_desktop_is_in_its_extras_chip() {
    let mut world = start(queue(two_desktops()));
    world.gesture(|app| app.key(chord(Action::SessionToggleSidebar)));
    assert!(has(&world, "queue-bar.desktop-extras"));
    assert!(!has(&world, "queue-bar.desktop.1"));

    assert_eq!(
        show(
            &mut world,
            "all",
            vec![step("desktops", "All your desktops")]
        ),
        answered(
            "all",
            &[
                "sidebar",
                "commands",
                "garden",
                "asks",
                "desktops",
                "queue",
                "settings-button"
            ]
        )
    );
    let strip = world
        .screen()
        .bounds(Target::key("queue-bar.desktops"))
        .unwrap();
    let card = world.screen().bounds(Target::key(CARD)).unwrap();
    assert!(
        card[1] >= strip[1] + strip[3],
        "the card sits below the queue bar"
    );
}
