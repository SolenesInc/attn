use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    BrowseDirectoryResultMessage, CreateWorktreeResultMessage, DelegationPreferences,
    DelegationPreferencesResultMessage, DeleteWorktreeResultMessage, Desktop, DesktopPane,
    DirectoryEntry, GetRepoInfoResultMessage, Harness, InitialStateMessage,
    InspectPathResultMessage, PathInspection, Profile, ProfileArrangementChangedMessage,
    RecentLocation, RecentLocationsResultMessage, RepoInfo, SettingsUpdatedMessage,
    SpawnResultMessage, Worktree,
};
use attn_testkit::{Daemon, Node, Request, Target, World, key_event, message};
use rune::{Key, KeyEvent, KeyPlatform, Mods};
use serde_json::Value;

const HOME: &str = "/home/me";

fn pane(desktop: &str, id: &str) -> DesktopPane {
    DesktopPane {
        desktop_id: desktop.into(),
        pane_id: id.into(),
        runtime_id: format!("rt-{id}"),
        session_id: format!("s-{id}"),
        title: format!("Agent {id}"),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktop(id: &str, name: &str, panes: Vec<DesktopPane>) -> Desktop {
    Desktop {
        id: id.into(),
        name: name.into(),
        active_pane_id: panes.first().map(|p| p.pane_id.clone()).unwrap_or_default(),
        panes,
        profile_id: "work".into(),
        ..Desktop::default()
    }
}

fn initial(desktops: Vec<Desktop>, settings: &[(&str, &str)]) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: desktops[0].id.clone(),
            ..Profile::default()
        }]),
        desktops: Some(desktops),
        settings: Some(
            settings
                .iter()
                .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
                .collect::<BTreeMap<_, _>>(),
        ),
        ..InitialStateMessage::new()
    }
}

fn recent(path: &str) -> RecentLocation {
    RecentLocation {
        path: path.into(),
        last_seen: "2026-11-01T10:00:00Z".into(),
        use_count: 1,
        ..RecentLocation::default()
    }
}

fn catalog(harnesses: &[&str]) -> Value {
    message(DelegationPreferencesResultMessage {
        success: true,
        preferences: Some(DelegationPreferences::default()),
        harnesses: Some(
            harnesses
                .iter()
                .map(|id| Harness {
                    id: (*id).into(),
                    name: (*id).into(),
                    available: true,
                    ..Harness::default()
                })
                .collect(),
        ),
        ..DelegationPreferencesResultMessage::new()
    })
}

fn recents(paths: &[&str]) -> Value {
    message(RecentLocationsResultMessage {
        success: true,
        home_path: Some(HOME.into()),
        recent_locations: paths.iter().map(|p| recent(p)).collect(),
        ..RecentLocationsResultMessage::new()
    })
}

/// What the daemon says of a path: a folder outside any repository, unless `missing`.
fn folder(request: &Request) -> Value {
    let path = request["path"].as_str().unwrap().to_owned();
    message(InspectPathResultMessage {
        success: true,
        inspection: Some(PathInspection {
            exists: true,
            is_directory: true,
            input_path: path.clone(),
            resolved_path: path,
            home_path: Some(HOME.into()),
            ..PathInspection::default()
        }),
        ..InspectPathResultMessage::new()
    })
}

fn placed(request: &Request) -> Value {
    message(SpawnResultMessage {
        id: request["id"].as_str().unwrap().into(),
        success: true,
        desktop_id: request["placement"]["desktop_id"]
            .as_str()
            .map(str::to_owned),
        pane_id: Some("p-new".into()),
        ..SpawnResultMessage::new()
    })
}

fn listing(directory: &str, children: &[&str]) -> Value {
    message(BrowseDirectoryResultMessage {
        success: true,
        directory: directory.into(),
        home_path: Some(HOME.into()),
        entries: children
            .iter()
            .map(|name| DirectoryEntry {
                name: (*name).into(),
                path: format!("{}/{name}", directory.trim_end_matches('/')),
                is_dir: true,
                ..DirectoryEntry::default()
            })
            .collect(),
        ..BrowseDirectoryResultMessage::new()
    })
}

/// A daemon that knows two harnesses, lists `recent` paths, browses nothing, says every path
/// is a plain folder, places every spawn, and keeps the settings the app sets.
fn daemon(state: InitialStateMessage, recent: &'static [&'static str]) -> Daemon {
    let settings = Arc::new(Mutex::new(state.settings.clone().unwrap_or_default()));
    let daemon = Daemon::scripted().state(state);
    daemon
        .on("set_setting", move |r| {
            let mut settings = settings.lock().unwrap();
            let key = r["key"].as_str().unwrap().to_owned();
            settings.insert(key.clone(), r["value"].as_str().unwrap().to_owned());
            message(SettingsUpdatedMessage {
                changed_key: Some(key),
                settings: Some(settings.clone()),
                ..SettingsUpdatedMessage::new()
            })
        })
        .on("delegation_preferences_get", |_| {
            catalog(&["claude", "codex"])
        })
        .on("get_recent_locations", move |_| recents(recent))
        .on("browse_directory", |r| {
            listing(r["input_path"].as_str().unwrap(), &[])
        })
        .on("inspect_path", folder)
        .on("spawn_session", placed);
    daemon
}

/// A desktop with one agent on it, so ⌘N opens the dialog over it.
fn busy_desktop(recent: &'static [&'static str]) -> World {
    busy_desktop_with(recent, &[])
}

fn busy_desktop_with(recent: &'static [&'static str], settings: &[(&str, &str)]) -> World {
    let mut world = World::start(daemon(
        initial(
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
            settings,
        ),
        recent,
    ));
    world.open_desktop();
    world
}

/// Holds every `cmd` the app sends, unanswered, for the test to answer with `reply_to`.
fn hold(world: &World, cmd: &str) -> Arc<Mutex<Vec<Request>>> {
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    world.daemon.on(cmd, move |r| {
        keep.lock().unwrap().push(r.clone());
    });
    held
}

fn agents(world: &World) -> Vec<(String, bool, bool)> {
    world
        .screen()
        .all(Target::role("radio-button"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("new-session.agent."))
        })
        .map(|n| {
            (
                n.name.clone().unwrap_or_default(),
                n.is("checked"),
                n.is("disabled"),
            )
        })
        .collect()
}

fn chosen(world: &World) -> String {
    agents(world)
        .into_iter()
        .find(|(_, checked, _)| *checked)
        .map(|(name, ..)| name)
        .unwrap_or_default()
}

fn agent_names(world: &World) -> Vec<String> {
    agents(world).into_iter().map(|(name, ..)| name).collect()
}

fn saved(world: &World, key: &str) -> Vec<String> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .filter(|r| r["key"] == key)
        .map(|r| r["value"].as_str().unwrap().to_owned())
        .collect()
}

fn yolo(world: &World) -> Node {
    world.screen().get(Target::key("new-session.yolo"))
}

fn launch_typed(world: &mut World, path: &str) -> Request {
    type_path(world, path);
    press(world, Key::Enter);
    spawns(world).pop().expect("the picker launched")
}

fn open_dialog(world: &mut World) {
    press_action(world, &Action::SessionNew);
    assert!(
        dialog_open(world),
        "the new-session key opened no picker:\n{}",
        world.screen()
    );
}

fn press_action(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
    world.gesture(|app| app.key(ev));
}

