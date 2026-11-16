use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, EditorLaunch, Keymap};
use attn_protocol::{
    CommandUsage, Desktop, DesktopPane, FsIndexResultMessage, GetCommandUsageResultMessage,
    InitialStateMessage, OutpostInfo, Profile, RecentFilesResultMessage,
    RecordCommandUsageResultMessage, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Daemon, Target, World, key_event, message};
use rune::KeyPlatform;
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const NOTEBOOK: &str = "/notebook";
const REFUSAL: &str = "is on Studio; opening files on an outpost isn't supported yet";

/// An agent `s1` in `directory`, on the outpost Studio when `remote`, alone on the desktop
/// beside the tiles of `tiles`, with leaf `active` active.
fn start(directory: &str, remote: bool, tiles: Vec<Value>, active: &str) -> World {
    let mut settings = BTreeMap::new();
    settings.insert("notebook.root.effective".to_owned(), NOTEBOOK.to_owned());
    settings.insert("editor_executable".to_owned(), "zed".to_owned());
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        outposts: Some(vec![OutpostInfo {
            id: "studio".into(),
            name: "Studio".into(),
            destination: "studio:2200".into(),
            state: "connected".into(),
            ..OutpostInfo::default()
        }]),
        sessions: Some(vec![Session {
            id: "s1".into(),
            label: "s1".into(),
            agent: "shell".into(),
            directory: directory.into(),
            profile_id: "work".into(),
            host: remote.then(|| "studio".into()),
            ..Session::default()
        }]),
        desktops: Some(vec![Desktop {
            id: DESKTOP.into(),
            profile_id: "work".into(),
            name: "Desktop 1".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            tree_json: row(std::iter::once(pane("p1")).chain(tiles)).to_string(),
            active_pane_id: active.into(),
            panes: vec![DesktopPane {
                desktop_id: DESKTOP.into(),
                pane_id: "p1".into(),
                runtime_id: "r1".into(),
                session_id: "s1".into(),
                kind: "agent".into(),
                ..DesktopPane::default()
            }],
            revision: 1,
        }]),
        ..InitialStateMessage::new()
    });
    daemon.on("recent_files", |r| {
        message(RecentFilesResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            ..RecentFilesResultMessage::new()
        })
    });
    daemon.on("fs_index", |r| {
        message(FsIndexResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            root: r["root"].as_str().unwrap_or_default().into(),
            ..FsIndexResultMessage::new()
        })
    });
    daemon.on("get_command_usage", |r| {
        message(GetCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            profile_id: r["profile_id"].as_str().unwrap().into(),
            success: true,
            entries: Vec::<CommandUsage>::new(),
            ..GetCommandUsageResultMessage::new()
        })
    });
    daemon.on("record_command_usage", |r| {
        message(RecordCommandUsageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            ..RecordCommandUsageResultMessage::new()
        })
    });
    daemon.on(
        "desktop_dock_tile",
        |_| json!({ "event": "profile_action_result", "success": true }),
    );
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    world.open_desktop();
    world
}

fn press(world: &mut World, action: Action) {
    let ev = match Keymap::from_setting(None, KeyPlatform::current()).binding(&action) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("{action:?} has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
}

fn run_command(world: &mut World, query: &str, command: &str) {
    press(world, Action::UiCommandPalette);
    world.gesture(|app| app.type_text(query));
    world.gesture(|app| app.click(Target::key(format!("palette.row.command:{command}"))));
}

fn notice(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name)
}

fn roots_sent(world: &World, cmd: &str) -> Vec<Value> {
    world
        .daemon
        .sent_of(cmd)
        .iter()
        .map(|r| r["root"].clone())
        .collect()
}

fn editor_root_offers(world: &mut World, option: &str) -> bool {
    world
        .automate(
            "find",
            json!({ "target": { "role": "option", "name": option } }),
        )
        .is_ok_and(|found| !found.is_null())
}

#[test]
fn open_in_editor_on_an_outpost_names_the_host_instead_of_opening_the_folder_here() {
    let mut world = start("/tmp", true, Vec::new(), "p1");

    run_command(&mut world, "Open in editor", "open-in-editor");

    assert_eq!(world.editor_launches(), Vec::<EditorLaunch>::new());
    assert_eq!(notice(&world), Some(format!("/tmp {REFUSAL}")));
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn open_in_editor_opens_a_local_agents_folder_with_the_editor_setting() {
    let folder = tempfile::tempdir().unwrap();
    let mut world = start(folder.path().to_str().unwrap(), false, Vec::new(), "p1");

    run_command(&mut world, "Open in editor", "open-in-editor");

    assert_eq!(
        world.editor_launches(),
        [EditorLaunch {
            command: "zed .".into(),
            cwd: folder.path().into(),
        }]
    );
    assert_eq!(notice(&world), None);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_editor_tile_for_an_outpost_agent_opens_on_the_notebook_not_a_folder_here() {
    let mut world = start("/home/victor/project", true, Vec::new(), "p1");

    run_command(&mut world, "Open Editor tile", "notebook-tile");

    let docked: Vec<(Value, Value)> = world
        .daemon
        .sent_of("desktop_dock_tile")
        .iter()
        .map(|r| (r["tile_kind"].clone(), r["tile_params"].clone()))
        .collect();
    assert_eq!(docked, [(json!("notebook"), Value::Null)]);
}

#[test]
fn the_markdown_opener_for_an_outpost_agent_roots_at_the_notebook_not_a_folder_here() {
    let mut world = start("/home/victor/project", true, Vec::new(), "p1");

    press(&mut world, Action::FileOpen);

    assert_eq!(roots_sent(&world, "fs_index"), [json!(NOTEBOOK)]);
    assert_eq!(roots_sent(&world, "recent_files"), [json!(NOTEBOOK)]);
}

#[test]
fn the_editor_root_picker_offers_a_local_agents_folder_but_not_an_outpost_agents() {
    let tile = json!({ "type": "tile", "tile_id": "n", "tile_kind": "notebook", "tile_params": "", "tile_session_id": "s1" });
    for (remote, offered) in [(false, true), (true, false)] {
        let mut world = start("/home/victor/project", remote, vec![tile.clone()], "n");
        world.gesture(|d| d.press("escape"));
        world
            .automate(
                "press",
                json!({ "target": { "key": "notebook.tile.n.root-trigger" } }),
            )
            .unwrap();

        assert!(editor_root_offers(&mut world, "Notebook"));
        assert_eq!(
            editor_root_offers(&mut world, "Desktop — project"),
            offered,
            "on an outpost: {remote}"
        );
    }
}
