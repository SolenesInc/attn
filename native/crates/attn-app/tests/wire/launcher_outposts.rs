//! Launcher 2.0 with outposts attached: recents from every machine, `<machine>:` addresses,
//! each machine's harnesses, and starts on a machine that stops answering.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    AgentCatalogResult, AgentCatalogResultMessage, BrowseDirectoryResultMessage,
    CreateWorktreeResultMessage, DelegationPreferences, DelegationPreferencesResultMessage,
    DeleteWorktreeResultMessage, Desktop, DesktopPane, DirectoryEntry, GetRepoInfoResultMessage,
    Harness, InitialStateMessage, InspectPathResultMessage, OutpostInfo, OutpostsUpdatedMessage,
    PathInspection, Profile, ProfileArrangementChangedMessage, RecentLocation,
    RecentLocationsResultMessage, RepoInfo, SettingsUpdatedMessage, SpawnResultMessage, Worktree,
};
use attn_testkit::{Daemon, Node, Request, Target, World, key_event, message};
use rune::{Chord, Key, KeyEvent, KeyPlatform, Mods};
use serde_json::Value;

const HOME: &str = "/home/me";
const REPO: &str = "/home/me/projects/shop";
const NOTES: &str = "/home/me/notes";
const SENSOR: &str = "/home/me/projects/sensor-hub";
const SANDBOX: &str = "outpost-ai";
const FIELD: &str = "outpost-field";