fn dialog_open(world: &World) -> bool {
    world.screen().find(Target::key("new-session")).is_some()
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn type_path(world: &mut World, text: &str) {
    world.gesture(|app| app.type_text(text));
    world.advance(Duration::from_millis(200));
}

fn field_text(world: &World, which: &str) -> String {
    world
        .screen()
        .get(Target::key(format!("{which}.path.field")))
        .value
        .unwrap_or_default()
}

fn highlighted(world: &World, which: &str) -> Option<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with(&format!("{which}.row.")))
        })
        .find(|n| n.is("selected"))
        .and_then(|n| n.description.or(n.name))
}

fn sent(world: &World, cmd: &str, field: &str) -> Vec<String> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r[field].as_str().unwrap_or_default().to_owned())
        .collect()
}

fn spawns(world: &World) -> Vec<Request> {
    world.daemon.sent_of("spawn_session")
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_picker_asks_for_this_machines_recents_and_opens_the_top_one_on_a_bare_enter() {
    let mut world = busy_desktop(&["/home/me/src/attn", "/home/me/src/rune"]);
    open_dialog(&mut world);

    let asked = world.daemon.sent_of("get_recent_locations");
    assert_eq!(asked.len(), 1);
    assert_eq!(asked[0]["home_only"], true);
    assert!(shows_row(&world, "attn", "~/src/attn"));
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/attn")
    );

    press(&mut world, Key::Enter);

    assert_eq!(sent(&world, "inspect_path", "path"), ["/home/me/src/attn"]);
    let spawned = spawns(&world);
    assert_eq!(spawned.len(), 1);
    assert_eq!(spawned[0]["cwd"], "/home/me/src/attn");
    assert_eq!(spawned[0]["agent"], "claude");
    assert_eq!(spawned[0]["label"], "attn");
    assert_eq!(spawned[0]["placement"]["desktop_id"], "d1");
    assert_eq!(spawned[0]["placement"]["anchor_pane_id"], "p1");
    assert_eq!(spawned[0]["placement"]["direction"], "vertical");
    assert_eq!(spawned[0]["spawned_from"], "s-p1");
    assert!(spawned[0].get("yolo_mode").is_none());
    assert!(!dialog_open(&world));
    rest(&mut world);
}

fn row_names(world: &World, which: &str) -> Vec<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with(&format!("{which}.row.")))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn shows_row(world: &World, name: &str, path: &str) -> bool {
    world
        .screen()
        .all(Target::role("list-box-option").name(name))
        .iter()
        .any(|n| n.description.as_deref() == Some(path))
}

#[test]
fn escape_closes_the_picker_while_the_highlight_is_the_automatic_one() {
    let mut world = busy_desktop(&["/home/me/src/attn"]);
    open_dialog(&mut world);
    assert!(highlighted(&world, "new-session").is_some());

    press(&mut world, Key::Escape);

    assert!(!dialog_open(&world));
    assert!(world.daemon.sent_of("inspect_path").is_empty());
    rest(&mut world);
}

#[test]
fn a_highlight_the_arrows_made_goes_on_the_first_escape_and_enter_opens_it() {
    let mut world = busy_desktop(&["/home/me/src/attn", "/home/me/src/rune"]);
    open_dialog(&mut world);

    press(&mut world, Key::Down);
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/rune")
    );
    press(&mut world, Key::Escape);
    assert!(dialog_open(&world));
    assert_eq!(highlighted(&world, "new-session"), None);

    press(&mut world, Key::Up);
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/rune")
    );
    press(&mut world, Key::Up);
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/attn")
    );
    press(&mut world, Key::Up);
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/rune")
    );
    press(&mut world, Key::Enter);

    assert_eq!(sent(&world, "inspect_path", "path"), ["/home/me/src/rune"]);
    assert_eq!(spawns(&world).len(), 1);
    rest(&mut world);
}

#[test]
fn typing_drops_the_automatic_highlight_so_enter_opens_the_typed_path() {
    let mut world = busy_desktop(&["/home/me/src/attn"]);
    open_dialog(&mut world);

    type_path(&mut world, "/tmp/elsewhere");
    assert_eq!(highlighted(&world, "new-session"), None);
    press(&mut world, Key::Enter);

    assert_eq!(sent(&world, "inspect_path", "path"), ["/tmp/elsewhere"]);
    assert_eq!(spawns(&world)[0]["cwd"], "/tmp/elsewhere");
    assert_eq!(spawns(&world)[0]["label"], "elsewhere");
    rest(&mut world);
}

#[test]
fn browsing_asks_once_for_the_path_the_user_settles_on() {
    let mut world = busy_desktop(&[]);
    open_dialog(&mut world);

    world.gesture(|app| app.type_text("/tm"));
    world.advance(Duration::from_millis(100));
    world.gesture(|app| app.type_text("p/"));
    world.advance(Duration::from_millis(200));

    assert_eq!(sent(&world, "browse_directory", "input_path"), ["/tmp/"]);
    rest(&mut world);
}

#[test]
fn a_browse_pending_when_the_picker_closes_is_never_sent() {
    let mut world = busy_desktop(&[]);
    open_dialog(&mut world);

    world.gesture(|app| app.type_text("/tmp/"));
    press(&mut world, Key::Escape);
    world.advance(Duration::from_millis(500));

    assert!(world.daemon.sent_of("browse_directory").is_empty());
    rest(&mut world);
}

#[test]
fn a_folder_ending_in_a_slash_lists_its_folders_and_a_click_launches_one() {
    let mut world = busy_desktop(&[]);
    world.daemon.on("browse_directory", |r| {
        listing(r["input_path"].as_str().unwrap(), &["alpha", "beta"])
    });
    open_dialog(&mut world);

    type_path(&mut world, "/srv/");
    assert_eq!(row_names(&world, "new-session"), ["alpha", "beta"]);

    world.gesture(|app| app.click(Target::key("new-session.row.1")));

    assert_eq!(sent(&world, "inspect_path", "path"), ["/srv/beta"]);
    assert_eq!(spawns(&world)[0]["cwd"], "/srv/beta");
    rest(&mut world);
}

#[test]
fn says_when_nothing_matches_a_typed_path() {
    let mut world = busy_desktop(&[]);
    open_dialog(&mut world);

    type_path(&mut world, "/nowhere/x");

    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.empty"))
            .name
            .as_deref(),
        Some("No matches. Press Enter to use path directly.")
    );
    rest(&mut world);
}

#[test]
fn tab_takes_the_ghost_completion_or_the_highlighted_row() {
    let mut world = busy_desktop(&[]);
    world.daemon.on("browse_directory", |r| {
        if r["input_path"] == "/srv/a" {
            listing("/srv/", &["alpha"])
        } else {
            listing("/srv/", &["alpha", "beta"])
        }
    });
    open_dialog(&mut world);

    type_path(&mut world, "/srv/a");
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.ghost"))
            .name
            .as_deref(),
        Some("lpha")
    );
    press(&mut world, Key::Tab);
    assert_eq!(field_text(&world, "new-session"), "/srv/alpha");

    world.gesture(|app| app.type_text("/"));
    world.advance(Duration::from_millis(200));
    press(&mut world, Key::Down);
    press(&mut world, Key::Down);
    assert_eq!(field_text(&world, "new-session"), "/srv/alpha/");
    press(&mut world, Key::Tab);
    assert_eq!(highlighted(&world, "new-session"), None);
    assert_eq!(field_text(&world, "new-session"), "/srv/beta");
    rest(&mut world);
}

