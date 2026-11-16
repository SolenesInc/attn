//! The Chief's slot at the top of the crew: its first setup, which opens on its own only while
//! nothing else needs the user, saves a harness, model and effort and starts the Chief; Start
//! and Ask to close; changing its route for the next session; and the menus and palettes that
//! reach it.

use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    CrewCloseResultMessage, CrewMember, CrewSetResultMessage, CrewStartResultMessage,
    CrewUpdatedMessage, DelegationPreferencesResultMessage, HarnessModelsResultMessage,
    InitialStateMessage, ProfileArrangementChangedMessage, Session, SessionsUpdatedMessage,
    SettingsUpdatedMessage,
};
use attn_testkit::{Answer, Daemon, Request, Target, World, message};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::queue::{crew, one_desktop};
use super::settings::Settings;
use super::settings::harness_catalogs::{catalog, harness, preferences_result, reported};
use super::sidebar::{desktop, pane, state};

const SLOT: &str = "chief-slot";
const SETUP: &str = "chief-setup";
const PRIMARY: &str = "chief-setup.primary";

fn chief(configured: bool) -> CrewMember {
    CrewMember {
        id: "chief-key".into(),
        name: "Captain".into(),
        chief: Some(true),
        profile_id: "work".into(),
        revision: 1,
        resolved_agent: "claude".into(),
        agent: configured.then(|| "claude".into()),
        model: configured.then(|| "opus".into()),
        effort: configured.then(|| "low".into()),
        ..CrewMember::default()
    }
}

fn bound(mut member: CrewMember, session: &str) -> CrewMember {
    member.binding_session = Some(session.into());
    member
}

fn settings(queue: bool) -> BTreeMap<String, String> {
    [
        ("claude_available", "true"),
        ("codex_available", "true"),
        ("plug_available", "true"),
        ("claude_cap_launch_instructions", "true"),
        ("codex_cap_launch_instructions", "true"),
        ("plug_cap_launch_instructions", "false"),
        ("new_session_agent", "claude"),
        ("queue_mode_enabled", if queue { "true" } else { "false" }),
        ("notebook.root.effective", "~/attn-notebook-work"),
    ]
    .into_iter()
    .map(|(k, v)| (k.to_owned(), v.to_owned()))
    .collect()
}

fn chief_day() -> Session {
    crew("chief-day", "chief-key", true)
}

fn initial(queue: bool, member: CrewMember, sessions: Vec<Session>) -> InitialStateMessage {
    let mut initial = if sessions.is_empty() {
        state(Vec::new(), Vec::new())
    } else {
        one_desktop(sessions)
    };
    initial.settings = Some(settings(queue));
    initial.crew = Some(vec![member]);
    initial
}

/// A daemon that lists Claude Code, Codex and a plugin harness with their test catalogs, and
/// keeps no palette history.
fn daemon(initial: InitialStateMessage) -> Daemon {
    let daemon = super::palette::daemon(initial, Vec::new());
    daemon.on("delegation_preferences_get", |_| {
        message(preferences_result(vec![
            harness("claude", "Claude"),
            harness("codex", "Codex"),
            harness("plug", "Plug"),
        ]))
    });
    daemon.on("harness_models", serve_models);
    daemon
}

fn serve_models(r: &Request) -> Value {
    let harness = r["harness"].as_str().unwrap_or_default().to_owned();
    message(reported(&harness, &Settings::default(), catalog(&harness)))
}

fn start(initial: InitialStateMessage) -> World {
    let mut world = World::start(daemon(initial));
    world.settle();
    world
}

fn click(world: &mut World, key: &str) {
    world
        .automate("press", json!({"target": {"key": key}}))
        .unwrap_or_else(|why| panic!("press {key}: {why}"));
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn dispatch(world: &mut World, action: Action) {
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, action.clone(), tx);
        })
    });
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn named(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn value(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.value)
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn checked(world: &World, group: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(format!("{SETUP}.{group}")))
        .into_iter()
        .filter(|n| n.role == "radio-button" && n.is("checked"))
        .filter_map(|n| n.name)
        .collect()
}

fn radios(world: &World, group: &str) -> Vec<String> {
    world
        .screen()
        .descendants(Target::key(format!("{SETUP}.{group}")))
        .into_iter()
        .filter(|n| n.role == "radio-button")
        .filter_map(|n| n.name)
        .collect()
}

fn sent(world: &World, cmd: &str) -> Vec<Value> {
    world.daemon.sent_of(cmd)
}

/// Tabs, or Shift-Tabs when `back`, until `key` holds the focus, as a keyboard user reaches a
/// radio group.
fn tab_to(world: &mut World, key: &str, back: bool) {
    let mut seen = Vec::new();
    loop {
        let at = focused(world);
        if at.as_deref() == Some(key) {
            return;
        }
        assert!(
            !seen.contains(&at),
            "Tab never reaches {key}; it went {seen:?}"
        );
        seen.push(at);
        world.gesture(|app| {
            app.key(KeyEvent {
                key: Key::Tab,
                mods: Mods {
                    shift: back,
                    ..Mods::default()
                },
            })
        });
    }
}

