//! Manage crew against a daemon that keeps the roster: the members of the selected profile,
//! each member's launch settings saved against the revision it was read at, its charter saved
//! against the version it was read at, its letters, its seeds, and Start, Close and Apply.

use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::Action;
use attn_protocol::{
    ActorView, CrewApplyResult, CrewApplyResultMessage, CrewCharterDocument,
    CrewCharterGetResultMessage, CrewCharterSetResultMessage, CrewCloseResultMessage,
    CrewHandoffDocument, CrewHandoffGetResultMessage, CrewHandoffSummary,
    CrewHandoffsGetResultMessage, CrewMember, CrewRenameResult, CrewRenameResultMessage,
    CrewSetResultMessage, CrewStartResultMessage, CrewUpdatedMessage, Desktop, DesktopPane,
    InitialStateMessage, OpenSeedResultMessage, PartyView, Profile,
    ProfileArrangementChangedMessage, Seed, Session, SettingsUpdatedMessage,
};
use attn_testkit::{Answer, Daemon, Request, Target, World, message, tree};
use rune::{Key, KeyEvent, Mods};
use serde_json::{Value, json};

use super::settings::Settings;
use super::settings::harness_catalogs::{
    catalog, harness, open_route, options, pick_model, picker_open, preferences_result,
    preview_harness, reported,
};

const PROFILE: &str = "work";

fn member(id: &str, revision: i64) -> CrewMember {
    let mut name = id.to_owned();
    name[..1].make_ascii_uppercase();
    CrewMember {
        id: id.into(),
        name,
        profile_id: PROFILE.into(),
        revision,
        resolved_agent: "claude".into(),
        home_dir: format!("/crew/{id}"),
        charter_path: format!("/crew/{id}/CHARTER.md"),
        ..CrewMember::default()
    }
}

fn with(mut m: CrewMember, f: impl FnOnce(&mut CrewMember)) -> CrewMember {
    f(&mut m);
    m
}

fn in_session(m: CrewMember, session: &str) -> CrewMember {
    with(m, |m| m.binding_session = Some(session.into()))
}

fn session(id: &str, agent: &str) -> Session {
    Session {
        id: id.into(),
        label: id.into(),
        agent: agent.into(),
        state: "idle".into(),
        profile_id: PROFILE.into(),
        ..Session::default()
    }
}

fn initial(crew: Vec<CrewMember>, sessions: Vec<Session>, seeds: Vec<Seed>) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some(PROFILE.into()),
        profiles: Some(vec![Profile {
            id: PROFILE.into(),
            name: "Work".into(),
            ..Profile::default()
        }]),
        settings: Some(BTreeMap::new()),
        sessions: Some(sessions),
        crew: Some(crew),
        seeds_total: Some(seeds.len() as i64),
        seeds: Some(seeds),
        ..InitialStateMessage::new()
    }
}

/// A daemon that lists Claude Code and Codex with their test catalogs, and keeps no palette
/// history.
fn daemon(crew: Vec<CrewMember>, sessions: Vec<Session>, seeds: Vec<Seed>) -> Daemon {
    let daemon = super::palette::daemon(initial(crew, sessions, seeds), Vec::new());
    daemon.on("delegation_preferences_get", |_| {
        message(preferences_result(vec![
            harness("claude", "Claude Code"),
            harness("codex", "Codex"),
        ]))
    });
    daemon.on("harness_models", |r| {
        let harness = r["harness"].as_str().unwrap_or_default().to_owned();
        message(reported(&harness, &Settings::default(), catalog(&harness)))
    });
    daemon
}

fn start(crew: Vec<CrewMember>) -> World {
    World::start(daemon(crew, vec![session("s1", "claude")], Vec::new()))
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

fn open(world: &mut World) {
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, Action::OpenCrew, tx);
        })
    });
    assert!(is_open(world), "Manage crew opened");
}

fn is_open(world: &World) -> bool {
    world.screen().find(Target::key("crew-dialog")).is_some()
}

