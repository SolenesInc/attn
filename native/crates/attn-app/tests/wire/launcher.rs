//! Launcher 2.0, which ⌘N and an empty desktop show while `new_session_launcher_enabled` is on.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    BrowseDirectoryResultMessage, CreateWorktreeResultMessage, DelegationPreferences,
    DelegationPreferencesResultMessage, DeleteWorktreeResultMessage, Desktop, DesktopPane,
    DirectoryEntry, GetRepoInfoResultMessage, Harness, InitialStateMessage,
    InspectPathResultMessage, OutpostInfo, OutpostsUpdatedMessage, PathInspection, Profile,
    ProfileArrangementChangedMessage, RecentLocation, RecentLocationsResultMessage, RepoInfo,
    SettingsUpdatedMessage, SpawnResultMessage, Worktree,
};
use attn_testkit::{Answer, Daemon, Driver, Target, World, key_event, message};
use rune::{Chord, Key, KeyEvent, Mods};
use serde_json::Value;

const HOME: &str = "/home/me";
const REPO: &str = "/home/me/projects/shop";
const TREE: &str = "/home/me/projects/shop--feature";
const OTHER: &str = "/home/me/projects/other";
const LAUNCHER: (&str, &str) = ("new_session_launcher_enabled", "true");

fn memory_key(repo: &str) -> String {
    format!("new_session_destination_local_{repo}")
}

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

fn initial(desktops: Vec<Desktop>, settings: &[(String, String)]) -> InitialStateMessage {
    InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: desktops[0].id.clone(),
            ..Profile::default()
        }]),
        desktops: Some(desktops),
        settings: Some(settings.iter().cloned().collect::<BTreeMap<_, _>>()),
        ..InitialStateMessage::new()
    }
}

/// The shop repository: on `topic`, defaulting to `origin/main`, with one worktree.
fn shop(default_branch: &str) -> RepoInfo {
    RepoInfo {
        repo: REPO.into(),
        current_branch: "topic".into(),
        current_commit_hash: "abcdef1234567890".into(),
        current_commit_time: "2026-05-16T16:00:00Z".into(),
        default_branch: default_branch.into(),
        worktrees: vec![Worktree {
            path: TREE.into(),
            branch: "feature".into(),
            main_repo: REPO.into(),
            ..Worktree::default()
        }],
    }
}

fn absolute(path: &str) -> String {
    let expanded = match path.strip_prefix('~') {
        Some(rest) if rest.is_empty() || rest.starts_with('/') => format!("{HOME}{rest}"),
        _ => path.to_owned(),
    };
    if expanded.len() > 1 {
        expanded.trim_end_matches('/').to_owned()
    } else {
        expanded
    }
}

fn repo_root(path: &str) -> Option<String> {
    let path = absolute(path);
    (path == REPO || path == TREE).then(|| REPO.to_owned())
}

/// A machine with recents newest first, the projects folder holding
/// `shop` and `never-opened`, the shop repository, worktrees made beside it and every spawn
/// placed on the desktop it asked for.
fn machine(
    recent: &[&str],
    info: RepoInfo,
    settings: &[(String, String)],
    desktops: Vec<Desktop>,
) -> Daemon {
    let state = initial(desktops, settings);
    let kept = Arc::new(Mutex::new(state.settings.clone().unwrap_or_default()));
    let recents: Vec<RecentLocation> = recent
        .iter()
        .enumerate()
        .map(|(i, path)| RecentLocation {
            path: (*path).into(),
            last_seen: format!("2026-05-16T16:0{}:00Z", 9 - i),
            use_count: 1,
            repo_root: repo_root(path),
            ..RecentLocation::default()
        })
        .collect();
    let daemon = Daemon::scripted().state(state);
    daemon
        .on("set_setting", move |r| {
            let mut settings = kept.lock().unwrap();
            let key = r["key"].as_str().unwrap().to_owned();
            settings.insert(key.clone(), r["value"].as_str().unwrap().to_owned());
            message(SettingsUpdatedMessage {
                changed_key: Some(key),
                settings: Some(settings.clone()),
                ..SettingsUpdatedMessage::new()
            })
        })
        .on("delegation_preferences_get", |_| {
            message(DelegationPreferencesResultMessage {
                success: true,
                preferences: Some(DelegationPreferences::default()),
                harnesses: Some(
                    ["claude", "codex"]
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
        })
        .on("get_recent_locations", move |_| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some(HOME.into()),
                recent_locations: recents.clone(),
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("browse_directory", |r| {
            let directory = absolute(r["input_path"].as_str().unwrap());
            let names: &[&str] = if directory == "/home/me/projects" {
                &["shop", "never-opened"]
            } else {
                &[]
            };
            message(BrowseDirectoryResultMessage {
                success: true,
                directory: directory.clone(),
                home_path: Some(HOME.into()),
                entries: names
                    .iter()
                    .map(|name| DirectoryEntry {
                        name: (*name).into(),
                        path: format!("{directory}/{name}"),
                        is_dir: true,
                        is_repo: Some(format!("{directory}/{name}") == REPO),
                    })
                    .collect(),
                ..BrowseDirectoryResultMessage::new()
            })
        })
        .on("inspect_path", |r| {
            let path = r["path"].as_str().unwrap();
            inspected(path, true)
        })
        .on("get_repo_info", move |_| {
            message(GetRepoInfoResultMessage {
                success: true,
                info: Some(info.clone()),
                ..GetRepoInfoResultMessage::new()
            })
        })
        .on("create_worktree", |r| {
            message(CreateWorktreeResultMessage {
                success: true,
                path: Some(format!(
                    "{}--{}",
                    r["main_repo"].as_str().unwrap(),
                    r["branch"].as_str().unwrap()
                )),
                ..CreateWorktreeResultMessage::new()
            })
        })
        .on("spawn_session", |r| {
            message(SpawnResultMessage {
                id: r["id"].as_str().unwrap().into(),
                success: true,
                desktop_id: r["placement"]["desktop_id"].as_str().map(str::to_owned),
                pane_id: Some(format!("pane-{}", r["id"].as_str().unwrap())),
                ..SpawnResultMessage::new()
            })
        });
    daemon
}

fn inspected(path: &str, exists: bool) -> Value {
    message(InspectPathResultMessage {
        success: true,
        inspection: Some(PathInspection {
            input_path: path.into(),
            resolved_path: absolute(path),
            home_path: Some(HOME.into()),
            exists,
            is_directory: exists,
            repo_root: repo_root(path).filter(|_| exists),
        }),
        ..InspectPathResultMessage::new()
    })
}

fn settings(memory: &str) -> Vec<(String, String)> {
    vec![
        (LAUNCHER.0.to_owned(), LAUNCHER.1.to_owned()),
        ("new_session_agent".to_owned(), "claude".to_owned()),
        ("projects_directory".to_owned(), format!("{HOME}/projects")),
        (memory_key(REPO), memory.to_owned()),
    ]
}

/// ⌘N over a desktop with one agent, Launcher 2.0 on, the shop repository last started in
/// `memory`.
fn open_with(memory: &str, default_branch: &str) -> World {
    let mut world = World::start(machine(
        &[REPO],
        shop(default_branch),
        &settings(memory),
        vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
    ));
    world.open_desktop();
    press_action(&mut world, &Action::SessionNew);
    world
}

fn open(memory: &str) -> World {
    open_with(memory, "origin/main")
}

fn action_key(action: &Action) -> KeyEvent {
    match Keymap::from_setting(None, attn_testkit::platform()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    }
}

fn press_action(world: &mut World, action: &Action) {
    let ev = action_key(action);
    world.gesture(|app| app.key(ev));
}

fn press(world: &mut World, key: Key) {
    world.gesture(|app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    });
}

fn chord(world: &mut World, spec: &str) {
    let ev = key_event(&Chord::parse(spec).unwrap());
    world.gesture(|app| app.key(ev));
}

fn letter(world: &mut World, letter: &str) {
    press(world, Key::Text(letter.into()));
}

fn field(world: &World, which: &str) -> String {
    world
        .screen()
        .get(Target::key(format!("{which}.path.field")))
        .value
        .unwrap_or_default()
}

/// Replaces the field's text with `text`, as a user who clears it and types does.
fn type_path(world: &mut World, which: &str, text: &str) {
    for _ in 0..field(world, which).chars().count() {
        press(world, Key::Backspace);
    }
    if !text.is_empty() {
        world.gesture(|app| app.type_text(text));
    }
}

fn options(world: &World, prefix: &str) -> Vec<(String, bool)> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| n.key.as_deref().is_some_and(|k| k.starts_with(prefix)))
        .map(|n| (n.name.clone().unwrap_or_default(), n.is("selected")))
        .collect()
}

/// The highlighted row, as a screen reader hears it from the focused field.
fn selected(world: &World, prefix: &str) -> Option<String> {
    let shown = options(world, prefix)
        .into_iter()
        .find(|(_, selected)| *selected)
        .map(|(name, _)| name);
    let heard = world.screen().active_option().and_then(|n| n.name);
    assert_eq!(heard, shown, "the field names the highlighted row");
    shown
}

fn where_shows(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("list-box").name("Where"))
        .is_some()
}

fn alert(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("new-session.error"))
        .and_then(|n| n.name)
}

