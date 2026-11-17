//! Launcher 2.0, which ⌘N and an empty desktop show while `new_session_launcher_enabled` is on.

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
use attn_testkit::{Daemon, Target, World, key_event, message};
use rune::{Chord, Key, KeyEvent, KeyPlatform, Mods};
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

/// A machine as the Tauri tests serve it: recents newest first, the projects folder holding
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

fn press_action(world: &mut World, action: &Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{} has no single chord: {other:?}", action.id()),
    };
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

fn selected(world: &World, prefix: &str) -> Option<String> {
    options(world, prefix)
        .into_iter()
        .find(|(_, selected)| *selected)
        .map(|(name, _)| name)
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

/// Tauri's `stepBack`: each Escape takes one step, the Where list first, then the typed text,
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