fn sandbox() -> OutpostInfo {
    OutpostInfo {
        id: SANDBOX.into(),
        name: "ai-sandbox".into(),
        destination: "ai-sandbox:2200".into(),
        state: "connected".into(),
        reach: "reachable".into(),
        state_since: "2026-05-16T16:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn field_lab() -> OutpostInfo {
    OutpostInfo {
        id: FIELD.into(),
        name: "field-lab".into(),
        destination: "field-lab:2200".into(),
        state: "lost".into(),
        reach: "unreachable".into(),
        state_since: "2026-05-16T16:00:00Z".into(),
        ..OutpostInfo::default()
    }
}

fn recent(host: Option<&str>, path: &str, repo: bool, minute: u32) -> RecentLocation {
    RecentLocation {
        host: host.map(str::to_owned),
        path: path.into(),
        repo_root: repo.then(|| REPO.to_owned()),
        last_seen: format!("2026-05-16T16:0{minute}:00Z"),
        use_count: 1,
    }
}

fn recents() -> Vec<RecentLocation> {
    vec![
        recent(Some(SANDBOX), REPO, true, 3),
        recent(None, REPO, true, 2),
        recent(Some(FIELD), SENSOR, false, 1),
        recent(None, NOTES, false, 0),
    ]
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

type Disk = BTreeMap<String, Vec<&'static str>>;

fn disk(folders: &[(&str, &[&'static str])]) -> Disk {
    folders
        .iter()
        .map(|(dir, names)| ((*dir).to_owned(), names.to_vec()))
        .collect()
}

fn sandbox_disk() -> Disk {
    disk(&[
        (HOME, &["projects", "scratch"]),
        ("/home/me/projects", &["evals", "shop"]),
        ("/home/me/scratch", &[]),
    ])
}

fn mac_disk() -> Disk {
    disk(&[
        (HOME, &["notes", "projects"]),
        ("/home/me/projects", &["shop"]),
        (NOTES, &[]),
    ])
}

fn disks() -> BTreeMap<String, Disk> {
    BTreeMap::from([
        (String::new(), mac_disk()),
        (SANDBOX.into(), sandbox_disk()),
    ])
}

fn host(r: &Request) -> String {
    r["host"].as_str().unwrap_or_default().to_owned()
}

fn listed(input_path: &str, directory: &str, names: &[&str]) -> Value {
    message(BrowseDirectoryResultMessage {
        success: true,
        input_path: input_path.into(),
        home_path: Some(HOME.into()),
        directory: directory.into(),
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
}

fn browsed(disks: &BTreeMap<String, Disk>, r: &Request) -> Value {
    let input = r["input_path"].as_str().unwrap();
    let directory = absolute(input);
    match disks.get(&host(r)).and_then(|d| d.get(&directory)) {
        Some(names) => listed(input, &directory, names),
        None => message(BrowseDirectoryResultMessage {
            success: false,
            input_path: input.into(),
            home_path: Some(HOME.into()),
            error: Some(format!("open {directory}: no such file or directory")),
            ..BrowseDirectoryResultMessage::new()
        }),
    }
}

fn inspection(path: &str, exists: bool, repo_root: Option<&str>) -> Value {
    message(InspectPathResultMessage {
        success: true,
        inspection: Some(PathInspection {
            input_path: path.into(),
            resolved_path: absolute(path),
            home_path: Some(HOME.into()),
            exists,
            is_directory: exists,
            repo_root: repo_root.map(str::to_owned),
        }),
        ..InspectPathResultMessage::new()
    })
}

fn inspected(disks: &BTreeMap<String, Disk>, r: &Request) -> Value {
    let path = r["path"].as_str().unwrap();
    let target = absolute(path);
    let disk = disks.get(&host(r)).cloned().unwrap_or_default();
    let exists = disk.contains_key(&target)
        || disk
            .iter()
            .any(|(dir, names)| names.iter().any(|n| format!("{dir}/{n}") == target));
    inspection(path, exists, (exists && target == REPO).then_some(REPO))
}

fn info() -> RepoInfo {
    RepoInfo {
        repo: REPO.into(),
        current_branch: "main".into(),
        current_commit_hash: "abcdef1234567890".into(),
        current_commit_time: "2026-05-16T16:00:00Z".into(),
        default_branch: "main".into(),
        worktrees: vec![Worktree {
            path: format!("{REPO}--feature"),
            branch: "feature".into(),
            main_repo: REPO.into(),
            ..Worktree::default()
        }],
    }
}

fn pane() -> DesktopPane {
    DesktopPane {
        desktop_id: "d1".into(),
        pane_id: "p1".into(),
        runtime_id: "rt-p1".into(),
        session_id: "s-p1".into(),
        title: "Agent p1".into(),
        kind: "agent".into(),
        ..DesktopPane::default()
    }
}

fn desktops() -> Vec<Desktop> {
    vec![
        Desktop {
            id: "d1".into(),
            name: "Desktop 1".into(),
            active_pane_id: "p1".into(),
            panes: vec![pane()],
            profile_id: "work".into(),
            ..Desktop::default()
        },
        Desktop {
            id: "d2".into(),
            name: "Desktop 2".into(),
            profile_id: "work".into(),
            ..Desktop::default()
        },
    ]
}

fn profile(current: &str) -> Profile {
    Profile {
        id: "work".into(),
        name: "Work".into(),
        current_desktop_id: current.into(),
        ..Profile::default()
    }
}

/// What a world starts with: the settings past Launcher 2.0, Claude saved and Outposts on,
/// the outposts attached, and what the sandbox has installed.
struct Setup {
    settings: Vec<(String, String)>,
    outposts: Vec<OutpostInfo>,
    sandbox_harnesses: Vec<&'static str>,
}

impl Default for Setup {
    fn default() -> Setup {
        Setup {
            settings: Vec::new(),
            outposts: vec![sandbox(), field_lab()],
            sandbox_harnesses: vec!["claude", "codex"],
        }
    }
}

/// A desktop with an agent, Launcher 2.0 on and `setup`'s outposts attached; ⌘N not pressed.
fn world(setup: Setup) -> World {
    let mut settings: BTreeMap<String, String> = [
        ("new_session_launcher_enabled", "true"),
        ("new_session_agent", "claude"),
        ("outposts_enabled", "true"),
    ]
    .iter()
    .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
    .collect();
    settings.extend(setup.settings);
    let state = InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![profile("d1")]),
        desktops: Some(desktops()),
        settings: Some(settings.clone()),
        outposts: Some(setup.outposts),
        ..InitialStateMessage::new()
    };
    let kept = Arc::new(Mutex::new(settings));
    let installed = setup.sandbox_harnesses;
    let disks = Arc::new(disks());
    let (browse_disks, inspect_disks) = (disks.clone(), disks);
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
                harnesses: Some(harnesses(&["claude", "codex"])),
                ..DelegationPreferencesResultMessage::new()
            })
        })
        .on("agent_catalog", move |r| {
            let machine = host(r);
            message(AgentCatalogResultMessage {
                success: true,
                host: machine.clone(),
                catalog: Some(AgentCatalogResult {
                    machine,
                    harnesses: harnesses(&["claude", "codex"])
                        .into_iter()
                        .map(|h| Harness {
                            available: installed.contains(&h.id.as_str()),
                            ..h
                        })
                        .collect(),
                    ..AgentCatalogResult::default()
                }),
                ..AgentCatalogResultMessage::new()
            })
        })
        .on("get_recent_locations", |_| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some(HOME.into()),
                recent_locations: recents(),
                ..RecentLocationsResultMessage::new()
            })
        })
        .on("browse_directory", move |r| browsed(&browse_disks, r))
        .on("inspect_path", move |r| inspected(&inspect_disks, r))
        .on("get_repo_info", |_| {
            message(GetRepoInfoResultMessage {
                success: true,
                info: Some(info()),
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
        .on("delete_worktree", |r| {
            message(DeleteWorktreeResultMessage {
                success: true,
                path: r["path"].as_str().unwrap_or_default().to_owned(),
                ..DeleteWorktreeResultMessage::new()
            })
        })
        .on("spawn_session", placed);
    let mut world = World::start(daemon);
    world.open_desktop();
    world
}

fn harnesses(ids: &[&str]) -> Vec<Harness> {
    ids.iter()
        .map(|id| Harness {
            id: (*id).into(),
            name: (*id).into(),
            available: true,
            ..Harness::default()
        })
        .collect()
}

fn placed(r: &Request) -> Value {
    message(SpawnResultMessage {
        id: r["id"].as_str().unwrap().into(),
        success: true,
        desktop_id: r["placement"]["desktop_id"].as_str().map(str::to_owned),
        pane_id: Some(format!("pane-{}", r["id"].as_str().unwrap())),
        ..SpawnResultMessage::new()
    })
}

/// `setup`'s world with the launcher open.
fn open(setup: Setup) -> World {
    let mut world = world(setup);
    press_action(&mut world, &Action::SessionNew);
    world
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

fn down(world: &mut World, times: usize) {
    for _ in 0..times {
        press(world, Key::Down);
    }
}

fn field(world: &World) -> String {
    world
        .screen()
        .get(Target::key("new-session.path.field"))
        .value
        .unwrap_or_default()
}

fn type_path(world: &mut World, text: &str) {
    for _ in 0..field(world).chars().count() {
        press(world, Key::Backspace);
    }
    if !text.is_empty() {
        world.gesture(|app| app.type_text(text));
    }
}

fn row_nodes(world: &World) -> Vec<Node> {
    let screen = world.screen();
    screen
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("new-session.row."))
        })
        .collect()
}

/// Each row's name, its machine badge and whether it is highlighted.
fn rows(world: &World) -> Vec<(String, Option<String>, bool)> {
    let screen = world.screen();
    row_nodes(world)
        .into_iter()
        .map(|n| {
            let key = n.key.clone().unwrap();
            let machine = screen
                .find(Target::key(format!("{key}.machine")))
                .and_then(|m| m.name);
            (
                n.name.clone().unwrap_or_default(),
                machine,
                n.is("selected"),
            )
        })
        .collect()
}

fn names(world: &World) -> Vec<String> {
    rows(world).into_iter().map(|(name, ..)| name).collect()
}

fn named(rows: &[(&str, Option<&str>, bool)]) -> Vec<(String, Option<String>, bool)> {
    rows.iter()
        .map(|(name, machine, selected)| {
            ((*name).to_owned(), machine.map(str::to_owned), *selected)
        })
        .collect()
}

fn text_of(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

fn status(world: &World) -> Option<String> {
    text_of(world, "new-session.status")
}

fn alert(world: &World) -> Option<String> {
    text_of(world, "new-session.error")
}

fn alert_reason(world: &World) -> Option<String> {
    text_of(world, "new-session.error.reason")
}

fn dialog_open(world: &World) -> bool {
    world.screen().find(Target::key("new-session")).is_some()
}

fn harness(world: &World, id: &str) -> Node {
    world
        .screen()
        .get(Target::key(format!("new-session.agent.{id}")))
}

fn checking_mark(world: &World, id: &str) -> bool {
    world
        .screen()
        .find(Target::key(format!("new-session.agent.{id}.checking")))
        .is_some()
}

fn spawns(world: &World) -> Vec<(String, String, String)> {
    world
        .daemon
        .sent_of("spawn_session")
        .iter()
        .map(|r| {
            (
                r["cwd"].as_str().unwrap().to_owned(),
                r["agent"].as_str().unwrap().to_owned(),
                host(r),
            )
        })
        .collect()
}

fn browses(world: &World) -> Vec<(String, String)> {
    world
        .daemon
        .sent_of("browse_directory")
        .iter()
        .map(|r| (r["input_path"].as_str().unwrap().to_owned(), host(r)))
        .collect()
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

fn hold(world: &World, cmd: &str) -> Arc<Mutex<Vec<Request>>> {
    let held: Arc<Mutex<Vec<Request>>> = Arc::default();
    let keep = held.clone();
    world
        .daemon
        .on(cmd, move |r| keep.lock().unwrap().push(r.clone()));
    held
}

fn emit_outposts(world: &mut World, outposts: Vec<OutpostInfo>) {
    world.daemon.emit(OutpostsUpdatedMessage {
        outposts,
        ..OutpostsUpdatedMessage::new()
    });
    world.settle();
}

fn stalled() -> OutpostInfo {
    OutpostInfo {
        state: "stalled".into(),
        ..sandbox()
    }
}

fn rest(world: &mut World) {
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn every_machines_recents_carry_their_badge_and_only_reachable_outposts_are_asked_for_harnesses() {
    let mut world = open(Setup::default());

    assert_eq!(
        rows(&world),
        named(&[
            ("shop", Some("ai-sandbox"), true),
            ("shop", Some("This Mac"), false),
            ("sensor-hub", Some("field-lab"), false),
            ("notes", Some("This Mac"), false),
        ])
    );
    let asked = world.daemon.sent_of("get_recent_locations");
    assert_eq!(asked.len(), 1);
    assert!(asked[0].get("home_only").is_none());
    assert_eq!(
        world
            .daemon
            .sent_of("agent_catalog")
            .iter()
            .map(host)
            .collect::<Vec<_>>(),
        [SANDBOX]
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("new-session.row.2.machine"))
            .description
            .as_deref(),
        Some("unreachable")
    );

    type_path(&mut world, "ai-s");
    assert_eq!(
        rows(&world),
        named(&[
            ("ai-sandbox:", None, true),
            ("shop", Some("ai-sandbox"), false)
        ])
    );
    rest(&mut world);
}

#[test]
fn an_unreachable_machines_recent_reads_relative_to_the_home_it_last_reported() {
    let world = open(Setup {
        outposts: vec![
            sandbox(),
            OutpostInfo {
                home: Some(HOME.into()),
                ..field_lab()
            },
        ],
        ..Setup::default()
    });

    let details: Vec<String> = row_nodes(&world)
        .into_iter()
        .filter_map(|n| n.description)
        .collect();
    assert_eq!(
        details,
        [
            "~/projects/shop",
            "~/projects/shop",
            "~/projects/sensor-hub",
            "~/notes"
        ]
    );
}

#[test]
fn enter_starts_a_remote_recent_on_its_machine_where_that_machine_last_started_it() {
    for memory in ["main_repo", "new_worktree"] {
        let mut world = open(Setup {
            settings: vec![(
                format!("new_session_destination_{SANDBOX}_{REPO}"),
                memory.into(),
            )],
            ..Setup::default()
        });

        press(&mut world, Key::Enter);

        let creates = world.daemon.sent_of("create_worktree");
        if memory == "new_worktree" {
            let infos = world.daemon.sent_of("get_repo_info");
            assert_eq!((infos.len(), host(&infos[0])), (1, SANDBOX.to_owned()));
            assert_eq!(creates.len(), 1);
            assert_eq!(
                (creates[0]["main_repo"].as_str(), host(&creates[0]).as_str()),
                (Some(REPO), SANDBOX)
            );
            let branch = creates[0]["branch"].as_str().unwrap();
            assert_eq!(
                spawns(&world),
                [(format!("{REPO}--{branch}"), "claude".into(), SANDBOX.into())]
            );
        } else {
            assert!(creates.is_empty());
            assert_eq!(
                spawns(&world),
                [(REPO.into(), "claude".into(), SANDBOX.into())]
            );
        }
        assert!(!dialog_open(&world), "{memory}");
        rest(&mut world);
    }
}

#[test]
fn the_where_list_terminal_and_delete_run_on_the_rows_machine_and_memory_is_per_machine() {
    let mut world = open(Setup::default());
    press(&mut world, Key::Tab);
    let infos = world.daemon.sent_of("get_repo_info");
    assert_eq!((infos.len(), host(&infos[0])), (1, SANDBOX.to_owned()));
    type_path(&mut world, "feature");
    chord(&mut world, "mod+backspace");
    press(&mut world, Key::Text("y".into()));
    let deletes = world.daemon.sent_of("delete_worktree");
    assert_eq!(deletes[0]["path"], format!("{REPO}--feature"));
    assert_eq!(host(&deletes[0]), SANDBOX);

    type_path(&mut world, "fix-login");
    press(&mut world, Key::Enter);

    let creates = world.daemon.sent_of("create_worktree");
    assert_eq!(creates[0]["branch"], "fix-login");
    assert_eq!(host(&creates[0]), SANDBOX);
    assert_eq!(
        spawns(&world),
        [(
            format!("{REPO}--fix-login"),
            "claude".into(),
            SANDBOX.into()
        )]
    );
    let memory: Vec<(String, String)> = world
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
                r["key"].as_str().unwrap().into(),
                r["value"].as_str().unwrap().into(),
            )
        })
        .collect();
    assert_eq!(
        memory,
        [(
            format!("new_session_destination_{SANDBOX}_{REPO}"),
            "new_worktree".to_owned()
        )]
    );

    press_action(&mut world, &Action::SessionNew);
    chord(&mut world, "mod+enter");
    assert_eq!(
        spawns(&world)[1],
        (REPO.into(), "shell".into(), SANDBOX.into())
    );
    rest(&mut world);
}