#[test]
fn trailing_slashes_and_home_are_resolved_before_inspecting_and_the_root_stays_the_root() {
    for (typed, inspected) in [
        ("/tmp/project/", "/tmp/project"),
        ("~/", HOME),
        ("~/src", "/home/me/src"),
        ("/", "/"),
    ] {
        let mut world = busy_desktop(&["/home/me/src/attn"]);
        open_dialog(&mut world);
        type_path(&mut world, typed);
        press(&mut world, Key::Enter);
        assert_eq!(sent(&world, "inspect_path", "path"), [inspected], "{typed}");
        assert_eq!(spawns(&world)[0]["cwd"], inspected, "{typed}");
        rest(&mut world);
    }
}

#[test]
fn a_missing_directory_says_so_and_launches_nothing() {
    let mut world = busy_desktop(&[]);
    world.daemon.on("inspect_path", |r| {
        message(InspectPathResultMessage {
            success: true,
            inspection: Some(PathInspection {
                exists: false,
                input_path: r["path"].as_str().unwrap().into(),
                ..PathInspection::default()
            }),
            ..InspectPathResultMessage::new()
        })
    });
    open_dialog(&mut world);

    type_path(&mut world, "/gone");
    press(&mut world, Key::Enter);

    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.error"))
            .name
            .as_deref(),
        Some("Directory not found: /gone")
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.operation"))
            .is_none()
    );
    assert!(spawns(&world).is_empty());
    assert!(dialog_open(&world));
    rest(&mut world);
}

#[test]
fn an_inspection_answered_after_the_user_typed_another_path_is_dropped() {
    let mut world = busy_desktop(&[]);
    let held: Arc<Mutex<Option<Request>>> = Arc::default();
    let hold = held.clone();
    world.daemon.on("inspect_path", move |r| {
        if r["path"] == "/first" {
            *hold.lock().unwrap() = Some(r.clone());
            None
        } else {
            Some(folder(r))
        }
    });
    open_dialog(&mut world);
    type_path(&mut world, "/first");
    press(&mut world, Key::Enter);
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.operation"))
            .name
            .as_deref(),
        Some("Inspecting path")
    );

    type_path(&mut world, "x");
    let request = held
        .lock()
        .unwrap()
        .take()
        .expect("the first path was inspected");
    world.daemon.reply_to(&request, folder(&request));
    world.settle();

    assert!(spawns(&world).is_empty());
    assert!(world.daemon.sent_of("get_repo_info").is_empty());
    assert!(dialog_open(&world));
    rest(&mut world);
}

#[test]
fn offers_the_built_in_agents_when_the_daemon_says_nothing_and_starts_on_claude() {
    let mut world = busy_desktop(&[]);
    world.daemon.on("delegation_preferences_get", |_| {
        catalog(&["claude", "codex", "plug"])
    });
    open_dialog(&mut world);

    assert_eq!(
        agents(&world),
        [
            ("Terminal".into(), false, false),
            ("Claude".into(), true, false),
            ("Codex".into(), false, false),
            ("Plug".into(), false, true),
        ]
    );
    rest(&mut world);
}

#[test]
fn offers_the_agents_the_daemon_advertises_and_falls_back_from_a_missing_one() {
    let mut world = busy_desktop_with(
        &[],
        &[
            ("claude_available", "false"),
            ("gemini-cli_available", "true"),
            ("plug_available", "true"),
        ],
    );
    world.daemon.on("delegation_preferences_get", |_| {
        catalog(&["claude", "codex", "gemini-cli"])
    });
    open_dialog(&mut world);

    assert_eq!(
        agents(&world),
        [
            ("Terminal".into(), false, false),
            ("Claude".into(), false, true),
            ("Codex".into(), true, false),
            ("Gemini Cli".into(), false, false),
            ("Plug".into(), false, false),
        ]
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.agent.claude"))
            .description
            .as_deref(),
        Some("Claude is not available on this machine")
    );
    rest(&mut world);
}

#[test]
fn a_plugin_display_name_is_shown_on_its_stored_harness_control() {
    let mut world = busy_desktop_with(
        &[],
        &[
            ("snipe_available", "true"),
            ("snipe_display_name", "Xirp Agent"),
        ],
    );
    open_dialog(&mut world);
    assert!(agent_names(&world).contains(&"Xirp Agent".into()));
    let _ = world.screen().get(Target::key("new-session.agent.snipe"));
    rest(&mut world);
}

#[test]
fn a_saved_agent_that_is_missing_gives_way_to_the_first_one_there_or_terminal_when_none_is() {
    let mut world = busy_desktop_with(
        &[],
        &[
            ("new_session_agent", "claude"),
            ("claude_available", "false"),
            ("codex_available", "false"),
            ("plug_available", "true"),
        ],
    );
    open_dialog(&mut world);
    assert_eq!(chosen(&world), "Plug");
    rest(&mut world);

    let mut world = busy_desktop_with(
        &[],
        &[("claude_available", "false"), ("codex_available", "false")],
    );
    open_dialog(&mut world);
    assert_eq!(chosen(&world), "Terminal");
    rest(&mut world);
}

#[test]
fn launches_an_available_plugin_the_harness_catalog_does_not_list() {
    let mut world = busy_desktop_with(&[], &[("interactive_available", "true")]);
    open_dialog(&mut world);
    assert_eq!(
        agent_names(&world),
        ["Terminal", "Claude", "Codex", "Interactive"]
    );

    world.gesture(|app| app.press("alt+3"));
    assert_eq!(chosen(&world), "Interactive");
    let spawned = launch_typed(&mut world, "/srv/app");

    assert_eq!(spawned["agent"], "interactive");
    rest(&mut world);
}

#[test]
fn remembers_the_agent_the_user_picks_for_the_next_picker() {
    let mut world = busy_desktop(&[]);
    open_dialog(&mut world);

    world.gesture(|app| app.click(Target::key("new-session.agent.codex")));
    assert_eq!(chosen(&world), "Codex");
    assert_eq!(saved(&world, "new_session_agent"), ["codex"]);
    press(&mut world, Key::Escape);
    open_dialog(&mut world);

    assert_eq!(chosen(&world), "Codex");
    rest(&mut world);
}

#[test]
fn alt_and_a_digit_switch_agents_but_never_to_one_that_is_not_there() {
    let mut world = busy_desktop_with(&[], &[("claude_available", "false")]);
    open_dialog(&mut world);
    assert_eq!(chosen(&world), "Codex");

    world.gesture(|app| app.press("alt+1"));
    assert_eq!(chosen(&world), "Codex");
    world.gesture(|app| app.press("alt+2"));
    assert_eq!(chosen(&world), "Codex");
    world.gesture(|app| app.press("alt+t"));
    assert_eq!(chosen(&world), "Terminal");
    world.gesture(|app| app.press("alt+2"));
    assert_eq!(chosen(&world), "Codex");
    assert!(
        saved(&world, "new_session_agent")
            .iter()
            .all(|a| a == "codex")
    );
    assert_eq!(field_text(&world, "new-session"), "");
    rest(&mut world);
}

#[test]
fn terminal_on_alt_t_launches_a_shell_without_a_label_or_becoming_the_saved_agent() {
    let mut world = busy_desktop(&[]);
    open_dialog(&mut world);

    world.gesture(|app| app.press("alt+t"));
    let spawned = launch_typed(&mut world, "/srv/app");

    assert_eq!(spawned["agent"], "shell");
    assert!(spawned.get("label").is_none_or(|l| l.is_null() || l == ""));
    assert!(saved(&world, "new_session_agent").is_empty());
    rest(&mut world);
}