fn named(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn shows(world: &World, key: &str) -> bool {
    world.screen().find(Target::key(key)).is_some()
}

fn enabled(world: &World, key: &str) -> bool {
    !world.screen().get(Target::key(key)).is("disabled")
}

fn roster(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("crew.roster.list"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn heading(world: &World) -> Option<String> {
    named(world, "crew.heading")
}

fn sets(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("crew_set")
        .iter()
        .map(|r| {
            json!({
                "expected_revision": r["expected_revision"],
                "agent": r["agent"],
                "model": r["model"],
                "effort": r["effort"],
            })
        })
        .collect()
}

fn saved(m: CrewMember) -> Answer {
    Answer::Reply(message(CrewSetResultMessage {
        success: true,
        member: Some(m),
        ..CrewSetResultMessage::new()
    }))
}

fn roster_push(world: &mut World, crew: Vec<CrewMember>) {
    world.daemon.emit(CrewUpdatedMessage {
        members: crew,
        ..CrewUpdatedMessage::new()
    });
    world.settle();
}

/// Past the charter's pause before it saves.
const PAUSE: Duration = Duration::from_millis(800);

fn rest(world: &mut World) {
    world.advance(PAUSE * 2);
    assert!(world.runtime_is_at_rest());
}

fn close_picker(world: &mut World) {
    press(world, Key::Escape);
    assert!(!picker_open(world));
    assert!(is_open(world), "Escape closes only the picker");
}

fn pick_opus(world: &mut World) {
    open_route(world, "Crew launch model");
    pick_model(world, "Opus");
    close_picker(world);
}

#[test]
fn the_palette_opens_manage_crew_on_the_profiles_first_member_and_escape_closes_it() {
    let other = with(member("otto", 1), |m| m.profile_id = "home".into());
    let retired = with(member("keel", 1), |m| m.retired = true);
    let mut world = start(vec![member("alder", 1), member("fern", 1), other, retired]);
    assert!(!is_open(&world));
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, Action::UiCommandPalette, tx);
        })
    });
    world.gesture(|app| app.type_text("Manage crew"));
    press(&mut world, Key::Enter);
    assert!(is_open(&world));
    assert_eq!(
        roster(&world),
        ["Alder Between sessions", "Fern Between sessions"]
    );
    assert_eq!(heading(&world).as_deref(), Some("Alder"));
    assert!(
        world
            .screen()
            .get(Target::key("crew.tab.launch"))
            .is("checked")
    );
    assert!(!shows(&world, "crew.find"));
    press(&mut world, Key::Escape);
    assert!(!is_open(&world));
    rest(&mut world);
}

#[test]
fn a_reconnect_whose_initial_state_carries_no_crew_empties_the_roster() {
    let mut world = start(vec![member("alder", 1)]);
    open(&mut world);
    assert_eq!(roster(&world).len(), 1);
    world.daemon.answer_hello_with(InitialStateMessage {
        crew: None,
        ..initial(Vec::new(), vec![session("s1", "claude")], Vec::new())
    });
    world.reconnect();
    assert!(roster(&world).is_empty());
    assert_eq!(
        named(&world, "crew.empty").as_deref(),
        Some("No crew members.")
    );
}

#[test]
fn past_five_members_a_find_field_narrows_the_roster_by_name_and_the_arrows_walk_it() {
    let names = ["ash", "birch", "cedar", "dahlia", "elm", "fern"];
    let mut world = start(names.iter().map(|n| member(n, 1)).collect());
    open(&mut world);
    click(&mut world, "crew.find");
    world.gesture(|app| app.type_text("ced"));
    assert_eq!(roster(&world), ["Cedar Between sessions"]);
    for _ in 0..3 {
        press(&mut world, Key::Backspace);
    }
    assert_eq!(roster(&world).len(), 6);
    world.gesture(|app| app.click(Target::key("crew.roster.birch")));
    assert_eq!(heading(&world).as_deref(), Some("Birch"));
    press(&mut world, Key::Down);
    assert_eq!(heading(&world).as_deref(), Some("Cedar"));
    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    assert_eq!(heading(&world).as_deref(), Some("Ash"));
}