#[test]
fn a_start_on_an_unreachable_machine_is_refused_without_a_request_and_says_why() {
    let lost = OutpostInfo {
        state_since: "2026-11-12T08:55:00Z".into(),
        ..field_lab()
    };
    let refused = OutpostInfo {
        state: "refused".into(),
        last_error: Some("field-lab runs attn 0.41.2 and this Mac runs 0.43.0.".into()),
        ..field_lab()
    };
    for (outpost, reason) in [
        (lost, "Lost for 5m."),
        (
            refused,
            "field-lab runs attn 0.41.2 and this Mac runs 0.43.0.",
        ),
    ] {
        let mut world = open(Setup {
            outposts: vec![sandbox(), outpost],
            ..Setup::default()
        });
        type_path(&mut world, "sensor");
        for spec in ["enter", "mod+enter"] {
            let before = world.daemon.sent().len();
            chord(&mut world, spec);
            assert_eq!(
                alert(&world).as_deref(),
                Some("field-lab is unreachable; this needs it now")
            );
            assert_eq!(alert_reason(&world).as_deref(), Some(reason));
            assert_eq!(world.daemon.sent().len(), before, "{spec}");
        }
        assert_eq!(field(&world), "sensor");
        assert!(dialog_open(&world));
        rest(&mut world);
    }
}