#[test]
fn yolo_turns_on_with_alt_q_or_a_click_is_remembered_and_launches_with_it() {
    let mut world = busy_desktop_with(&[], &[("claude_cap_yolo", "true")]);
    open_dialog(&mut world);
    assert!(!yolo(&world).is("checked"));
    assert_eq!(
        yolo(&world).description.as_deref(),
        Some("Run the agent without asking for permission")
    );

    world.gesture(|app| app.press("alt+q"));
    assert!(yolo(&world).is("checked"));
    world.gesture(|app| app.click(Target::key("new-session.yolo")));
    assert!(!yolo(&world).is("checked"));
    world.gesture(|app| app.click(Target::key("new-session.yolo")));
    assert_eq!(
        saved(&world, "new_session_yolo_local"),
        ["true", "false", "true"]
    );
    press(&mut world, Key::Escape);
    open_dialog(&mut world);
    assert!(yolo(&world).is("checked"));

    let spawned = launch_typed(&mut world, "/srv/app");
    assert_eq!(spawned["yolo_mode"], true);
    rest(&mut world);
}

#[test]
fn yolo_stays_off_for_an_agent_that_does_not_support_it_whatever_was_remembered() {
    let mut world = busy_desktop_with(&[], &[("new_session_yolo_local", "true")]);
    open_dialog(&mut world);
    assert!(yolo(&world).is("disabled"));
    assert!(!yolo(&world).is("checked"));
    assert_eq!(
        yolo(&world).description.as_deref(),
        Some("Claude does not support yolo mode")
    );

    world.gesture(|app| app.press("alt+q"));
    let spawned = launch_typed(&mut world, "/srv/app");

    assert!(saved(&world, "new_session_yolo_local").is_empty());
    assert!(spawned.get("yolo_mode").is_none());
    rest(&mut world);
}

#[test]
fn agent_shortcuts_wait_for_the_harness_order() {
    let mut world = busy_desktop_with(&[], &[("plug_available", "true")]);
    let asked = hold(&world, "delegation_preferences_get");
    open_dialog(&mut world);
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.agents.loading"))
            .name
            .as_deref(),
        Some("Loading harnesses…")
    );
    assert_eq!(agent_names(&world), ["Terminal"]);
    world.gesture(|app| app.press("alt+1"));
    assert_eq!(chosen(&world), "");

    let request = asked.lock().unwrap().remove(0);
    world
        .daemon
        .reply_to(&request, catalog(&["plug", "claude", "codex"]));
    world.settle();
    world.gesture(|app| app.press("alt+1"));

    assert_eq!(agent_names(&world), ["Terminal", "Plug", "Claude", "Codex"]);
    assert_eq!(chosen(&world), "Plug");
    rest(&mut world);
}

#[test]
fn a_catalog_error_keeps_agents_selectable_without_numbers_until_retry_loads_it() {
    let mut world = busy_desktop(&[]);
    let asked = hold(&world, "delegation_preferences_get");
    open_dialog(&mut world);
    let first = asked.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &first,
        message(DelegationPreferencesResultMessage {
            success: false,
            error: Some("catalog unreachable".into()),
            ..DelegationPreferencesResultMessage::new()
        }),
    );
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.agents.error"))
            .name
            .as_deref(),
        Some("catalog unreachable")
    );
    world.gesture(|app| app.click(Target::key("new-session.agent.codex")));
    world.gesture(|app| app.press("alt+1"));
    assert_eq!(chosen(&world), "Codex");

    world.gesture(|app| app.click(Target::key("new-session.agents.retry")));
    let second = asked.lock().unwrap().remove(0);
    world
        .daemon
        .reply_to(&second, catalog(&["claude", "codex"]));
    world.settle();
    assert_eq!(chosen(&world), "Codex");
    world.gesture(|app| app.press("alt+1"));

    assert!(
        world
            .screen()
            .find(Target::key("new-session.agents.error"))
            .is_none()
    );
    assert_eq!(chosen(&world), "Claude");
    rest(&mut world);
}

#[test]
fn a_harness_being_checked_stays_selectable_then_gives_way_once_it_turns_out_missing() {
    let mut world = busy_desktop_with(&[], &[("claude_available", "checking")]);
    open_dialog(&mut world);
    let claude = world.screen().get(Target::key("new-session.agent.claude"));
    assert!(!claude.is("disabled"));
    assert!(claude.is("checked"));
    assert_eq!(
        claude.description.as_deref(),
        Some("Checking for Claude on this machine")
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.agent.claude.checking"))
            .is_some()
    );

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("claude_available".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();

    let claude = world.screen().get(Target::key("new-session.agent.claude"));
    assert!(claude.is("disabled"));
    assert_eq!(
        claude.description.as_deref(),
        Some("Claude is not available on this machine")
    );
    assert_eq!(chosen(&world), "Codex");
    rest(&mut world);
}

const REPO: &str = "/src/repo";

fn worktree(branch: &str) -> Worktree {
    Worktree {
        branch: branch.into(),
        path: format!("{REPO}--{branch}"),
        main_repo: REPO.into(),
        ..Worktree::default()
    }
}

fn repo_info(branches: &[&str]) -> Value {
    message(GetRepoInfoResultMessage {
        success: true,
        info: Some(RepoInfo {
            repo: REPO.into(),
            current_branch: "main".into(),
            default_branch: "origin/main".into(),
            current_commit_hash: "0123456789abcdef".into(),
            current_commit_time: "2026-11-01T10:00:00Z".into(),
            worktrees: branches.iter().map(|b| worktree(b)).collect(),
        }),
        ..GetRepoInfoResultMessage::new()
    })
}

/// The daemon's inspection: paths under `/src/repo` are in the repository there.
fn in_repo(request: &Request) -> Value {
    let path = request["path"].as_str().unwrap().to_owned();
    let root = path.starts_with(REPO).then(|| REPO.to_owned());
    message(InspectPathResultMessage {
        success: true,
        inspection: Some(PathInspection {
            exists: true,
            is_directory: true,
            input_path: path.clone(),
            resolved_path: path,
            repo_root: root,
            home_path: Some(HOME.into()),
        }),
        ..InspectPathResultMessage::new()
    })
}

/// A dialog whose daemon knows a repository with `feature` and `fix` worktrees.
fn repo_world(settings: &[(&str, &str)]) -> World {
    let world = busy_desktop_with(&[], settings);
    world
        .daemon
        .on("inspect_path", in_repo)
        .on("get_repo_info", |_| repo_info(&["feature", "fix"]))
        .on("create_worktree", |r| {
            message(CreateWorktreeResultMessage {
                success: true,
                path: Some(format!("{REPO}--{}", r["branch"].as_str().unwrap())),
                ..CreateWorktreeResultMessage::new()
            })
        });
    world
}

fn open_repo(world: &mut World, path: &str) {
    open_dialog(world);
    type_path(world, path);
    press(world, Key::Enter);
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_some(),
        "{path} opened no repository options:\n{}",
        world.screen()
    );
}

fn destinations(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("new-session.repo.row."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn committed(world: &World) -> Option<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("new-session.repo.row."))
        })
        .find(|n| n.is("selected"))
        .and_then(|n| n.name)
}

fn name_field(world: &World) -> Node {
    world
        .screen()
        .get(Target::key("new-session.repo.name.field"))
}

fn is_worktree_name(name: &str) -> bool {
    name.split_once('-').is_some_and(|(adjective, noun)| {
        !adjective.is_empty()
            && !noun.is_empty()
            && name.chars().all(|c| c.is_ascii_lowercase() || c == '-')
    })
}