#[test]
fn a_launch_change_writes_one_selection_against_the_acknowledged_revision_and_start_waits_for_it() {
    let world_daemon = daemon(
        vec![member("trellis", 4)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    world_daemon.answer_in_turn("crew_set", [Answer::Hold]);
    let mut world = World::start(world_daemon);
    open(&mut world);
    assert!(enabled(&world, "crew.start"));
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "Opus");
    close_picker(&mut world);
    assert_eq!(
        sets(&world),
        [json!({"expected_revision": 4, "agent": "", "model": "opus", "effort": ""})]
    );
    assert_eq!(
        named(&world, "crew.launch.status").as_deref(),
        Some("Saving…")
    );
    assert!(!enabled(&world, "crew.start"));

    roster_push(
        &mut world,
        vec![with(member("trellis", 5), |m| {
            m.model = Some("opus".into());
            m.resolved_model = Some("opus".into());
        })],
    );
    assert!(
        named(&world, "crew.acknowledged").unwrap().contains("opus"),
        "the next session follows the roster while the write is out"
    );
    roster_push(&mut world, vec![member("trellis", 4)]);
    assert!(named(&world, "crew.acknowledged").unwrap().contains("opus"));

    let write = world.daemon.sent_of("crew_set").remove(0);
    world.daemon.reply_to(
        &write,
        CrewSetResultMessage {
            success: true,
            member: Some(with(member("trellis", 5), |m| {
                m.model = Some("opus".into());
                m.resolved_model = Some("opus".into());
            })),
            ..CrewSetResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        named(&world, "crew.launch.status").as_deref(),
        Some("Saved")
    );
    assert!(enabled(&world, "crew.start"));
    rest(&mut world);
}

#[test]
fn a_launch_write_against_a_changed_member_keeps_the_edit_says_why_and_retry_writes_against_the_new_revision()
 {
    const CHANGED: &str =
        "the member changed after it was read; reconcile the returned revision and retry";
    let codex = |m: CrewMember, model: &str, effort: &str| {
        with(m, |m| {
            m.agent = Some("codex".into());
            m.resolved_agent = "codex".into();
            m.model = Some(model.into());
            m.resolved_model = Some(model.into());
            m.effort = Some(effort.into());
            m.resolved_effort = Some(effort.into());
        })
    };
    let d = daemon(
        vec![codex(member("keel", 7), "", "low")],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            Answer::Reply(message(CrewSetResultMessage {
                success: false,
                conflict: true,
                error: Some(CHANGED.into()),
                member: Some(codex(member("keel", 8), "", "high")),
                ..CrewSetResultMessage::new()
            })),
            saved(codex(member("keel", 9), "gpt-6-astra", "high")),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "gpt-6-astra");
    close_picker(&mut world);
    assert_eq!(
        named(&world, "crew.launch.status").as_deref(),
        Some("Not saved")
    );
    assert_eq!(named(&world, "crew.launch.error").as_deref(), Some(CHANGED));
    assert!(
        world
            .screen()
            .get(Target::key("settings.route.crew.keel"))
            .value
            .unwrap()
            .contains("gpt-6-astra"),
        "the edit is kept"
    );
    click(&mut world, "crew.launch.retry");
    assert_eq!(
        sets(&world)[1],
        json!({"expected_revision": 8, "agent": "codex", "model": "gpt-6-astra", "effort": "high"})
    );
    assert_eq!(
        named(&world, "crew.launch.status").as_deref(),
        Some("Saved")
    );
    rest(&mut world);
}

#[test]
fn a_launch_write_lost_with_the_connection_reads_not_saved_and_writes_again_once_it_returns() {
    let d = daemon(
        vec![member("keel", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            Answer::Hold,
            saved(with(member("keel", 2), |m| m.effort = Some("high".into()))),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    pick_opus(&mut world);
    world.reconnect();
    assert_eq!(
        sets(&world).len(),
        2,
        "the lost write goes again on the new connection"
    );
    assert_eq!(
        named(&world, "crew.launch.status").as_deref(),
        Some("Saved")
    );
}

#[test]
fn the_harness_list_marks_an_unavailable_harness_and_follows_availability_without_reading_again() {
    let codex = with(member("trellis", 1), |m| {
        m.agent = Some("codex".into());
        m.resolved_agent = "codex".into();
    });
    let mut world = World::start(daemon(
        vec![codex],
        vec![session("s1", "claude")],
        Vec::new(),
    ));
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("claude_available".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    let claude = |world: &World| {
        options(world, "harnesses")
            .into_iter()
            .find(|(name, _, _)| name.starts_with("Claude Code"))
            .map(|(_, _, enabled)| enabled)
    };
    assert_eq!(claude(&world), Some(false));
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("claude_available".to_owned(), "true".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(claude(&world), Some(true));
    preview_harness(&mut world, "Crew default");
    assert!(world.daemon.sent_of("crew_set").len() <= 1);
    assert_eq!(world.daemon.sent_of("delegation_preferences_get").len(), 1);
}

#[test]
fn start_launches_the_member_and_shows_its_session_and_a_refusal_shows_the_daemons_words() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_start",
        [
            Answer::Reply(message(CrewStartResultMessage {
                success: false,
                error: Some("Fern has no harness".into()),
                ..CrewStartResultMessage::new()
            })),
            Answer::Reply(message(CrewStartResultMessage {
                success: true,
                session_id: Some("s1".into()),
                show_error: Some("no desktop".into()),
                ..CrewStartResultMessage::new()
            })),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.start");
    assert_eq!(
        world.daemon.sent_of("crew_start")[0]["member"],
        "member:fern"
    );
    assert_eq!(
        named(&world, "notice.text").as_deref(),
        Some("Fern has no harness")
    );
    assert!(is_open(&world));
    click(&mut world, "crew.start");
    assert!(!is_open(&world), "Start closes the panel");
    assert_eq!(
        named(&world, "notice.text").as_deref(),
        Some("The member's session started, but showing it failed: no desktop")
    );
}

#[test]
fn close_asks_a_member_in_session_to_file_its_letter_and_a_refusal_shows_the_daemons_words() {
    let d = daemon(
        vec![in_session(member("fern", 1), "s1")],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_close",
        [Answer::Reply(message(CrewCloseResultMessage {
            success: false,
            error: Some("Fern's session is gone".into()),
            ..CrewCloseResultMessage::new()
        }))],
    );
    let mut world = World::start(d);
    open(&mut world);
    assert_eq!(named(&world, "crew.close").as_deref(), Some("Close"));
    assert!(!shows(&world, "crew.start"));
    assert!(named(&world, "crew.running").is_some());
    click(&mut world, "crew.close");
    assert_eq!(
        world.daemon.sent_of("crew_close")[0]["member"],
        "member:fern"
    );
    assert_eq!(
        named(&world, "notice.text").as_deref(),
        Some("Fern's session is gone")
    );
}

#[test]
fn running_now_names_what_the_session_runs_apart_from_the_next_sessions_settings() {
    let trellis = with(in_session(member("trellis", 4), "s-trellis"), |m| {
        m.agent = Some("codex".into());
        m.resolved_agent = "codex".into();
        m.effort = Some("high".into());
        m.resolved_effort = Some("high".into());
    });
    let mut world = World::start(daemon(
        vec![trellis],
        vec![session("s-trellis", "claude")],
        Vec::new(),
    ));
    open(&mut world);
    let running = world
        .screen()
        .get(Target::key("crew.running.route"))
        .name
        .unwrap();
    assert!(running.starts_with("Claude Code · Model not reported · Effort not reported"));
    assert!(
        named(&world, "crew.acknowledged")
            .unwrap()
            .ends_with("Codex · default · high")
    );
}

#[test]
fn apply_shows_what_happened_waits_on_a_pending_switch_and_is_offered_only_in_session() {
    let d = daemon(
        vec![
            with(in_session(member("fern", 1), "s1"), |m| {
                m.switch_pending = Some(true);
                m.resolved_agent = "codex".into();
            }),
            member("alder", 1),
        ],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_apply",
        [Answer::Reply(message(CrewApplyResultMessage {
            success: true,
            result: Some(CrewApplyResult {
                detail: "Reloaded Fern's session s1 in place".into(),
                ..CrewApplyResult::default()
            }),
            ..CrewApplyResultMessage::new()
        }))],
    );
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.roster.fern");
    assert_eq!(
        named(&world, "crew.switch-pending").as_deref(),
        Some("Waiting for Fern’s letter to continue on codex.")
    );
    click(&mut world, "crew.apply");
    assert_eq!(
        world.daemon.sent_of("crew_apply")[0]["member"],
        "member:fern"
    );
    assert_eq!(
        named(&world, "crew.apply.outcome").as_deref(),
        Some("Reloaded Fern's session s1 in place.")
    );
    click(&mut world, "crew.roster.alder");
    assert!(!shows(&world, "crew.apply"));
}

#[test]
fn rename_saves_on_enter_keeping_the_key_escape_cancels_it_and_a_refusal_stays_in_the_field() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_rename",
        [
            Answer::Reply(message(CrewRenameResultMessage {
                success: false,
                error: Some("\"chief\" is reserved".into()),
                ..CrewRenameResultMessage::new()
            })),
            Answer::Reply(message(CrewRenameResultMessage {
                success: true,
                result: Some(CrewRenameResult::default()),
                ..CrewRenameResultMessage::new()
            })),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.rename");
    world.gesture(|app| app.type_text("Ferdinand"));
    press(&mut world, Key::Escape);
    assert!(!shows(&world, "crew.rename.field"));
    assert!(is_open(&world), "Escape leaves the panel open");
    assert!(world.daemon.sent_of("crew_rename").is_empty());

    click(&mut world, "crew.rename");
    world.gesture(|app| app.type_text("chief"));
    press(&mut world, Key::Enter);
    assert_eq!(
        named(&world, "crew.rename.error").as_deref(),
        Some("\"chief\" is reserved")
    );
    click(&mut world, "crew.rename.field");
    world.gesture(|app| app.type_text("x"));
    assert!(!shows(&world, "crew.rename.error"));
    for _ in 0.."chiefx".len() {
        press(&mut world, Key::Backspace);
    }
    world.gesture(|app| app.type_text("Ferdinand"));
    press(&mut world, Key::Enter);
    let sent = world.daemon.sent_of("crew_rename");
    assert_eq!(sent[1]["member"], "member:fern");
    assert_eq!(sent[1]["name"], "Ferdinand");
    roster_push(
        &mut world,
        vec![with(member("fern", 2), |m| m.name = "Ferdinand".into())],
    );
    assert_eq!(heading(&world).as_deref(), Some("Ferdinand"));
}

fn charter(content: &str, token: &str) -> CrewCharterDocument {
    CrewCharterDocument {
        content: content.into(),
        token: token.into(),
    }
}

fn charter_read(member: &str, content: &str, token: &str) -> Answer {
    Answer::Reply(message(CrewCharterGetResultMessage {
        success: true,
        member: Some(member.into()),
        charter: Some(charter(content, token)),
        ..CrewCharterGetResultMessage::new()
    }))
}

fn charter_saved(member: &str, content: &str, token: &str) -> Answer {
    Answer::Reply(message(CrewCharterSetResultMessage {
        success: true,
        member: Some(member.into()),
        charter: Some(charter(content, token)),
        ..CrewCharterSetResultMessage::new()
    }))
}

fn editor(world: &World) -> String {
    world
        .screen()
        .get(Target::key("crew.charter.editor"))
        .value
        .unwrap_or_default()
}

fn on_charter(world: &mut World) {
    open(world);
    click(world, "crew.tab.charter");
}

#[test]
fn the_charter_saves_after_a_pause_against_the_version_it_read_and_at_once_when_the_user_leaves() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_charter_get", [charter_read("fern", "# Fern\n", "t1")]);
    d.answer_in_turn(
        "crew_charter_set",
        [
            charter_saved("fern", "# Fern\nA", "t2"),
            charter_saved("fern", "# Fern\nAB", "t3"),
        ],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    assert_eq!(
        world.daemon.sent_of("crew_charter_get")[0]["member"],
        "member:fern"
    );
    assert_eq!(editor(&world), "# Fern\n");
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text("A"));
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Waiting to save")
    );
    assert!(world.daemon.sent_of("crew_charter_set").is_empty());
    world.advance(PAUSE);
    let first = &world.daemon.sent_of("crew_charter_set")[0];
    assert_eq!(first["content"], "# Fern\nA");
    assert_eq!(first["expected_token"], "t1");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );

    world.gesture(|app| app.type_text("B"));
    click(&mut world, "crew.heading");
    let second = &world.daemon.sent_of("crew_charter_set")[1];
    assert_eq!(second["content"], "# Fern\nAB");
    assert_eq!(second["expected_token"], "t2");
    rest(&mut world);
}

#[test]
fn an_edit_made_while_a_save_is_out_is_sent_once_it_lands_and_saved_shows_only_after_the_last() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_charter_get", [charter_read("fern", "", "t1")]);
    d.answer_in_turn(
        "crew_charter_set",
        [Answer::Hold, charter_saved("fern", "AB", "t3")],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.type_text("A"));
    world.advance(PAUSE);
    world.gesture(|app| app.type_text("B"));
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saving…")
    );
    let held = world.daemon.sent_of("crew_charter_set").remove(0);
    world.daemon.reply_to(
        &held,
        CrewCharterSetResultMessage {
            success: true,
            member: Some("fern".into()),
            charter: Some(charter("A", "t2")),
            ..CrewCharterSetResultMessage::new()
        },
    );
    world.settle();
    let sent = world.daemon.sent_of("crew_charter_set");
    assert_eq!(sent.len(), 2);
    assert_eq!(sent[1]["content"], "AB");
    assert_eq!(sent[1]["expected_token"], "t2");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
    rest(&mut world);
}

#[test]
fn a_reread_that_finds_another_version_under_an_edit_asks_which_wins_and_keep_mine_writes_over_it()
{
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("fern", "mine", "t1"),
            charter_read("fern", "theirs", "t9"),
            charter_read("fern", "theirs", "t9"),
        ],
    );
    d.answer_in_turn(
        "crew_charter_set",
        [
            Answer::Reply(message(CrewCharterSetResultMessage {
                success: false,
                error: Some("disk full".into()),
                ..CrewCharterSetResultMessage::new()
            })),
            charter_saved("fern", "mine!", "t10"),
        ],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text("!"));
    click(&mut world, "crew.heading");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Not saved")
    );
    assert!(
        named(&world, "crew.charter.error")
            .unwrap()
            .contains("disk full")
    );

    click(&mut world, "crew.tab.launch");
    assert!(
        world
            .screen()
            .get(Target::key("crew.tab.launch"))
            .is("checked"),
        "a shown failure lets the user leave"
    );
    click(&mut world, "crew.tab.charter");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Changed elsewhere")
    );
    assert_eq!(editor(&world), "mine!");
    click(&mut world, "crew.charter.keep-mine");
    let kept = &world.daemon.sent_of("crew_charter_set")[1];
    assert_eq!(kept["content"], "mine!");
    assert_eq!(kept["expected_token"], "t9");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
}