#[test]
fn a_harness_the_target_machine_lacks_reads_as_missing_and_is_refused() {
    let mut world = open(Setup {
        sandbox_harnesses: vec!["codex"],
        ..Setup::default()
    });
    assert_eq!(
        harness(&world, "claude").description.as_deref(),
        Some("Claude is not installed on ai-sandbox")
    );

    press(&mut world, Key::Enter);
    assert!(
        alert(&world).is_some_and(|a| a.starts_with("Claude is not installed on ai-sandbox.")),
        "{:?}",
        alert(&world)
    );
    assert!(spawns(&world).is_empty());
    down(&mut world, 1);
    assert_eq!(harness(&world, "claude").description, None);
    press(&mut world, Key::Up);
    chord(&mut world, "alt+2");
    press(&mut world, Key::Enter);

    assert_eq!(
        spawns(&world),
        [(REPO.into(), "codex".into(), SANDBOX.into())]
    );
    rest(&mut world);
}

#[test]
fn a_harness_only_an_outpost_has_can_be_picked_there_and_is_refused_on_this_mac() {
    let mut world = open(Setup {
        settings: vec![("codex_available".into(), "false".into())],
        ..Setup::default()
    });
    let codex = harness(&world, "codex");
    assert!(!codex.is("disabled"));
    assert_eq!(codex.description, None);

    chord(&mut world, "alt+2");
    assert!(harness(&world, "codex").is("checked"));
    down(&mut world, 1);
    let codex = harness(&world, "codex");
    assert_eq!(
        codex.description.as_deref(),
        Some("Codex is not installed on This Mac")
    );
    assert!(codex.is("checked"));
    press(&mut world, Key::Enter);
    assert!(alert(&world).is_some_and(|a| a.starts_with("Codex is not installed on This Mac.")));
    assert!(spawns(&world).is_empty());

    press(&mut world, Key::Up);
    press(&mut world, Key::Enter);
    assert_eq!(
        spawns(&world),
        [(REPO.into(), "codex".into(), SANDBOX.into())]
    );
    rest(&mut world);
}