fn memory(world: &World) -> Vec<String> {
    saved(world, &format!("new_session_destination_local_{REPO}"))
}

#[test]
fn a_typed_worktree_lands_on_its_row_and_enter_opens_it_without_creating_one() {
    let mut world = repo_world(&[("new_session_destination_local_/src/repo", "main_repo")]);
    open_repo(&mut world, "/src/repo--feature");

    assert_eq!(destinations(&world), ["main", "feature", "fix"]);
    assert_eq!(committed(&world).as_deref(), Some("feature"));
    press(&mut world, Key::Enter);

    assert_eq!(spawns(&world)[0]["cwd"], "/src/repo--feature");
    assert!(world.daemon.sent_of("create_worktree").is_empty());
    assert!(memory(&world).is_empty());
    assert!(!dialog_open(&world));
    rest(&mut world);
}

#[test]
fn the_repository_root_offers_a_new_worktree_with_a_generated_name_and_draws_another() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, REPO);

    let field = name_field(&world);
    assert!(field.is("focused"));
    let first = field.value.unwrap_or_default();
    assert!(is_worktree_name(&first), "{first}");
    assert_eq!(committed(&world), None);

    let mut drawn = first.clone();
    for _ in 0..8 {
        world.gesture(|app| app.click(Target::key("new-session.repo.reroll")));
        drawn = name_field(&world).value.unwrap_or_default();
        if drawn != first {
            break;
        }
    }
    assert_ne!(drawn, first);
    assert!(is_worktree_name(&drawn), "{drawn}");
    rest(&mut world);
}

#[test]
fn a_repository_last_used_through_a_new_worktree_opens_on_the_create_form() {
    let mut world = repo_world(&[("new_session_destination_local_/src/repo", "new_worktree")]);
    open_repo(&mut world, REPO);

    assert!(name_field(&world).is("focused"));
    assert_eq!(committed(&world), None);
    rest(&mut world);
}

#[test]
fn the_arrows_move_between_the_create_form_and_the_destinations() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, REPO);

    press(&mut world, Key::Down);
    assert_eq!(committed(&world).as_deref(), Some("main"));
    press(&mut world, Key::Down);
    assert_eq!(committed(&world).as_deref(), Some("feature"));
    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    assert!(name_field(&world).is("focused"));
    assert_eq!(committed(&world), None);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);

    assert_eq!(spawns(&world)[0]["cwd"], REPO);
    rest(&mut world);
}

#[test]
fn starting_in_the_checkout_is_remembered_once_and_reopens_on_that_row() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, REPO);
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(memory(&world), ["main_repo"]);

    open_repo(&mut world, REPO);
    assert_eq!(committed(&world).as_deref(), Some("main"));
    press(&mut world, Key::Enter);

    assert_eq!(memory(&world), ["main_repo"]);
    assert_eq!(spawns(&world).len(), 2);
    assert_eq!(spawns(&world)[1]["cwd"], REPO);
    rest(&mut world);
}

#[test]
fn a_new_worktree_starts_from_the_default_branch_or_the_chosen_rows_branch() {
    for (typed, current, from) in [
        (REPO, false, "origin/main"),
        (REPO, true, "main"),
        ("/src/repo--feature", true, "feature"),
    ] {
        let mut world = repo_world(&[]);
        open_repo(&mut world, typed);
        world.gesture(|app| app.click(Target::key("new-session.repo.name.field")));
        if current {
            press(&mut world, Key::Tab);
        }
        let branch = name_field(&world).value.unwrap_or_default();
        press(&mut world, Key::Enter);

        let created = world.daemon.sent_of("create_worktree");
        assert_eq!(created.len(), 1, "{typed}");
        assert_eq!(created[0]["main_repo"], REPO);
        assert_eq!(created[0]["branch"], branch.as_str());
        assert_eq!(
            created[0]["starting_from"], from,
            "{typed} current={current}"
        );
        assert_eq!(spawns(&world)[0]["cwd"], format!("{REPO}--{branch}"));
        assert_eq!(memory(&world), ["new_worktree"]);
        assert!(world.screen().find(Target::key("worktree-setup")).is_none());
        rest(&mut world);
    }
}

#[test]
fn a_worktree_that_cannot_be_made_is_reported_once_in_the_corner_without_launching() {
    for error in ["branch 'taken' already exists", "disk full"] {
        let mut world = repo_world(&[]);
        world.daemon.on("create_worktree", move |_| {
            message(CreateWorktreeResultMessage {
                success: false,
                error: Some(error.into()),
                ..CreateWorktreeResultMessage::new()
            })
        });
        open_repo(&mut world, REPO);
        world.gesture(|app| app.type_text("taken"));
        press(&mut world, Key::Enter);

        let created = world.daemon.sent_of("create_worktree");
        assert_eq!(created.len(), 1);
        assert_eq!(created[0]["branch"], "taken");
        assert!(spawns(&world).is_empty());
        assert!(!dialog_open(&world));
        assert_eq!(
            world
                .screen()
                .get(Target::key("worktree-setup"))
                .name
                .as_deref(),
            Some("Create failed: taken failed")
        );
        assert!(
            !world
                .screen()
                .get(Target::key("worktree-setup"))
                .is("focused")
        );

        world.gesture(|app| app.click(Target::key("worktree-setup")));
        let opened = world
            .screen()
            .get(Target::role("dialog").name("Session was not created"));
        assert_eq!(opened.key.as_deref(), Some("worktree-setup"));
        let shown = world.screen().get(Target::key("worktree-setup.error")).name;
        assert!(
            shown.as_deref().is_some_and(|s| s.contains(error)),
            "{shown:?}"
        );
        world.gesture(|app| app.click(Target::key("worktree-setup.dismiss")));

        assert!(world.screen().find(Target::key("worktree-setup")).is_none());
        assert_eq!(world.daemon.sent_of("create_worktree").len(), 1);
        rest(&mut world);
    }
}

#[test]
fn the_letter_d_types_into_the_create_form() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, REPO);

    world.gesture(|app| app.type_text("d"));

    assert_eq!(name_field(&world).value.as_deref(), Some("d"));
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo.delete"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn escape_or_the_create_form_drops_a_delete_prompt_without_leaving_the_chooser() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, "/src/repo--feature");

    world.gesture(|app| app.type_text("d"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.repo.delete"))
            .name
            .as_deref(),
        Some("Delete repo--feature?")
    );
    press(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo.delete"))
            .is_none()
    );
    assert_eq!(committed(&world).as_deref(), Some("feature"));

    world.gesture(|app| app.type_text("d"));
    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    press(&mut world, Key::Up);
    assert!(name_field(&world).is("focused"));
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo.delete"))
            .is_none()
    );
    assert!(world.daemon.sent_of("delete_worktree").is_empty());
    rest(&mut world);
}

#[test]
fn escape_on_the_create_form_goes_back_to_the_path() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, REPO);

    press(&mut world, Key::Escape);

    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_none()
    );
    assert_eq!(field_text(&world, "new-session"), REPO);
    assert!(
        world
            .screen()
            .get(Target::key("new-session.path.field"))
            .is("focused")
    );
    rest(&mut world);
}