fn launched(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("spawn_session")
        .iter()
        .map(|r| {
            (
                r["cwd"].as_str().unwrap().to_owned(),
                r["agent"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn memory_writes(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("set_setting")
        .iter()
        .filter(|r| {
            r["key"]
                .as_str()
                .is_some_and(|k| k.starts_with("new_session_destination"))
        })
        .map(|r| {
            (
                r["key"].as_str().unwrap().to_owned(),
                r["value"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn creates(world: &World) -> Vec<(String, String, String)> {
    world
        .daemon
        .sent_of("create_worktree")
        .iter()
        .map(|r| {
            (
                r["main_repo"].as_str().unwrap().to_owned(),
                r["branch"].as_str().unwrap().to_owned(),
                r["starting_from"].as_str().unwrap_or_default().to_owned(),
            )
        })
        .collect()
}

fn dialog_open(world: &World) -> bool {
    world.screen().find(Target::key("new-session")).is_some()
}

fn hints(world: &World) -> String {
    world
        .screen()
        .descendants(Target::key("new-session.hints"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect::<Vec<_>>()
        .join(" | ")
}

/// Tab on the highlighted shop recent opens its Where list, after one repository lookup.
fn open_where(world: &mut World) {
    press(world, Key::Tab);
    assert!(where_shows(world), "no Where list:\n{}", world.screen());
    assert_eq!(
        world
            .daemon
            .sent_of("get_repo_info")
            .iter()
            .map(|r| r["repo"].as_str().unwrap().to_owned())
            .collect::<Vec<_>>(),
        [REPO]
    );
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_bare_enter_starts_the_top_recent_where_the_repository_last_started() {
    for memory in ["main_repo", "new_worktree"] {
        let mut world = open(memory);
        assert_eq!(field(&world, "new-session"), "");
        assert_eq!(
            selected(&world, "new-session.row.").as_deref(),
            Some("shop")
        );

        press(&mut world, Key::Enter);

        let creates = creates(&world);
        if memory == "new_worktree" {
            assert_eq!(creates.len(), 1, "{memory}");
            let (repo, branch, from) = &creates[0];
            assert_eq!((repo.as_str(), from.as_str()), (REPO, "origin/main"));
            assert!(branch.contains('-'), "{branch}");
            assert_eq!(
                launched(&world),
                [(format!("{REPO}--{branch}"), "claude".into())]
            );
        } else {
            assert!(creates.is_empty());
            assert_eq!(launched(&world), [(REPO.into(), "claude".into())]);
        }
        assert!(memory_writes(&world).is_empty(), "{memory}");
        assert!(!dialog_open(&world), "{memory}");
        rest(&mut world);
    }
}

#[test]
fn searching_matches_recents_without_listing_folders() {
    let mut world = open("main_repo");
    assert!(world.daemon.sent_of("browse_directory").is_empty());

    type_path(&mut world, "new-session", "never");
    assert!(options(&world, "new-session.row.").is_empty());
    assert!(
        world
            .screen()
            .find(Target::key("new-session.empty"))
            .and_then(|n| n.name)
            .as_deref()
            == Some("No recent project matches. Type ~/ to browse folders.")
    );
    type_path(&mut world, "new-session", "SHOp");
    assert_eq!(options(&world, "new-session.row.").len(), 1);
    assert!(world.daemon.sent_of("browse_directory").is_empty());

    press(&mut world, Key::Enter);

    assert_eq!(launched(&world), [(REPO.into(), "claude".into())]);
    assert!(world.daemon.sent_of("inspect_path").is_empty());
    rest(&mut world);
}

#[test]
fn with_terminal_tab_browses_a_repository_as_a_folder() {
    for query in ["", REPO, "/home/me/projects/shop/"] {
        let mut world = open("new_worktree");
        chord(&mut world, "alt+t");
        if !query.is_empty() {
            type_path(&mut world, "new-session", query);
        }
        assert!(
            world
                .screen()
                .find(Target::key("new-session.row.0.badge"))
                .is_none_or(|b| b.name.as_deref() != Some("⎇ new worktree")),
            "{query}"
        );
        assert!(
            hints(&world).contains("Enter open terminal"),
            "{}",
            hints(&world)
        );
        assert!(
            !hints(&world).contains("terminal here"),
            "{}",
            hints(&world)
        );

        press(&mut world, Key::Tab);

        assert!(!where_shows(&world), "{query}");
        assert!(world.daemon.sent_of("get_repo_info").is_empty(), "{query}");
        if query.is_empty() {
            assert_eq!(field(&world, "new-session"), "~/projects/shop/");
        }
        type_path(&mut world, "new-session", REPO);
        press(&mut world, Key::Enter);
        assert_eq!(launched(&world), [(REPO.into(), "shell".into())], "{query}");
        assert!(creates(&world).is_empty());
        assert!(memory_writes(&world).is_empty());
        rest(&mut world);
    }
}

#[test]
fn tab_on_a_typed_checkout_opens_its_where_list_and_backspace_returns_to_the_path() {
    for path in [REPO, "/home/me/projects/shop/"] {
        let mut world = open("main_repo");
        type_path(&mut world, "new-session", path);
        open_where(&mut world);
        assert_eq!(field(&world, "new-session"), "");

        press(&mut world, Key::Backspace);
        assert_eq!(field(&world, "new-session"), path);
        press(&mut world, Key::Escape);
        assert_eq!(field(&world, "new-session"), "");
        assert!(dialog_open(&world));
        rest(&mut world);
    }
}

/// Each Escape takes one step, the Where list first, then the typed text,
/// and the dialog last, so a typed path takes three presses and a recent two.
#[test]
fn escape_steps_back_from_the_where_list_through_the_typed_path_and_then_closes() {
    for typed in [Some(REPO), None] {
        let mut world = open("main_repo");
        if let Some(path) = typed {
            type_path(&mut world, "new-session", path);
        }
        open_where(&mut world);

        press(&mut world, Key::Escape);
        assert!(!where_shows(&world), "{typed:?}");
        assert_eq!(field(&world, "new-session"), typed.unwrap_or_default());
        if typed.is_some() {
            press(&mut world, Key::Escape);
            assert_eq!(field(&world, "new-session"), "");
        }
        assert!(dialog_open(&world), "{typed:?}");

        press(&mut world, Key::Escape);
        assert!(!dialog_open(&world), "{typed:?}");
        rest(&mut world);
    }
}

#[test]
fn a_long_new_worktree_name_ends_inside_the_where_list_in_the_smallest_window_at_the_largest_text()
{
    let mut settings = settings("main_repo");
    settings.push(("uiScale".to_owned(), "1.5".to_owned()));
    let mut world = World::start_sized(
        machine(
            &[REPO],
            shop("origin/main"),
            &settings,
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ),
        (800.0, 600.0),
    );
    world.open_desktop();
    press_action(&mut world, &Action::SessionNew);
    open_where(&mut world);
    type_path(
        &mut world,
        "new-session",
        "account-permissions-migration-本番の権限を移行する作業ツリー-東京リージョン",
    );

    let heading = world
        .screen()
        .bounds(Target::key("new-session.where.new"))
        .expect("the new worktree's heading is laid out");
    let rt = world.runtime();
    let [x, y, w, h] = heading.map(|v| v * rt.env.scale);
    let past: Vec<[f32; 4]> = rt
        .instances
        .iter()
        .filter(|i| i.params[1] == rune::render::KIND_GLYPH)
        .map(|i| i.rect)
        .filter(|g| {
            let mid = g[1] + g[3] / 2.0;
            mid > y && mid < y + h && g[0] + g[2] > x + w + 0.5
        })
        .collect();
    assert!(
        past.is_empty(),
        "the heading {heading:?} draws glyphs past its right edge: {past:?}"
    );
    rest(&mut world);
}

#[test]
fn a_long_repository_name_leaves_the_new_worktree_name_its_field_in_the_smallest_window_at_the_largest_text()
 {
    const LONG: &str =
        "/home/me/projects/customer-identity-and-access-management-platform-services";
    let mut settings = settings("main_repo");
    settings.push(("uiScale".to_owned(), "1.5".to_owned()));
    let daemon = machine(
        &[REPO],
        RepoInfo {
            repo: LONG.into(),
            worktrees: Vec::new(),
            ..shop("origin/main")
        },
        &settings,
        vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
    );
    daemon.on("get_recent_locations", |_| {
        message(RecentLocationsResultMessage {
            success: true,
            home_path: Some(HOME.into()),
            recent_locations: vec![RecentLocation {
                path: LONG.into(),
                last_seen: "2026-05-16T16:09:00Z".into(),
                use_count: 1,
                repo_root: Some(LONG.into()),
                ..RecentLocation::default()
            }],
            ..RecentLocationsResultMessage::new()
        })
    });
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    world.open_desktop();
    press_action(&mut world, &Action::SessionNew);
    press(&mut world, Key::Tab);
    assert!(where_shows(&world), "no Where list:\n{}", world.screen());

    type_path(&mut world, "new-session", "topic-probe");
    let [_, _, width, _] = world
        .screen()
        .bounds(Target::key("new-session.path.field"))
        .expect("the name field is laid out");
    assert!(
        width > 0.0,
        "the name field is {width} wide beside the repository's chip"
    );
    press(&mut world, Key::Enter);
    assert_eq!(
        creates(&world),
        [(LONG.into(), "topic-probe".into(), "origin/main".into())]
    );
    rest(&mut world);
}

#[test]
fn a_named_worktree_starts_from_the_default_or_the_current_branch() {
    for (current, default_branch, from) in [
        (false, "origin/main", "origin/main"),
        (false, "main", "main"),
        (false, "develop", "develop"),
        (true, "origin/main", "topic"),
    ] {
        let mut world = open_with("main_repo", default_branch);
        open_where(&mut world);
        type_path(&mut world, "new-session", "fix-login");
        assert_eq!(
            selected(&world, "new-session.where.row.").as_deref(),
            Some(format!("from {default_branch}").as_str())
        );
        if current {
            press(&mut world, Key::Down);
        }

        press(&mut world, Key::Enter);

        assert_eq!(
            creates(&world),
            [(REPO.into(), "fix-login".into(), from.into())]
        );
        assert_eq!(
            launched(&world),
            [(format!("{REPO}--fix-login"), "claude".into())]
        );
        assert_eq!(
            memory_writes(&world),
            [(memory_key(REPO), "new_worktree".into())]
        );
        rest(&mut world);
    }
}

#[test]
fn an_existing_worktree_starts_without_writing_where_the_repository_started() {
    let mut world = open("new_worktree");
    open_where(&mut world);
    type_path(&mut world, "new-session", "feature");

    press(&mut world, Key::Enter);

    assert_eq!(launched(&world), [(TREE.into(), "claude".into())]);
    assert!(creates(&world).is_empty());
    assert!(memory_writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn mod_enter_opens_a_terminal_in_the_main_checkout_whatever_the_repository_last_used() {
    let mut world = open("new_worktree");

    chord(&mut world, "mod+enter");

    assert_eq!(launched(&world), [(REPO.into(), "shell".into())]);
    assert!(
        world.daemon.sent_of("spawn_session")[0]
            .get("label")
            .is_none()
    );
    assert!(creates(&world).is_empty());
    assert!(memory_writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn mod_enter_on_a_new_worktree_opens_a_terminal_there_without_remembering() {
    let mut world = open("main_repo");
    open_where(&mut world);
    type_path(&mut world, "new-session", "terminal-work");

    chord(&mut world, "mod+enter");

    assert_eq!(
        creates(&world),
        [(REPO.into(), "terminal-work".into(), "origin/main".into())]
    );
    assert_eq!(
        launched(&world),
        [(format!("{REPO}--terminal-work"), "shell".into())]
    );
    assert!(
        world.daemon.sent_of("spawn_session")[0]
            .get("label")
            .is_none()
    );
    assert!(memory_writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn new_terminal_picks_terminal_for_one_opening_in_either_launcher() {
    for enabled in ["true", "false"] {
        let mut world = World::start(machine(
            &[],
            shop("main"),
            &[
                (LAUNCHER.0.to_owned(), enabled.to_owned()),
                ("new_session_agent".to_owned(), "claude".to_owned()),
            ],
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ));
        world.open_desktop();
        press_action(&mut world, &Action::SessionNewTerminal);
        let checked = |world: &World, name: &str| {
            world
                .screen()
                .find(Target::role("radio-button").name(name))
                .is_some_and(|n| n.is("checked"))
        };
        assert!(checked(&world, "Terminal"), "{enabled}");
        press(&mut world, Key::Escape);
        press_action(&mut world, &Action::SessionNew);
        assert!(checked(&world, "Claude"), "{enabled}");
        assert!(world.daemon.sent_of("set_setting").is_empty(), "{enabled}");
        rest(&mut world);
    }
}

#[test]
fn a_refused_branch_name_leaves_the_form_and_says_why() {
    let mut world = open("main_repo");
    open_where(&mut world);
    type_path(&mut world, "new-session", "duplicate");
    world.daemon.on("create_worktree", |_| {
        message(CreateWorktreeResultMessage {
            success: false,
            error: Some("a branch named 'duplicate' already exists".into()),
            ..CreateWorktreeResultMessage::new()
        })
    });

    press(&mut world, Key::Enter);

    assert_eq!(field(&world, "new-session"), "duplicate");
    assert!(alert(&world).is_some_and(|a| a.contains("already exists")));
    assert_eq!(
        selected(&world, "new-session.where.row.").as_deref(),
        Some("from origin/main")
    );
    assert!(launched(&world).is_empty());
    assert!(memory_writes(&world).is_empty());
    rest(&mut world);
}

#[test]
fn a_worktree_made_before_its_start_failed_is_started_again_without_making_another() {
    let mut world = open("main_repo");
    open_where(&mut world);
    type_path(&mut world, "new-session", "keep-me");
    world.daemon.on("spawn_session", |r| {
        message(SpawnResultMessage {
            id: r["id"].as_str().unwrap().into(),
            success: false,
            error: Some("provider refused".into()),
            ..SpawnResultMessage::new()
        })
    });

    press(&mut world, Key::Enter);
    assert_eq!(field(&world, "new-session"), "keep-me");
    assert!(alert(&world).is_some_and(|a| a.contains("provider refused")));
    press(&mut world, Key::Enter);

    assert_eq!(creates(&world).len(), 1);
    assert_eq!(launched(&world).len(), 2);
    rest(&mut world);
}

#[test]
fn deleting_a_worktree_asks_first_offers_force_and_keeps_the_rows_place() {
    let mut world = open("main_repo");
    open_where(&mut world);
    type_path(&mut world, "new-session", "feature");
    world.daemon.on("delete_worktree", |r| {
        let path = r["path"].as_str().unwrap().to_owned();
        message(if r["force"] == true {
            DeleteWorktreeResultMessage {
                success: true,
                path: path.clone(),
                ..DeleteWorktreeResultMessage::new()
            }
        } else {
            DeleteWorktreeResultMessage {
                success: false,
                forceable: Some(true),
                error: Some("dirty worktree".into()),
                path: path.clone(),
                ..DeleteWorktreeResultMessage::new()
            }
        })
    });

    chord(&mut world, "mod+backspace");
    let asking = world.screen().get(Target::key("new-session.where.row.3"));
    assert_eq!(asking.name.as_deref(), Some("Delete feature?"));
    assert_eq!(
        asking.description.as_deref(),
        Some("~/projects/shop--feature (y/n)")
    );
    assert!(asking.is("selected"));
    assert!(
        !hints(&world).contains("terminal here"),
        "{}",
        hints(&world)
    );

    letter(&mut world, "y");
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.where.row.3"))
            .description
            .as_deref(),
        Some("Force delete local worktree and branch? (y/n)")
    );
    letter(&mut world, "y");

    let deletes: Vec<(String, Value)> = world
        .daemon
        .sent_of("delete_worktree")
        .iter()
        .map(|r| (r["path"].as_str().unwrap().to_owned(), r["force"].clone()))
        .collect();
    assert_eq!(
        deletes,
        [(TREE.into(), Value::Null), (TREE.into(), Value::Bool(true))]
    );
    assert_eq!(options(&world, "new-session.where.row.").len(), 3);
    assert_eq!(
        selected(&world, "new-session.where.row.").as_deref(),
        Some("main checkout")
    );
    rest(&mut world);
}

#[test]
fn n_keeps_the_worktree_and_other_keys_do_nothing_while_deletion_asks() {
    let mut world = open("main_repo");
    open_where(&mut world);
    type_path(&mut world, "new-session", "feature");
    chord(&mut world, "mod+backspace");

    for key in [Key::Down, Key::Enter, Key::Tab, Key::Text("a".into())] {
        press(&mut world, key);
    }
    assert_eq!(field(&world, "new-session"), "feature");
    assert_eq!(
        selected(&world, "new-session.where.row.").as_deref(),
        Some("Delete feature?")
    );
    assert!(launched(&world).is_empty());

    letter(&mut world, "n");

    assert_eq!(
        selected(&world, "new-session.where.row.").as_deref(),
        Some("feature")
    );
    assert!(
        world
            .screen()
            .find(Target::role("list-box-option").name("Delete feature?"))
            .is_none()
    );
    assert!(world.daemon.sent_of("delete_worktree").is_empty());
    rest(&mut world);
}

#[test]
fn leaving_the_where_list_returns_to_the_first_row_of_the_previous_search() {
    for key in [Key::Escape, Key::Backspace] {
        let mut world = World::start(machine(
            &[OTHER, REPO],
            shop("origin/main"),
            &settings("main_repo"),
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ));
        world.open_desktop();
        press_action(&mut world, &Action::SessionNew);
        press(&mut world, Key::Down);
        open_where(&mut world);

        press(&mut world, key.clone());

        assert_eq!(field(&world, "new-session"), "");
        assert_eq!(
            selected(&world, "new-session.row.").as_deref(),
            Some("other")
        );
        rest(&mut world);
    }
}

#[test]
fn a_failed_repository_lookup_stays_in_the_where_list_until_escape() {
    let mut world = open("main_repo");
    world.daemon.on("get_repo_info", |_| {
        message(GetRepoInfoResultMessage {
            success: false,
            info: Some(shop("origin/main")),
            error: Some("repository is unavailable".into()),
            ..GetRepoInfoResultMessage::new()
        })
    });

    press(&mut world, Key::Tab);
    assert!(alert(&world).is_some_and(|a| a.contains("repository is unavailable")));
    assert_eq!(field(&world, "new-session"), "");

    press(&mut world, Key::Escape);

    assert!(
        world
            .screen()
            .find(Target::role("list-box").name("Locations"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn recents_from_an_earlier_opening_are_not_highlighted_and_a_bare_enter_starts_nothing() {
    let mut world = open("main_repo");
    press(&mut world, Key::Escape);
    world.daemon.on("get_recent_locations", |_| {});

    press_action(&mut world, &Action::SessionNew);

    assert_eq!(selected(&world, "new-session.row."), None);
    press(&mut world, Key::Enter);
    assert!(launched(&world).is_empty());
}

#[test]
fn a_typed_folder_that_is_missing_is_reported_without_starting() {
    let mut world = open("main_repo");
    world.daemon.on("inspect_path", |r| {
        inspected(r["path"].as_str().unwrap(), false)
    });
    type_path(&mut world, "new-session", "/missing");

    press(&mut world, Key::Enter);

    assert_eq!(
        alert(&world).as_deref(),
        Some("Directory not found: /missing")
    );
    assert_eq!(field(&world, "new-session"), "/missing");
    assert!(launched(&world).is_empty());
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

fn three_desktops() -> Vec<Desktop> {
    vec![
        desktop("d1", "Desktop 1", vec![pane("d1", "p1")]),
        desktop("d2", "Desktop 2", vec![]),
        desktop("d3", "Desktop 3", vec![]),
    ]
}

/// Desktop 2, empty, is shown beside Desktop 1's agent and an empty Desktop 3.
fn on_empty_desktop(settings: &[(String, String)]) -> World {
    let mut state_desktops = three_desktops();
    state_desktops.rotate_left(1);
    let mut world = World::start(machine(
        &[REPO],
        shop("origin/main"),
        settings,
        state_desktops,
    ));
    world.open_desktop();
    world
}

/// What the empty desktop's launcher hints: the classic picker selects, Launcher 2.0 starts.
fn inline_hints(world: &World) -> String {
    world
        .screen()
        .descendants(Target::key("launcher.hints"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect::<Vec<_>>()
        .join(" | ")
}

fn launcher_focused(world: &World) -> bool {
    world
        .screen()
        .find(Target::key("launcher.path.field"))
        .is_some_and(|n| n.is("focused"))
}

#[test]
fn the_empty_desktops_launcher_follows_the_setting_keeps_the_keyboard_and_never_goes_on_escape() {
    let mut world = on_empty_desktop(&[("new_session_agent".to_owned(), "claude".to_owned())]);
    assert!(inline_hints(&world).contains("Enter select"));
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([(
            LAUNCHER.0.to_owned(),
            LAUNCHER.1.to_owned(),
        )])),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(inline_hints(&world).contains("Enter start"));

    type_path(&mut world, "launcher", REPO);
    press(&mut world, Key::Tab);
    assert!(where_shows(&world));
    assert_eq!(world.daemon.sent_of("get_repo_info")[0]["repo"], REPO);
    press_action(&mut world, &Action::SessionNew);
    assert!(launcher_focused(&world));
    assert!(!dialog_open(&world));

    press(&mut world, Key::Escape);
    assert_eq!(field(&world, "launcher"), REPO);
    press(&mut world, Key::Escape);
    press(&mut world, Key::Escape);

    assert!(world.screen().find(Target::key("launcher")).is_some());
    assert!(launcher_focused(&world));
    rest(&mut world);
}

#[test]
fn overlapping_worktree_starts_land_on_the_desktops_they_were_picked_on() {
    let mut world = on_empty_desktop(&settings("main_repo"));
    let held: Arc<Mutex<Vec<attn_testkit::Request>>> = Arc::default();
    let keep = held.clone();
    world.daemon.on("create_worktree", move |r| {
        keep.lock().unwrap().push(r.clone())
    });
    for (desktop, branch) in [("d2", "first"), ("d3", "second")] {
        world.daemon.emit(arrangement(desktop, three_desktops()));
        world.settle();
        press(&mut world, Key::Tab);
        type_path(&mut world, "launcher", branch);
        press(&mut world, Key::Enter);
    }
    let requests = held.lock().unwrap().clone();
    assert_eq!(requests.len(), 2);
    assert_ne!(requests[0]["request_id"], requests[1]["request_id"]);

    for request in requests.iter().rev() {
        let branch = request["branch"].as_str().unwrap();
        world.daemon.reply_to(
            request,
            message(CreateWorktreeResultMessage {
                success: true,
                path: Some(format!("{REPO}--{branch}")),
                ..CreateWorktreeResultMessage::new()
            }),
        );
        world.settle();
    }

    let spawned: Vec<(String, String)> = world
        .daemon
        .sent_of("spawn_session")
        .iter()
        .map(|r| {
            (
                r["cwd"].as_str().unwrap().to_owned(),
                r["placement"]["desktop_id"].as_str().unwrap().to_owned(),
            )
        })
        .collect();
    assert_eq!(
        spawned,
        [
            (format!("{REPO}--second"), "d3".to_owned()),
            (format!("{REPO}--first"), "d2".to_owned())
        ]
    );
    rest(&mut world);
}

#[test]
fn a_start_under_way_finishes_though_a_tab_answered_after_it() {
    let mut world = open("main_repo");
    let held: Arc<Mutex<Vec<attn_testkit::Request>>> = Arc::default();
    let keep = held.clone();
    world.daemon.on("inspect_path", move |r| {
        keep.lock().unwrap().push(r.clone())
    });
    type_path(&mut world, "new-session", "/srv/shop");
    press(&mut world, Key::Tab);
    press(&mut world, Key::Enter);
    let asked = held.lock().unwrap().clone();
    assert_eq!(asked.len(), 2);

    let mut tab = inspected("/srv/shop", true);
    tab["inspection"]["repo_root"] = Value::String("/srv/shop".into());
    world.daemon.reply_to(&asked[0], tab);
    world.settle();
    world
        .daemon
        .reply_to(&asked[1], inspected("/srv/shop", true));
    world.settle();

    assert_eq!(launched(&world), [("/srv/shop".into(), "claude".into())]);
    assert!(!dialog_open(&world));
    rest(&mut world);
}

#[test]
fn the_ghost_completion_follows_the_typed_text() {
    let mut world = open("main_repo");
    let field = world
        .screen()
        .bounds(Target::key("new-session.path.field"))
        .expect("the field shows");
    let ghost = |world: &World| {
        let node = world.screen().get(Target::key("new-session.ghost"));
        let x = world
            .screen()
            .bounds(Target::key("new-session.ghost"))
            .expect("the ghost shows")[0];
        (node.name.unwrap_or_default(), x - field[0])
    };

    type_path(&mut world, "new-session", "~/projects/s");
    world.advance(Duration::from_millis(200));
    let (shown, after_s) = ghost(&world);
    assert_eq!(shown, "hop/");
    letter(&mut world, "h");
    world.advance(Duration::from_millis(200));
    let (shown, after_sh) = ghost(&world);
    assert_eq!(shown, "op/");

    assert!(
        after_s > 0.0 && after_s < field[2] / 4.0,
        "the ghost starts right after the typed path, {after_s} points into a {} point field",
        field[2]
    );
    assert!(
        after_sh > after_s,
        "one more typed character moves the ghost on: {after_s} then {after_sh}"
    );
    rest(&mut world);
}

/// Where an element's content starts: the leftmost text or icon inside it.
fn starts_at(world: &World, key: &str) -> f32 {
    let all = world.runtime().elements();
    let at = all
        .iter()
        .position(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("{key} is not drawn"));
    all.iter()
        .enumerate()
        .filter(|(i, e)| {
            *i == at || std::iter::successors(e.parent, |&p| all[p].parent).any(|p| p == at)
        })
        .filter(|(_, e)| {
            e.role == "icon" || e.text.as_deref().is_some_and(|s| !s.trim().is_empty())
        })
        .map(|(_, e)| e.rect[0])
        .fold(f32::INFINITY, f32::min)
}

fn field_box_starts_at(world: &World, key: &str) -> f32 {
    let all = world.runtime().elements();
    let at = all
        .iter()
        .position(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("{key} is not drawn"));
    std::iter::successors(all[at].parent, |&p| all[p].parent)
        .map(|p| &all[p])
        .find(|e| e.fill.a > 0.0)
        .map(|e| e.rect[0])
        .expect("the field sits in a box")
}

#[test]
fn the_launchers_title_field_headings_rows_and_hints_start_on_one_line() {
    let mut world = open("");
    let w = "new-session";
    let all = world.runtime().elements();
    let heading = all
        .iter()
        .filter(|e| e.role == "heading")
        .filter_map(|e| e.key.clone())
        .find(|k| k.starts_with(&format!("{w}.")) && k != &format!("{w}.title"))
        .expect("the recents have a heading");
    let edges = [
        ("title", starts_at(&world, &format!("{w}.title"))),
        (
            "field's box",
            field_box_starts_at(&world, &format!("{w}.path.field")),
        ),
        ("heading", starts_at(&world, &heading)),
        ("first row", starts_at(&world, &format!("{w}.row.0"))),
        (
            "first hint's keycap",
            world
                .screen()
                .bounds(Target::key(format!("{w}.hint.0")))
                .expect("the footer has hints")[0],
        ),
    ];
    let line = edges[0].1;
    for (what, x) in edges {
        assert!(
            (x - line).abs() < 0.5,
            "the {what} starts at {x}, off the line {line} the title starts on: {edges:?}"
        );
    }
    rest(&mut world);
}

const PLACES: [&str; 8] = [
    "/home/me/projects/alpha",
    "/home/me/projects/bravo",
    "/home/me/projects/charlie",
    "/home/me/projects/delta",
    "/home/me/projects/echo",
    "/home/me/projects/foxtrot",
    "/home/me/projects/golf",
    "/home/me/projects/hotel",
];

/// The shop repository with eight worktrees, `w1` to `w8`.
fn eight_worktrees() -> RepoInfo {
    let mut info = shop("origin/main");
    info.worktrees = (1..=8)
        .map(|i| Worktree {
            path: format!("{REPO}--w{i}"),
            branch: format!("w{i}"),
            main_repo: REPO.into(),
            ..Worktree::default()
        })
        .collect();
    info
}

/// The smallest window at text `scale`, where at 150% the launcher's list is shorter than its
/// rows.
fn small_with(scale: &str, recent: &[&str], info: RepoInfo) -> World {
    let mut settings = settings("main_repo");
    settings.push(("uiScale".to_owned(), scale.to_owned()));
    let mut world = World::start_sized(
        machine(
            recent,
            info,
            &settings,
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ),
        (800.0, 600.0),
    );
    world.open_desktop();
    press_action(&mut world, &Action::SessionNew);
    world
}

fn highlighted_key(world: &World, prefix: &str) -> String {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .find(|n| n.is("selected") && n.key.as_deref().is_some_and(|k| k.starts_with(prefix)))
        .and_then(|n| n.key)
        .expect("a row is highlighted")
}

fn inside(inner: [f32; 4], outer: [f32; 4]) -> bool {
    const EPS: f32 = 0.5;
    inner[0] >= outer[0] - EPS
        && inner[1] >= outer[1] - EPS
        && inner[0] + inner[2] <= outer[0] + outer[2] + EPS
        && inner[1] + inner[3] <= outer[1] + outer[3] + EPS
}

/// Runs `cause`, named `what`, and checks the highlighted row of `rows` from the first frame it
/// shows on, the cause's own or the one its answer arrives on: the list's scroll moves at most
/// once, a jump and never a glide; the row stays inside the window and the list's settled box on
/// every frame; and once the window settles it shows whole while the field keeps the keyboard.
/// The rows and the list may still move into place as they did before, as inserted rows slide in
/// and a message above the list takes its room. Returns the highlighted row's key.
fn keeps_highlight_in_view(
    world: &mut World,
    what: &str,
    list: &str,
    rows: &str,
    cause: impl FnOnce(&Daemon, &mut Driver),
) -> String {
    let rec = world.record(60.0, 0.5, cause);
    let row = highlighted_key(world, rows);
    let drawn_as = |key: &str, k: usize| {
        let suffix = format!("/{key}");
        rec.tracks
            .iter()
            .filter(|t| t.path.ends_with(&suffix))
            .find_map(|t| t.frames.iter().find(|(f, _)| *f == k).map(|(_, at)| *at))
    };
    let from = rec.inputs[0].0;
    let settled = rec.frames - 1;
    let shown: Vec<_> = (from..rec.frames)
        .filter_map(|k| drawn_as(&row, k).map(|at| (k, at)))
        .collect();
    let (first, _) = *shown
        .first()
        .unwrap_or_else(|| panic!("{row} was not drawn after {what}"));
    assert_eq!(
        shown.len(),
        rec.frames - first,
        "{row} left the window after it showed"
    );
    let list_box = drawn_as(list, settled)
        .unwrap_or_else(|| panic!("{list} was drawn"))
        .vis;
    let window = [0.0, 0.0, rec.size.0, rec.size.1];
    let jumps: Vec<usize> = shown
        .windows(2)
        .filter(|pair| pair[0].1.scrolled != pair[1].1.scrolled)
        .map(|pair| pair[1].0)
        .collect();
    assert!(
        jumps.len() <= 1,
        "{list} scrolled on frames {jumps:?} after {row} showed on frame {first} ({what} was on {from}): a glide, not a jump"
    );
    for (k, at) in &shown {
        assert!(
            inside(at.rect, list_box) && inside(at.rect, window),
            "after {what}, {row} on frame {k} is at {:?}, outside {list}'s {list_box:?}",
            at.rect,
        );
    }
    let at = drawn_as(&row, settled).expect("drawn");
    assert!(
        inside(at.rect, at.vis),
        "after {what}, {row} is at {:?} but shows {:?}",
        at.rect,
        at.vis,
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.path.field"))
            .is_some_and(|n| n.is("focused")),
        "the field keeps the keyboard"
    );
    row
}

fn press_keeps_highlight_in_view(world: &mut World, key: Key, list: &str, rows: &str) -> String {
    let what = format!("{key:?}");
    keeps_highlight_in_view(world, &what, list, rows, |_, app| {
        app.key(KeyEvent {
            key,
            mods: Mods::default(),
        })
    })
}

fn type_keeps_highlight_in_view(world: &mut World, text: &str, list: &str, rows: &str) -> String {
    let what = format!("typing {text:?}");
    keeps_highlight_in_view(world, &what, list, rows, |_, app| app.type_text(text))
}

fn index_of(key: &str) -> usize {
    key.rsplit('.').next().unwrap().parse().unwrap()
}

/// Presses Down from the highlighted row onto the last, checking each step.
fn down_to_the_last(world: &mut World, list: &str, rows: &str) {
    let count = options(world, rows).len();
    for _ in index_of(&highlighted_key(world, rows)) + 1..count {
        press_keeps_highlight_in_view(world, Key::Down, list, rows);
    }
    assert_eq!(highlighted_key(world, rows), format!("{rows}{}", count - 1));
}

/// Down through every row and Up back to the first, where the list shows its top with its heading
/// again as it opened, then Up once more onto the last, checking each step; returns the last
/// row's key.
fn walk_the_list(world: &mut World, list: &str, rows: &str) -> String {
    let count = options(world, rows).len();
    down_to_the_last(world, list, rows);
    for _ in 1..count {
        press_keeps_highlight_in_view(world, Key::Up, list, rows);
    }
    assert_eq!(highlighted_key(world, rows), format!("{rows}0"));
    assert_eq!(world.screen().scrolled(Target::key(list)), Some(0.0));
    let last = press_keeps_highlight_in_view(world, Key::Up, list, rows);
    assert_eq!(last, format!("{rows}{}", count - 1));
    keeps_the_list_inset_under_the_last(world, list, rows, "Up onto the last");
    last
}

/// The room a launcher's list leaves under its last row at rest.
const LIST_END_INSET: f32 = 8.0;

/// Checks that the last row of `rows` shows the list's end inset between it and the bottom of
/// `list`, as the list does at rest when it does not scroll.
fn keeps_the_list_inset_under_the_last(world: &World, list: &str, rows: &str, how: &str) {
    let last = format!("{rows}{}", options(world, rows).len() - 1);
    let elements = world.runtime().elements();
    let find = |key: &str| {
        elements
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("{key} is drawn"))
    };
    let shown = find(list).visible;
    let row = find(&last).rect;
    let room_under = shown[1] + shown[3] - (row[1] + row[3]);
    assert!(
        (room_under - LIST_END_INSET).abs() < 0.5,
        "{how}: {last} has {room_under} points under it in {list}, not the list's {LIST_END_INSET}"
    );
}

const LOCATIONS: (&str, &str) = ("new-session.locations", "new-session.row.");
const WHERE: (&str, &str) = ("new-session.where", "new-session.where.row.");

#[test]
fn arrows_keep_the_highlighted_location_whole_in_the_smallest_window_at_the_largest_text() {
    let mut world = small_with("1.5", &PLACES, shop("origin/main"));
    walk_the_list(&mut world, LOCATIONS.0, LOCATIONS.1);
    assert_eq!(selected(&world, LOCATIONS.1), Some("hotel".to_owned()));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [("/home/me/projects/hotel".to_owned(), "claude".to_owned())]
    );
}

#[test]
fn arrows_keep_the_highlighted_worktree_whole_in_the_smallest_window_at_the_largest_text() {
    let mut world = small_with("1.5", &[REPO], eight_worktrees());
    open_where(&mut world);
    walk_the_list(&mut world, WHERE.0, WHERE.1);
    assert_eq!(selected(&world, WHERE.1), Some("w8".to_owned()));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [(format!("{REPO}--w8"), "claude".to_owned())]
    );
}

#[test]
fn typing_after_the_last_location_shows_the_top_row_it_highlights() {
    for scale in ["1", "1.5"] {
        let mut world = small_with(scale, &PLACES, shop("origin/main"));
        down_to_the_last(&mut world, LOCATIONS.0, LOCATIONS.1);
        let row = type_keeps_highlight_in_view(&mut world, "o", LOCATIONS.0, LOCATIONS.1);
        assert_eq!(row, format!("{}0", LOCATIONS.1), "at {scale}");
        let name = selected(&world, LOCATIONS.1).expect("a row is highlighted");
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(format!("/home/me/projects/{name}"), "claude".to_owned())],
            "at {scale}"
        );
    }
}

#[test]
fn typing_after_the_last_worktree_shows_the_new_worktree_it_highlights() {
    for scale in ["1", "1.5"] {
        let mut world = small_with(scale, &[REPO], eight_worktrees());
        open_where(&mut world);
        down_to_the_last(&mut world, WHERE.0, WHERE.1);
        let row = type_keeps_highlight_in_view(&mut world, "w", WHERE.0, WHERE.1);
        assert_eq!(row, format!("{}0", WHERE.1), "at {scale}");
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(format!("{REPO}--w"), "claude".to_owned())],
            "at {scale}"
        );
    }
}

#[test]
fn switching_to_where_and_back_from_the_last_location_shows_the_highlighted_rows() {
    let mut recent = PLACES[..7].to_vec();
    recent.push(REPO);
    let mut world = small_with("1.5", &recent, eight_worktrees());
    down_to_the_last(&mut world, LOCATIONS.0, LOCATIONS.1);
    assert_eq!(selected(&world, LOCATIONS.1), Some("shop".to_owned()));
    press_keeps_highlight_in_view(&mut world, Key::Tab, WHERE.0, WHERE.1);
    down_to_the_last(&mut world, WHERE.0, WHERE.1);
    let row = press_keeps_highlight_in_view(&mut world, Key::Escape, LOCATIONS.0, LOCATIONS.1);
    assert_eq!(row, format!("{}0", LOCATIONS.1));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [("/home/me/projects/alpha".to_owned(), "claude".to_owned())]
    );
}

#[test]
fn worktrees_that_arrive_after_a_branch_was_typed_show_the_row_it_names() {
    let mut world = small_with("1.5", &[REPO], eight_worktrees());
    world.daemon.answer_in_turn("get_repo_info", [Answer::Hold]);
    press(&mut world, Key::Tab);
    world.gesture(|app| app.type_text("w8"));
    let asked = world.daemon.sent_of("get_repo_info").remove(0);
    let row = keeps_highlight_in_view(
        &mut world,
        "the worktrees arriving",
        WHERE.0,
        WHERE.1,
        |daemon, _| {
            daemon.reply_to(
                &asked,
                GetRepoInfoResultMessage {
                    success: true,
                    info: Some(eight_worktrees()),
                    ..GetRepoInfoResultMessage::new()
                },
            )
        },
    );
    assert_eq!(selected(&world, WHERE.1), Some("w8".to_owned()));
    assert_eq!(row, format!("{}3", WHERE.1));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [(format!("{REPO}--w8"), "claude".to_owned())]
    );
}

fn outpost(name: &str) -> OutpostInfo {
    OutpostInfo {
        id: format!("outpost-{name}"),
        name: name.into(),
        destination: format!("{name}:2200"),
        state: "connected".into(),
        reach: "reachable".into(),
        state_since: "2026-05-16T16:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn outposts_now(outposts: Vec<OutpostInfo>) -> OutpostsUpdatedMessage {
    OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    }
}

fn alpha_machines(names: &[&str]) -> OutpostsUpdatedMessage {
    outposts_now(names.iter().map(|n| outpost(n)).collect())
}

/// A search for "al" in the smallest window at the largest text, matching each machine in
/// `machines` and the recent alpha, below them.
fn searching_alpha(machines: &[&str]) -> World {
    let mut settings = settings("main_repo");
    settings.push(("uiScale".to_owned(), "1.5".to_owned()));
    settings.push(("outposts_enabled".to_owned(), "true".to_owned()));
    let mut world = World::start_sized(
        machine(
            &PLACES,
            shop("origin/main"),
            &settings,
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ),
        (800.0, 600.0),
    );
    world.open_desktop();
    world.daemon.emit(alpha_machines(machines));
    world.settle();
    press_action(&mut world, &Action::SessionNew);
    world.gesture(|app| app.type_text("al"));
    assert_eq!(options(&world, LOCATIONS.1).len(), machines.len() + 1);
    world
}

fn down_to(world: &mut World, name: &str) {
    while selected(world, LOCATIONS.1).as_deref() != Some(name) {
        press(world, Key::Down);
    }
}

#[test]
fn machines_leaving_above_the_highlight_keep_it_on_its_row() {
    let mut world = searching_alpha(&["alpha-box", "alpha-dev", "alpha-lab"]);
    down_to_the_last(&mut world, LOCATIONS.0, LOCATIONS.1);
    assert_eq!(selected(&world, LOCATIONS.1), Some("alpha".to_owned()));

    keeps_highlight_in_view(
        &mut world,
        "two machines leaving",
        LOCATIONS.0,
        LOCATIONS.1,
        |daemon, _| daemon.emit(alpha_machines(&["alpha-box"])),
    );
    assert_eq!(selected(&world, LOCATIONS.1), Some("alpha".to_owned()));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [("/home/me/projects/alpha".to_owned(), "claude".to_owned())]
    );
}

#[test]
fn the_highlighted_machine_leaving_gives_the_highlight_to_the_top_row() {
    let all = [
        "alpha-1", "alpha-2", "alpha-3", "alpha-4", "alpha-5", "alpha-6",
    ];
    let mut world = searching_alpha(&all);
    down_to(&mut world, "alpha-6:");
    assert!(world.screen().scrolled(Target::key(LOCATIONS.0)) > Some(0.0));

    let row = keeps_highlight_in_view(
        &mut world,
        "the highlighted machine leaving",
        LOCATIONS.0,
        LOCATIONS.1,
        |daemon, _| daemon.emit(alpha_machines(&all[..5])),
    );
    assert_eq!(row, format!("{}0", LOCATIONS.1));
    assert_eq!(world.screen().scrolled(Target::key(LOCATIONS.0)), Some(0.0));
    press(&mut world, Key::Enter);
    assert_eq!(field(&world, "new-session"), "alpha-1:~/");
}

#[test]
fn machines_reordering_keep_the_highlight_on_its_row() {
    let all = [
        "alpha-1", "alpha-2", "alpha-3", "alpha-4", "alpha-5", "alpha-6",
    ];
    let mut world = searching_alpha(&all);
    down_to(&mut world, "alpha-2:");
    let before = highlighted_key(&world, LOCATIONS.1);

    let mut reversed = all;
    reversed.reverse();
    let row = keeps_highlight_in_view(
        &mut world,
        "the machines reordering",
        LOCATIONS.0,
        LOCATIONS.1,
        |daemon, _| daemon.emit(alpha_machines(&reversed)),
    );
    assert_ne!(row, before, "alpha-2 moved");
    assert_eq!(selected(&world, LOCATIONS.1), Some("alpha-2:".to_owned()));
    press(&mut world, Key::Enter);
    assert_eq!(field(&world, "new-session"), "alpha-2:~/");
}

/// Eight recents a search for "bu" matches, which a machine named build-box also matches.
fn build_folders() -> Vec<String> {
    (1..=8)
        .map(|i| format!("/home/me/projects/build-notes-{i:02}"))
        .collect()
}

fn with_outposts(scale: &str, recent: &[String]) -> World {
    let mut settings = settings("main_repo");
    settings.push(("uiScale".to_owned(), scale.to_owned()));
    settings.push(("outposts_enabled".to_owned(), "true".to_owned()));
    let recent: Vec<&str> = recent.iter().map(String::as_str).collect();
    let mut world = World::start_sized(
        machine(
            &recent,
            shop("origin/main"),
            &settings,
            vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        ),
        (800.0, 600.0),
    );
    world.open_desktop();
    world
}

fn middle_of(world: &World, key: &str) -> (f32, f32) {
    let [x, y, w, h] = world.screen().bounds(Target::key(key)).expect("laid out");
    (x + w / 2.0, y + h / 2.0)
}

/// A wheel turn of `dy` points down at `at`, as the platform delivers it.
fn wheel_at(world: &mut World, at: (f32, f32), dy: f32) {
    world.gesture(|app| {
        app.runtime
            .wheel_event(rune::WheelEvent::new(0.0, -dy).at(at.0, at.1))
    });
}

fn wheel_down(world: &mut World, dy: f32) {
    let at = middle_of(world, LOCATIONS.0);
    wheel_at(world, at, dy);
    assert!(world.screen().scrolled(Target::key(LOCATIONS.0)) > Some(0.0));
}

#[test]
fn a_machine_appearing_above_the_highlight_keeps_it_on_its_row() {
    let folders = build_folders();
    for scale in ["1", "1.5"] {
        let mut world = with_outposts(scale, &folders);
        press_action(&mut world, &Action::SessionNew);
        world.gesture(|app| app.type_text("bu"));
        let count = options(&world, LOCATIONS.1).len();
        for _ in 1..count - 1 {
            press(&mut world, Key::Down);
        }
        let before = selected(&world, LOCATIONS.1).expect("a row is highlighted");
        let hovered = middle_of(&world, &format!("{}{}", LOCATIONS.1, count - 3));
        world.gesture(|app| app.move_to(hovered));

        keeps_highlight_in_view(
            &mut world,
            "a machine appearing above",
            LOCATIONS.0,
            LOCATIONS.1,
            |daemon, _| daemon.emit(outposts_now(vec![outpost("build-box")])),
        );
        assert_eq!(options(&world, LOCATIONS.1).len(), count + 1, "at {scale}");
        assert_eq!(selected(&world, LOCATIONS.1).as_ref(), Some(&before));
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(format!("/home/me/projects/{before}"), "claude".to_owned())],
            "at {scale}"
        );
    }
}

#[test]
fn recents_that_arrive_after_a_wheel_show_the_row_they_highlight() {
    let folders = build_folders();
    let mut world = with_outposts("1.5", &folders);
    press_action(&mut world, &Action::SessionNew);
    press(&mut world, Key::Escape);
    world.daemon.on("get_recent_locations", |_| ());
    press_action(&mut world, &Action::SessionNew);
    assert!(world.screen().active_option().is_none());
    wheel_down(&mut world, 400.0);
    let asked = world.daemon.sent_of("get_recent_locations").pop().unwrap();
    let recents: Vec<RecentLocation> = folders
        .iter()
        .enumerate()
        .map(|(i, path)| RecentLocation {
            path: path.clone(),
            last_seen: format!("2026-05-16T16:0{}:00Z", 9 - i),
            use_count: 1,
            ..RecentLocation::default()
        })
        .collect();

    let row = keeps_highlight_in_view(
        &mut world,
        "the recents arriving",
        LOCATIONS.0,
        LOCATIONS.1,
        |daemon, _| {
            daemon.reply_to(
                &asked,
                RecentLocationsResultMessage {
                    success: true,
                    home_path: Some(HOME.into()),
                    recent_locations: recents,
                    ..RecentLocationsResultMessage::new()
                },
            )
        },
    );
    assert_eq!(row, format!("{}0", LOCATIONS.1));
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [(folders[0].clone(), "claude".to_owned())]
    );
}

#[test]
fn a_machine_appearing_above_the_top_row_keeps_the_highlight_on_it() {
    let folders = build_folders();
    let mut world = with_outposts("1.5", &folders);
    press_action(&mut world, &Action::SessionNew);
    world.gesture(|app| app.type_text("bu"));
    assert_eq!(
        selected(&world, LOCATIONS.1),
        Some("build-notes-01".to_owned())
    );

    keeps_highlight_in_view(
        &mut world,
        "a machine appearing above",
        LOCATIONS.0,
        LOCATIONS.1,
        |daemon, _| daemon.emit(outposts_now(vec![outpost("build-box")])),
    );
    assert_eq!(options(&world, LOCATIONS.1)[0].0, "build-box:");
    assert_eq!(
        selected(&world, LOCATIONS.1),
        Some("build-notes-01".to_owned())
    );
    press(&mut world, Key::Enter);
    assert_eq!(
        launched(&world),
        [(folders[0].clone(), "claude".to_owned())]
    );
}

#[test]
fn wheeled_to_its_end_the_list_keeps_its_inset_under_the_last_row() {
    for (list, rows) in [LOCATIONS, WHERE] {
        let mut world = if list == WHERE.0 {
            let mut world = small_with("1.5", &[REPO], eight_worktrees());
            open_where(&mut world);
            world
        } else {
            small_with("1.5", &PLACES, shop("origin/main"))
        };
        let at = middle_of(&world, list);
        wheel_at(&mut world, at, 2000.0);
        assert!(world.screen().scrolled(Target::key(list)) > Some(0.0));
        keeps_the_list_inset_under_the_last(&world, list, rows, "wheeled to the end");
    }
}

/// Scrolls the locations by hand, as `how` names: the wheel, or a drag down the scroll bar.
fn scroll_by_hand(world: &mut World, how: &str) {
    let [x, y, w, h] = world
        .screen()
        .bounds(Target::key(LOCATIONS.0))
        .expect("laid out");
    match how {
        "wheel" => wheel_at(world, (x + w / 2.0, y + h / 2.0), 200.0),
        _ => world.gesture(|app| app.drag_from((x + w - 4.0, y + 8.0), (x + w - 4.0, y + h - 8.0))),
    }
    assert!(
        world.screen().scrolled(Target::key(LOCATIONS.0)) > Some(0.0),
        "the {how} scrolled the list"
    );
}

#[test]
fn rows_arriving_after_the_user_scrolls_leave_the_list_where_they_scrolled_it() {
    let folders = build_folders();
    for how in ["wheel", "scroll bar"] {
        let mut world = with_outposts("1.5", &folders);
        press_action(&mut world, &Action::SessionNew);
        world.gesture(|app| app.type_text("bu"));
        press(&mut world, Key::Down);
        assert_eq!(
            selected(&world, LOCATIONS.1),
            Some("build-notes-02".to_owned())
        );
        scroll_by_hand(&mut world, how);
        let scrolled = world.screen().scrolled(Target::key(LOCATIONS.0));

        world.daemon.emit(outposts_now(vec![outpost("build-box")]));
        world.settle();
        rest(&mut world);
        assert_eq!(
            options(&world, LOCATIONS.1)[0].0,
            "build-box:",
            "after the {how}"
        );
        assert_eq!(
            world.screen().scrolled(Target::key(LOCATIONS.0)),
            scrolled,
            "after the {how}"
        );
        assert_eq!(
            selected(&world, LOCATIONS.1),
            Some("build-notes-02".to_owned())
        );
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(folders[1].clone(), "claude".to_owned())],
            "after the {how}"
        );
    }
}

fn recents_reply(paths: Vec<(String, Option<String>)>) -> RecentLocationsResultMessage {
    RecentLocationsResultMessage {
        success: true,
        home_path: Some(HOME.into()),
        recent_locations: paths
            .into_iter()
            .enumerate()
            .map(|(i, (path, host))| RecentLocation {
                path,
                host,
                last_seen: format!("2026-05-16T16:{:02}:00Z", 30 - i),
                use_count: 1,
                ..RecentLocation::default()
            })
            .collect(),
        ..RecentLocationsResultMessage::new()
    }
}

fn whole_in(world: &World, row: &str, list: &str) -> bool {
    let bounds = |key: &str| world.screen().bounds(Target::key(key)).expect("laid out");
    inside(bounds(row), bounds(list))
}

#[test]
fn a_press_on_the_list_that_does_not_scroll_it_keeps_the_highlight_in_view() {
    let folders = build_folders();
    for scale in ["1", "1.5"] {
        let mut world = with_outposts(scale, &folders);
        press_action(&mut world, &Action::SessionNew);
        world.gesture(|app| app.type_text("bu"));
        down_to(&mut world, "build-notes-08");
        let scrolled = world.screen().scrolled(Target::key(LOCATIONS.0));
        let [x, y, _, h] = world
            .screen()
            .bounds(Target::key(LOCATIONS.0))
            .expect("laid out");
        world.gesture(|app| app.click_at((x + 2.0, y + h / 2.0)));
        assert_eq!(world.screen().scrolled(Target::key(LOCATIONS.0)), scrolled);
        assert!(launched(&world).is_empty());

        keeps_highlight_in_view(
            &mut world,
            "a machine appearing above",
            LOCATIONS.0,
            LOCATIONS.1,
            |daemon, _| daemon.emit(outposts_now(vec![outpost("build-box")])),
        );
        assert_eq!(
            selected(&world, LOCATIONS.1),
            Some("build-notes-08".to_owned())
        );
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(folders[7].clone(), "claude".to_owned())],
            "at {scale}"
        );
    }
}

#[test]
fn recents_a_dialog_hears_keep_the_inline_launchers_highlight_in_view() {
    const INLINE: (&str, &str) = ("launcher.locations", "launcher.row.");
    let folders = build_folders();
    for scale in ["1", "1.5"] {
        let mut settings = settings("main_repo");
        settings.push(("uiScale".to_owned(), scale.to_owned()));
        let recent: Vec<&str> = folders.iter().map(String::as_str).collect();
        let mut world = World::start_sized(
            machine(
                &recent,
                shop("origin/main"),
                &settings,
                vec![desktop("d1", "Desktop 1", vec![])],
            ),
            (800.0, 600.0),
        );
        world.open_desktop();
        for _ in 1..folders.len() {
            press(&mut world, Key::Down);
        }
        assert_eq!(
            selected(&world, INLINE.1),
            Some("build-notes-08".to_owned())
        );
        world.daemon.on("get_recent_locations", |_| ());
        press_action(&mut world, &Action::SessionNewTerminal);
        let asked = world.daemon.sent_of("get_recent_locations").pop().unwrap();
        let newest = "/home/me/projects/build-notes-09".to_owned();
        let paths = std::iter::once(newest)
            .chain(folders.iter().cloned())
            .map(|p| (p, None))
            .collect();
        world.daemon.reply_to(&asked, recents_reply(paths));
        world.settle();
        press(&mut world, Key::Escape);

        assert_eq!(
            selected(&world, INLINE.1),
            Some("build-notes-08".to_owned())
        );
        let row = highlighted_key(&world, INLINE.1);
        assert_eq!(row, format!("{}8", INLINE.1));
        assert!(whole_in(&world, &row, INLINE.0), "at {scale}");
        press(&mut world, Key::Enter);
        assert_eq!(
            launched(&world),
            [(folders[7].clone(), "claude".to_owned())],
            "at {scale}"
        );
    }
}

#[test]
fn a_listing_that_fails_keeps_the_highlight_whole_as_its_message_takes_room() {
    let folders = build_folders();
    for scale in ["1", "1.5"] {
        let mut world = with_outposts(scale, &[]);
        world.daemon.on("browse_directory", |_| ());
        world.daemon.on("get_recent_locations", |_| ());
        world.daemon.emit(outposts_now(vec![outpost("build-box")]));
        world.settle();
        press_action(&mut world, &Action::SessionNew);
        let asked = world.daemon.sent_of("get_recent_locations").pop().unwrap();
        let paths = folders
            .iter()
            .map(|p| (p.clone(), Some("outpost-build-box".to_owned())))
            .collect();
        world.daemon.reply_to(&asked, recents_reply(paths));
        world.settle();
        world.gesture(|app| app.type_text("build-box:/home/me/projects/bu"));
        let listing = world
            .daemon
            .sent_of("browse_directory")
            .into_iter()
            .find(|r| {
                absolute(r["input_path"].as_str().unwrap()) == "/home/me/projects"
                    && r["host"] == "outpost-build-box"
            })
            .expect("the folder was asked for");
        assert_eq!(options(&world, LOCATIONS.1).len(), folders.len());
        press(&mut world, Key::Up);
        assert_eq!(
            selected(&world, LOCATIONS.1),
            Some("build-notes-08".to_owned())
        );

        keeps_highlight_in_view(
            &mut world,
            "the listing failing",
            LOCATIONS.0,
            LOCATIONS.1,
            |daemon, _| {
                daemon.reply_to(
                    &listing,
                    BrowseDirectoryResultMessage {
                        success: false,
                        error: Some("open /home/me/projects: permission denied".into()),
                        ..BrowseDirectoryResultMessage::new()
                    },
                )
            },
        );
        world.settle();
        rest(&mut world);
        let status = world
            .screen()
            .find(Target::key("new-session.status"))
            .and_then(|n| n.name);
        assert!(
            status
                .as_deref()
                .is_some_and(|s| s.contains("permission denied")),
            "{status:?}"
        );
        press(&mut world, Key::Enter);
        let started = world.daemon.sent_of("spawn_session");
        let started = started.last().expect("a start");
        assert_eq!(started["cwd"], folders[7].as_str(), "at {scale}");
        assert_eq!(started["host"], "outpost-build-box", "at {scale}");
    }
}

#[test]
fn a_browsed_result_that_leaves_while_highlighted_gives_the_highlight_to_the_first_left() {
    const MISSING: &str = "/home/me/projects/checkout";
    for scale in ["1", "1.5"] {
        let mut world = with_outposts(scale, &[]);
        world.daemon.on("browse_directory", |r| {
            let dir = absolute(r["input_path"].as_str().unwrap());
            if dir == MISSING && (r["host"].is_null() || r["host"] == "outpost-build-box") {
                return message(BrowseDirectoryResultMessage {
                    success: false,
                    error: Some(format!("open {dir}: no such file or directory")),
                    ..BrowseDirectoryResultMessage::new()
                });
            }
            message(BrowseDirectoryResultMessage {
                success: true,
                directory: dir,
                home_path: Some(HOME.into()),
                ..BrowseDirectoryResultMessage::new()
            })
        });
        world.daemon.on("inspect_path", |r| {
            let path = r["path"].as_str().unwrap();
            let elsewhere = r["host"] == "outpost-blue" || r["host"] == "outpost-green";
            inspected(path, absolute(path) != MISSING || elsewhere)
        });
        world.daemon.emit(outposts_now(vec![
            outpost("build-box"),
            outpost("blue"),
            outpost("green"),
        ]));
        world.settle();
        press_action(&mut world, &Action::SessionNew);
        world.gesture(|app| app.type_text("build-box:~/projects/checkout/"));
        let found = options(&world, LOCATIONS.1);
        assert_eq!(found.len(), 2, "{found:?}");
        press(&mut world, Key::Up);
        let left = selected(&world, LOCATIONS.1).expect("a row is highlighted");
        let (gone, kept) = if left.ends_with("blue") {
            ("blue", "green")
        } else {
            ("green", "blue")
        };

        world
            .daemon
            .emit(outposts_now(vec![outpost("build-box"), outpost(kept)]));
        world.settle();
        let found = options(&world, LOCATIONS.1);
        assert_eq!(found.len(), 1, "{found:?} after {gone} left");
        assert!(found[0].1, "{found:?}: the row left takes the highlight");
        assert_eq!(world.screen().scrolled(Target::key(LOCATIONS.0)), Some(0.0));
        press(&mut world, Key::Enter);
        assert!(
            field(&world, "new-session").starts_with(&format!("{kept}:")),
            "at {scale}: {}",
            field(&world, "new-session")
        );
    }
}

#[test]
fn an_answer_that_moves_no_row_keeps_the_highlight_whole_and_the_launcher_at_rest() {
    let mut world = with_outposts("1.5", &build_folders());
    press_action(&mut world, &Action::SessionNew);
    world.gesture(|app| app.type_text("bu"));
    down_to(&mut world, "build-notes-08");
    rest(&mut world);

    world.daemon.emit(outposts_now(vec![outpost("tokyo")]));
    world.settle();
    rest(&mut world);
    assert_eq!(
        selected(&world, LOCATIONS.1),
        Some("build-notes-08".to_owned())
    );
    let row = highlighted_key(&world, LOCATIONS.1);
    assert!(whole_in(&world, &row, LOCATIONS.0));
}

#[test]
fn tab_shows_every_control_whole_in_the_smallest_window() {
    let mut world = open("main_repo");
    world.resize((533.0, 400.0));
    super::reach::tab_round_shows_whole(&mut world, "").unwrap_or_else(|why| panic!("{why}"));
}

/// Three plugins whose names are too long to share a line, beside Claude and Codex.
const LONG_NAMED: [&str; 3] = ["review-0", "review-1", "review-2"];

/// Where the smallest window at the largest text shows a launcher: the dialog ⌘N opens over a
/// desktop with an agent, or the empty desktop's own.
#[derive(Clone, Copy, Debug)]
enum Opening {
    Dialog,
    EmptyDesktop,
}

impl Opening {
    fn key(self) -> &'static str {
        match self {
            Opening::Dialog => "new-session",
            Opening::EmptyDesktop => "launcher",
        }
    }
}

/// The smallest window at 150% text with Claude and Codex, which run YOLO, and `plugins`, each
/// named too long to share a line, the launcher showing where `opening` puts it.
fn smallest_at_largest_text(launcher: bool, opening: Opening, plugins: &[&'static str]) -> World {
    let mut settings = settings("main_repo");
    settings.retain(|(key, _)| key != LAUNCHER.0);
    settings.push((LAUNCHER.0.to_owned(), launcher.to_string()));
    settings.push(("uiScale".to_owned(), "1.5".to_owned()));
    for id in ["claude", "codex"] {
        settings.push((format!("{id}_cap_yolo"), "true".to_owned()));
    }
    for (n, id) in plugins.iter().enumerate() {
        settings.push((format!("{id}_available"), "true".to_owned()));
        settings.push((
            format!("{id}_display_name"),
            format!("Engineering observability staging review agent {n}"),
        ));
    }
    let desktops = match opening {
        Opening::Dialog => vec![desktop("d1", "Desktop 1", vec![pane("d1", "p1")])],
        Opening::EmptyDesktop => {
            let mut desktops = three_desktops();
            desktops.rotate_left(1);
            desktops
        }
    };
    let daemon = machine(&PLACES, shop("origin/main"), &settings, desktops);
    let harnesses: Vec<&str> = ["claude", "codex"]
        .into_iter()
        .chain(plugins.iter().copied())
        .collect();
    daemon.on("delegation_preferences_get", move |_| {
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
    });
    let mut world = World::start_sized(daemon, (800.0, 600.0));
    world.open_desktop();
    if let Opening::Dialog = opening {
        press_action(&mut world, &Action::SessionNew);
    }
    world
}

fn with_long_named_plugins(launcher: bool) -> World {
    smallest_at_largest_text(launcher, Opening::Dialog, &LONG_NAMED)
}

#[test]
fn the_chosen_harness_yolo_and_the_first_recent_show_whole_from_opening_in_the_smallest_window() {
    for launcher in [true, false] {
        for opening in [Opening::Dialog, Opening::EmptyDesktop] {
            for plugins in [&[][..], &LONG_NAMED[..]] {
                shows_its_controls_and_first_recent_whole(launcher, opening, plugins);
            }
        }
    }
}

/// With the harnesses stacked, the YOLO switch is past the band's end, and its scroll and ⌥Q
/// reach it.
fn shows_its_controls_and_first_recent_whole(
    launcher: bool,
    opening: Opening,
    plugins: &[&'static str],
) {
    let mut world = smallest_at_largest_text(launcher, opening, plugins);
    let w = opening.key();
    let whole_in_the_band: &[&str] = if plugins.is_empty() {
        &["agent.claude", "yolo", "yolo.keys"]
    } else {
        &["agent.claude"]
    };
    for when in ["on opening", "after typing"] {
        if when == "after typing" {
            type_path(&mut world, w, "alpha");
        }
        let how = format!(
            "launcher {launcher}, {opening:?} with {} plugins, {when}",
            plugins.len()
        );
        let bounds = |key: String| {
            world
                .screen()
                .bounds(Target::key(key.clone()))
                .unwrap_or_else(|| panic!("{how}: {key} is laid out"))
        };
        let band = bounds(format!("{w}.controls"));
        for part in whole_in_the_band {
            let shown = bounds(format!("{w}.{part}"));
            assert!(
                inside(shown, band),
                "{how}: {part} {shown:?} shows whole in its band {band:?}"
            );
        }
        let list = bounds(format!("{w}.locations"));
        let first = bounds(format!("{w}.row.0"));
        assert!(
            inside(first, list),
            "{how}: the first recent {first:?} shows whole in the list {list:?}"
        );
    }
}

#[test]
fn harnesses_that_stack_leave_the_recents_in_view_and_each_one_reachable() {
    for launcher in [true, false] {
        let mut world = with_long_named_plugins(launcher);
        let list = world
            .screen()
            .bounds(Target::key(LOCATIONS.0))
            .expect("the locations are laid out");
        let first = world
            .screen()
            .bounds(Target::key(format!("{}0", LOCATIONS.1)))
            .expect("the first recent is laid out");
        assert!(
            inside(first, list),
            "launcher {launcher}: the first recent {first:?} shows whole in the list {list:?}"
        );

        for (chord, agent) in [
            ("alt+t", "shell"),
            ("alt+1", "claude"),
            ("alt+2", "codex"),
            ("alt+3", "review-0"),
            ("alt+4", "review-1"),
            ("alt+5", "review-2"),
        ] {
            let chord = Chord::parse(chord).unwrap();
            world.gesture(|app| app.key(key_event(&chord)));
            shows_whole_in_its_band(&world, launcher, agent);
        }
    }
}

fn shows_whole_in_its_band(world: &World, launcher: bool, agent: &str) {
    let key = format!("new-session.agent.{agent}");
    let option = world.screen().get(Target::key(key.clone()));
    assert!(
        option.is("checked"),
        "launcher {launcher}: {agent} is chosen"
    );
    let band = world
        .screen()
        .bounds(Target::key("new-session.controls"))
        .expect("the harness band is laid out");
    let shown = world.screen().bounds(Target::key(key)).unwrap();
    assert!(
        inside(shown, band),
        "launcher {launcher}: {agent} {shown:?} shows whole in its band {band:?}"
    );
}