#[test]
fn a_harness_reads_as_being_checked_only_while_this_mac_is_the_target() {
    let mut world = open(Setup {
        settings: vec![("claude_available".into(), "checking".into())],
        ..Setup::default()
    });
    let mut seen = Vec::new();
    for row in 0..4 {
        let claude = harness(&world, "claude");
        assert!(!claude.is("disabled"));
        assert!(claude.is("checked"));
        seen.push((claude.description, checking_mark(&world, "claude")));
        if row < 3 {
            down(&mut world, 1);
        }
    }
    let checking = Some("Checking for Claude on this machine".to_owned());
    assert_eq!(
        seen,
        [
            (None, false),
            (checking.clone(), true),
            (None, false),
            (checking, true)
        ]
    );

    press(&mut world, Key::Enter);
    assert_eq!(
        spawns(&world),
        [(NOTES.into(), "claude".into(), String::new())]
    );
    rest(&mut world);
}

#[test]
fn a_harness_an_outpost_lacks_reads_as_missing_there_while_this_mac_checks_for_it() {
    let mut world = open(Setup {
        settings: vec![("claude_available".into(), "checking".into())],
        outposts: vec![sandbox()],
        sandbox_harnesses: vec!["codex"],
    });
    assert_eq!(
        harness(&world, "claude").description.as_deref(),
        Some("Claude is not installed on ai-sandbox")
    );
    assert!(!checking_mark(&world, "claude"));

    down(&mut world, 1);
    assert_eq!(
        harness(&world, "claude").description.as_deref(),
        Some("Checking for Claude on this machine")
    );
    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some(BTreeMap::from([
            ("new_session_launcher_enabled".to_owned(), "true".to_owned()),
            ("new_session_agent".to_owned(), "claude".to_owned()),
            ("outposts_enabled".to_owned(), "true".to_owned()),
            ("claude_available".to_owned(), "false".to_owned()),
        ])),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();

    let claude = harness(&world, "claude");
    assert!(claude.is("disabled"));
    assert_eq!(
        claude.description.as_deref(),
        Some("Claude is not available on this machine")
    );
    rest(&mut world);
}

#[test]
fn a_start_on_a_stalled_machine_counts_the_wait_and_escape_lets_it_finish_in_the_background() {
    let mut world = open(Setup::default());
    let held = hold(&world, "spawn_session");
    press(&mut world, Key::Enter);
    emit_outposts(&mut world, vec![stalled(), field_lab()]);
    world.advance(Duration::from_secs(4));

    assert_eq!(
        text_of(&world, "new-session.progress").as_deref(),
        Some("Starting on ai-sandbox… no answer for 4s")
    );
    assert!(hints(&world).contains("Esc keep waiting in the background"));
    press(&mut world, Key::Escape);
    assert!(!dialog_open(&world));

    let spawn = held.lock().unwrap().remove(0);
    assert_eq!(
        (spawn["cwd"].as_str(), host(&spawn).as_str()),
        (Some(REPO), SANDBOX)
    );
    world.daemon.reply_to(
        &spawn,
        message(SpawnResultMessage {
            id: spawn["id"].as_str().unwrap().into(),
            success: false,
            error: Some("ai-sandbox is unreachable; this needs it now".into()),
            ..SpawnResultMessage::new()
        }),
    );
    world.settle();
    assert_eq!(
        text_of(&world, "notice.text").as_deref(),
        Some("Starting on ai-sandbox failed: ai-sandbox is unreachable; this needs it now")
    );
}

#[test]
fn a_start_left_to_the_background_lands_on_the_desktop_it_was_picked_on() {
    let mut world = open(Setup {
        settings: vec![(
            format!("new_session_destination_{SANDBOX}_{REPO}"),
            "new_worktree".into(),
        )],
        ..Setup::default()
    });
    let infos = hold(&world, "get_repo_info");
    press(&mut world, Key::Enter);
    emit_outposts(&mut world, vec![stalled(), field_lab()]);
    press(&mut world, Key::Escape);
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: profile("d2"),
        desktops: desktops(),
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    let read = infos.lock().unwrap().remove(0);
    world.daemon.reply_to(
        &read,
        message(GetRepoInfoResultMessage {
            success: true,
            info: Some(info()),
            ..GetRepoInfoResultMessage::new()
        }),
    );
    world.settle();

    let placed: Vec<(String, String)> = world
        .daemon
        .sent_of("spawn_session")
        .iter()
        .map(|r| {
            (
                host(r),
                r["placement"]["desktop_id"].as_str().unwrap().to_owned(),
            )
        })
        .collect();
    assert_eq!(placed, [(SANDBOX.to_owned(), "d1".to_owned())]);
}

