use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use attn_protocol::{
    Desktop, DesktopPane, FsChangedMessage, InitialStateMessage, Profile,
    ProfileArrangementChangedMessage, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Answer, Daemon, Target, World};
use serde_json::{Value, json};

const NOTEBOOK: &str = "/home/me/attn-notebook";
const DESKTOP: &str = "work/desktop_1";

/// The notebook folder the daemon serves: files by root-relative path.
#[derive(Clone, Default)]
struct Disk(Arc<Mutex<BTreeMap<String, String>>>);

fn hash(content: &str) -> String {
    let sum = content
        .bytes()
        .fold(7u64, |h, b| h.wrapping_mul(31).wrapping_add(b.into()));
    format!("h{sum:x}")
}

impl Disk {
    fn with(files: &[(&str, &str)]) -> Disk {
        let disk = Disk::default();
        for (path, content) in files {
            disk.put(path, content);
        }
        disk
    }

    fn put(&self, path: &str, content: &str) {
        self.0.lock().unwrap().insert(path.into(), content.into());
    }

    fn get(&self, path: &str) -> Option<String> {
        self.0.lock().unwrap().get(path).cloned()
    }

    fn serve(&self, daemon: &Daemon) {
        let disk = self.clone();
        daemon.on("fs_list", move |req| {
            let folder = req["path"].as_str().unwrap_or("");
            let prefix = if folder.is_empty() { String::new() } else { format!("{folder}/") };
            let mut entries = BTreeMap::new();
            for path in disk.0.lock().unwrap().keys() {
                let Some(rest) = path.strip_prefix(&prefix) else { continue };
                let (name, is_dir) = match rest.split_once('/') {
                    Some((dir, _)) => (dir, true),
                    None => (rest, false),
                };
                entries.insert(name.to_owned(), json!({
                    "path": format!("{prefix}{name}"), "name": name, "is_dir": is_dir, "size": 1,
                }));
            }
            json!({ "event": "fs_list_result", "success": true, "entries": entries.into_values().collect::<Vec<_>>() })
        });
        let disk = self.clone();
        daemon.on("fs_read", move |req| {
            let path = req["path"].as_str().unwrap_or("");
            match disk.get(path) {
                Some(content) => json!({ "event": "fs_read_result", "success": true,
                    "result": { "path": path, "content": content, "hash": hash(&content) } }),
                None => json!({ "event": "fs_read_result", "success": false,
                    "error": format!("fsdoc: {path} not found") }),
            }
        });
        let disk = self.clone();
        daemon.on("fs_write", move |req| {
            let path = req["path"].as_str().unwrap_or("").to_owned();
            let content = req["content"].as_str().unwrap_or("").to_owned();
            let current = disk.get(&path).map(|c| hash(&c));
            if req.get("base_hash").and_then(Value::as_str).map(str::to_owned) != current {
                return json!({ "event": "fs_write_result", "success": true,
                    "result": { "path": path, "conflict": true, "current_hash": current.unwrap_or_default() } });
            }
            disk.put(&path, &content);
            json!({ "event": "fs_write_result", "success": true,
                "result": { "path": path, "conflict": false, "hash": hash(&content) } })
        });
        daemon.on("notebook_backlinks", |req| {
            let entries = if req["path"] == "knowledge/index.md" {
                json!([{ "path": "journal/2026-11-14.md", "title": "Friday", "size": 10 }])
            } else {
                json!([])
            };
            json!({ "event": "notebook_backlinks_result", "success": true, "entries": entries })
        });
    }
}

fn start(disk: &Disk) -> World {
    start_with(disk, None)
}

fn start_with(disk: &Disk, tree: Option<Value>) -> World {
    let mut settings = BTreeMap::new();
    settings.insert("notebook.root.effective".to_owned(), NOTEBOOK.to_owned());
    let daemon = Daemon::scripted().state(InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "Agent a".into(),
            directory: "/work/a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        desktops: Some(vec![desktop(
            tree.clone().unwrap_or_else(|| row(vec![pane("a")])),
            1,
        )]),
        ..InitialStateMessage::new()
    });
    disk.serve(&daemon);
    daemon.on("fs_watch", |req| {
        let root = req["root"].as_str().unwrap_or("");
        json!({ "event": "fs_watch_result", "success": true, "root": format!("/private{root}") })
    });
    daemon.on(
        "fs_unwatch",
        |req| json!({ "event": "fs_unwatch_result", "success": true, "root": req["root"] }),
    );
    daemon.on(
        "desktop_update_tile",
        |_| json!({ "event": "profile_action_result", "success": true }),
    );
    daemon.on(
        "desktop_dock_tile",
        |_| json!({ "event": "profile_action_result", "success": true }),
    );
    let mut world = World::start_sized(daemon, (1600.0, 900.0));
    if tree.is_some() {
        world.open_desktop();
    }
    world
}

fn desktop(tree: Value, revision: i64) -> Desktop {
    Desktop {
        id: DESKTOP.into(),
        profile_id: "work".into(),
        name: "Desktop 1".into(),
        shortcut_slot: Some(1),
        order_key: "1".into(),
        tree_json: tree.to_string(),
        active_pane_id: "a".into(),
        panes: vec![DesktopPane {
            desktop_id: DESKTOP.into(),
            pane_id: "a".into(),
            runtime_id: "r-a".into(),
            session_id: "s-a".into(),
            kind: "agent".into(),
            ..DesktopPane::default()
        }],
        revision,
    }
}

fn arrange(world: &mut World, tree: Value, revision: i64) {
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        },
        desktops: vec![desktop(tree, revision)],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();
}