/// Answers the held `cmd` request with `reply`, after the events the daemon sends before it.
fn answer(world: &mut World, cmd: &str, events: Vec<Value>, reply: Value) {
    let request = sent(world, cmd).last().cloned().expect("the app asked");
    for event in events {
        world.daemon.emit(event);
    }
    world.daemon.reply_to(&request, reply);
    world.settle();
}

/// The daemon saving `member`: the roster, then the result.
fn save(world: &mut World, member: CrewMember) {
    answer(
        world,
        "crew_set",
        vec![message(CrewUpdatedMessage {
            members: vec![member.clone()],
            ..CrewUpdatedMessage::new()
        })],
        message(CrewSetResultMessage {
            success: true,
            member: Some(member),
            ..CrewSetResultMessage::new()
        }),
    );
}

/// The daemon starting `member`: the Chief's session on its own desktop, the roster bound to
/// it, then the result.
fn start_session(world: &mut World, member: CrewMember) {
    let arranged = state(
        vec![chief_day()],
        vec![desktop(
            "d-chief",
            "a",
            "",
            vec![pane("d-chief", "p0", "chief-day")],
        )],
    );
    answer(
        world,
        "crew_start",
        vec![
            message(SessionsUpdatedMessage {
                sessions: Some(vec![chief_day()]),
                ..SessionsUpdatedMessage::new()
            }),
            message(ProfileArrangementChangedMessage {
                profile: arranged.profiles.unwrap().remove(0),
                desktops: arranged.desktops.unwrap(),
                ..ProfileArrangementChangedMessage::new()
            }),
            message(CrewUpdatedMessage {
                members: vec![bound(member.clone(), "chief-day")],
                ..CrewUpdatedMessage::new()
            }),
        ],
        message(CrewStartResultMessage {
            success: true,
            session_id: Some("chief-day".into()),
            member: Some(member.id),
            ..CrewStartResultMessage::new()
        }),
    );
}

fn shown(world: &World) -> Vec<Value> {
    sent(world, "desktop_show_session")
        .iter()
        .map(|m| m["session_id"].clone())
        .collect()
}

#[test]
fn a_first_setup_opens_on_its_own_and_saves_the_recommended_route_then_starts_the_chief() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    assert_eq!(value(&world, SLOT).as_deref(), Some("unconfigured"));
    assert_eq!(
        named(&world, "chief-setup.heading").as_deref(),
        Some("Set up Captain")
    );
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    assert_eq!(named(&world, PRIMARY).as_deref(), Some("Start Captain"));
    assert_eq!(radios(&world, "harness"), ["Claude", "Codex"]);
    assert_eq!(checked(&world, "harness"), ["Claude"]);
    assert_eq!(checked(&world, "model"), ["Opus 5.5 · recommended"]);
    assert!(
        !radios(&world, "model")
            .iter()
            .any(|m| m.starts_with("Default"))
    );
    assert_eq!(checked(&world, "effort"), ["low"]);
    assert_eq!(
        named(&world, "chief-setup.folder").as_deref(),
        Some("~/attn-notebook-work")
    );
    click(&mut world, "chief-setup.folder.choose");
    world.answer_file_dialog(Some("/Users/you/Notes/Work"));
    assert_eq!(
        named(&world, "chief-setup.folder").as_deref(),
        Some("/Users/you/Notes/Work")
    );
    let configured = CrewMember {
        revision: 2,
        ..chief(true)
    };
    world.daemon.on("crew_set", |_| ());
    world.daemon.on("crew_start", |_| ());
    click(&mut world, PRIMARY);
    save(&mut world, configured.clone());
    start_session(&mut world, configured);
    let set = &sent(&world, "crew_set")[0];
    assert_eq!(
        (
            &set["member"],
            &set["agent"],
            &set["model"],
            &set["effort"],
            &set["expected_revision"],
            &set["notebook_root"],
        ),
        (
            &json!("member:chief-key"),
            &json!("claude"),
            &json!("opus"),
            &json!("low"),
            &json!(1),
            &json!("/Users/you/Notes/Work"),
        )
    );
    assert_eq!(sent(&world, "crew_start")[0]["member"], "member:chief-key");
    assert!(!shows(&world, SETUP));
    assert_eq!(
        named(&world, "queue-chief.chief-day").as_deref(),
        Some("Open Captain")
    );
    assert_eq!(shown(&world).last(), Some(&json!("chief-day")));
}