#[test]
fn use_file_version_takes_the_files_text_over_the_edit() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("fern", "mine", "t1"),
            charter_read("fern", "theirs", "t9"),
        ],
    );
    d.answer_in_turn(
        "crew_charter_set",
        [Answer::Reply(message(CrewCharterSetResultMessage {
            success: true,
            conflict: true,
            member: Some("fern".into()),
            charter: Some(charter("theirs", "t9")),
            ..CrewCharterSetResultMessage::new()
        }))],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.type_text("x"));
    world.advance(PAUSE);
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Changed elsewhere")
    );
    click(&mut world, "crew.charter.use-file");
    assert_eq!(editor(&world), "theirs");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
}

#[test]
fn a_charter_that_cannot_be_read_offers_a_retry_and_a_reconnect_reads_it_again() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            Answer::Reply(message(CrewCharterGetResultMessage {
                success: false,
                error: Some("CHARTER.md is unreadable".into()),
                ..CrewCharterGetResultMessage::new()
            })),
            charter_read("fern", "one", "t1"),
            charter_read("fern", "two", "t2"),
        ],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    assert_eq!(
        named(&world, "crew.charter.load-error").as_deref(),
        Some("CHARTER.md is unreadable")
    );
    click(&mut world, "crew.charter.load-retry");
    assert_eq!(editor(&world), "one");
    world.reconnect();
    assert_eq!(editor(&world), "two");
}