#[test]
fn without_outposts_the_launcher_says_this_mac_is_checking_and_asks_nothing_of_machines() {
    for (outposts, enabled) in [(vec![], "true"), (vec![sandbox()], "false")] {
        let mut world = open(Setup {
            settings: vec![
                ("outposts_enabled".into(), enabled.into()),
                ("claude_available".into(), "checking".into()),
            ],
            outposts,
            ..Setup::default()
        });
        world.daemon.on("get_recent_locations", |_| {
            message(RecentLocationsResultMessage {
                success: true,
                home_path: Some(HOME.into()),
                recent_locations: vec![recent(None, REPO, true, 2), recent(None, NOTES, false, 0)],
                ..RecentLocationsResultMessage::new()
            })
        });
        press(&mut world, Key::Escape);
        press_action(&mut world, &Action::SessionNew);
        let claude = harness(&world, "claude");
        assert!(!claude.is("disabled"));
        assert_eq!(
            claude.description.as_deref(),
            Some("Checking for Claude on this machine")
        );
        assert!(
            world
                .screen()
                .find(Target::key("new-session.row.0.machine"))
                .is_none()
        );

        type_path(&mut world, "note");
        type_path(&mut world, "~/projects/");
        type_path(&mut world, "");
        press(&mut world, Key::Tab);
        type_path(&mut world, "fix");
        press(&mut world, Key::Enter);

        assert_eq!(
            spawns(&world),
            [(format!("{REPO}--fix"), "claude".into(), String::new())]
        );
        assert!(world.daemon.sent_of("agent_catalog").is_empty());
        assert!(world.daemon.sent().iter().all(|r| r.get("host").is_none()));
        let asked = world.daemon.sent_of("get_recent_locations");
        assert!(asked.iter().all(|r| r["home_only"] == true), "{enabled}");
        assert_eq!(
            browses(&world),
            [
                ("~/".to_owned(), String::new()),
                ("~/projects/".to_owned(), String::new())
            ],
            "only the folders typed on the way to ~/projects/"
        );
        rest(&mut world);
    }
}

#[test]
fn a_machines_name_suggests_it_and_tab_browses_it_from_what_the_opening_fetched() {
    let mut world = open(Setup::default());
    assert_eq!(
        browses(&world),
        [
            ("~/".to_owned(), SANDBOX.to_owned()),
            ("/home/me/projects/".to_owned(), SANDBOX.to_owned())
        ]
    );
    assert!(hints(&world).contains("name: machine"));

    type_path(&mut world, "ai");
    assert_eq!(
        rows(&world),
        named(&[
            ("ai-sandbox:", None, true),
            ("shop", Some("ai-sandbox"), false)
        ])
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.section.0"))
            .is_some()
    );
    assert_eq!(
        row_nodes(&world)[0].description.as_deref(),
        Some("connected · Tab browses ai-sandbox:~/")
    );
    assert_eq!(
        text_of(&world, "new-session.ghost").as_deref(),
        Some("-sandbox:~/")
    );

    press(&mut world, Key::Tab);
    assert_eq!(field(&world), "ai-sandbox:~/");
    assert_eq!(status(&world), None);
    assert_eq!(names(&world), ["shop", "projects/", "scratch/"]);
    assert_eq!(browses(&world).len(), 2);

    type_path(&mut world, "ai-sandbox:~/projects/e");
    assert_eq!(names(&world), ["evals/"]);
    press(&mut world, Key::Tab);
    assert_eq!(field(&world), "ai-sandbox:~/projects/evals/");
    assert_eq!(
        browses(&world)[2],
        ("~/projects/evals/".to_owned(), SANDBOX.to_owned())
    );
    rest(&mut world);
}

#[test]
fn tab_writes_a_remote_recents_address() {
    let mut world = world(Setup::default());
    press_action(&mut world, &Action::SessionNewTerminal);

    press(&mut world, Key::Tab);

    assert_eq!(field(&world), "ai-sandbox:~/projects/shop/");
    rest(&mut world);
}

#[test]
fn an_aimed_search_asks_the_machine_and_keeps_the_highlight_while_confirmed_rows_arrive() {
    let mut world = world(Setup::default());
    let asked = hold(&world, "inspect_path");
    press_action(&mut world, &Action::SessionNew);
    type_path(&mut world, "ai-sandbox:e");
    let asked = asked.lock().unwrap().clone();
    assert_eq!(
        asked
            .iter()
            .map(|r| (r["path"].as_str().unwrap().to_owned(), host(r)))
            .collect::<Vec<_>>(),
        [
            ("~/projects/shop".to_owned(), SANDBOX.to_owned()),
            (SENSOR.to_owned(), SANDBOX.to_owned()),
            ("~/notes".to_owned(), SANDBOX.to_owned())
        ]
    );
    assert_eq!(rows(&world), named(&[("shop", Some("ai-sandbox"), true)]));

    let answer = |world: &mut World, i: usize, exists: bool| {
        let path = asked[i]["path"].as_str().unwrap();
        world.daemon.reply_to(
            &asked[i],
            inspection(path, exists, (i == 0).then_some(REPO)),
        );
        world.settle();
    };
    answer(&mut world, 2, true);
    press(&mut world, Key::Down);
    assert_eq!(
        rows(&world),
        named(&[
            ("shop", Some("ai-sandbox"), false),
            ("notes", Some("ai-sandbox"), true)
        ])
    );
    answer(&mut world, 1, false);
    answer(&mut world, 0, true);
    assert_eq!(
        rows(&world),
        named(&[
            ("shop", Some("ai-sandbox"), false),
            ("notes", Some("ai-sandbox"), true)
        ])
    );

    press(&mut world, Key::Enter);
    assert_eq!(
        spawns(&world),
        [(NOTES.into(), "claude".into(), SANDBOX.into())]
    );
}

#[test]
fn a_confirmed_row_keeps_its_highlight_when_an_earlier_candidate_answers_later() {
    let mut world = world(Setup::default());
    let asked = hold(&world, "inspect_path");
    press_action(&mut world, &Action::SessionNew);
    type_path(&mut world, "ai-sandbox:o");
    let asked = asked.lock().unwrap().clone();
    let answer = |world: &mut World, path: &str| {
        let request = asked.iter().find(|r| r["path"] == path).unwrap();
        world.daemon.reply_to(request, inspection(path, true, None));
        world.settle();
    };
    answer(&mut world, "~/notes");
    press(&mut world, Key::Down);
    answer(&mut world, SENSOR);

    assert_eq!(
        rows(&world),
        named(&[
            ("shop", Some("ai-sandbox"), false),
            ("notes", Some("ai-sandbox"), true),
            ("sensor-hub", Some("ai-sandbox"), false)
        ])
    );
}