#[test]
fn a_refresh_keeps_the_rows_listed_and_the_chosen_worktree_chosen() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, "/src/repo--fix");
    let asked = hold(&world, "get_repo_info");

    world.gesture(|app| app.type_text("r"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.repo.refreshing"))
            .name
            .as_deref(),
        Some("Refreshing repo options")
    );
    assert_eq!(destinations(&world), ["main", "feature", "fix"]);
    let request = asked.lock().unwrap().remove(0);
    world
        .daemon
        .reply_to(&request, repo_info(&["added", "feature", "fix"]));
    world.settle();

    assert_eq!(destinations(&world), ["main", "added", "feature", "fix"]);
    assert_eq!(committed(&world).as_deref(), Some("fix"));
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo.refreshing"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn deleting_a_worktree_shows_it_going_then_moves_to_its_neighbour() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, "/src/repo--feature");
    let deletes = hold(&world, "delete_worktree");
    world.daemon.on("get_repo_info", |_| repo_info(&["fix"]));

    world.gesture(|app| app.type_text("D"));
    world.gesture(|app| app.type_text("y"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.repo.delete"))
            .name
            .as_deref(),
        Some("Deleting repo--feature")
    );
    let request = deletes.lock().unwrap().remove(0);
    assert_eq!(request["path"], "/src/repo--feature");
    world.daemon.reply_to(
        &request,
        message(DeleteWorktreeResultMessage {
            success: true,
            path: "/src/repo--feature".into(),
            ..DeleteWorktreeResultMessage::new()
        }),
    );
    world.settle();

    assert_eq!(destinations(&world), ["main", "fix"]);
    assert_eq!(committed(&world).as_deref(), Some("fix"));
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo.delete"))
            .is_none()
    );
    rest(&mut world);
}

#[test]
fn repository_options_that_cannot_be_read_launch_the_inspected_folder() {
    let mut world = repo_world(&[]);
    world.daemon.on("get_repo_info", |_| {
        message(GetRepoInfoResultMessage {
            success: false,
            error: Some("not a git repository".into()),
            ..GetRepoInfoResultMessage::new()
        })
    });
    open_dialog(&mut world);
    type_path(&mut world, "/src/repo/sub");
    press(&mut world, Key::Enter);

    assert_eq!(spawns(&world)[0]["cwd"], "/src/repo/sub");
    assert_eq!(
        world
            .screen()
            .get(Target::key("notice.text"))
            .name
            .as_deref(),
        Some("not a git repository")
    );
    assert!(!dialog_open(&world));
    rest(&mut world);
}

#[test]
fn the_dialogs_alt_shortcuts_work_while_the_destinations_have_the_keyboard() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, "/src/repo--feature");

    world.gesture(|app| app.press("alt+2"));
    assert_eq!(chosen(&world), "Codex");
    press(&mut world, Key::Enter);

    assert_eq!(spawns(&world)[0]["agent"], "codex");
    rest(&mut world);
}

#[test]
fn says_what_it_waits_for_while_the_path_then_the_repository_are_read() {
    let mut world = repo_world(&[]);
    let inspections = hold(&world, "inspect_path");
    let infos = hold(&world, "get_repo_info");
    open_dialog(&mut world);
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    let operation = |world: &World| {
        world
            .screen()
            .find(Target::key("new-session.operation"))
            .and_then(|n| n.name)
    };
    assert_eq!(operation(&world).as_deref(), Some("Inspecting path"));

    let request = inspections.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, in_repo(&request));
    world.settle();
    assert_eq!(operation(&world).as_deref(), Some("Loading repo options"));

    let request = infos.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, repo_info(&[]));
    world.settle();
    assert_eq!(operation(&world), None);
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn answers_for_a_picker_that_closed_and_opened_again_are_dropped() {
    let mut world = repo_world(&[]);
    let inspections = hold(&world, "inspect_path");
    open_dialog(&mut world);
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    press(&mut world, Key::Escape);
    open_dialog(&mut world);
    let request = inspections.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, in_repo(&request));
    world.settle();

    assert!(world.daemon.sent_of("get_repo_info").is_empty());
    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_none()
    );
    assert!(spawns(&world).is_empty());

    let infos = hold(&world, "get_repo_info");
    world.daemon.on("inspect_path", in_repo);
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    press(&mut world, Key::Escape);
    press(&mut world, Key::Escape);
    open_dialog(&mut world);
    let request = infos.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, repo_info(&[]));
    world.settle();

    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_none()
    );
    assert!(spawns(&world).is_empty());
    rest(&mut world);
}

#[test]
fn repository_options_answered_after_a_newer_pick_launched_are_dropped() {
    let mut world = repo_world(&[]);
    let infos = hold(&world, "get_repo_info");
    open_dialog(&mut world);
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("/tmp/plain");
    });
    world.advance(Duration::from_millis(200));
    press(&mut world, Key::Enter);
    assert_eq!(spawns(&world)[0]["cwd"], "/tmp/plain");

    open_dialog(&mut world);
    let request = infos.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, repo_info(&[]));
    world.settle();

    assert!(
        world
            .screen()
            .find(Target::key("new-session.repo"))
            .is_none()
    );
    assert_eq!(spawns(&world).len(), 1);
    rest(&mut world);
}

fn arrangement(current: &str, desktops: Vec<Desktop>) -> ProfileArrangementChangedMessage {
    ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: current.into(),
            ..Profile::default()
        },
        desktops,
        ..ProfileArrangementChangedMessage::new()
    }
}

/// Desktop 2 is empty and shown; Desktop 1 holds an agent.
fn empty_desktop() -> World {
    let mut state = initial(
        vec![
            desktop("d2", "Desktop 2", vec![]),
            desktop("d1", "Desktop 1", vec![pane("d1", "p1")]),
        ],
        &[],
    );
    state.desktops.as_mut().unwrap().reverse();
    state.profiles.as_mut().unwrap()[0].current_desktop_id = "d2".into();
    let mut world = World::start(daemon(state, &[]));
    world.open_desktop();
    world.daemon.on("inspect_path", in_repo);
    world
        .daemon
        .on("get_repo_info", |_| repo_info(&["feature"]));
    world
}

fn launcher_field(world: &World) -> Option<Node> {
    world.screen().find(Target::key("launcher.path.field"))
}

fn focused(world: &World) -> Option<String> {
    world.screen().focused().and_then(|n| n.key)
}

fn launcher_focused(world: &World) -> bool {
    launcher_field(world).is_some_and(|n| n.is("focused"))
}

fn both_desktops(d1: Vec<DesktopPane>, d2: Vec<DesktopPane>) -> Vec<Desktop> {
    vec![
        desktop("d1", "Desktop 1", d1),
        desktop("d2", "Desktop 2", d2),
    ]
}

#[test]
fn an_empty_desktop_offers_the_launcher_focused() {
    let mut world = empty_desktop();

    assert_eq!(
        world
            .screen()
            .get(Target::key("launcher.title"))
            .name
            .as_deref(),
        Some("New agent on Desktop 2")
    );
    assert!(launcher_focused(&world));
    assert!(
        world
            .screen()
            .find(Target::key("launcher.agents"))
            .is_some()
    );
    assert!(!dialog_open(&world));
    rest(&mut world);
}

#[test]
fn the_launcher_starts_on_its_desktop_and_gives_way_to_the_new_pane() {
    let mut world = empty_desktop();
    let spawned = hold(&world, "spawn_session");
    type_path(&mut world, "/tmp/plain");
    press(&mut world, Key::Enter);

    let request = spawned.lock().unwrap().remove(0);
    assert_eq!(request["placement"]["desktop_id"], "d2");
    assert!(
        request["placement"]
            .get("anchor_pane_id")
            .is_none_or(Value::is_null)
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("launcher.starting"))
            .name
            .as_deref(),
        Some("Starting plain…")
    );
    world.daemon.emit(ProfileArrangementChangedMessage {
        request_id: request["id"].as_str().map(str::to_owned),
        ..arrangement(
            "d2",
            both_desktops(vec![pane("d1", "p1")], vec![pane("d2", "p-new")]),
        )
    });
    world.daemon.reply_to(&request, placed(&request));
    world.settle();

    assert!(world.screen().find(Target::key("launcher")).is_none());
    assert!(
        world.screen().find(Target::key("notice")).is_none(),
        "{}",
        world.screen()
    );
    rest(&mut world);
}