#[test]
fn leaving_the_charter_waits_for_its_save_runs_only_the_latest_move_and_closing_waits_too() {
    let d = daemon(
        vec![member("alder", 1), member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("alder", "", "t1"),
            charter_read("alder", "x", "t2"),
        ],
    );
    d.answer_in_turn("crew_charter_set", [Answer::Hold, Answer::Hold]);
    d.on("crew_handoffs_get", |_| {
        message(CrewHandoffsGetResultMessage {
            success: true,
            member: Some("alder".into()),
            ..CrewHandoffsGetResultMessage::new()
        })
    });
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.type_text("x"));
    click(&mut world, "crew.roster.fern");
    assert_eq!(
        heading(&world).as_deref(),
        Some("Alder"),
        "the member waits for the save"
    );
    click(&mut world, "crew.tab.handoffs");
    let held = world.daemon.sent_of("crew_charter_set").remove(0);
    world.daemon.reply_to(
        &held,
        CrewCharterSetResultMessage {
            success: true,
            member: Some("alder".into()),
            charter: Some(charter("x", "t2")),
            ..CrewCharterSetResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        heading(&world).as_deref(),
        Some("Alder"),
        "only the latest move runs"
    );
    assert!(
        world
            .screen()
            .get(Target::key("crew.tab.handoffs"))
            .is("checked")
    );

    click(&mut world, "crew.tab.charter");
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.type_text("y"));
    press(&mut world, Key::Escape);
    assert!(is_open(&world), "closing waits for the save");
    let held = world.daemon.sent_of("crew_charter_set").remove(1);
    assert_eq!(held["content"], "xy");
    world.daemon.reply_to(
        &held,
        CrewCharterSetResultMessage {
            success: true,
            member: Some("alder".into()),
            charter: Some(charter("xy", "t3")),
            ..CrewCharterSetResultMessage::new()
        },
    );
    world.settle();
    assert!(!is_open(&world), "the panel closes once the charter landed");
}