fn editor_tile(id: &str, params: &str) -> Value {
    json!({ "type": "tile", "tile_id": id, "tile_kind": "notebook", "tile_params": params })
}

fn open_fullscreen(world: &mut World) {
    world
        .automate("run", json!({ "name": "notebook.openFullscreen" }))
        .unwrap();
    world.settle();
}

fn status(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notebook.status"))
        .and_then(|n| n.name.clone())
}

fn type_at_end(world: &mut World, words: &str) {
    world.gesture(|d| {
        d.click(Target::key("notebook.editor"));
        d.press("mod+down");
        d.type_text(words);
    });
}

const INDEX: &str =
    "---\ntype: hub\n---\n# Index\n\n## Areas\n\nSee [Friday](/journal/2026-11-14.md).\n";

#[test]
fn the_notebook_opens_its_index_with_its_kind_outline_and_the_notes_that_link_to_it() {
    let disk = Disk::with(&[
        ("knowledge/index.md", INDEX),
        ("journal/2026-11-14.md", "# Friday\n"),
    ]);
    let mut world = start(&disk);

    open_fullscreen(&mut world);

    let screen = world.screen();
    assert_eq!(
        screen.get(Target::key("notebook.title")).name.as_deref(),
        Some("Notebook")
    );
    assert_eq!(
        screen
            .get(Target::key("notebook.document.title"))
            .name
            .as_deref(),
        Some("index")
    );
    assert_eq!(
        screen
            .get(Target::key("notebook.document.kind"))
            .name
            .as_deref(),
        Some("hub")
    );
    assert!(
        screen
            .find(Target::role("navigation").name("Notebook files"))
            .is_some()
    );
    let rail = screen.get(Target::key("notebook.context"));
    assert_eq!(rail.name.as_deref(), Some("Context"));
    assert!(screen.find(Target::role("button").name("Index")).is_some());
    assert!(screen.find(Target::role("button").name("Areas")).is_some());
    assert!(screen.find(Target::role("button").name("Friday")).is_some());
    assert_eq!(
        world.daemon.sent_of("fs_read")[0]["path"],
        "knowledge/index.md"
    );
    assert!(world.daemon.sent_of("fs_read")[0].get("root").is_none());

    world.gesture(|d| d.click(Target::role("button").name("Hide context rail")));

    assert!(
        world
            .screen()
            .find(Target::key("notebook.context"))
            .is_none()
    );
    world.gesture(|d| d.click(Target::role("button").name("Show context rail")));
    world.gesture(|d| d.click(Target::role("button").name("Friday")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.document.title"))
            .name
            .as_deref(),
        Some("2026-11-14")
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_edit_saves_itself_against_the_version_it_read_and_overwrites_a_changed_file_only_when_asked()
{
    let disk = Disk::with(&[("knowledge/index.md", INDEX)]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);

    type_at_end(&mut world, "Typed in attn.");
    assert_eq!(status(&world).as_deref(), Some("Unsaved…"));
    assert!(world.daemon.sent_of("fs_write").is_empty());
    world.advance(Duration::from_millis(700));
    let writes = world.daemon.sent_of("fs_write");
    assert_eq!(writes.len(), 1);
    assert_eq!(writes[0]["base_hash"], hash(INDEX));
    assert!(
        disk.get("knowledge/index.md")
            .unwrap()
            .contains("Typed in attn."),
        "{:?}",
        disk.get("knowledge/index.md")
    );
    assert_eq!(status(&world).as_deref(), Some("Saved"));
    world.advance(Duration::from_millis(2500));
    assert_eq!(status(&world), None);

    disk.put("knowledge/index.md", "changed elsewhere\n");
    type_at_end(&mut world, " More.");
    world.advance(Duration::from_millis(700));
    let screen = world.screen();
    assert!(
        screen
            .find(Target::role("alert").name("This file changed on disk since you opened it."))
            .is_some()
            || screen
                .find(Target::key("notebook.document.conflict"))
                .is_some()
    );
    assert_eq!(
        disk.get("knowledge/index.md").as_deref(),
        Some("changed elsewhere\n")
    );

    world.gesture(|d| d.click(Target::role("button").name("Overwrite anyway")));
    let last = world.daemon.sent_of("fs_write").pop().unwrap();
    assert_eq!(last["base_hash"], hash("changed elsewhere\n"));
    let saved = disk.get("knowledge/index.md").unwrap();
    assert!(
        saved.contains("Typed in attn.") && saved.contains(" More."),
        "{saved}"
    );
    assert!(
        world
            .screen()
            .find(Target::key("notebook.document.conflict"))
            .is_none()
    );
    world.advance(Duration::from_millis(2500));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_notebook_follows_the_disk_but_keeps_an_unsaved_edit() {
    let disk = Disk::with(&[("knowledge/index.md", INDEX)]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.gesture(|d| d.click(Target::key("notebook.file.knowledge")));
    let lists = |world: &World, folder: &str| {
        world
            .daemon
            .sent_of("fs_list")
            .iter()
            .filter(|r| r["path"].as_str().unwrap_or("") == folder)
            .count()
    };
    let (reads, roots, folders) = (
        world.daemon.sent_of("fs_read").len(),
        lists(&world, ""),
        lists(&world, "knowledge"),
    );

    disk.put(
        "knowledge/index.md",
        &format!("{INDEX}appended while open\n"),
    );
    disk.put("knowledge/new.md", "# New\n");
    world.daemon.emit(FsChangedMessage {
        paths: vec!["knowledge/index.md".into()],
        origin: "agent".into(),
        root: NOTEBOOK.into(),
        ..FsChangedMessage::new()
    });
    world.settle();
    assert_eq!(world.daemon.sent_of("fs_read").len(), reads + 1);
    assert_eq!(
        (lists(&world, ""), lists(&world, "knowledge")),
        (roots + 1, folders + 1),
        "the root and the open folder list again"
    );
    assert!(
        world
            .screen()
            .find(Target::key("notebook.file.knowledge/new.md"))
            .is_some()
    );
    assert!(editor_text(&world, "notebook.editor").contains("appended while open"));

    type_at_end(&mut world, "mine");
    disk.put("knowledge/index.md", "someone else\n");
    world.daemon.emit(FsChangedMessage {
        paths: vec![],
        origin: "agent".into(),
        root: NOTEBOOK.into(),
        ..FsChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        world.daemon.sent_of("fs_read").len(),
        reads + 1,
        "an unsaved edit is never read over"
    );

    let (lists_before, reads_before) = (
        world.daemon.sent_of("fs_list").len(),
        world.daemon.sent_of("fs_read").len(),
    );
    world.daemon.emit(FsChangedMessage {
        paths: vec![],
        origin: "agent".into(),
        root: "/somewhere/else".into(),
        ..FsChangedMessage::new()
    });
    world.settle();
    assert_eq!(
        (
            world.daemon.sent_of("fs_list").len(),
            world.daemon.sent_of("fs_read").len()
        ),
        (lists_before, reads_before),
        "a change under another root is not ours"
    );
    world.advance(Duration::from_millis(700));
    world.advance(Duration::from_millis(2500));
    assert!(world.runtime_is_at_rest());
}

const PLAN: &str = "# Plan\n\nShip it.\n";

#[test]
fn an_editor_tile_docks_beside_the_agent_on_its_folder_unless_that_is_the_notebook_or_nothing() {
    let disk = Disk::default();
    let mut world = start_with(&disk, Some(row(vec![pane("a")])));
    for (directory, params) in [
        ("/work/a", Some(r#"{"root":"/work/a"}"#)),
        ("  /work/b  ", Some(r#"{"root":"/work/b"}"#)),
        (NOTEBOOK, None),
        ("", None),
    ] {
        world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
            sessions: Some(vec![Session {
                id: "s-a".into(),
                label: "Agent a".into(),
                directory: directory.into(),
                profile_id: "work".into(),
                ..Session::default()
            }]),
            ..attn_protocol::SessionsUpdatedMessage::new()
        });
        world.settle();
        world
            .automate("run", json!({ "name": "notebook.openTile" }))
            .unwrap();
        world.settle();
        let dock = world.daemon.sent_of("desktop_dock_tile").pop().unwrap();
        assert_eq!(dock["tile_kind"], "notebook");
        assert_eq!(dock["edge"], "right");
        assert_eq!(dock["tile_share"], 0.4);
        assert!(
            dock["tile_id"]
                .as_str()
                .unwrap()
                .starts_with("notebook-tile-")
        );
        assert_eq!(
            dock.get("tile_params").and_then(Value::as_str),
            params,
            "{directory:?}"
        );
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_editor_tile_on_a_folder_watches_it_reads_through_it_and_shows_no_notebook_rail() {
    let disk = Disk::with(&[("plan.md", PLAN), ("notes.md", "# Notes\n")]);
    let params = r#"{"root":"/work/a","path":"plan.md"}"#;
    let mut world = start_with(&disk, Some(row(vec![pane("a"), editor_tile("n", params)])));

    let watches = world.daemon.sent_of("fs_watch");
    assert_eq!(watches.len(), 1);
    assert_eq!(watches[0]["root"], "/work/a");
    let read = world.daemon.sent_of("fs_read").pop().unwrap();
    assert_eq!(read["path"], "plan.md");
    let screen = world.screen();
    assert_eq!(
        screen
            .get(Target::key("notebook.tile.n.document.title"))
            .name
            .as_deref(),
        Some("plan")
    );
    assert!(
        screen
            .find(Target::key("notebook.tile.n.context"))
            .is_none()
    );
    assert!(world.daemon.sent_of("notebook_backlinks").is_empty());

    world.gesture(|d| d.click(Target::key("notebook.tile.n.file.notes.md")));
    let read = world.daemon.sent_of("fs_read").pop().unwrap();
    assert_eq!(read["root"], "/private/work/a");
    let update = world.daemon.sent_of("desktop_update_tile").pop().unwrap();
    assert_eq!(update["tile_id"], "n");
    assert_eq!(
        update["tile_params"],
        r#"{"path":"notes.md","root":"/work/a"}"#
    );

    arrange(&mut world, row(vec![pane("a")]), 2);
    let unwatch = world.daemon.sent_of("fs_unwatch");
    assert_eq!(unwatch.len(), 1);
    assert_eq!(unwatch[0]["root"], "/private/work/a");
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_edit_made_just_before_the_tile_leaves_the_screen_is_saved_and_comes_back_with_it() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let tile = || editor_tile("n", "plan.md");
    let mut world = start_with(&disk, Some(row(vec![pane("a"), tile()])));
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Typed before leaving. ");
    });

    arrange(&mut world, row(vec![pane("a")]), 2);
    assert!(
        disk.get("plan.md")
            .unwrap()
            .contains("Typed before leaving.")
    );

    arrange(&mut world, row(vec![pane("a"), tile()]), 3);
    let editor = world.screen().get(Target::key("notebook.tile.n.editor"));
    assert!(
        editor
            .value
            .as_deref()
            .unwrap_or("")
            .contains("Typed before leaving."),
        "{editor:?}"
    );

    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Again. ");
    });
    disk.put("plan.md", "changed on disk\n");
    arrange(&mut world, row(vec![pane("a")]), 4);
    assert_eq!(disk.get("plan.md").as_deref(), Some("changed on disk\n"));
    arrange(&mut world, row(vec![pane("a"), tile()]), 5);
    let screen = world.screen();
    assert!(
        screen
            .find(Target::key("notebook.tile.n.document.conflict"))
            .is_some()
    );
    let editor = screen.get(Target::key("notebook.tile.n.editor"));
    assert!(
        editor.value.as_deref().unwrap_or("").contains("Again."),
        "{editor:?}"
    );

    world.gesture(|d| d.click(Target::key("notebook.tile.n.document.overwrite")));
    assert!(disk.get("plan.md").unwrap().contains("Again."));
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

fn root_trigger(tile: &str) -> String {
    format!("notebook.tile.{tile}.root-trigger")
}

fn root_options(world: &mut World, tile: &str) -> Vec<String> {
    world
        .automate("press", json!({ "target": { "key": root_trigger(tile) } }))
        .unwrap();
    world
        .automate(
            "wait",
            json!({ "target": { "role": "option", "name": "Notebook" } }),
        )
        .unwrap();
    ["Notebook", "Desktop — a", "x", "Browse…"]
        .into_iter()
        .filter(|name| shows_option(world, name))
        .map(str::to_owned)
        .collect()
}

fn shows_option(world: &mut World, name: &str) -> bool {
    world
        .automate(
            "find",
            json!({ "target": { "role": "option", "name": name } }),
        )
        .is_ok_and(|found| !found.is_null())
}

fn choose_root(world: &mut World, tile: &str, option: &str) {
    if !shows_option(world, option) {
        world
            .automate("press", json!({ "target": { "key": root_trigger(tile) } }))
            .unwrap();
    }
    world
        .automate(
            "press",
            json!({ "target": { "role": "option", "name": option } }),
        )
        .unwrap();
    world.settle();
}

fn last_params(world: &World) -> Option<Value> {
    world
        .daemon
        .sent_of("desktop_update_tile")
        .pop()
        .map(|u| u["tile_params"].clone())
}

#[test]
fn the_root_picker_offers_the_notebook_the_agent_s_folder_and_the_tile_s_own_folder() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = start_with(&disk, Some(row(vec![pane("a"), editor_tile("n", "")])));
    world.gesture(|d| d.press("escape"));
    assert_eq!(
        root_options(&mut world, "n"),
        ["Notebook", "Desktop — a", "Browse…"]
    );
    world.gesture(|d| d.press("escape"));

    arrange(
        &mut world,
        row(vec![pane("a"), editor_tile("n", r#"{"root":"/other/x"}"#)]),
        2,
    );
    world.gesture(|d| d.press("escape"));
    assert_eq!(
        root_options(&mut world, "n"),
        ["Notebook", "Desktop — a", "x", "Browse…"]
    );
    world.gesture(|d| d.press("escape"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_root_picker_leaves_out_the_desktop_when_the_agent_has_no_folder() {
    let disk = Disk::default();
    let mut world = start_with(&disk, Some(row(vec![pane("a"), editor_tile("n", "")])));
    world.gesture(|d| d.press("escape"));
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![Session {
            id: "s-a".into(),
            label: "Agent a".into(),
            profile_id: "work".into(),
            ..Session::default()
        }]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(root_options(&mut world, "n"), ["Notebook", "Browse…"]);
    world.gesture(|d| d.press("escape"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn choosing_a_root_writes_it_without_the_open_file_and_the_notebook_writes_no_params() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = start_with(
        &disk,
        Some(row(vec![
            pane("a"),
            editor_tile("n", r#"{"root":"/other/x","path":"plan.md"}"#),
        ])),
    );

    choose_root(&mut world, "n", "Desktop — a");
    assert_eq!(last_params(&world), Some(json!(r#"{"root":"/work/a"}"#)));
    choose_root(&mut world, "n", "Notebook");
    assert_eq!(last_params(&world), Some(json!("")));
    assert!(
        world.daemon.sent_of("fs_write").is_empty(),
        "nothing unsaved, nothing written"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn browsing_roots_the_tile_at_the_chosen_folder_and_a_cancel_changes_nothing() {
    let disk = Disk::default();
    let mut world = start_with(&disk, Some(row(vec![pane("a"), editor_tile("n", "")])));
    world.gesture(|d| d.press("escape"));

    choose_root(&mut world, "n", "Browse…");
    world.answer_file_dialog(None);
    world.settle();
    assert_eq!(last_params(&world), None);

    choose_root(&mut world, "n", "Browse…");
    world.answer_file_dialog(Some("/chosen/folder"));
    world.settle();
    assert_eq!(
        last_params(&world),
        Some(json!(r#"{"root":"/chosen/folder"}"#))
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn switching_the_root_saves_the_edit_first_and_stays_when_that_save_conflicts() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = start_with(
        &disk,
        Some(row(vec![pane("a"), editor_tile("n", "plan.md")])),
    );
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Kept. ");
    });
    choose_root(&mut world, "n", "Desktop — a");
    assert!(disk.get("plan.md").unwrap().contains("Kept."));
    assert_eq!(last_params(&world), Some(json!(r#"{"root":"/work/a"}"#)));

    let mut world = start_with(
        &disk,
        Some(row(vec![pane("a"), editor_tile("n", "plan.md")])),
    );
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Mine. ");
    });
    disk.put("plan.md", "theirs\n");
    choose_root(&mut world, "n", "Desktop — a");
    assert_eq!(last_params(&world), None);
    assert!(
        world
            .screen()
            .find(Target::key("notebook.tile.n.document.conflict"))
            .is_some()
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

fn serve_index(world: &World) {
    world.daemon.on("fs_index", |req| {
        json!({ "event": "fs_index_result", "success": true, "root": req["root"].as_str().unwrap_or(NOTEBOOK),
            "files": ["journal/2026-11-14.md", "knowledge/index.md", "knowledge/rust-notes.md"], "truncated": false })
    });
}

#[test]
fn the_finder_opens_a_note_by_name_inside_the_notebook_and_the_opener_once_it_closes() {
    let disk = Disk::with(&[
        ("knowledge/index.md", INDEX),
        ("knowledge/rust-notes.md", "# Rust\n"),
    ]);
    let mut world = start(&disk);
    serve_index(&world);
    open_fullscreen(&mut world);

    world
        .automate("run", json!({ "name": "file.open" }))
        .unwrap();
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_some()
    );
    assert!(world.screen().find(Target::key("opener")).is_none());
    let index = world.daemon.sent_of("fs_index");
    assert_eq!(index.len(), 1);
    assert!(index[0].get("root").is_none());

    world.gesture(|d| {
        d.type_text("rust");
        d.press("enter");
    });
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_none()
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.document.title"))
            .name
            .as_deref(),
        Some("rust-notes")
    );

    world.gesture(|d| d.press("escape"));
    world
        .automate("run", json!({ "name": "file.open" }))
        .unwrap();
    world.settle();
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_none()
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_finder_of_the_active_editor_tile_lists_what_its_folder_indexed_even_cut_short() {
    let disk = Disk::with(&[("plan.md", PLAN), ("README.md", "# Read me\n")]);
    let tiles = || {
        row(vec![
            editor_tile("n", r#"{"root":"/work/a","path":"plan.md"}"#),
            editor_tile("m", r#"{"root":"/work/m","path":"plan.md"}"#),
        ])
    };
    let mut world = start_with(&disk, Some(tiles()));
    world.daemon.on("fs_index", |req| {
        json!({ "event": "fs_index_result", "success": true, "root": req["root"],
            "files": ["README.md", "plan.md"], "truncated": true })
    });
    let mut active = desktop(tiles(), 2);
    active.active_pane_id = "n".into();
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        },
        desktops: vec![active],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    world
        .automate("run", json!({ "name": "file.open" }))
        .unwrap();
    world.settle();
    let index = world.daemon.sent_of("fs_index");
    assert_eq!(index.len(), 1);
    assert_eq!(index[0]["root"], "/private/work/a");
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_some()
    );
    assert!(shows_option(&mut world, "README.md README.md"));
    assert!(shows_option(&mut world, "plan.md plan.md"));

    world.gesture(|d| {
        d.type_text("read");
        d.press("enter");
    });
    let title = |world: &World, tile: &str| {
        world
            .screen()
            .get(Target::key(format!("notebook.tile.{tile}.document.title")))
            .name
    };
    assert_eq!(title(&world, "n").as_deref(), Some("README"));
    assert_eq!(title(&world, "m").as_deref(), Some("plan"));
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_tree_lists_a_folder_once_however_often_it_folds_marks_the_open_file_and_says_why_a_folder_cannot_be_listed()
 {
    let disk = Disk::with(&[
        ("knowledge/index.md", INDEX),
        ("knowledge/areas/home.md", "# Home\n"),
        ("journal/2026-11-14.md", "# Friday\n"),
    ]);
    let mut world = start(&disk);
    world
        .daemon
        .on("fs_list", |req| match req["path"].as_str() {
            Some("journal") => {
                json!({ "event": "fs_list_result", "success": false, "error": "permission denied" })
            }
            Some("knowledge") => json!({ "event": "fs_list_result", "success": true, "entries": [
            { "path": "knowledge/areas", "name": "areas", "is_dir": true, "size": 0 },
            { "path": "knowledge/index.md", "name": "index.md", "is_dir": false, "size": 1 }] }),
            _ => json!({ "event": "fs_list_result", "success": true, "entries": [
            { "path": "journal", "name": "journal", "is_dir": true, "size": 0 },
            { "path": "knowledge", "name": "knowledge", "is_dir": true, "size": 0 }] }),
        });
    open_fullscreen(&mut world);
    let lists = |world: &World, folder: &str| {
        world
            .daemon
            .sent_of("fs_list")
            .iter()
            .filter(|r| r["path"].as_str().unwrap_or("") == folder)
            .count()
    };
    assert_eq!(lists(&world, ""), 1);

    for _ in 0..3 {
        world.gesture(|d| d.click(Target::key("notebook.file.knowledge")));
    }
    assert_eq!(lists(&world, "knowledge"), 1);
    let open = world
        .screen()
        .get(Target::key("notebook.file.knowledge/index.md"));
    assert_eq!(open.value.as_deref(), Some("open"));

    world.gesture(|d| d.click(Target::key("notebook.file.journal")));
    assert!(
        world
            .screen()
            .find(Target::role("label").name("permission denied"))
            .is_some()
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_file_the_notebook_cannot_show_opens_as_a_placeholder_without_a_read() {
    let disk = Disk::with(&[("knowledge/index.md", INDEX), ("clip.MP4", "binary")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    let reads = world.daemon.sent_of("fs_read").len();

    world.gesture(|d| d.click(Target::key("notebook.file.clip.MP4")));
    assert_eq!(world.daemon.sent_of("fs_read").len(), reads);
    let screen = world.screen();
    assert!(
        screen
            .find(Target::role("heading").name("Preview not available"))
            .is_some()
    );
    assert!(
        screen
            .find(Target::role("label").name("clip.MP4 can't be opened here yet."))
            .is_some()
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_empty_notebook_says_so_and_offers_the_finder_and_escape_closes_it() {
    let disk = Disk::default();
    let mut world = start(&disk);
    serve_index(&world);
    open_fullscreen(&mut world);
    assert!(
        world
            .screen()
            .find(Target::role("heading").name("Nothing selected"))
            .is_some()
    );

    world.gesture(|d| d.click(Target::key("notebook.find")));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_some()
    );
    world.gesture(|d| d.press("escape"));
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_none()
    );
    assert!(world.screen().find(Target::key("notebook.panel")).is_some());
    world.gesture(|d| d.press("escape"));
    assert!(world.screen().find(Target::key("notebook.panel")).is_none());
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_editor_tile_with_no_file_opens_its_finder() {
    let disk = Disk::default();
    let mut world = start_with(&disk, Some(row(vec![pane("a")])));
    serve_index(&world);
    arrange(&mut world, row(vec![pane("a"), editor_tile("n", "")]), 2);
    assert!(
        world
            .screen()
            .find(Target::role("dialog").name("Find a note"))
            .is_some()
    );
    world.gesture(|d| d.press("escape"));
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_notebook_shows_whether_the_selected_profile_s_chief_works_and_nothing_without_one() {
    let disk = Disk::default();
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    assert!(world.screen().find(Target::key("notebook.chief")).is_none());

    let chief = |profile: &str, state: &str, id: &str| Session {
        id: id.into(),
        label: "chief".into(),
        profile_id: profile.into(),
        chief_of_staff: Some(true),
        state: state.into(),
        ..Session::default()
    };
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![
            chief("other", "working", "c-other"),
            chief("work", "idle", "c-work"),
        ]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.chief"))
            .name
            .as_deref(),
        Some("chief: idle")
    );
    world.daemon.emit(attn_protocol::SessionsUpdatedMessage {
        sessions: Some(vec![
            chief("other", "idle", "c-other"),
            chief("work", "working", "c-work"),
        ]),
        ..attn_protocol::SessionsUpdatedMessage::new()
    });
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.chief"))
            .name
            .as_deref(),
        Some("chief: active")
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

fn editor_text(world: &World, key: &str) -> String {
    world
        .screen()
        .get(Target::key(key))
        .value
        .unwrap_or_default()
}

fn shows_line(world: &World, words: &str) -> bool {
    world
        .screen()
        .find(Target::role("label").name(words))
        .is_some()
}

#[test]
fn command_z_undoes_typing_in_the_editor_and_shift_command_z_redoes_it() {
    let disk = Disk::with(&[("knowledge/index.md", INDEX)]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    type_at_end(&mut world, "Typed in attn.");
    world.advance(Duration::from_millis(700));
    assert!(
        disk.get("knowledge/index.md")
            .unwrap()
            .contains("Typed in attn.")
    );

    world.gesture(|d| d.press("mod+z"));
    assert!(!editor_text(&world, "notebook.editor").contains("Typed in attn."));
    world.advance(Duration::from_millis(700));
    assert_eq!(disk.get("knowledge/index.md").as_deref(), Some(INDEX));

    world.gesture(|d| d.press("mod+shift+z"));
    assert!(editor_text(&world, "notebook.editor").contains("Typed in attn."));
    world.advance(Duration::from_millis(700));
    assert!(
        disk.get("knowledge/index.md")
            .unwrap()
            .contains("Typed in attn.")
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_line_the_caret_is_on_shows_its_source_while_the_editor_has_the_keyboard() {
    let disk = Disk::with(&[("knowledge/index.md", "# Index\n\nSee **this**.\n")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    assert!(shows_line(&world, "Index"));
    assert!(shows_line(&world, "See this."));

    world.gesture(|d| d.click(Target::key("notebook.editor.line.2")));
    assert!(shows_line(&world, "See **this**."));
    assert!(shows_line(&world, "Index"));

    world.gesture(|d| d.press("up"));
    world.gesture(|d| d.press("up"));
    assert!(shows_line(&world, "# Index"));
    assert!(shows_line(&world, "See this."));

    world.gesture(|d| d.click(Target::key("notebook.title")));
    assert!(shows_line(&world, "Index"));
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_input_method_composes_at_the_caret_and_only_its_commit_is_saved() {
    let disk = Disk::with(&[("knowledge/index.md", "# Index\n")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.gesture(|d| {
        d.click(Target::key("notebook.editor.line.0"));
        d.press("mod+down");
    });
    world.gesture(|d| d.runtime.ime(rune::Ime::Preedit("にほん".into(), None)));
    assert!(!editor_text(&world, "notebook.editor").contains("にほん"));
    world.gesture(|d| d.runtime.ime(rune::Ime::Commit("日本".into())));
    assert!(editor_text(&world, "notebook.editor").contains("日本"));
    world.advance(Duration::from_millis(700));
    assert!(disk.get("knowledge/index.md").unwrap().contains("日本"));
    assert!(!disk.get("knowledge/index.md").unwrap().contains("にほん"));
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_paste_goes_into_the_editor_that_has_the_keyboard() {
    let disk = Disk::with(&[("knowledge/index.md", "# Index\n")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.gesture(|d| {
        d.click(Target::key("notebook.editor.line.0"));
        d.press("mod+down");
        d.runtime.paste("- pasted\n- lines".to_owned());
    });
    let text = editor_text(&world, "notebook.editor");
    assert!(text.contains("- pasted\n- lines"), "{text}");
    world.advance(Duration::from_millis(700));
    assert!(
        disk.get("knowledge/index.md")
            .unwrap()
            .contains("- pasted\n- lines")
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn find_shows_every_match_and_moves_the_selection_to_the_next_and_the_previous() {
    let disk = Disk::with(&[(
        "knowledge/index.md",
        "# Index\n\na lantern\n\nno match\n\nanother Lantern\n",
    )]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.gesture(|d| {
        d.click(Target::key("notebook.editor.line.0"));
        d.press("mod+f");
    });
    assert!(
        world
            .screen()
            .find(Target::role("search").name("Find in note"))
            .is_some()
    );
    world.gesture(|d| d.type_text("lantern"));
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.editor.findcount"))
            .name
            .as_deref(),
        Some("2 matches")
    );

    world.gesture(|d| d.press("enter"));
    let selection = |world: &World| {
        world
            .screen()
            .find(Target::role("label").name("a lantern"))
            .is_some()
    };
    assert!(selection(&world));
    world.gesture(|d| d.press("escape"));
    assert!(
        world
            .screen()
            .find(Target::role("search").name("Find in note"))
            .is_none()
    );
    world.gesture(|d| d.type_text("X"));
    assert!(
        editor_text(&world, "notebook.editor").contains("a X\n")
            || editor_text(&world, "notebook.editor").contains("another X"),
        "typing replaces the found match: {}",
        editor_text(&world, "notebook.editor")
    );
    world.advance(Duration::from_millis(700));
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn pressing_a_task_box_checks_it_without_moving_the_caret() {
    let disk = Disk::with(&[("knowledge/index.md", "- [ ] todo\n- [x] done\n")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    assert!(shows_line(
        &world,
        &format!("{} todo", attn_ui::icon::UNCHECKED)
    ));

    world
        .automate(
            "press",
            json!({ "target": { "key": "notebook.editor.line.0" }, "at": [0.004, 0.5] }),
        )
        .unwrap();
    world.settle();
    assert!(
        editor_text(&world, "notebook.editor").starts_with("- [x] todo"),
        "{}",
        editor_text(&world, "notebook.editor")
    );
    assert!(shows_line(
        &world,
        &format!("{} todo", attn_ui::icon::CHECKED)
    ));
    world.advance(Duration::from_millis(700));
    assert!(
        disk.get("knowledge/index.md")
            .unwrap()
            .starts_with("- [x] todo")
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_bold_shortcut_reaches_the_editor_and_bolds_the_word_under_the_caret() {
    let disk = Disk::with(&[("knowledge/index.md", "make it bold\n")]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.gesture(|d| {
        d.click(Target::key("notebook.editor.line.0"));
        d.press("mod+down");
        d.press("up");
        d.press("end");
        d.press("left");
        d.press("mod+b");
    });
    assert_eq!(editor_text(&world, "notebook.editor"), "make it **bold**\n");
    world.advance(Duration::from_millis(700));
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

fn written(path: &str, content: &str) -> Value {
    json!({ "event": "fs_write_result", "success": true,
        "result": { "path": path, "conflict": false, "hash": hash(content) } })
}

#[test]
fn a_save_that_answers_while_its_tile_is_away_still_lands_and_the_tile_saves_again_on_return() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let tile = || editor_tile("n", "plan.md");
    let mut world = start_with(&disk, Some(row(vec![pane("a"), tile()])));
    world.daemon.answer_in_turn(
        "fs_write",
        [Answer::Hold, Answer::Reply(written("plan.md", "later"))],
    );
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("First. ");
    });
    world.advance(Duration::from_millis(700));
    let held = world.daemon.sent_of("fs_write").pop().unwrap();

    arrange(&mut world, row(vec![pane("a")]), 2);
    disk.put("plan.md", held["content"].as_str().unwrap());
    world
        .daemon
        .reply_to(&held, written("plan.md", held["content"].as_str().unwrap()));
    world.settle();
    arrange(&mut world, row(vec![pane("a"), tile()]), 3);
    assert_ne!(status(&world).as_deref(), Some("Saving…"));

    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Second. ");
    });
    world.advance(Duration::from_millis(700));
    let writes = world.daemon.sent_of("fs_write");
    assert_eq!(writes.len(), 2, "the tile saves again");
    assert_eq!(
        writes[1]["base_hash"],
        hash(held["content"].as_str().unwrap())
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn opening_another_note_while_a_save_is_in_flight_waits_for_it_then_opens() {
    let disk = Disk::with(&[
        ("knowledge/index.md", INDEX),
        ("journal/2026-11-14.md", "# Friday\n"),
    ]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world.daemon.answer_in_turn("fs_write", [Answer::Hold]);
    type_at_end(&mut world, "Kept.");
    world.advance(Duration::from_millis(700));
    let held = world.daemon.sent_of("fs_write").pop().unwrap();

    world.gesture(|d| d.click(Target::role("button").name("Friday")));
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.document.title"))
            .name
            .as_deref(),
        Some("index")
    );
    world.daemon.reply_to(
        &held,
        written("knowledge/index.md", held["content"].as_str().unwrap()),
    );
    world.settle();
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.document.title"))
            .name
            .as_deref(),
        Some("2026-11-14")
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

fn popped_out(mut tile: Value) -> Value {
    tile["window"] = json!({"x": 60.0, "y": 60.0, "width": 900.0, "height": 700.0});
    tile
}

fn tile_window_editor(world: &World, window: &str) -> String {
    world
        .tile_screen(window)
        .find(Target::key("notebook.tile.n.editor"))
        .and_then(|n| n.value.clone())
        .unwrap_or_default()
}

#[test]
fn a_popped_out_editor_tile_opens_its_note_saves_while_the_window_shows_another_desktop_and_comes_back_with_its_edit()
 {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let tile = || editor_tile("n", "plan.md");
    let mut world = start_with(&disk, Some(row(vec![pane("a"), tile()])));
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Typed before popping out. ");
    });

    arrange(&mut world, row(vec![pane("a"), popped_out(tile())]), 2);
    let windows = world.tile_windows();
    assert_eq!(windows.len(), 1, "{windows:?}");
    let window = windows[0].key.clone();
    assert!(
        disk.get("plan.md")
            .unwrap()
            .contains("Typed before popping out.")
    );
    assert!(
        tile_window_editor(&world, &window).contains("Typed before popping out."),
        "{}",
        world.tile_screen(&window)
    );

    let mut away = desktop(row(vec![pane("a")]), 1);
    away.id = "work/desktop_2".into();
    away.name = "Desktop 2".into();
    away.shortcut_slot = Some(2);
    away.order_key = "2".into();
    world.daemon.emit(ProfileArrangementChangedMessage {
        profile: Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: away.id.clone(),
            ..Profile::default()
        },
        desktops: vec![desktop(row(vec![pane("a"), popped_out(tile())]), 3), away],
        ..ProfileArrangementChangedMessage::new()
    });
    world.settle();

    world.tile_gesture(&window, |d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.press("mod+down");
        d.type_text("Typed in its own window.");
    });
    world.advance(Duration::from_secs(1));
    assert!(
        disk.get("plan.md")
            .unwrap()
            .contains("Typed in its own window."),
        "{:?}",
        disk.get("plan.md")
    );
    assert!(tile_window_editor(&world, &window).contains("Typed in its own window."));

    world.tile_gesture(&window, |d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.press("mod+down");
        d.type_text(" Typed just before coming back.");
    });
    arrange(&mut world, row(vec![pane("a"), tile()]), 4);
    assert!(
        disk.get("plan.md")
            .unwrap()
            .contains("Typed just before coming back."),
        "{:?}",
        disk.get("plan.md")
    );
    world.daemon.emit(FsChangedMessage {
        paths: vec!["plan.md".into()],
        origin: "ui".into(),
        root: NOTEBOOK.into(),
        ..FsChangedMessage::new()
    });
    world.settle();
    let editor = world.screen().get(Target::key("notebook.tile.n.editor"));
    assert!(
        editor
            .value
            .as_deref()
            .unwrap_or("")
            .contains("Typed just before coming back."),
        "{editor:?}"
    );
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[test]
fn a_link_to_a_missing_note_is_marked_and_each_note_is_asked_about_once() {
    let disk = Disk::with(&[(
        "knowledge/index.md",
        "[here](/journal/2026-11-14.md) [gone](gone.md) [again](gone.md) [web](https://example.com)\n",
    )]);
    let mut world = start(&disk);
    world.daemon.on("fs_exists", |req| {
        json!({ "event": "fs_exists_result", "success": true,
            "result": { "path": req["path"], "exists": req["path"] != "knowledge/gone.md" } })
    });
    open_fullscreen(&mut world);
    let mut asked: Vec<String> = world
        .daemon
        .sent_of("fs_exists")
        .iter()
        .map(|r| r["path"].as_str().unwrap().to_owned())
        .collect();
    asked.sort();
    assert_eq!(asked, ["journal/2026-11-14.md", "knowledge/gone.md"]);
    let row = world.screen().get(Target::key("notebook.editor.line.0"));
    assert_eq!(
        row.description.as_deref(),
        Some("Link target not found in the notebook")
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_image_on_its_own_line_shows_the_picture_and_one_that_cannot_be_read_says_so() {
    use base64::Engine;
    let disk = Disk::with(&[(
        "knowledge/index.md",
        "# Pictures\n\n![a cat](img/cat.png)\n\n![gone](missing.png)\n",
    )]);
    let mut world = start(&disk);
    let png =
        base64::engine::general_purpose::STANDARD.encode(include_bytes!("../../src/home/logo.png"));
    world.daemon.on("fs_read_asset", move |req| {
        if req["path"] == "knowledge/img/cat.png" {
            json!({ "event": "fs_read_asset_result", "success": true,
                "result": { "path": req["path"], "mime_type": "image/png", "data_base64": png } })
        } else {
            json!({ "event": "fs_read_asset_result", "success": false, "error": "fsdoc: not found" })
        }
    });
    open_fullscreen(&mut world);
    world.advance(Duration::from_millis(100));
    let mut asked: Vec<String> = world
        .daemon
        .sent_of("fs_read_asset")
        .iter()
        .map(|r| r["path"].as_str().unwrap().to_owned())
        .collect();
    asked.sort();
    assert_eq!(asked, ["knowledge/img/cat.png", "knowledge/missing.png"]);
    assert!(
        world
            .screen()
            .find(Target::role("label").name("image not found"))
            .is_some()
    );
    let cat = world
        .screen()
        .bounds(Target::role("image").name("a cat"))
        .unwrap();
    assert!(
        (cat[3] - 220.0).abs() > 1.0,
        "the picture, not the room held for it: {cat:?}"
    );
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}