#[test]
fn the_launcher_returns_when_the_last_pane_leaves_and_goes_when_one_arrives() {
    let mut world = busy_desktop(&[]);
    assert!(world.screen().find(Target::key("launcher")).is_none());

    world.daemon.emit(arrangement(
        "d1",
        both_desktops(vec![], vec![pane("d2", "p1")]),
    ));
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("launcher.title"))
            .name
            .as_deref(),
        Some("New agent on Desktop 1")
    );
    assert!(launcher_focused(&world));

    world.daemon.emit(arrangement(
        "d2",
        both_desktops(vec![], vec![pane("d2", "p1")]),
    ));
    world.settle();
    assert!(world.screen().find(Target::key("launcher")).is_none());

    world.daemon.emit(arrangement(
        "d1",
        both_desktops(vec![pane("d1", "p1")], vec![]),
    ));
    world.settle();
    assert!(world.screen().find(Target::key("launcher")).is_none());
    rest(&mut world);
}

#[test]
fn new_session_on_an_empty_desktop_focuses_its_launcher_instead_of_the_dialog() {
    let mut world = empty_desktop();
    press_action(&mut world, &Action::UiCommandPalette);
    assert!(!launcher_focused(&world));
    press(&mut world, Key::Escape);
    assert!(launcher_focused(&world));

    for close in [Some(Key::Escape), None] {
        press_action(&mut world, &Action::FileOpen);
        assert!(!launcher_focused(&world));
        match close {
            Some(key) => press(&mut world, key),
            None => press_action(&mut world, &Action::FileOpen),
        }
        assert!(launcher_focused(&world));
    }

    world.gesture(|app| app.click(Target::key("launcher.agent.shell")));
    press_action(&mut world, &Action::SessionNew);

    assert!(!dialog_open(&world));
    assert!(launcher_focused(&world));
    rest(&mut world);
}

#[test]
fn the_launcher_keeps_the_keyboard_in_a_repositorys_options() {
    let mut world = empty_desktop();
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    let name_focused =
        |world: &World| focused(world).as_deref() == Some("launcher.repo.name.field");
    assert!(name_focused(&world));

    press_action(&mut world, &Action::SessionNew);
    assert!(!dialog_open(&world));
    assert!(name_focused(&world));

    press_action(&mut world, &Action::UiCommandPalette);
    assert!(!name_focused(&world));
    press(&mut world, Key::Escape);
    assert!(
        name_focused(&world),
        "no element matches\n{}",
        world.screen()
    );
    rest(&mut world);
}

#[test]
fn repository_options_that_arrive_under_an_overlay_leave_it_the_keyboard() {
    let mut world = empty_desktop();
    let infos = hold(&world, "get_repo_info");
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    press_action(&mut world, &Action::UiCommandPalette);
    let palette_focus = focused(&world);

    let request = infos.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, repo_info(&[]));
    world.settle();

    assert_eq!(focused(&world), palette_focus);
    press(&mut world, Key::Escape);
    assert_eq!(focused(&world).as_deref(), Some("launcher.repo.name.field"));
    rest(&mut world);
}

#[test]
fn a_pick_answered_after_the_user_left_still_starts_on_the_launchers_desktop() {
    let mut world = empty_desktop();
    let inspections = hold(&world, "inspect_path");
    type_path(&mut world, "/tmp/plain");
    press(&mut world, Key::Enter);
    world.daemon.emit(arrangement(
        "d1",
        both_desktops(vec![pane("d1", "p1")], vec![]),
    ));
    world.settle();

    let request = inspections.lock().unwrap().remove(0);
    world.daemon.reply_to(&request, folder(&request));
    world.settle();

    let spawned = spawns(&world);
    assert_eq!(spawned.len(), 1);
    assert_eq!(spawned[0]["placement"]["desktop_id"], "d2");
    assert!(world.screen().find(Target::key("launcher")).is_none());
    rest(&mut world);
}

#[test]
fn a_worktree_agent_starts_on_the_desktop_it_was_asked_for_after_the_user_moved_on() {
    let mut world = empty_desktop();
    let creates = hold(&world, "create_worktree");
    type_path(&mut world, REPO);
    press(&mut world, Key::Enter);
    press(&mut world, Key::Enter);
    world.daemon.emit(arrangement(
        "d1",
        both_desktops(vec![pane("d1", "p1")], vec![]),
    ));
    world.settle();

    let request = creates.lock().unwrap().remove(0);
    let branch = request["branch"].as_str().unwrap().to_owned();
    world.daemon.reply_to(
        &request,
        message(CreateWorktreeResultMessage {
            success: true,
            path: Some(format!("{REPO}--{branch}")),
            ..CreateWorktreeResultMessage::new()
        }),
    );
    world.settle();

    let spawned = spawns(&world);
    assert_eq!(spawned.len(), 1);
    assert_eq!(spawned[0]["placement"]["desktop_id"], "d2");
    assert_eq!(spawned[0]["cwd"], format!("{REPO}--{branch}"));
    rest(&mut world);
}

#[test]
fn escape_never_dismisses_the_launcher() {
    let mut world = empty_desktop();
    world
        .daemon
        .on("browse_directory", |_| listing("/srv/", &["alpha", "beta"]));
    type_path(&mut world, "/srv/");
    press(&mut world, Key::Down);
    assert_eq!(highlighted(&world, "launcher").as_deref(), Some("alpha"));

    press(&mut world, Key::Escape);
    assert_eq!(highlighted(&world, "launcher"), None);
    press(&mut world, Key::Escape);
    press(&mut world, Key::Escape);

    assert!(launcher_focused(&world));
    assert_eq!(field_text(&world, "launcher"), "/srv/");
    rest(&mut world);
}

#[test]
fn hovering_a_row_changes_neither_the_path_nor_the_highlight() {
    let mut world = busy_desktop(&["/home/me/src/attn", "/home/me/src/rune"]);
    open_dialog(&mut world);

    world.gesture(|app| {
        let (x, y) = app
            .runtime
            .find("new-session.row.1")
            .expect("the row is laid out");
        app.runtime.pointer_move(x, y);
    });

    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/attn")
    );
    assert_eq!(field_text(&world, "new-session"), "");
    rest(&mut world);
}

#[test]
fn enter_opens_a_typed_folder_rather_than_its_first_child() {
    let mut world = busy_desktop(&[]);
    world.daemon.on("browse_directory", |_| {
        listing("/tmp/project/", &[".claude", "src"])
    });
    open_dialog(&mut world);

    type_path(&mut world, "/tmp/project/");
    assert_eq!(row_names(&world, "new-session"), [".claude", "src"]);
    press(&mut world, Key::Enter);

    assert_eq!(sent(&world, "inspect_path", "path"), ["/tmp/project"]);
    assert_eq!(spawns(&world)[0]["cwd"], "/tmp/project");
    assert_eq!(spawns(&world)[0]["agent"], "claude");
    rest(&mut world);
}