#[test]
fn a_folder_the_target_lacks_is_found_on_another_machine_and_enter_aims_the_text_there() {
    let mut world = open(Setup::default());
    type_path(&mut world, "~/scratch/");
    assert_eq!(
        rows(&world),
        named(&[("~/scratch/ is on ai-sandbox", Some("ai-sandbox"), false)])
    );
    assert_eq!(
        row_nodes(&world)[0].description.as_deref(),
        Some("Enter browses ai-sandbox:~/scratch/")
    );
    assert!(browses(&world).iter().all(|(_, h)| h != FIELD));
    press(&mut world, Key::Down);
    press(&mut world, Key::Enter);
    assert_eq!(field(&world), "ai-sandbox:~/scratch/");
    assert!(spawns(&world).is_empty());

    type_path(&mut world, "ai-sandbox:~/notes/");
    assert_eq!(
        status(&world).as_deref(),
        Some("~/notes is not on ai-sandbox")
    );
    assert_eq!(
        rows(&world),
        named(&[("~/notes/ is on This Mac", Some("This Mac"), false)])
    );
    press(&mut world, Key::Down);
    press(&mut world, Key::Tab);
    assert_eq!(field(&world), "~/notes/");

    type_path(&mut world, "ai-sandbox:~/projects/");
    assert!(names(&world).iter().all(|n| !n.contains(" is on ")));
    rest(&mut world);
}

#[test]
fn a_folder_stays_missing_after_its_machine_reports_its_home() {
    let mut world = world(Setup {
        outposts: vec![],
        ..Setup::default()
    });
    emit_outposts(&mut world, vec![sandbox(), field_lab()]);
    let held = hold(&world, "browse_directory");
    press_action(&mut world, &Action::SessionNew);
    type_path(&mut world, "ai-sandbox:~/nope/");
    let nope = held
        .lock()
        .unwrap()
        .iter()
        .find(|r| r["input_path"] == "~/nope/")
        .cloned()
        .unwrap();
    world.daemon.reply_to(
        &nope,
        message(BrowseDirectoryResultMessage {
            success: false,
            input_path: "~/nope/".into(),
            home_path: Some(HOME.into()),
            error: Some("open /home/me/nope: no such file or directory".into()),
            ..BrowseDirectoryResultMessage::new()
        }),
    );
    world.settle();
    assert_eq!(
        status(&world).as_deref(),
        Some("~/nope is not on ai-sandbox")
    );

    let prefetch = held
        .lock()
        .unwrap()
        .iter()
        .find(|r| r["input_path"] == "~/")
        .cloned()
        .unwrap();
    world.daemon.reply_to(&prefetch, listed("~/", HOME, &[]));
    world.settle();
    assert_eq!(
        status(&world).as_deref(),
        Some("~/nope is not on ai-sandbox")
    );
}

#[test]
fn the_status_line_says_how_a_slow_and_an_unreachable_machine_answer() {
    let mut world = world(Setup::default());
    let held = hold(&world, "browse_directory");
    press_action(&mut world, &Action::SessionNew);
    type_path(&mut world, "ai-sandbox:~/notes/");
    world.advance(Duration::from_millis(150));
    assert_eq!(
        status(&world).as_deref(),
        Some("asking ai-sandbox for ~/notes/ · 150 ms")
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.empty"))
            .is_none()
    );
    world.advance(Duration::from_millis(250));
    assert_eq!(
        status(&world).as_deref(),
        Some("asking ai-sandbox for ~/notes/ · 400 ms")
    );

    let prefetch = held
        .lock()
        .unwrap()
        .iter()
        .find(|r| r["input_path"] == "~/")
        .cloned()
        .unwrap();
    world
        .daemon
        .reply_to(&prefetch, listed("~/", HOME, &["projects"]));
    world.settle();
    emit_outposts(
        &mut world,
        vec![
            OutpostInfo {
                state: "lost".into(),
                reach: "unreachable".into(),
                ..sandbox()
            },
            field_lab(),
        ],
    );
    type_path(&mut world, "ai-sandbox:~/");
    assert_eq!(
        status(&world).as_deref(),
        Some("ai-sandbox unreachable · folders as of just now")
    );
    assert_eq!(names(&world), ["shop", "projects/"]);

    let before = world.daemon.sent().len();
    type_path(&mut world, "field-lab:~/");
    assert_eq!(
        status(&world).as_deref(),
        Some("field-lab unreachable · no folders cached for ~")
    );
    assert_eq!(world.daemon.sent().len(), before);
}

#[test]
fn a_typed_start_on_a_stalled_machine_finishes_in_the_background_after_escape() {
    let mut world = world(Setup::default());
    let held = hold(&world, "inspect_path");
    press_action(&mut world, &Action::SessionNew);
    type_path(&mut world, "ai-sandbox:~/projects/");
    press(&mut world, Key::Enter);
    emit_outposts(&mut world, vec![stalled(), field_lab()]);
    world.advance(Duration::from_secs(4));
    press(&mut world, Key::Escape);
    assert!(!dialog_open(&world));

    let asked = held.lock().unwrap().pop().unwrap();
    assert_eq!(asked["path"], "/home/me/projects");
    world
        .daemon
        .reply_to(&asked, inspection("/home/me/projects", true, None));
    world.settle();

    assert_eq!(
        spawns(&world),
        [("/home/me/projects".into(), "claude".into(), SANDBOX.into())]
    );
}