fn letter(filename: &str, at: &str) -> CrewHandoffSummary {
    CrewHandoffSummary {
        filename: filename.into(),
        occurred_at: at.into(),
    }
}

fn letter_read(filename: &str, content: &str) -> Value {
    message(CrewHandoffGetResultMessage {
        success: true,
        member: Some("fern".into()),
        handoff: Some(CrewHandoffDocument {
            filename: filename.into(),
            content: content.into(),
            occurred_at: "2026-11-16T10:00:00Z".into(),
            token: "t".into(),
        }),
        ..CrewHandoffGetResultMessage::new()
    })
}

#[test]
fn the_handoffs_tab_lists_the_letters_newest_first_and_reads_one_at_a_time() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.on("crew_handoffs_get", |_| {
        message(CrewHandoffsGetResultMessage {
            success: true,
            member: Some("fern".into()),
            handoffs: vec![
                letter("2026-11-16-1000.md", "2026-11-16T10:00:00Z"),
                letter("2026-11-15-0900.md", "2026-11-15T09:00:00Z"),
            ],
            ..CrewHandoffsGetResultMessage::new()
        })
    });
    d.answer_in_turn(
        "crew_handoff_get",
        [
            Answer::Reply(letter_read(
                "2026-11-16-1000.md",
                "Resting. Nothing pending.",
            )),
            Answer::Reply(message(CrewHandoffGetResultMessage {
                success: false,
                error: Some("the letter is gone".into()),
                ..CrewHandoffGetResultMessage::new()
            })),
            Answer::Reply(letter_read("2026-11-15-0900.md", "Check the drive seed.")),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.tab.handoffs");
    assert_eq!(
        named(&world, "crew.handoffs.count").as_deref(),
        Some("2 letters")
    );
    assert_eq!(
        named(&world, "crew.handoff.0").as_deref(),
        Some("2026-11-16 Latest · 10:00 UTC")
    );
    assert!(
        world
            .screen()
            .descendants(Target::key("crew.handoff.reader"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Resting. Nothing pending."))
    );
    click(&mut world, "crew.handoff.1");
    assert_eq!(
        named(&world, "crew.handoff.letter-error").as_deref(),
        Some("the letter is gone")
    );
    click(&mut world, "crew.handoff.letter-retry");
    assert!(
        world
            .screen()
            .descendants(Target::key("crew.handoff.reader"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Check the drive seed."))
    );
    assert_eq!(
        world.daemon.sent_of("crew_handoffs_get").len(),
        1,
        "a letter's retry reads no history"
    );
}

#[test]
fn a_failed_history_read_shows_once_and_retry_reads_an_honest_empty_history() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_handoffs_get",
        [
            Answer::Reply(message(CrewHandoffsGetResultMessage {
                success: false,
                error: Some("handoffs folder unreadable".into()),
                ..CrewHandoffsGetResultMessage::new()
            })),
            Answer::Reply(message(CrewHandoffsGetResultMessage {
                success: true,
                member: Some("fern".into()),
                ..CrewHandoffsGetResultMessage::new()
            })),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.tab.handoffs");
    assert_eq!(
        named(&world, "crew.handoffs.error").as_deref(),
        Some("handoffs folder unreadable")
    );
    click(&mut world, "crew.handoffs.retry");
    assert!(shows(&world, "crew.handoffs.empty"));
}

#[test]
fn a_history_read_lost_with_the_connection_gives_way_to_the_new_connections_read() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_handoffs_get",
        [
            Answer::Hold,
            Answer::Reply(message(CrewHandoffsGetResultMessage {
                success: true,
                member: Some("fern".into()),
                handoffs: vec![letter("a.md", "2026-11-16T10:00:00Z")],
                ..CrewHandoffsGetResultMessage::new()
            })),
        ],
    );
    d.on("crew_handoff_get", |_| letter_read("a.md", "Fresh."));
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.tab.handoffs");
    world.reconnect();
    assert_eq!(
        named(&world, "crew.handoffs.count").as_deref(),
        Some("1 letter")
    );
}

fn planted_by(seed: Seed, member: &str) -> Seed {
    Seed {
        planter: ActorView {
            name: member.into(),
            r#ref: format!("member:{member}"),
        },
        ..seed
    }
}

fn tended_by(seed: Seed, member: &str, session: Option<&str>) -> Seed {
    Seed {
        status: "growing".into(),
        tender: Some(PartyView {
            name: member.into(),
            r#ref: format!("member:{member}"),
            session_id: session.map(str::to_owned),
        }),
        ..seed
    }
}

fn seed(id: &str, title: &str) -> Seed {
    let mut seed = super::garden::seed(id, title);
    seed.profile_id = PROFILE.into();
    seed
}

fn seed_rows(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("crew.seeds.list"))
        .into_iter()
        .filter_map(|n| n.key)
        .collect()
}

#[test]
fn the_seeds_tab_lists_what_a_member_tends_and_planted_and_says_plainly_when_nothing() {
    let seeds = vec![
        tended_by(seed("s-a", "Tended one"), "keel", None),
        planted_by(seed("s-b", "Planted one"), "keel"),
        planted_by(seed("s-c", "Another planted"), "keel"),
        seed("s-d", "Not Keel's"),
    ];
    let mut world = World::start(daemon(
        vec![member("keel", 1), member("alder", 1)],
        vec![session("s1", "claude")],
        seeds,
    ));
    open(&mut world);
    click(&mut world, "crew.roster.keel");
    click(&mut world, "crew.tab.seeds");
    assert_eq!(seed_rows(&world), ["crew.seed.s-a"]);
    assert_eq!(
        named(&world, "crew.seeds.tending").as_deref(),
        Some("Tending 1")
    );
    click(&mut world, "crew.seeds.planted");
    assert_eq!(seed_rows(&world), ["crew.seed.s-b", "crew.seed.s-c"]);
    click(&mut world, "crew.seeds.find");
    world.gesture(|app| app.type_text("another"));
    assert_eq!(seed_rows(&world), ["crew.seed.s-c"]);
    click(&mut world, "crew.roster.alder");
    assert_eq!(
        named(&world, "crew.seeds.empty").as_deref(),
        Some("No seeds were explicitly planted by Alder.")
    );
}

const DESKTOP: &str = "d1";

fn desktop(tiles: Vec<Value>) -> Desktop {
    let tree = if tiles.is_empty() {
        tree::pane("a")
    } else {
        tree::row(
            std::iter::once(tree::pane("a"))
                .chain(tiles)
                .collect::<Vec<_>>(),
        )
    };
    Desktop {
        id: DESKTOP.into(),
        profile_id: PROFILE.into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: "a".into(),
        panes: vec![DesktopPane {
            desktop_id: DESKTOP.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "s1".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision: 1,
    }
}

fn on_desktop() -> Profile {
    Profile {
        id: PROFILE.into(),
        name: "Work".into(),
        current_desktop_id: DESKTOP.into(),
        ..Profile::default()
    }
}

#[test]
fn a_member_with_no_session_opens_its_seed_as_a_standalone_reader_and_back_returns_to_where_crew_was()
 {
    let artifact = planted_by(
        seed("s-g9yxwv", "Artifact presence comes from the daemon"),
        "keel",
    );
    let seeds = vec![artifact.clone()];
    let d = daemon(
        vec![member("alder", 1), member("keel", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_hello_with(InitialStateMessage {
        profiles: Some(vec![on_desktop()]),
        desktops: Some(vec![desktop(Vec::new())]),
        ..initial(
            vec![member("alder", 1), member("keel", 1)],
            vec![session("s1", "claude")],
            seeds,
        )
    });
    d.on("seed_document_get", move |r: &Request| {
        super::garden::document_reply(r, Some(artifact.clone()))
    });
    let docked = desktop(vec![json!({
        "type": "tile",
        "tile_id": "tile-seed",
        "tile_kind": "seed",
        "tile_params": "s-g9yxwv",
    })]);
    d.on("open_seed", move |r: &Request| {
        vec![
            message(OpenSeedResultMessage {
                request_id: r["request_id"].as_str().map(str::to_owned),
                success: true,
                seed_id: "s-g9yxwv".into(),
                desktop_id: Some(DESKTOP.into()),
                tile_id: Some("tile-seed".into()),
                ..OpenSeedResultMessage::new()
            }),
            message(ProfileArrangementChangedMessage {
                profile: on_desktop(),
                desktops: vec![Desktop {
                    revision: 2,
                    ..docked.clone()
                }],
                ..ProfileArrangementChangedMessage::new()
            }),
        ]
    });
    let mut world = World::start_sized(d, (1600.0, 900.0));
    world.open_desktop();
    open(&mut world);
    click(&mut world, "crew.roster.keel");
    click(&mut world, "crew.tab.seeds");
    click(&mut world, "crew.seeds.planted");
    click(&mut world, "crew.seeds.find");
    world.gesture(|app| app.type_text("Artifact"));
    click(&mut world, "crew.seed.s-g9yxwv");
    let opened = &world.daemon.sent_of("open_seed")[0];
    assert_eq!(opened["seed_id"], "s-g9yxwv");
    assert_eq!(opened["standalone"], true);
    assert!(opened.get("session_id").is_none_or(Value::is_null));
    assert!(!is_open(&world));

    click(&mut world, "tile.tile-seed.crew-back");
    assert!(is_open(&world));
    assert_eq!(heading(&world).as_deref(), Some("Keel"));
    assert!(
        world
            .screen()
            .get(Target::key("crew.tab.seeds"))
            .is("checked")
    );
    assert!(
        world
            .screen()
            .get(Target::key("crew.seeds.planted"))
            .is("checked")
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("crew.seeds.find"))
            .value
            .as_deref(),
        Some("Artifact")
    );
}

#[test]
fn a_member_in_session_opens_its_seed_beside_its_session() {
    let seeds = vec![tended_by(seed("s-a", "Tended one"), "keel", Some("s-keel"))];
    let d = daemon(
        vec![in_session(member("keel", 1), "s-keel")],
        vec![session("s-keel", "claude")],
        seeds,
    );
    d.on("open_seed", |r: &Request| {
        message(OpenSeedResultMessage {
            success: true,
            seed_id: r["seed_id"].as_str().unwrap_or_default().to_owned(),
            desktop_id: Some("d1".into()),
            tile_id: Some("tile-seed".into()),
            ..OpenSeedResultMessage::new()
        })
    });
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.tab.seeds");
    click(&mut world, "crew.seed.s-a");
    let opened = &world.daemon.sent_of("open_seed")[0];
    assert_eq!(opened["session_id"], "s-keel");
    assert!(opened.get("standalone").is_none_or(Value::is_null));
}

#[test]
fn the_model_picker_closes_when_another_layer_replaces_manage_crew() {
    let mut world = start(vec![member("keel", 1)]);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, Action::UiOpenSettings, tx);
        })
    });
    assert!(!picker_open(&world));
    assert!(world.daemon.sent_of("crew_set").is_empty());
}