#[test]
fn recents_filter_by_a_home_or_absolute_path_without_rewriting_it_or_highlighting() {
    let mut world = busy_desktop(&["/home/me/src/attn", "/home/me/src/rune", "/opt/tool"]);
    open_dialog(&mut world);

    type_path(&mut world, "~/src/r");
    assert_eq!(row_names(&world, "new-session"), ["rune"]);
    assert_eq!(field_text(&world, "new-session"), "~/src/r");
    assert_eq!(highlighted(&world, "new-session"), None);

    world.gesture(|app| {
        app.press("mod+a");
        app.type_text("/home/me/src/a");
    });
    world.advance(Duration::from_millis(200));
    assert_eq!(row_names(&world, "new-session"), ["attn"]);
    assert_eq!(field_text(&world, "new-session"), "/home/me/src/a");
    assert_eq!(highlighted(&world, "new-session"), None);
    rest(&mut world);
}

#[test]
fn recents_remembered_from_the_last_picker_are_not_highlighted_until_the_daemon_answers_again() {
    let mut world = busy_desktop(&["/home/me/src/attn"]);
    open_dialog(&mut world);
    press(&mut world, Key::Escape);
    let asked = hold(&world, "get_recent_locations");
    open_dialog(&mut world);

    assert!(shows_row(&world, "attn", "~/src/attn"));
    assert_eq!(highlighted(&world, "new-session"), None);
    press(&mut world, Key::Enter);
    assert!(world.daemon.sent_of("inspect_path").is_empty());

    let request = asked.lock().unwrap().remove(0);
    world
        .daemon
        .reply_to(&request, recents(&["/home/me/src/attn"]));
    world.settle();
    assert_eq!(
        highlighted(&world, "new-session").as_deref(),
        Some("~/src/attn")
    );
    rest(&mut world);
}

#[test]
fn the_launcher_stays_off_home_where_new_session_opens_the_dialog() {
    let mut state = initial(vec![desktop("d2", "Desktop 2", vec![])], &[]);
    state.profiles.as_mut().unwrap()[0].current_desktop_id = "d2".into();
    let mut world = World::start(daemon(state, &[]));

    assert!(world.screen().find(Target::key("launcher")).is_none());
    press_action(&mut world, &Action::SessionNew);
    assert!(dialog_open(&world));
    press(&mut world, Key::Escape);

    world.open_desktop();
    assert!(launcher_focused(&world));
    rest(&mut world);
}

#[test]
fn a_worktree_lost_to_a_daemon_restart_fails_in_the_corner_and_the_next_one_is_made() {
    let mut world = repo_world(&[]);
    let held = hold(&world, "create_worktree");
    open_repo(&mut world, REPO);
    world.gesture(|app| app.type_text("lost"));
    press(&mut world, Key::Enter);
    assert_eq!(held.lock().unwrap().len(), 1);

    world.reconnect();
    assert_eq!(
        world
            .screen()
            .get(Target::key("worktree-setup"))
            .name
            .as_deref(),
        Some("Create failed: lost failed")
    );
    world.gesture(|app| app.click(Target::key("worktree-setup")));
    world.gesture(|app| app.click(Target::key("worktree-setup.dismiss")));

    world.daemon.on("create_worktree", |r| {
        message(CreateWorktreeResultMessage {
            success: true,
            path: Some(format!("{REPO}--{}", r["branch"].as_str().unwrap())),
            ..CreateWorktreeResultMessage::new()
        })
    });
    open_repo(&mut world, REPO);
    world.gesture(|app| app.type_text("again"));
    press(&mut world, Key::Enter);

    let spawned = spawns(&world);
    assert_eq!(spawned.len(), 1);
    assert_eq!(spawned[0]["cwd"], format!("{REPO}--again"));
    rest(&mut world);
}

#[test]
fn a_delete_lost_to_a_daemon_restart_hands_the_chooser_back_to_the_keyboard() {
    let mut world = repo_world(&[]);
    open_repo(&mut world, "/src/repo--feature");
    let _deletes = hold(&world, "delete_worktree");
    world.gesture(|app| app.type_text("D"));
    world.gesture(|app| app.type_text("y"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.repo.delete"))
            .name
            .as_deref(),
        Some("Deleting repo--feature")
    );

    world.reconnect();
    assert_eq!(
        world
            .screen()
            .get(Target::key("notice.text"))
            .name
            .as_deref(),
        Some("Failed to delete worktree")
    );
    press(&mut world, Key::Escape);
    world.gesture(|app| app.type_text("D"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.repo.delete"))
            .name
            .as_deref(),
        Some("Delete repo--feature?"),
        "{}",
        world.screen()
    );
    rest(&mut world);
}

#[test]
fn a_launch_lost_to_a_daemon_restart_brings_the_launcher_back() {
    let mut world = empty_desktop();
    let _spawned = hold(&world, "spawn_session");
    type_path(&mut world, "/tmp/plain");
    press(&mut world, Key::Enter);
    assert!(
        world
            .screen()
            .find(Target::key("launcher.starting"))
            .is_some()
    );

    world.reconnect();

    assert!(
        world
            .screen()
            .find(Target::key("launcher.starting"))
            .is_none(),
        "{}",
        world.screen()
    );
    assert!(launcher_focused(&world), "{}", world.screen());
    rest(&mut world);
}

#[test]
fn an_agent_started_with_the_picker_lands_without_ringing_its_tile() {
    let tree = |leaves: &[&str]| {
        attn_testkit::tree::row(leaves.iter().map(|l| attn_testkit::tree::pane(l))).to_string()
    };
    let d1 = Desktop {
        tree_json: tree(&["p1"]),
        ..desktop("d1", "Desktop 1", vec![pane("d1", "p1")])
    };
    let mut world = World::start(daemon(initial(vec![d1], &[]), &[]));
    world.open_desktop();
    let spawned = hold(&world, "spawn_session");
    open_dialog(&mut world);
    type_path(&mut world, "/tmp/plain");
    press(&mut world, Key::Enter);

    let request = spawned.lock().unwrap().remove(0);
    let started = DesktopPane {
        session_id: request["id"].as_str().unwrap().into(),
        ..pane("d1", "p-new")
    };
    world.daemon.emit(arrangement(
        "d1",
        vec![Desktop {
            tree_json: tree(&["p1", "p-new"]),
            active_pane_id: "p-new".into(),
            ..desktop("d1", "Desktop 1", vec![pane("d1", "p1"), started])
        }],
    ));
    world.daemon.reply_to(&request, placed(&request));
    world.settle();

    assert!(
        world.screen().find(Target::key("tile.p-new")).is_some(),
        "{}",
        world.screen()
    );
    assert_eq!(super::desktops::ringing(&world), Vec::<String>::new());
    rest(&mut world);
}

#[test]
fn an_agent_started_from_home_is_shown_holding_the_keyboard() {
    let mut world = World::start(daemon(
        initial(
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
            &[],
        ),
        &[],
    ));
    let spawned = hold(&world, "spawn_session");
    open_dialog(&mut world);
    type_path(&mut world, "/tmp/plain");
    press(&mut world, Key::Enter);

    let request = spawned.lock().unwrap().remove(0);
    world.daemon.emit(arrangement(
        "d1",
        vec![desktop(
            "d1",
            "Desktop 1",
            vec![pane("d1", "p1"), pane("d1", "p-new")],
        )],
    ));
    world.daemon.reply_to(&request, placed(&request));
    world.settle();

    let window = world
        .automate("state", serde_json::json!({"of": "window"}))
        .unwrap();
    assert_eq!(window["view"], "Desktop", "{window}");
    assert_eq!(window["keyboard"]["pane"], "p-new", "{window}");
    rest(&mut world);
}