#[test]
fn a_name_two_machines_share_lists_both_and_browses_the_one_picked() {
    let twin = OutpostInfo {
        id: "outpost-ai-2".into(),
        name: "AI-Sandbox".into(),
        destination: "ai-sandbox-2".into(),
        ..sandbox()
    };
    let mut world = open(Setup {
        outposts: vec![sandbox(), twin],
        ..Setup::default()
    });
    let before = browses(&world).len();
    type_path(&mut world, "ai-sandbox:~/");
    assert_eq!(
        status(&world).as_deref(),
        Some("ai-sandbox names 2 machines · pick one below")
    );
    assert_eq!(names(&world), ["ai-sandbox:", "AI-Sandbox:"]);
    assert_eq!(
        row_nodes(&world)[1].description.as_deref(),
        Some("connected · Tab browses outpost-ai-2:~/")
    );
    assert_eq!(browses(&world).len(), before);

    press(&mut world, Key::Down);
    press(&mut world, Key::Tab);

    assert_eq!(field(&world), "outpost-ai-2:~/");
    assert!(browses(&world)[before..].iter().all(|(_, h)| h != SANDBOX));
    rest(&mut world);
}

#[test]
fn a_terminal_opens_at_an_address_on_its_machine() {
    let mut world = world(Setup::default());
    press_action(&mut world, &Action::SessionNewTerminal);
    type_path(&mut world, "ai-sandbox:~/projects/");

    press(&mut world, Key::Enter);

    let started = world.daemon.sent_of("inspect_path").pop().unwrap();
    assert_eq!(
        (started["path"].as_str(), host(&started).as_str()),
        (Some("/home/me/projects"), SANDBOX)
    );
    assert_eq!(
        spawns(&world),
        [("/home/me/projects".into(), "shell".into(), SANDBOX.into())]
    );
    rest(&mut world);
}

#[test]
fn an_outpost_that_becomes_reachable_is_asked_for_the_folder_the_text_browses() {
    let mut world = open(Setup {
        outposts: vec![
            sandbox(),
            OutpostInfo {
                state: "connecting".into(),
                ..field_lab()
            },
        ],
        ..Setup::default()
    });
    type_path(&mut world, "field-lab:~/");
    assert_eq!(
        status(&world).as_deref(),
        Some("field-lab unreachable · no folders cached for ~")
    );

    emit_outposts(
        &mut world,
        vec![
            sandbox(),
            OutpostInfo {
                state: "connected".into(),
                reach: "reachable".into(),
                ..field_lab()
            },
        ],
    );

    assert!(browses(&world).contains(&("~/".to_owned(), FIELD.to_owned())));
    rest(&mut world);
}

#[test]
fn an_outpost_that_becomes_reachable_is_asked_once_for_its_harnesses_and_offers_them() {
    let mut world = open(Setup {
        settings: vec![("codex_available".into(), "false".into())],
        outposts: vec![OutpostInfo {
            state: "connecting".into(),
            ..field_lab()
        }],
        ..Setup::default()
    });
    assert!(world.daemon.sent_of("agent_catalog").is_empty());
    assert!(harness(&world, "codex").is("disabled"));

    let reachable = OutpostInfo {
        state: "connected".into(),
        reach: "reachable".into(),
        ..field_lab()
    };
    emit_outposts(&mut world, vec![reachable.clone()]);
    emit_outposts(&mut world, vec![reachable]);

    assert_eq!(
        world
            .daemon
            .sent_of("agent_catalog")
            .iter()
            .map(host)
            .collect::<Vec<_>>(),
        [FIELD]
    );
    assert!(!harness(&world, "codex").is("disabled"));
    rest(&mut world);
}

#[test]
fn an_outpost_that_failed_to_say_its_harnesses_is_asked_again_when_it_is_reachable_again() {
    let reachable = OutpostInfo {
        state: "connected".into(),
        reach: "reachable".into(),
        ..field_lab()
    };
    let mut world = open(Setup {
        settings: vec![("codex_available".into(), "false".into())],
        outposts: vec![field_lab()],
        ..Setup::default()
    });
    let held = hold(&world, "agent_catalog");
    emit_outposts(&mut world, vec![reachable.clone()]);
    let first = held.lock().unwrap()[0].clone();
    world.daemon.reply_to(
        &first,
        message(AgentCatalogResultMessage {
            success: false,
            host: FIELD.into(),
            ..AgentCatalogResultMessage::new()
        }),
    );
    world.settle();
    assert!(harness(&world, "codex").is("disabled"));

    emit_outposts(&mut world, vec![field_lab()]);
    emit_outposts(&mut world, vec![reachable]);
    let asked = held.lock().unwrap().clone();
    assert_eq!(asked.len(), 2);
    world.daemon.reply_to(
        &asked[1],
        message(AgentCatalogResultMessage {
            success: true,
            host: FIELD.into(),
            catalog: Some(AgentCatalogResult {
                machine: FIELD.into(),
                harnesses: harnesses(&["claude", "codex"]),
                ..AgentCatalogResult::default()
            }),
            ..AgentCatalogResultMessage::new()
        }),
    );
    world.settle();
    assert!(!harness(&world, "codex").is("disabled"));
    rest(&mut world);
}
