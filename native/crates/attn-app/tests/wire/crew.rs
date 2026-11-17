//! Manage crew against a daemon that keeps the roster: the members of the selected profile,
//! each member's launch settings saved against the revision it was read at, its charter saved
//! against the version it was read at, its letters, its seeds, and Start, Close and Apply.

use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Keymap};
use attn_protocol::{
    ActorView, CrewApplyResult, CrewApplyResultMessage, CrewCharterDocument,
    CrewCharterGetResultMessage, CrewCharterSetResultMessage, CrewCloseResultMessage,
    CrewHandoffDocument, CrewHandoffGetResultMessage, CrewHandoffSummary,
    CrewHandoffsGetResultMessage, CrewMember, CrewRenameResult, CrewRenameResultMessage,
    CrewSetResultMessage, CrewStartResultMessage, CrewUpdatedMessage, Desktop, DesktopPane,
    InitialStateMessage, LaunchDesktopResultMessage, LaunchDesktopSetting, OpenSeedResultMessage,
    PartyView, Profile, ProfileArrangementChangedMessage, Seed, Session, SettingsUpdatedMessage,
};
use attn_testkit::{Answer, Daemon, Request, Target, World, message, tree};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
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
fn a_roster_row_grows_with_the_lines_it_shows_and_no_row_covers_the_next() {
    let with_desktop = |m| {
        with(m, |m| {
            m.launch_desktop = Some(LaunchDesktopSetting {
                desktop_id: Some("work/desktop_2".into()),
                label: Some("2 · Review".into()),
                ..LaunchDesktopSetting::default()
            })
        })
    };
    let d = daemon(
        vec![
            member("ash", 1),
            with_desktop(member("birch", 1)),
            with_desktop(in_session(member("cedar", 1), "s-cedar")),
        ],
        vec![session("s-cedar", "claude")],
        Vec::new(),
    );
    let mut world = World::start_sized(d, (1200.0, 900.0));
    open(&mut world);
    let rows: Vec<[f32; 4]> = ["ash", "birch", "cedar"]
        .iter()
        .map(|id| {
            world
                .screen()
                .bounds(Target::key(format!("crew.roster.{id}")))
                .unwrap_or_else(|| panic!("{id}'s row is drawn"))
        })
        .collect();
    assert!(
        rows[1][3] > rows[0][3],
        "a row with its launch desktop is taller than one without: {rows:?}"
    );
    for pair in rows.windows(2) {
        assert!(
            pair[0][1] + pair[0][3] <= pair[1][1],
            "a row ends before the next starts: {rows:?}"
        );
    }
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

fn slot_keys(slot: i64) -> String {
    let keymap = Keymap::from_setting(None, KeyPlatform::current());
    let action = Action::desktop_select(slot).expect("a desktop slot");
    keymap.label(keymap.binding(&action).expect("the slot has a key"))
}

fn choose_option(world: &mut World, name: &str) {
    world
        .automate("press", json!({"target": {"role": "option", "name": name}}))
        .unwrap_or_else(|why| panic!("choose {name}: {why}"));
}

fn desktop_options(world: &mut World) -> Vec<String> {
    world
        .automate(
            "wait",
            json!({"target": {"role": "option", "name": "A new desktop…"}}),
        )
        .expect("the Desktop list opened");
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn launch_desktops(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("crew_set")
        .iter()
        .map(|r| r["launch_desktop_setting"].clone())
        .collect()
}

const DESKTOP_SELECT: &str = "crew.launch.keel.desktop-trigger";

fn with_launch_desktops(d: &Daemon) {
    d.on("launch_desktop_get", |r: &Request| {
        message(LaunchDesktopResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            action: "launch_desktop_get".into(),
            success: true,
            desktops: Some(vec![
                Desktop {
                    id: "d-work".into(),
                    profile_id: PROFILE.into(),
                    name: "Work".into(),
                    shortcut_slot: Some(1),
                    ..Desktop::default()
                },
                Desktop {
                    id: "d-review-a".into(),
                    profile_id: PROFILE.into(),
                    name: "Review".into(),
                    ..Desktop::default()
                },
                Desktop {
                    id: "d-review-b".into(),
                    profile_id: PROFILE.into(),
                    name: "Review".into(),
                    ..Desktop::default()
                },
            ]),
            ..LaunchDesktopResultMessage::new()
        })
    });
}

#[test]
fn the_desktop_select_offers_the_profiles_desktops_and_empty_slots_as_the_daemon_reads_them() {
    let d = daemon(
        vec![member("keel", 5)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    with_launch_desktops(&d);
    d.on("crew_set", |_| {
        message(CrewSetResultMessage {
            success: true,
            member: Some(member("keel", 6)),
            ..CrewSetResultMessage::new()
        })
    });
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, DESKTOP_SELECT);
    let read = &world.daemon.sent_of("launch_desktop_get")[0];
    assert_eq!(
        (read["kind"].as_str(), read["item_id"].as_str()),
        (Some("crew"), Some("keel"))
    );
    let options = desktop_options(&mut world);
    assert_eq!(
        options[..6],
        [
            "Its own desktop",
            "A new desktop…",
            "1 · Work",
            "Review",
            "Review (2)",
            "Empty slot 2…"
        ]
    );
    assert_eq!(options.last().map(String::as_str), Some("Empty slot 9…"));
    choose_option(&mut world, "Review (2)");
    assert_eq!(
        launch_desktops(&world),
        [json!({"desktop_id": "d-review-b"})]
    );

    click(&mut world, DESKTOP_SELECT);
    choose_option(&mut world, "Empty slot 3…");
    assert_eq!(
        named(&world, "launch-desktop.naming.hint").as_deref(),
        Some(format!("On {}.", slot_keys(3)).as_str())
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("launch-desktop.name"))
            .value
            .as_deref(),
        Some("Keel")
    );
    press(&mut world, Key::Enter);
    assert_eq!(
        launch_desktops(&world)[1],
        json!({"desktop_id": "work/desktop_3", "desktop_name": "Keel"})
    );
}

#[test]
fn closing_manage_crew_lets_go_of_an_open_desktop_select_and_an_unfinished_name() {
    let d = daemon(
        vec![member("keel", 5)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    with_launch_desktops(&d);
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, DESKTOP_SELECT);
    choose_option(&mut world, "A new desktop…");
    world.gesture(|app| {
        app.update(|state, tx| {
            attn_app::dispatch(state, Action::UiOpenSettings, tx);
        })
    });
    open(&mut world);
    assert!(!shows(&world, "launch-desktop.naming.hint"));
    assert!(
        world
            .screen()
            .all(Target::role("list-box-option"))
            .is_empty()
    );
}

#[test]
fn a_new_desktop_named_while_a_save_is_out_is_kept_when_another_launch_field_changes() {
    let d = daemon(
        vec![member("keel", 5)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    with_launch_desktops(&d);
    d.answer_in_turn("crew_set", [Answer::Hold, Answer::Hold]);
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, DESKTOP_SELECT);
    choose_option(&mut world, "Empty slot 2…");
    world.gesture(|app| app.type_text("Review"));
    click(&mut world, "launch-desktop.naming.use");
    assert_eq!(
        launch_desktops(&world),
        [json!({"desktop_id": "work/desktop_2", "desktop_name": "Review"})]
    );
    pick_opus(&mut world);
    let launch_desktop = LaunchDesktopSetting {
        desktop_id: Some("work/desktop_2".into()),
        label: Some("2 · Review".into()),
        ..LaunchDesktopSetting::default()
    };
    let first = world.daemon.sent_of("crew_set").remove(0);
    world.daemon.reply_to(
        &first,
        CrewSetResultMessage {
            success: true,
            member: Some(with(member("keel", 6), |m| {
                m.launch_desktop = Some(launch_desktop.clone())
            })),
            ..CrewSetResultMessage::new()
        },
    );
    world.settle();
    let sent = world.daemon.sent_of("crew_set");
    assert_eq!(sent.len(), 2);
    assert_eq!(sent[1]["launch_desktop_setting"], json!(launch_desktop));
    assert_eq!(sent[1]["model"], "opus");
}

#[test]
fn cancelling_the_new_desktops_name_writes_nothing() {
    let d = daemon(
        vec![member("keel", 5)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    with_launch_desktops(&d);
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, DESKTOP_SELECT);
    choose_option(&mut world, "A new desktop…");
    assert_eq!(
        named(&world, "launch-desktop.naming.hint").as_deref(),
        Some("No shortcut.")
    );
    press(&mut world, Key::Escape);
    assert!(!shows(&world, "launch-desktop.naming.hint"));
    assert!(is_open(&world), "Escape closes only the name dialog");
    assert!(world.daemon.sent_of("crew_set").is_empty());
}

fn push_seeds(world: &mut World, seeds: Vec<Seed>, total: i64) {
    world.daemon.emit(attn_protocol::GardenSeedsUpdatedMessage {
        profile_id: PROFILE.into(),
        total,
        seeds,
        ..attn_protocol::GardenSeedsUpdatedMessage::new()
    });
    world.settle();
}

fn tab_to(world: &mut World, key: &str) {
    for _ in 0..60 {
        if world.screen().focused().and_then(|n| n.key).as_deref() == Some(key) {
            return;
        }
        press(world, Key::Tab);
    }
    panic!("Tab never reached {key}:\n{}", world.screen());
}

#[test]
fn a_planted_seed_shows_its_state_and_plot_and_opens_from_the_keyboard() {
    let plot = Seed {
        plot_progress: Some(attn_protocol::SeedPlotProgress {
            total: 5,
            done: 2,
            growing: 1,
            parked: 1,
            ready: 1,
            ..attn_protocol::SeedPlotProgress::default()
        }),
        ..seed("s-dssvxq", "Garden follow-up work")
    };
    let child = Seed {
        status: "planted".into(),
        edges: vec![attn_protocol::SeedEdge {
            kind: "part-of".into(),
            to: plot.id.clone(),
        }],
        ..planted_by(
            seed("s-g9yxwv", "Artifact presence comes from the daemon"),
            "trellis",
        )
    };
    let d = daemon(
        vec![member("trellis", 4)],
        vec![session("s1", "claude")],
        vec![child, plot],
    );
    d.on("open_seed", |_| ());
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.tab.seeds");
    click(&mut world, "crew.seeds.planted");
    let row = named(&world, "crew.seed.s-g9yxwv").unwrap();
    assert!(row.contains("Garden follow-up work · 2/5"), "{row}");
    assert!(row.ends_with("Planted"), "{row}");

    tab_to(&mut world, "crew.seed.s-g9yxwv");
    press(&mut world, Key::Enter);
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], "s-g9yxwv");
}

#[test]
fn what_a_member_tends_follows_garden_broadcasts_its_days_claims_and_its_own_alike() {
    let claimed = tended_by(seed("s-live", "Live claim"), "day", Some("day-current"));
    let own = tended_by(seed("s-member", "Member claim"), "alder", None);
    let mut world = World::start(daemon(
        vec![in_session(member("alder", 1), "day-current")],
        vec![session("day-current", "claude")],
        vec![claimed.clone(), own.clone()],
    ));
    open(&mut world);
    click(&mut world, "crew.tab.seeds");
    assert_eq!(
        seed_rows(&world),
        ["crew.seed.s-live", "crew.seed.s-member"]
    );

    push_seeds(
        &mut world,
        vec![
            Seed {
                status: "parked".into(),
                tender: None,
                ..claimed.clone()
            },
            Seed {
                status: "harvested".into(),
                tender: None,
                ..own
            },
        ],
        2,
    );
    assert_eq!(
        named(&world, "crew.seeds.empty").as_deref(),
        Some("Alder isn't tending a seed.")
    );

    click(&mut world, "crew.seeds.planted");
    push_seeds(
        &mut world,
        vec![Seed {
            status: "planted".into(),
            tender: None,
            ..planted_by(claimed, "alder")
        }],
        1,
    );
    assert_eq!(seed_rows(&world), ["crew.seed.s-live"]);
}

#[test]
fn the_seeds_tab_says_when_its_list_is_only_the_newest_part_of_the_garden() {
    let mut world = World::start(daemon(
        vec![member("keel", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    ));
    open(&mut world);
    click(&mut world, "crew.tab.seeds");
    click(&mut world, "crew.seeds.planted");
    assert_eq!(
        named(&world, "crew.seeds.empty").as_deref(),
        Some("No seeds were explicitly planted by Keel.")
    );

    let others = ["a", "b", "c", "d"]
        .map(|id| planted_by(seed(&format!("s-{id}"), id), "alder"))
        .to_vec();
    push_seeds(&mut world, others, 481);
    assert_eq!(
        named(&world, "crew.seeds.cap").as_deref(),
        Some("Showing matches in the newest 4 of 481 seeds.")
    );
    assert_eq!(
        named(&world, "crew.seeds.empty").as_deref(),
        Some("No seeds in this Garden snapshot are explicitly planted by Keel.")
    );
    assert_eq!(
        named(&world, "crew.seeds.planted").as_deref(),
        Some("Planted 0+")
    );
}

fn charter_saves(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("crew_charter_set")
        .iter()
        .map(|r| {
            (
                r["content"].as_str().unwrap_or_default().to_owned(),
                r["expected_token"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

fn saves(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
    pairs
        .iter()
        .map(|(c, t)| ((*c).to_owned(), (*t).to_owned()))
        .collect()
}

#[test]
fn a_save_whose_answer_was_lost_writes_again_even_once_the_text_is_back_to_what_was_saved() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("fern", "A", "a"),
            charter_read("fern", "A", "a"),
        ],
    );
    d.answer_in_turn("crew_charter_set", [Answer::Hold, Answer::Hold]);
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text("B"));
    click(&mut world, "crew.heading");
    super::ledger::hang_up(&mut world);
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Not saved")
    );

    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    press(&mut world, Key::Backspace);
    super::ledger::come_back(&mut world);
    click(&mut world, "crew.heading");
    assert_eq!(charter_saves(&world), saves(&[("AB", "a"), ("A", "a")]));
}

#[test]
fn a_save_that_failed_for_want_of_a_connection_goes_once_it_reconnects_and_other_failures_wait_for_the_user()
 {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("fern", "old", "old"),
            charter_read("fern", "old", "old"),
            charter_read("fern", "old", "old"),
        ],
    );
    d.answer_in_turn(
        "crew_charter_set",
        [Answer::Reply(message(CrewCharterSetResultMessage {
            success: false,
            error: Some("disk is read-only".into()),
            ..CrewCharterSetResultMessage::new()
        }))],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    super::ledger::hang_up(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text(" offline edit"));
    click(&mut world, "crew.heading");
    assert!(named(&world, "crew.charter.error").is_some());
    assert_eq!(charter_saves(&world), saves(&[]));

    super::ledger::come_back(&mut world);
    assert_eq!(charter_saves(&world), saves(&[("old offline edit", "old")]));
    let error = named(&world, "crew.charter.error").unwrap_or_default();
    assert!(error.contains("disk is read-only"), "{error}");

    world.reconnect();
    assert_eq!(charter_saves(&world).len(), 1);
    assert_eq!(editor(&world), "old offline edit");
}

#[test]
fn a_write_the_daemon_acknowledged_stays_when_an_older_reread_answers_after_it() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [charter_read("fern", "A", "a"), Answer::Hold],
    );
    d.answer_in_turn("crew_charter_set", [charter_saved("fern", "AB", "b")]);
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.tab.launch");
    click(&mut world, "crew.tab.charter");
    let reread = world.daemon.sent_of("crew_charter_get").remove(1);

    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text("B"));
    click(&mut world, "crew.heading");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );

    world.daemon.reply_to(
        &reread,
        CrewCharterGetResultMessage {
            success: true,
            member: Some("fern".into()),
            charter: Some(charter("A", "a")),
            ..CrewCharterGetResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(editor(&world), "AB");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
}

const CHANGED_ELSEWHERE: &str =
    "the member changed after it was read; reconcile the returned revision and retry";

fn routed(m: CrewMember, agent: &str, model: &str, effort: &str) -> CrewMember {
    with(m, |m| {
        m.agent = (!agent.is_empty()).then(|| agent.into());
        m.resolved_agent = if agent.is_empty() { "codex" } else { agent }.into();
        m.model = (!model.is_empty()).then(|| model.into());
        m.resolved_model = (!model.is_empty()).then(|| model.into());
        m.effort = (!effort.is_empty()).then(|| effort.into());
        m.resolved_effort = (!effort.is_empty()).then(|| effort.into());
    })
}

fn conflicted(current: CrewMember) -> Answer {
    Answer::Reply(message(CrewSetResultMessage {
        success: false,
        conflict: true,
        error: Some(CHANGED_ELSEWHERE.into()),
        member: Some(current),
        ..CrewSetResultMessage::new()
    }))
}

fn launch_status(world: &World) -> Option<String> {
    named(world, "crew.launch.status")
}

#[test]
fn a_lost_launch_write_stays_not_saved_though_the_roster_shows_its_values() {
    let d = daemon(
        vec![member("keel", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_set", [Answer::Hold, Answer::Hold]);
    let mut world = World::start(d);
    open(&mut world);
    pick_opus(&mut world);
    roster_push(&mut world, vec![routed(member("keel", 2), "", "opus", "")]);
    super::ledger::hang_up(&mut world);
    assert_eq!(launch_status(&world).as_deref(), Some("Not saved"));
    assert!(
        named(&world, "crew.acknowledged")
            .unwrap_or_default()
            .contains("opus")
    );
}

#[test]
fn an_unavailable_stored_route_stays_visible_and_crew_default_clears_every_pin() {
    let current = routed(member("trellis", 1), "codex", "gpt-6-astra", "high");
    let d = daemon(vec![current], vec![session("s1", "claude")], Vec::new());
    d.answer_in_turn(
        "crew_set",
        [saved(routed(member("trellis", 2), "", "", ""))],
    );
    let mut world = World::start(d);
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("codex_available".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    open(&mut world);
    let route = world
        .screen()
        .get(Target::key("settings.route.crew.trellis"))
        .value
        .unwrap_or_default();
    assert!(
        route.contains("gpt-6-astra") && route.contains("high"),
        "{route}"
    );
    open_route(&mut world, "Crew launch model");
    let harnesses = options(&world, "harnesses");
    let codex = harnesses.iter().find(|(n, _, _)| n.starts_with("Codex"));
    assert!(
        codex.is_some_and(|(_, _, enabled)| !enabled),
        "{harnesses:?}"
    );
    super::settings::harness_catalogs::preview_harness(&mut world, "Crew default");
    assert_eq!(
        sets(&world),
        [json!({"expected_revision": 1, "agent": "", "model": "", "effort": ""})]
    );
}

#[test]
fn the_launch_settings_follow_what_the_daemon_derives_at_one_revision_and_drop_a_write_answer_older_than_the_roster()
 {
    let d = daemon(
        vec![routed(member("alder", 3), "", "gpt-6-astra", "")],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            Answer::Hold,
            saved(routed(member("alder", 7), "", "external", "high")),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    roster_push(
        &mut world,
        vec![with(
            routed(member("alder", 3), "", "gpt-6-astra", ""),
            |m| {
                m.binding_session = Some("session-new".into());
                m.resolved_effort = Some("medium".into());
            },
        )],
    );
    assert!(
        named(&world, "crew.acknowledged")
            .unwrap_or_default()
            .contains("medium")
    );

    for effort in ["low", "high"] {
        open_route(&mut world, "Crew launch model");
        super::settings::harness_catalogs::pick_effort(&mut world, effort);
        if picker_open(&world) {
            close_picker(&mut world);
        }
    }
    roster_push(
        &mut world,
        vec![routed(member("alder", 6), "", "external", "")],
    );
    let first = world.daemon.sent_of("crew_set").remove(0);
    world.daemon.reply_to(
        &first,
        CrewSetResultMessage {
            success: true,
            member: Some(routed(member("alder", 4), "", "", "low")),
            ..CrewSetResultMessage::new()
        },
    );
    world.settle();
    assert_eq!(
        sets(&world)[1],
        json!({"expected_revision": 6, "agent": "", "model": "external", "effort": "high"})
    );
    assert_eq!(launch_status(&world).as_deref(), Some("Saved"));
    assert!(
        named(&world, "crew.acknowledged")
            .unwrap_or_default()
            .contains("high")
    );
}

#[test]
fn a_conflicting_launch_edit_takes_in_what_changed_elsewhere_keeps_what_the_user_cleared_and_retries()
 {
    let d = daemon(
        vec![routed(member("keel", 7), "codex", "gpt-6-astra", "low")],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            conflicted(routed(member("keel", 8), "codex", "gpt-6-astra", "high")),
            saved(routed(member("keel", 9), "codex", "gpt-6-luna", "high")),
            conflicted(routed(member("keel", 10), "codex", "gpt-6-luna", "high")),
            saved(member("keel", 11)),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "gpt-6-luna");
    close_picker(&mut world);
    assert_eq!(launch_status(&world).as_deref(), Some("Not saved"));
    assert_eq!(
        named(&world, "crew.launch.error").as_deref(),
        Some(CHANGED_ELSEWHERE)
    );
    let route = world
        .screen()
        .get(Target::key("settings.route.crew.keel"))
        .value
        .unwrap_or_default();
    assert!(
        route.contains("gpt-6-luna") && route.contains("high"),
        "{route}"
    );
    click(&mut world, "crew.launch.retry");
    assert_eq!(
        sets(&world)[1],
        json!({"expected_revision": 8, "agent": "codex", "model": "gpt-6-luna", "effort": "high"})
    );
    assert_eq!(launch_status(&world).as_deref(), Some("Saved"));

    open_route(&mut world, "Crew launch model");
    preview_harness(&mut world, "Claude");
    pick_model(&mut world, "Claude");
    close_picker(&mut world);
    assert_eq!(launch_status(&world).as_deref(), Some("Not saved"));
    click(&mut world, "crew.launch.retry");
    assert_eq!(
        sets(&world)[3],
        json!({"expected_revision": 10, "agent": "claude", "model": "", "effort": ""})
    );
    assert_eq!(launch_status(&world).as_deref(), Some("Saved"));
}

fn route_shows(world: &World, member: &str) -> String {
    world
        .screen()
        .get(Target::key(format!("settings.route.crew.{member}")))
        .value
        .unwrap_or_default()
}

fn serve_models(world: &World, models: Vec<attn_protocol::HarnessModel>) {
    world.daemon.on("harness_models", move |r| {
        let harness = r["harness"].as_str().unwrap_or_default().to_owned();
        let listed = models
            .iter()
            .filter(|m| m.harness == harness)
            .cloned()
            .collect();
        message(reported(&harness, &Settings::default(), listed))
    });
}

#[test]
fn crew_default_writes_one_cleared_selection_and_close_stays_available_while_it_saves() {
    let d = daemon(
        vec![in_session(
            routed(member("alder", 7), "codex", "gpt-6-astra", "high"),
            "session-alder",
        )],
        vec![session("session-alder", "codex")],
        Vec::new(),
    );
    d.answer_in_turn("crew_set", [Answer::Hold]);
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    preview_harness(&mut world, "Crew default");
    assert_eq!(
        sets(&world),
        [json!({"expected_revision": 7, "agent": "", "model": "", "effort": ""})]
    );
    assert!(enabled(&world, "crew.close"));
    let write = world.daemon.sent_of("crew_set").remove(0);
    world.daemon.reply_to(
        &write,
        CrewSetResultMessage {
            success: true,
            member: Some(in_session(member("alder", 8), "session-alder")),
            ..CrewSetResultMessage::new()
        },
    );
    world.settle();
    if picker_open(&world) {
        close_picker(&mut world);
    }
    assert!(enabled(&world, "crew.close"));
    assert!(
        route_shows(&world, "alder").contains("Crew default"),
        "{}",
        route_shows(&world, "alder")
    );
}

#[test]
fn a_failed_member_edit_survives_roster_navigation_and_retries() {
    let d = daemon(
        vec![member("alder", 2), member("keel", 3)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            Answer::Reply(message(CrewSetResultMessage {
                success: false,
                error: Some("model discovery is unavailable".into()),
                ..CrewSetResultMessage::new()
            })),
            saved(routed(member("alder", 3), "codex", "", "")),
        ],
    );
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    preview_harness(&mut world, "Codex");
    pick_model(&mut world, "Codex");
    if picker_open(&world) {
        close_picker(&mut world);
    }
    assert_eq!(launch_status(&world).as_deref(), Some("Not saved"));
    assert_eq!(
        named(&world, "crew.launch.error").as_deref(),
        Some("model discovery is unavailable")
    );
    click(&mut world, "crew.roster.keel");
    click(&mut world, "crew.roster.alder");
    assert!(
        route_shows(&world, "alder").contains("Codex"),
        "{}",
        route_shows(&world, "alder")
    );
    click(&mut world, "crew.launch.retry");
    assert_eq!(world.daemon.sent_of("crew_set").len(), 2);
    assert_eq!(launch_status(&world).as_deref(), Some("Saved"));
}

#[test]
fn the_roster_stays_navigable_while_the_harness_list_is_read() {
    let d = daemon(
        vec![member("alder", 2), member("keel", 3)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("delegation_preferences_get", [Answer::Hold]);
    let mut world = World::start(d);
    open(&mut world);
    click(&mut world, "crew.roster.keel");
    assert_eq!(heading(&world).as_deref(), Some("Keel"));
    let read = world.daemon.sent_of("delegation_preferences_get").remove(0);
    world.daemon.reply_to(
        &read,
        preferences_result(vec![harness("claude", "Claude Code")]),
    );
    world.settle();
    assert!(enabled(&world, "settings.route.crew.keel"));
    assert_eq!(heading(&world).as_deref(), Some("Keel"));
    assert_eq!(world.daemon.sent_of("delegation_preferences_get").len(), 1);
}

#[test]
fn a_model_and_an_effort_pin_while_the_harness_follows_the_crew_default() {
    let d = daemon(
        vec![with(member("keel", 5), |m| {
            m.resolved_agent = "codex".into()
        })],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            saved(with(
                routed(member("keel", 6), "", "gpt-6-astra", ""),
                |m| m.agent = None,
            )),
            saved(routed(member("keel", 7), "", "gpt-6-astra", "high")),
        ],
    );
    let mut world = World::start_sized(d, (1200.0, 1000.0));
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "gpt-6-astra");
    if picker_open(&world) {
        close_picker(&mut world);
    }
    open_route(&mut world, "Crew launch model");
    super::settings::harness_catalogs::pick_effort(&mut world, "high");
    if picker_open(&world) {
        close_picker(&mut world);
    }
    assert_eq!(
        sets(&world),
        [
            json!({"expected_revision": 5, "agent": "", "model": "gpt-6-astra", "effort": ""}),
            json!({"expected_revision": 6, "agent": "", "model": "gpt-6-astra", "effort": "high"}),
        ]
    );
    assert!(route_shows(&world, "keel").contains("gpt-6-astra"));
}

#[test]
fn a_typed_model_id_goes_on_return_as_one_write_and_the_draft_outlasts_roster_pushes() {
    let d = daemon(
        vec![member("keel", 6)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [saved(routed(member("keel", 8), "", "gpt-7", ""))],
    );
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    click(&mut world, "settings.route-picker.filter");
    world.gesture(|app| app.type_text("gpt-7"));
    roster_push(&mut world, vec![member("keel", 7)]);
    assert_eq!(
        world
            .screen()
            .get(Target::key("settings.route-picker.filter"))
            .value
            .as_deref(),
        Some("gpt-7")
    );
    assert!(sets(&world).is_empty());
    press(&mut world, Key::Enter);
    assert_eq!(
        sets(&world),
        [json!({"expected_revision": 7, "agent": "", "model": "gpt-7", "effort": ""})]
    );
}

#[test]
fn the_model_list_waits_while_an_explicit_harness_pin_clears() {
    let d = daemon(
        vec![routed(member("keel", 5), "codex", "gpt-6-astra", "")],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_set", [Answer::Hold]);
    let mut world = World::start(d);
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    preview_harness(&mut world, "Crew default");
    if !picker_open(&world) {
        open_route(&mut world, "Crew launch model");
    }
    let model_list = |world: &World| {
        world
            .screen()
            .find(Target::key("settings.route-picker.models"))
            .is_some()
    };
    assert!(!model_list(&world));
    let write = world.daemon.sent_of("crew_set").remove(0);
    world.daemon.reply_to(
        &write,
        CrewSetResultMessage {
            success: true,
            member: Some(with(member("keel", 6), |m| {
                m.resolved_agent = "claude".into()
            })),
            ..CrewSetResultMessage::new()
        },
    );
    world.settle();
    assert!(model_list(&world));
}

#[test]
fn a_provider_qualified_model_is_picked_when_bare_ids_collide() {
    let d = daemon(
        vec![with(member("keel", 5), |m| {
            m.agent = Some("codex".into());
            m.resolved_agent = "codex".into();
        })],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [saved(routed(
            member("keel", 6),
            "codex",
            "second/shared",
            "",
        ))],
    );
    let mut world = World::start(d);
    let shared = |provider: &str, name: &str, effort: &str| attn_protocol::HarnessModel {
        provider: provider.into(),
        ..super::settings::harness_catalogs::model("codex", "shared", name, None, &[effort])
    };
    serve_models(
        &world,
        vec![
            shared("first", "Shared one", "low"),
            shared("second", "Shared two", "high"),
        ],
    );
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "Shared two");
    assert_eq!(
        sets(&world),
        [json!({"expected_revision": 5, "agent": "codex", "model": "second/shared", "effort": ""})]
    );
    if picker_open(&world) {
        close_picker(&mut world);
    }
    assert!(
        route_shows(&world, "keel").contains("Shared two"),
        "{}",
        route_shows(&world, "keel")
    );
}

#[test]
fn an_unsupported_models_cleared_effort_stays_cleared_through_a_conflict() {
    let d = daemon(
        vec![with(member("keel", 5), |m| {
            m.agent = Some("codex".into());
            m.resolved_agent = "codex".into();
        })],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_set",
        [
            conflicted(routed(member("keel", 6), "codex", "", "high")),
            saved(routed(member("keel", 7), "codex", "local/fixed", "")),
        ],
    );
    let mut world = World::start(d);
    serve_models(
        &world,
        vec![attn_protocol::HarnessModel {
            provider: "local".into(),
            ..super::settings::harness_catalogs::model("codex", "fixed", "Fixed", None, &[])
        }],
    );
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    pick_model(&mut world, "Fixed");
    if picker_open(&world) {
        close_picker(&mut world);
    }
    assert_eq!(launch_status(&world).as_deref(), Some("Not saved"));
    click(&mut world, "crew.launch.retry");
    assert_eq!(
        sets(&world)[1],
        json!({"expected_revision": 6, "agent": "codex", "model": "local/fixed", "effort": ""})
    );
    assert_eq!(launch_status(&world).as_deref(), Some("Saved"));
}

fn on_tab(world: &World, tab: &str) -> bool {
    world
        .screen()
        .get(Target::key(format!("crew.tab.{tab}")))
        .is("checked")
}

#[test]
fn a_normal_reopen_returns_to_the_first_member_on_launch_settings() {
    let mut world = start(vec![member("alder", 2), member("trellis", 3)]);
    open(&mut world);
    click(&mut world, "crew.roster.trellis");
    click(&mut world, "crew.tab.handoffs");
    assert!(on_tab(&world, "handoffs"));
    press(&mut world, Key::Escape);
    assert!(!is_open(&world));
    open(&mut world);
    assert_eq!(heading(&world).as_deref(), Some("Alder"));
    assert!(on_tab(&world, "launch"));
}

#[test]
fn a_failed_save_on_leaving_the_charter_stays_in_view_and_retry_writes_the_kept_edit() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_charter_get", [charter_read("fern", "old", "old")]);
    d.answer_in_turn(
        "crew_charter_set",
        [
            Answer::Reply(message(CrewCharterSetResultMessage {
                success: false,
                error: Some("disk is read-only".into()),
                ..CrewCharterSetResultMessage::new()
            })),
            charter_saved("fern", "old local edit", "new"),
        ],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text(" local edit"));
    click(&mut world, "crew.tab.launch");
    let error = named(&world, "crew.charter.error").unwrap_or_default();
    assert!(error.contains("disk is read-only"), "{error}");
    assert_eq!(editor(&world), "old local edit");
    assert!(on_tab(&world, "charter"));
    click(&mut world, "crew.charter.retry");
    assert_eq!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
    click(&mut world, "crew.tab.launch");
    assert!(on_tab(&world, "launch"));
}

#[test]
fn an_offline_charter_edit_outlasts_closing_and_reopening_manage_crew() {
    let d = daemon(
        vec![member("fern", 1)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn("crew_charter_get", [charter_read("fern", "old", "old")]);
    let mut world = World::start(d);
    on_charter(&mut world);
    super::ledger::hang_up(&mut world);
    click(&mut world, "crew.charter.editor");
    world.gesture(|app| app.press("mod+down"));
    world.gesture(|app| app.type_text(" offline edit"));
    click(&mut world, "crew.tab.launch");
    assert!(named(&world, "crew.charter.error").is_some());

    press(&mut world, Key::Escape);
    if is_open(&world) {
        press(&mut world, Key::Escape);
    }
    assert!(!is_open(&world));
    open(&mut world);
    assert!(on_tab(&world, "launch"));
    click(&mut world, "crew.tab.charter");
    assert_eq!(editor(&world), "old offline edit");
    assert_ne!(
        named(&world, "crew.charter.status").as_deref(),
        Some("Saved")
    );
}

#[test]
fn entering_the_charter_tab_again_reads_the_saved_charter_again() {
    let d = daemon(
        vec![member("trellis", 4)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.answer_in_turn(
        "crew_charter_get",
        [
            charter_read("trellis", "first", "first"),
            charter_read("trellis", "edited elsewhere", "second"),
        ],
    );
    let mut world = World::start(d);
    on_charter(&mut world);
    assert_eq!(editor(&world), "first");
    click(&mut world, "crew.tab.launch");
    click(&mut world, "crew.tab.charter");
    assert_eq!(editor(&world), "edited elsewhere");
    assert_eq!(world.daemon.sent_of("crew_charter_get").len(), 2);
}

#[test]
fn the_inner_escape_commits_a_launch_field_and_the_next_closes_manage_crew() {
    let d = daemon(
        vec![member("keel", 6)],
        vec![session("s1", "claude")],
        Vec::new(),
    );
    d.on("crew_set", |r| {
        let mut m = member("keel", 7);
        m.model = r["model"].as_str().map(str::to_owned);
        m.effort = r["effort"].as_str().map(str::to_owned);
        message(CrewSetResultMessage {
            success: true,
            member: Some(m),
            ..CrewSetResultMessage::new()
        })
    });
    let mut world = World::start_sized(d, (1200.0, 1000.0));
    open(&mut world);
    open_route(&mut world, "Crew launch model");
    click(&mut world, "settings.route-picker.filter");
    world.gesture(|app| app.type_text("unlisted"));
    click(&mut world, "settings.route-picker.manual");
    let before = world.daemon.sent_of("crew_set").len();
    click(&mut world, "settings.route-picker.effort");
    world.gesture(|app| app.type_text("high"));
    press(&mut world, Key::Escape);
    assert!(is_open(&world));
    let sent = world.daemon.sent_of("crew_set");
    assert_eq!(sent.len(), before + 1);
    assert_eq!(sent.last().unwrap()["effort"], "high");
    if picker_open(&world) {
        press(&mut world, Key::Escape);
    }
    press(&mut world, Key::Escape);
    assert!(!is_open(&world));
}

fn letter_opens_its_seed_link(fern: CrewMember) -> Value {
    let d = daemon(vec![fern], vec![session("s-fern", "claude")], Vec::new());
    d.on("crew_handoffs_get", |_| {
        message(CrewHandoffsGetResultMessage {
            success: true,
            member: Some("fern".into()),
            handoffs: vec![letter("2026-09-01T21-37Z-fern.md", "2026-09-01T21:37:00Z")],
            ..CrewHandoffsGetResultMessage::new()
        })
    });
    d.on("crew_handoff_get", |_| {
        letter_read(
            "2026-09-01T21-37Z-fern.md",
            "# Full handoff\n\nA paragraph at the end that must not be truncated.\n\n[Open the seed](s-w0rk11)\n",
        )
    });
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
    click(&mut world, "crew.tab.handoffs");
    let reader = world
        .screen()
        .descendants(Target::key("crew.handoff.letter"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect::<Vec<_>>()
        .join("\n");
    assert!(
        reader.contains("A paragraph at the end that must not be truncated."),
        "{reader}"
    );
    world.gesture(|app| app.click(Target::role("link").name("Open the seed")));
    let opened = world.daemon.sent_of("open_seed");
    assert_eq!(opened.len(), 1);
    assert_eq!(opened[0]["seed_id"], "s-w0rk11");
    opened[0].clone()
}

#[test]
fn a_seed_link_in_a_letter_opens_the_seed_as_a_standalone_reader_for_a_member_between_sessions() {
    let opened = letter_opens_its_seed_link(member("fern", 1));
    assert_eq!(opened["standalone"], true);
    assert!(opened.get("session_id").is_none_or(Value::is_null));
}

#[test]
fn a_seed_link_in_a_letter_opens_the_seed_beside_the_session_of_a_member_in_session() {
    let opened = letter_opens_its_seed_link(in_session(member("fern", 1), "s-fern"));
    assert_eq!(opened["session_id"], "s-fern");
    assert!(opened.get("standalone").is_none_or(Value::is_null));
}