#[test]
fn while_agents_run_the_first_setup_waits_for_the_user_to_ask_and_escape_returns_to_the_slot() {
    let mut world = start(initial(
        true,
        chief(false),
        vec![super::sidebar::session("agent", "idle")],
    ));
    assert!(!shows(&world, SETUP));
    world.daemon.emit(SettingsUpdatedMessage {
        settings: {
            let mut s = settings(true);
            s.insert("claude_executable".into(), "/updated".into());
            Some(s)
        },
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!shows(&world, SETUP));
    click(&mut world, SLOT);
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    press(&mut world, Key::Escape);
    assert!(!shows(&world, SETUP));
    assert_eq!(focused(&world).as_deref(), Some(SLOT));
}

#[test]
fn arrows_walk_a_radio_group_picking_as_they_go_and_enter_on_a_radio_saves() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    tab_to(&mut world, "chief-setup.harness.claude", true);
    press(&mut world, Key::Right);
    assert_eq!(
        focused(&world).as_deref(),
        Some("chief-setup.harness.codex")
    );
    assert_eq!(checked(&world, "model"), ["gpt-6.1-sol · recommended"]);
    tab_to(&mut world, "chief-setup.effort.low", false);
    press(&mut world, Key::Right);
    assert_eq!(
        focused(&world).as_deref(),
        Some("chief-setup.effort.medium")
    );
    world.daemon.on("crew_set", |_| ());
    press(&mut world, Key::Enter);
    let set = &sent(&world, "crew_set")[0];
    assert_eq!(
        (&set["agent"], &set["model"], &set["effort"]),
        (&json!("codex"), &json!("gpt-6.1-sol"), &json!("medium"))
    );
    assert_eq!(
        named(&world, "chief-slot.pill").as_deref(),
        Some("starting…")
    );
    world.daemon.emit(CrewUpdatedMessage {
        members: vec![CrewMember {
            agent: Some("codex".into()),
            model: Some("gpt-6.1-sol".into()),
            ..chief(false)
        }],
        ..CrewUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        named(&world, "chief-setup.heading").as_deref(),
        Some("Set up Captain")
    );
    assert!(
        world
            .screen()
            .get(Target::key("chief-setup.folder.choose"))
            .is("disabled")
    );
    assert_eq!(named(&world, PRIMARY).as_deref(), Some("Starting…"));
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
}

#[test]
fn a_refused_save_says_why_and_keeps_the_route_and_folder() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    world.gesture(|app| app.click(Target::key("chief-setup.model./sonnet")));
    world.daemon.on("crew_set", |_| {
        message(CrewSetResultMessage {
            success: false,
            error: Some("Cannot start here".into()),
            ..CrewSetResultMessage::new()
        })
    });
    click(&mut world, PRIMARY);
    assert_eq!(
        named(&world, "chief-setup.alert").as_deref(),
        Some("Cannot start here")
    );
    assert_eq!(checked(&world, "model"), ["Sonnet 5.5 · standard"]);
    assert_eq!(
        named(&world, "chief-setup.folder").as_deref(),
        Some("~/attn-notebook-work")
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    assert!(sent(&world, "crew_start").is_empty());
}

#[test]
fn with_no_harness_for_the_chief_check_again_reads_them_and_one_harness_hides_the_choice() {
    let mut first = initial(true, chief(false), Vec::new());
    let mut none = settings(true);
    none.insert("claude_available".into(), "false".into());
    none.insert("codex_available".into(), "false".into());
    first.settings = Some(none);
    let mut world = start(first);
    assert_eq!(
        named(&world, "chief-setup.empty").as_deref(),
        Some("No harness here can run the Chief. Install Claude Code or Codex to start it.")
    );
    assert_eq!(focused(&world).as_deref(), Some("chief-setup.recheck"));
    assert!(shows(&world, "chief-setup.settings"));
    assert!(!shows(&world, PRIMARY));
    let mut codex_only = settings(true);
    codex_only.insert("claude_available".into(), "false".into());
    world.daemon.on("get_settings", move |_| {
        message(SettingsUpdatedMessage {
            settings: Some(codex_only.clone()),
            ..SettingsUpdatedMessage::new()
        })
    });
    click(&mut world, "chief-setup.recheck");
    assert_eq!(sent(&world, "get_settings").len(), 1);
    assert!(!shows(&world, "chief-setup.harness"));
    assert_eq!(checked(&world, "model"), ["gpt-6.1-sol · recommended"]);
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    assert!(sent(&world, "crew_set").is_empty());
}

#[test]
fn a_failed_harness_or_model_lookup_says_why_and_check_again_retries_before_start() {
    let mut world = World::start({
        let daemon = daemon(initial(true, chief(false), Vec::new()));
        daemon.answer_in_turn(
            "delegation_preferences_get",
            [
                Answer::Reply(message(DelegationPreferencesResultMessage {
                    success: false,
                    error: Some("Harness lookup failed".into()),
                    ..DelegationPreferencesResultMessage::new()
                })),
                Answer::Reply(message(preferences_result(vec![
                    harness("claude", "Claude"),
                    harness("codex", "Codex"),
                ]))),
            ],
        );
        daemon.answer_in_turn(
            "harness_models",
            [
                Answer::Reply(message(HarnessModelsResultMessage {
                    success: false,
                    error: Some("Model lookup failed".into()),
                    ..HarnessModelsResultMessage::new()
                })),
                Answer::Reply(message(reported(
                    "claude",
                    &Settings::default(),
                    catalog("claude"),
                ))),
            ],
        );
        daemon
    });
    world.settle();
    assert_eq!(
        named(&world, "chief-setup.alert").as_deref(),
        Some("Harness lookup failed")
    );
    assert!(!shows(&world, PRIMARY));
    click(&mut world, "chief-setup.recheck");
    assert_eq!(
        named(&world, "chief-setup.alert").as_deref(),
        Some("Model lookup failed")
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    click(&mut world, "chief-setup.recheck");
    assert!(!shows(&world, "chief-setup.alert"));
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    assert!(sent(&world, "crew_set").is_empty());
}

#[test]
fn the_chief_starts_with_one_click_closes_until_unbound_and_starts_again() {
    let between = chief(true);
    let mut world = start(initial(true, between.clone(), Vec::new()));
    assert!(!shows(&world, SETUP));
    assert_eq!(value(&world, SLOT).as_deref(), Some("between sessions"));
    world.daemon.on("crew_start", |_| ());
    click(&mut world, "chief-slot.start");
    start_session(&mut world, between.clone());
    assert_eq!(sent(&world, "crew_start").len(), 1);
    assert_eq!(shown(&world).last(), Some(&json!("chief-day")));
    world.daemon.on("crew_close", |_| ());
    super::crew_start::hover(&mut world, "queue-chief.chief-day");
    click(&mut world, "queue-chief.chief-day.ask-close");
    assert_eq!(
        named(&world, "queue-chief.chief-day.age").as_deref(),
        Some("closing")
    );
    let request = sent(&world, "crew_close")[0].clone();
    world.daemon.emit(CrewCloseResultMessage {
        request_id: request["request_id"]
            .as_str()
            .unwrap_or_default()
            .to_owned(),
        success: true,
        ..CrewCloseResultMessage::new()
    });
    world.settle();
    assert!(!shows(&world, "queue-chief.chief-day.age"));
    world.daemon.emit(CrewUpdatedMessage {
        members: vec![between],
        ..CrewUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(value(&world, SLOT).as_deref(), Some("between sessions"));
    world.advance(Duration::from_secs(1));
    click(&mut world, "chief-slot.start");
    assert_eq!(sent(&world, "crew_start").len(), 2);
}

#[test]
fn a_changed_route_saves_for_the_next_session_without_starting_a_chief_between_sessions() {
    let mut world = start(initial(true, chief(true), Vec::new()));
    super::crew_start::hover(&mut world, SLOT);
    click(&mut world, "chief-slot.actions");
    assert!(!shows(&world, "session.menu.close"));
    click(&mut world, "crew-member-menu.route");
    assert_eq!(
        named(&world, "chief-setup.heading").as_deref(),
        Some("Captain's harness")
    );
    world.gesture(|app| app.click(Target::key("chief-setup.model./sonnet")));
    world.daemon.on("crew_set", |_| {
        message(CrewSetResultMessage {
            success: true,
            ..CrewSetResultMessage::new()
        })
    });
    click(&mut world, PRIMARY);
    let set = &sent(&world, "crew_set")[0];
    assert_eq!(
        (
            &set["agent"],
            &set["model"],
            &set["effort"],
            set.get("notebook_root")
        ),
        (&json!("claude"), &json!("sonnet"), &json!("low"), None)
    );
    assert!(sent(&world, "crew_start").is_empty());
    assert!(!shows(&world, SETUP));
    assert_eq!(focused(&world).as_deref(), Some("chief-slot.start"));
}

#[test]
fn the_chief_offers_its_own_actions_in_session_and_between_sessions() {
    let mut world = start(initial(
        true,
        bound(chief(true), "chief-day"),
        vec![chief_day()],
    ));
    super::crew_start::hover(&mut world, "queue-chief.chief-day");
    click(&mut world, "queue-chief.chief-day.actions");
    let items: Vec<String> = world
        .screen()
        .all(Target::role("menu-item"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    for item in [
        "Harness and model…",
        "Edit charter",
        "Mark priority",
        "Rename session",
        "Reload session",
        "Ask Captain to close",
    ] {
        assert!(items.iter().any(|i| i == item), "{item} in {items:?}");
    }
    assert!(!items.iter().any(|i| i == "Close session"));
    click(&mut world, "session.menu.route");
    assert_eq!(
        named(&world, PRIMARY).as_deref(),
        Some("Save for next session")
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    press(&mut world, Key::Escape);
    world.daemon.on("crew_close", |_| {
        message(CrewCloseResultMessage {
            success: true,
            ..CrewCloseResultMessage::new()
        })
    });
    super::crew_start::hover(&mut world, "queue-chief.chief-day");
    click(&mut world, "queue-chief.chief-day.ask-close");
    assert_eq!(sent(&world, "crew_close").len(), 1);
    world.daemon.emit(CrewUpdatedMessage {
        members: vec![chief(true)],
        ..CrewUpdatedMessage::new()
    });
    world.settle();
    super::crew_start::hover(&mut world, SLOT);
    click(&mut world, "chief-slot.actions");
    let items: Vec<String> = world
        .screen()
        .all(Target::role("menu-item"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert_eq!(
        items,
        ["Harness and model…", "Edit charter", "Start Captain"]
    );
    click(&mut world, "crew-member-menu.details");
    assert!(shows(&world, "crew-dialog"));
}

#[test]
fn a_press_outside_the_first_setup_closes_it_and_hiding_and_showing_the_sidebar_keeps_it_closed() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    world.gesture(|app| app.click(Target::key("queue.waiting-head")));
    assert!(!shows(&world, SETUP));
    dispatch(&mut world, Action::SessionToggleSidebar);
    dispatch(&mut world, Action::SessionToggleSidebar);
    assert!(!shows(&world, SETUP));
}

#[test]
fn the_palettes_offer_the_chiefs_setup_and_nothing_is_sent_until_the_user_acts() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    press(&mut world, Key::Escape);
    assert!(!shows(&world, SETUP));
    dispatch(&mut world, Action::UiActionMenu);
    assert_eq!(
        value(&world, "palette.row.member:chief-key").as_deref(),
        Some("not set up")
    );
    press(&mut world, Key::Enter);
    assert!(shows(&world, SETUP));
    for cmd in ["crew_set", "crew_start", "crew_close"] {
        assert!(sent(&world, cmd).is_empty(), "{cmd}");
    }
}

#[test]
fn in_desktop_flow_the_slot_sits_in_the_desktop_sidebar_and_opens_its_first_setup() {
    let world = start(initial(false, chief(false), Vec::new()));
    assert!(!shows(&world, "sidebar.queue"));
    assert_eq!(value(&world, SLOT).as_deref(), Some("unconfigured"));
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
}

#[test]
fn a_hidden_sidebar_shows_again_for_a_chief_that_needs_setup_but_not_for_a_configured_one() {
    let mut world = start(initial(true, chief(true), Vec::new()));
    dispatch(&mut world, Action::SessionToggleSidebar);
    assert!(!shows(&world, SLOT));
    world.daemon.emit(CrewUpdatedMessage {
        members: vec![CrewMember {
            id: "second-chief".into(),
            ..chief(true)
        }],
        ..CrewUpdatedMessage::new()
    });
    world.settle();
    assert!(!shows(&world, SLOT));
    world.daemon.emit(CrewUpdatedMessage {
        members: vec![CrewMember {
            id: "third-chief".into(),
            ..chief(false)
        }],
        ..CrewUpdatedMessage::new()
    });
    world.settle();
    assert!(shows(&world, SLOT));
}

#[test]
fn a_save_that_lands_after_the_user_moved_on_leaves_the_focus_where_the_user_put_it() {
    let mut world = start(initial(
        true,
        bound(chief(true), "chief-day"),
        vec![chief_day()],
    ));
    dispatch(&mut world, Action::OpenChiefSetup);
    world.daemon.on("crew_set", |_| ());
    click(&mut world, PRIMARY);
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    let elsewhere = focused(&world);
    assert!(
        !shows(&world, SETUP),
        "the focus leaving the setup closes it"
    );
    let request = sent(&world, "crew_set")[0].clone();
    world.daemon.emit(CrewSetResultMessage {
        request_id: request["request_id"]
            .as_str()
            .unwrap_or_default()
            .to_owned(),
        success: true,
        ..CrewSetResultMessage::new()
    });
    world.settle();
    assert!(!shows(&world, SETUP));
    assert_eq!(focused(&world), elsewhere);
}

#[test]
fn a_failed_model_lookup_for_one_harness_leaves_another_harness_to_start_with() {
    let mut world = World::start({
        let daemon = daemon(initial(true, chief(false), Vec::new()));
        daemon.on("harness_models", |r| {
            if r["harness"] == "claude" {
                message(HarnessModelsResultMessage {
                    success: false,
                    error: Some("Claude model lookup failed".into()),
                    ..HarnessModelsResultMessage::new()
                })
            } else {
                serve_models(r)
            }
        });
        daemon
    });
    world.settle();
    assert_eq!(
        named(&world, "chief-setup.alert").as_deref(),
        Some("Claude model lookup failed")
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    click(&mut world, "chief-setup.harness.codex");
    assert!(!shows(&world, "chief-setup.alert"));
    assert_eq!(checked(&world, "model"), ["gpt-6.1-sol · recommended"]);
    world.daemon.on("crew_set", |_| ());
    click(&mut world, PRIMARY);
    let set = &sent(&world, "crew_set")[0];
    assert_eq!(
        (&set["agent"], &set["model"]),
        (&json!("codex"), &json!("gpt-6.1-sol"))
    );
}

#[test]
fn after_a_close_reply_while_still_bound_the_chief_can_be_asked_again_and_its_route_saved() {
    let mut world = start(initial(
        true,
        bound(chief(true), "chief-day"),
        vec![chief_day()],
    ));
    world.daemon.on("crew_close", |_| ());
    super::crew_start::hover(&mut world, "queue-chief.chief-day");
    click(&mut world, "queue-chief.chief-day.ask-close");
    dispatch(&mut world, Action::OpenChiefSetup);
    assert_eq!(
        named(&world, PRIMARY).as_deref(),
        Some("Save for next session")
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    let request = sent(&world, "crew_close")[0].clone();
    world.daemon.emit(CrewCloseResultMessage {
        request_id: request["request_id"]
            .as_str()
            .unwrap_or_default()
            .to_owned(),
        success: true,
        ..CrewCloseResultMessage::new()
    });
    world.settle();
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    world.daemon.on("crew_close", |_| {
        message(CrewCloseResultMessage {
            success: true,
            ..CrewCloseResultMessage::new()
        })
    });
    dispatch(&mut world, Action::AskChiefToClose);
    assert_eq!(sent(&world, "crew_close").len(), 2);
    world.daemon.on("crew_set", |_| ());
    click(&mut world, PRIMARY);
    assert_eq!(sent(&world, "crew_set").len(), 1);
}

#[test]
fn a_dialog_opened_over_the_first_setup_keeps_the_focus_its_controls_take() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    assert!(shows(&world, SETUP));
    dispatch(&mut world, Action::OpenShortcutEditor);
    click(&mut world, "shortcut-editor.capture.keys");
    assert!(!shows(&world, SETUP));
    assert_eq!(
        focused(&world).as_deref(),
        Some("shortcut-editor.capture.keys")
    );
}

fn reply_to_claude_models(world: &mut World, models: Vec<attn_protocol::HarnessModel>) {
    for request in sent(world, "harness_models")
        .into_iter()
        .filter(|r| r["harness"] == "claude")
    {
        world.daemon.reply_to(
            &request,
            reported("claude", &Settings::default(), models.clone()),
        );
    }
    world.settle();
}

fn deep(id: &str, name: &str) -> attn_protocol::HarnessModel {
    super::settings::harness_catalogs::model("claude", id, name, Some("deep"), &["low", "high"])
}

#[test]
fn a_late_model_list_readies_start_on_the_focus_it_held_and_leaves_a_focus_the_user_moved() {
    let held = daemon(initial(true, chief(false), Vec::new()));
    held.on("harness_models", |r: &Request| {
        if r["harness"] == "claude" {
            Value::Null
        } else {
            serve_models(r)
        }
    });
    let mut world = World::start(held);
    world.settle();
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    assert_eq!(focused(&world).as_deref(), Some(SETUP));
    reply_to_claude_models(&mut world, vec![deep("opus", "Opus 5.5")]);
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));

    let held = daemon(initial(
        true,
        chief(false),
        vec![super::sidebar::session("agent", "idle")],
    ));
    held.on("harness_models", |r: &Request| {
        if r["harness"] == "claude" {
            Value::Null
        } else {
            serve_models(r)
        }
    });
    let mut world = World::start(held);
    world.settle();
    click(&mut world, SLOT);
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    world.gesture(|app| app.click(Target::key("queue.agents-toggle")));
    let elsewhere = focused(&world);
    reply_to_claude_models(&mut world, vec![deep("opus", "Opus 5.5")]);
    assert_eq!(focused(&world), elsewhere);
    assert!(sent(&world, "crew_set").is_empty());
}

fn refreshed_catalog(world: &mut World) {
    world.daemon.on("harness_models", |r: &Request| {
        if r["harness"] == "claude" {
            Value::Null
        } else {
            serve_models(r)
        }
    });
    world.daemon.emit(SettingsUpdatedMessage {
        settings: {
            let mut s = settings(true);
            s.insert("claude_executable".into(), "/refreshed-harness".into());
            Some(s)
        },
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
}

#[test]
fn a_replaced_catalog_keeps_the_focus_on_the_model_group_on_close_or_outside_the_setup() {
    for destination in ["model", "close", "outside"] {
        let mut world = start(initial(
            true,
            chief(false),
            vec![super::sidebar::session("agent", "idle")],
        ));
        click(&mut world, SLOT);
        tab_to(&mut world, "chief-setup.model./opus", true);
        assert_eq!(focused(&world).as_deref(), Some("chief-setup.model./opus"));
        refreshed_catalog(&mut world);
        match destination {
            "close" => tab_to(&mut world, "chief-setup.close", false),
            "outside" => world.gesture(|app| app.click(Target::key("queue.agents-toggle"))),
            _ => {}
        }
        let before = focused(&world);
        reply_to_claude_models(&mut world, vec![deep("new-deep", "New deep model")]);
        let expected = match destination {
            "model" => Some("chief-setup.model./new-deep".to_owned()),
            _ => before,
        };
        assert_eq!(focused(&world), expected, "{destination}");
        assert!(sent(&world, "crew_set").is_empty(), "{destination}");
    }
}

#[test]
fn option_digits_in_the_new_agent_picker_pick_its_harness_and_leave_the_setup_behind_it() {
    let mut world = start(initial(true, chief(false), Vec::new()));
    assert!(shows(&world, SETUP));
    dispatch(&mut world, Action::SessionNew);
    world.gesture(|app| app.press("alt+2"));
    assert!(!shows(&world, SETUP));
    assert!(
        world
            .screen()
            .get(Target::key("new-session.agent.codex"))
            .is("checked")
    );
    assert!(sent(&world, "crew_set").is_empty());
}

fn reached(world: &World) -> Option<String> {
    let focused = focused(world);
    if focused.as_deref() != Some("sidebar.queue") {
        return focused;
    }
    world
        .screen()
        .children(Target::key("sidebar.queue"))
        .into_iter()
        .find(|n| n.is("selected"))
        .and_then(|n| n.key)
}

fn open_route(world: &mut World, in_session: bool) {
    if in_session {
        super::crew_start::hover(world, "queue-chief.chief-day");
        click(world, "queue-chief.chief-day.actions");
        click(world, "session.menu.route");
    } else {
        super::crew_start::hover(world, SLOT);
        click(world, "chief-slot.actions");
        click(world, "crew-member-menu.route");
    }
    assert!(shows(world, SETUP));
}

#[test]
fn escape_and_a_late_configured_save_both_hand_the_focus_back_to_the_chiefs_row() {
    for in_session in [false, true] {
        let (member, sessions) = if in_session {
            (
                bound(chief(true), "chief-day"),
                vec![chief_day(), super::queue::turn("agent", 5)],
            )
        } else {
            (chief(true), Vec::new())
        };
        let mut world = start(initial(true, member, sessions));
        let row = if in_session {
            "queue-chief.chief-day"
        } else {
            "chief-slot.start"
        };
        open_route(&mut world, in_session);
        press(&mut world, Key::Escape);
        assert!(!shows(&world, SETUP));
        assert_eq!(
            reached(&world).as_deref(),
            Some(row),
            "in session={in_session}"
        );
        if in_session {
            press(&mut world, Key::Down);
            assert_ne!(reached(&world).as_deref(), Some(row));
            assert_eq!(focused(&world).as_deref(), Some("sidebar.queue"));
        }

        open_route(&mut world, in_session);
        world.daemon.on("crew_set", |_| ());
        click(&mut world, PRIMARY);
        assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
        save(
            &mut world,
            CrewMember {
                revision: 2,
                ..if in_session {
                    bound(chief(true), "chief-day")
                } else {
                    chief(true)
                }
            },
        );
        assert!(!shows(&world, SETUP));
        assert_eq!(
            reached(&world).as_deref(),
            Some(row),
            "in session={in_session}"
        );
        assert!(sent(&world, "crew_start").is_empty());
    }
}

#[test]
fn enter_on_a_radio_saves_a_configured_chiefs_route_and_returns_to_its_row_without_starting_it() {
    let mut world = start(initial(true, chief(true), Vec::new()));
    open_route(&mut world, false);
    tab_to(&mut world, "chief-setup.model./opus", true);
    world.daemon.on("crew_set", |_| ());
    press(&mut world, Key::Enter);
    let set = &sent(&world, "crew_set")[0];
    assert_eq!(
        (&set["member"], &set["agent"], &set["model"], &set["effort"]),
        (
            &json!("member:chief-key"),
            &json!("claude"),
            &json!("opus"),
            &json!("low")
        )
    );
    save(
        &mut world,
        CrewMember {
            revision: 2,
            ..chief(true)
        },
    );
    assert!(!shows(&world, SETUP));
    assert_eq!(focused(&world).as_deref(), Some("chief-slot.start"));
    assert!(sent(&world, "crew_start").is_empty());
}

#[test]
fn a_focused_primary_stays_inert_while_the_models_are_read_again_and_works_once_they_return() {
    let mut world = start(initial(
        true,
        bound(chief(true), "chief-day"),
        vec![chief_day()],
    ));
    open_route(&mut world, true);
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    world.daemon.on("harness_models", |r: &Request| {
        if r["harness"] == "claude" {
            Value::Null
        } else {
            serve_models(r)
        }
    });
    world.daemon.emit(SettingsUpdatedMessage {
        settings: {
            let mut s = settings(true);
            s.insert(
                "model_tier_overrides".into(),
                r#"[{"harness":"claude","provider":"","model":"opus","tier":"standard"}]"#.into(),
            );
            Some(s)
        },
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("unavailable"));
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    world.daemon.on("crew_set", |_| ());
    click(&mut world, PRIMARY);
    press(&mut world, Key::Enter);
    press(&mut world, Key::Text(" ".into()));
    assert!(sent(&world, "crew_set").is_empty());
    reply_to_claude_models(
        &mut world,
        vec![super::settings::harness_catalogs::model(
            "claude",
            "opus",
            "Opus",
            Some("standard"),
            &["low", "high"],
        )],
    );
    assert_eq!(value(&world, PRIMARY).as_deref(), Some("ready"));
    assert_eq!(focused(&world).as_deref(), Some(PRIMARY));
    press(&mut world, Key::Enter);
    let set = sent(&world, "crew_set");
    assert_eq!(set.len(), 1);
    assert_eq!(
        (&set[0]["member"], &set[0]["model"]),
        (&json!("member:chief-key"), &json!("opus"))
    );
}

fn profile(id: &str, desktop: &str) -> attn_protocol::Profile {
    attn_protocol::Profile {
        id: id.into(),
        name: id.into(),
        current_desktop_id: desktop.into(),
        ..attn_protocol::Profile::default()
    }
}

fn arrange(world: &mut World, id: &str, desktop_id: &str) {
    let mut d = desktop(desktop_id, "a", "", Vec::new());
    d.profile_id = id.into();
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile(id, desktop_id),
        desktops: vec![d],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

#[test]
fn a_save_that_completes_in_another_profile_starts_nothing_there_and_leaves_start_to_the_user() {
    for configured in [false, true] {
        let mut world = start(initial(true, chief(configured), Vec::new()));
        world.daemon.on("crew_set", |_| ());
        if configured {
            open_route(&mut world, false);
            click(&mut world, "chief-setup.model./sonnet");
        }
        click(&mut world, PRIMARY);
        assert_eq!(sent(&world, "crew_set").len(), 1, "configured={configured}");
        world.daemon.emit(attn_protocol::ProfilesChangedMessage {
            profiles: vec![profile("work", "d-work"), profile("side", "d-side")],
            ..attn_protocol::ProfilesChangedMessage::new()
        });
        arrange(&mut world, "side", "d-side");
        assert!(!shows(&world, SLOT), "configured={configured}");
        world.daemon.on("crew_start", |_| ());
        save(
            &mut world,
            CrewMember {
                revision: 2,
                model: Some(if configured { "sonnet" } else { "opus" }.into()),
                ..chief(true)
            },
        );
        assert!(
            sent(&world, "crew_start").is_empty(),
            "configured={configured}"
        );
        arrange(&mut world, "work", "d-work");
        assert_eq!(value(&world, SLOT).as_deref(), Some("between sessions"));
        assert!(!shows(&world, SETUP), "configured={configured}");
        assert!(
            sent(&world, "crew_start").is_empty(),
            "configured={configured}"
        );
        click(&mut world, "chief-slot.start");
        assert_eq!(sent(&world, "crew_start")[0]["member"], "member:chief-key");
    }
}

#[test]
fn edit_charter_opens_the_chiefs_charter_and_closing_crew_returns_the_focus_to_its_menu_button() {
    for in_session in [false, true] {
        let (member, sessions) = if in_session {
            (bound(chief(true), "chief-day"), vec![chief_day()])
        } else {
            (chief(true), Vec::new())
        };
        let daemon = daemon(initial(true, member, sessions));
        daemon.on("crew_charter_get", |_| {
            message(attn_protocol::CrewCharterGetResultMessage {
                success: true,
                member: Some("chief-key".into()),
                charter: Some(attn_protocol::CrewCharterDocument {
                    content: "# Captain".into(),
                    token: "charter-one".into(),
                }),
                ..attn_protocol::CrewCharterGetResultMessage::new()
            })
        });
        let mut world = World::start(daemon);
        world.settle();
        let (row, trigger, item) = if in_session {
            (
                "queue-chief.chief-day",
                "queue-chief.chief-day.actions",
                "session.menu.member-details",
            )
        } else {
            (SLOT, "chief-slot.actions", "crew-member-menu.details")
        };
        super::crew_start::hover(&mut world, row);
        click(&mut world, trigger);
        click(&mut world, item);
        assert!(
            world
                .screen()
                .get(Target::key("crew.tab.charter"))
                .is("checked"),
            "in session={in_session}"
        );
        click(&mut world, "crew.charter.editor");
        let close = world
            .screen()
            .descendants(Target::key("crew-dialog"))
            .into_iter()
            .find(|n| n.role == "button" && n.name.as_deref() == Some("Close"))
            .and_then(|n| n.key)
            .expect("Manage crew has a close button");
        click(&mut world, &close);
        assert!(!shows(&world, "crew-dialog"), "in session={in_session}");
        assert_eq!(
            focused(&world).as_deref(),
            Some(trigger),
            "in session={in_session}"
        );
    }
}

#[test]
fn home_shows_the_chief_slot_and_no_chief_of_staff_card() {
    let world = start(initial(true, chief(false), Vec::new()));
    assert!(shows(&world, "home.new"));
    assert!(shows(&world, SLOT));
    let screen = world.screen();
    assert!(
        !screen
            .all(Target::role("group"))
            .into_iter()
            .chain(screen.all(Target::role("region")))
            .any(|n| n.name.as_deref() == Some("Chief of Staff")
                || n.key
                    .as_deref()
                    .is_some_and(|k| k.contains("chief-of-staff")))
    );
}
