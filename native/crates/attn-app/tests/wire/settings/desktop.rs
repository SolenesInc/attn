use attn_protocol::{
    BrowseDirectoryResultMessage, DirectoryEntry, RecentLocationsResultMessage,
    SettingsUpdatedMessage,
};
use attn_testkit::{Target, World, message};
use rune::{FileDialogKind, Key, KeyEvent, Mods, PhysicalInput, PhysicalKeyCode};

use super::{accepting, click, field, keeping, named, open, press, rest, retype, value, writes};

const EFFECTIVE: (&str, &str) = ("notebook.root.effective", "/Users/me/attn-notebook");

fn on_desktop(world: &mut World) {
    open(world);
    click(world, "settings.nav.desktop");
}

fn pills(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("settings.pills"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn checked(world: &World, setting: &str) -> bool {
    world
        .screen()
        .get(Target::key(format!("settings.switch.{setting}")))
        .is("checked")
}

#[test]
fn the_notebook_folder_shows_the_profile_field_and_the_folder_it_resolves_to() {
    let (daemon, _) = accepting(&[
        ("notebook.root", "~/work-notes"),
        ("notebook.root.effective", "/Users/me/work-notes"),
    ]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    let notebook = field("notebook.root");
    assert_eq!(value(&world, &notebook), "~/work-notes");
    assert_eq!(named(&world, &notebook).as_deref(), Some("Notebook folder"));
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/work-notes")
    );
    rest(&mut world);
}

#[test]
fn the_notebook_field_is_empty_while_the_profile_uses_the_default_folder() {
    let (daemon, _) = accepting(&[EFFECTIVE]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    assert_eq!(value(&world, &field("notebook.root")), "");
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/attn-notebook")
    );
    rest(&mut world);
}

#[test]
fn the_notebook_folder_saves_when_focus_leaves_and_an_empty_value_goes_back_to_the_default() {
    let (daemon, _) = accepting(&[EFFECTIVE]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let notebook = field("notebook.root");

    retype(&mut world, &notebook, "/Users/me/work");
    click(&mut world, "settings.heading");
    retype(&mut world, &notebook, "");
    click(&mut world, "settings.heading");

    assert_eq!(
        writes(&world),
        [
            ("notebook.root".to_owned(), "/Users/me/work".to_owned()),
            ("notebook.root".to_owned(), String::new()),
        ]
    );
    rest(&mut world);
}

#[test]
fn a_browsed_notebook_folder_the_daemon_refuses_shows_why_and_keeps_the_folder_in_use() {
    let refusal = "profile Side's Notebook folder /Users/me/attn-notebook/side is inside the default Notebook folder /Users/me/attn-notebook";
    let (daemon, _) = keeping(&[EFFECTIVE], move |key, _| {
        (key == "notebook.root").then(|| refusal.to_owned())
    });
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    click(&mut world, "settings.browse.notebook.root");
    assert_eq!(
        world.file_dialog(),
        Some((FileDialogKind::Folder, "Select Notebook Folder".to_owned()))
    );
    world.answer_file_dialog(Some("/Users/me/attn-notebook/browsed"));

    assert_eq!(
        writes(&world),
        [(
            "notebook.root".to_owned(),
            "/Users/me/attn-notebook/browsed".to_owned()
        )]
    );
    assert_eq!(
        value(&world, &field("notebook.root")),
        "/Users/me/attn-notebook/browsed"
    );
    assert_eq!(
        named(&world, "settings.refusal.notebook.root").as_deref(),
        Some(refusal)
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.notebook.root"))
            .is_none()
    );
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/attn-notebook")
    );
    rest(&mut world);
}

#[test]
fn a_cancelled_folder_dialog_saves_nothing() {
    let (daemon, _) = accepting(&[("projects_directory", "/Users/me/projects")]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    click(&mut world, "settings.browse.projects_directory");
    assert_eq!(
        world.file_dialog(),
        Some((
            FileDialogKind::Folder,
            "Select Projects Directory".to_owned()
        ))
    );
    world.answer_file_dialog(None);

    assert!(writes(&world).is_empty());
    assert_eq!(
        value(&world, &field("projects_directory")),
        "/Users/me/projects"
    );
    rest(&mut world);
}

#[test]
fn the_projects_directory_pill_says_edited_until_the_path_saves_and_browse_saves_the_chosen_folder()
{
    let (daemon, stored) = accepting(&[("projects_directory", "/Users/me/projects")]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let projects = field("projects_directory");
    assert_eq!(
        named(&world, &projects).as_deref(),
        Some("Projects directory")
    );
    assert_eq!(pills(&world), ["project path saved", "sent files open"]);

    retype(&mut world, &projects, "/Users/me/src");
    assert_eq!(pills(&world)[0], "project path edited");
    press(&mut world, Key::Enter);
    assert_eq!(
        stored.lock().unwrap()["projects_directory"],
        "/Users/me/src"
    );
    assert_eq!(pills(&world)[0], "project path saved");

    click(&mut world, "settings.browse.projects_directory");
    world.answer_file_dialog(Some("/Users/me/code"));
    assert_eq!(
        writes(&world).last().unwrap(),
        &("projects_directory".to_owned(), "/Users/me/code".to_owned())
    );
    assert_eq!(value(&world, &projects), "/Users/me/code");
    assert_eq!(pills(&world)[0], "project path saved");
    rest(&mut world);
}

#[test]
fn the_editor_saves_on_enter() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let editor = field("editor_executable");
    assert_eq!(named(&world, &editor).as_deref(), Some("Editor"));

    retype(&mut world, &editor, "zed --wait");
    press(&mut world, Key::Enter);

    assert_eq!(
        writes(&world),
        [("editor_executable".to_owned(), "zed --wait".to_owned())]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.editor_executable"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn sent_files_open_by_default_and_the_switch_saves_off_and_back_on() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    assert!(checked(&world, "open_sent_files_enabled"));

    click(&mut world, "settings.switch.open_sent_files_enabled");
    assert!(!checked(&world, "open_sent_files_enabled"));
    assert_eq!(pills(&world)[1], "sent files ignored");
    click(&mut world, "settings.switch.open_sent_files_enabled");
    assert!(checked(&world, "open_sent_files_enabled"));

    assert_eq!(
        writes(&world),
        [
            ("open_sent_files_enabled".to_owned(), "false".to_owned()),
            ("open_sent_files_enabled".to_owned(), "true".to_owned()),
        ]
    );
    rest(&mut world);
}

#[test]
fn the_worktree_sweep_switch_follows_a_change_from_the_shell_and_saves() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    assert!(checked(&world, "worktree_sweep_enabled"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("worktree_sweep_enabled".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!checked(&world, "worktree_sweep_enabled"));

    click(&mut world, "settings.switch.worktree_sweep_enabled");
    assert_eq!(
        writes(&world),
        [("worktree_sweep_enabled".to_owned(), "true".to_owned())]
    );
    assert!(checked(&world, "worktree_sweep_enabled"));
    rest(&mut world);
}

#[test]
fn a_refused_notebook_folder_shows_its_refusal_in_view_in_the_smallest_window_at_the_largest_text()
{
    let (daemon, _) = super::keeping(&[("uiScale", "1.5")], |key, _| {
        (key == "notebook.root").then(|| "cannot create directory: permission denied".to_owned())
    });
    let mut world = World::start(daemon);
    world.resize((800.0, 600.0));
    on_desktop(&mut world);
    retype(&mut world, &field("notebook.root"), "~/elsewhere");
    let rec = world.record(60.0, 0.5, |_, app| {
        app.key(rune::KeyEvent {
            key: Key::Enter,
            mods: rune::Mods::default(),
        })
    });
    crate::wire::whole_on_its_first_frame(&rec, "settings.refusal.notebook.root");
    let body = world.rect("settings.body");
    let refusal = world.rect("settings.refusal.notebook.root");
    assert!(
        refusal[1] >= body[1] && refusal[1] + refusal[3] <= body[1] + body[3],
        "the refusal at {refusal:?} lies inside the page's view {body:?}"
    );
    let focused = world.rect(&field("notebook.root"));
    assert!(
        focused[1] >= body[1] && focused[1] + focused[3] <= body[1] + body[3],
        "the refused field at {focused:?} stays inside the page's view {body:?}"
    );
    rest(&mut world);
}

/// A key pressed and let go as a window reports it: by its code, with the key it types.
fn press_as_a_window_does(world: &mut World, key: Key) {
    let code = match key {
        Key::Enter => PhysicalKeyCode::Enter,
        Key::Tab => PhysicalKeyCode::Tab,
        _ => unreachable!("{key:?}"),
    };
    world.gesture(|app| {
        for pressed in [true, false] {
            app.runtime.physical_key_input(
                PhysicalInput::Key {
                    key: code,
                    mods: Mods::default(),
                    pressed,
                    repeat: false,
                },
                KeyEvent {
                    key: key.clone(),
                    mods: Mods::default(),
                },
                None,
            );
        }
    });
}

#[test]
fn a_refusal_that_arrives_after_the_keyboard_moved_on_leaves_the_page_where_the_person_is() {
    for (keys, press) in [
        ("keys", press as fn(&mut World, Key)),
        ("keys as a window reports them", press_as_a_window_does),
    ] {
        moved_on_with(keys, press);
    }
}

fn moved_on_with(keys: &str, press: fn(&mut World, Key)) {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    daemon.on("set_setting", |_| ());
    let mut world = World::start(daemon);
    world.resize((800.0, 600.0));
    on_desktop(&mut world);
    retype(&mut world, &field("notebook.root"), "部署/ノート");
    press(&mut world, Key::Enter);
    press(&mut world, Key::Tab);
    press(&mut world, Key::Tab);
    let switch = "settings.switch.worktree_sweep_enabled";
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some(switch),
        "{keys}: Tab reaches the sweep switch"
    );
    let sent = world.daemon.sent_of("set_setting");
    let request = sent.last().expect("the notebook folder was sent");
    world.daemon.emit(SettingsUpdatedMessage {
        request_id: request["request_id"].as_str().map(str::to_owned),
        changed_key: Some("notebook.root".into()),
        success: Some(false),
        error: Some("cannot create directory: permission denied".into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(
        named(&world, "settings.refusal.notebook.root").is_some(),
        "the refusal shows under its field"
    );
    let body = world.rect("settings.body");
    let focused = world.rect(switch);
    assert!(
        focused[1] >= body[1] && focused[1] + focused[3] <= body[1] + body[3],
        "{keys}: the focused switch at {focused:?} stays inside the page's view {body:?}"
    );
    rest(&mut world);
}

#[test]
fn a_notebook_folder_with_a_backtick_in_its_name_reads_whole() {
    let root = "/Users/me/notes `draft` folder";
    let (daemon, _) = accepting(&[("notebook.root.effective", root)]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/notes `draft` folder")
    );
    rest(&mut world);
}

/// The newly focused control, and where it shows and how much of it, after the person types
/// into the Notebook folder field at the smallest window with the largest text and leaves it
/// by `leave`, when the daemon `refuses` the folder or saves it. The daemon answers once
/// leaving has settled.
fn left_notebook_folder(refuses: bool, leave: fn(&mut World)) -> (String, [f32; 4], [f32; 4]) {
    let refuse = move |key: &str, _: &str| {
        (refuses && key == "notebook.root")
            .then(|| "cannot create directory: permission denied".to_owned())
    };
    let (daemon, stored) = super::keeping(&[("uiScale", "1.5")], refuse);
    daemon.on("set_setting", |_| ());
    let mut world = World::start(daemon);
    world.resize((800.0, 600.0));
    on_desktop(&mut world);
    retype(&mut world, &field("notebook.root"), "~/elsewhere");
    leave(&mut world);
    world.settle();
    let sent = world.daemon.sent_of("set_setting");
    let save = sent.last().expect("leaving the field saves it");
    for answer in super::write(&stored, &refuse, save) {
        world.daemon.emit(answer);
    }
    world.settle();
    assert_eq!(
        named(&world, "settings.refusal.notebook.root").is_some(),
        refuses,
        "the refusal stands under the field that was left"
    );
    let focused = world
        .screen()
        .focused()
        .and_then(|n| n.key)
        .expect("the person's destination holds the focus");
    let e = world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(focused.as_str()))
        .expect("the destination is drawn");
    rest(&mut world);
    (focused, e.rect, e.visible)
}

#[test]
fn leaving_a_refused_field_by_tab_or_click_keeps_the_page_where_the_person_went() {
    fn by_tab(world: &mut World) {
        press(world, Key::Tab);
    }
    fn by_click(world: &mut World) {
        let find = |world: &World, key: &str| {
            world
                .runtime()
                .elements()
                .into_iter()
                .find(|e| e.key.as_deref() == Some(key))
                .unwrap_or_else(|| panic!("no {key}"))
                .rect
        };
        let body = find(world, "settings.body");
        world.gesture(|app| {
            app.runtime
                .wheel(body[0] + body[2] / 2.0, body[1] + body[3] / 2.0, -2000.0)
        });
        world.settle();
        let [x, y, w, h] = find(world, "settings.search.field");
        world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
    }
    for (how, leave) in [("Tab", by_tab as fn(&mut World)), ("a click", by_click)] {
        let (saved_on, saved_rect, _) = left_notebook_folder(false, leave);
        let (on, rect, visible) = left_notebook_folder(true, leave);
        assert_eq!(on, saved_on, "{how}: the focus went where it would have");
        assert_eq!(
            rect, saved_rect,
            "{how}: {on} shows where the save would have left it"
        );
        assert!(
            (visible[3] - rect[3]).abs() < 0.5,
            "{how}: {on} shows {} of its {} points",
            visible[3],
            rect[3]
        );
    }
}

/// Where the focus stands when the answer to a Notebook folder save the person made with Enter
/// lands after they clicked `went_to` at the top of the page; the page stays there.
fn answered_after_going_to(went_to: &str, answer: fn(&mut World)) -> Option<String> {
    let (daemon, _) = accepting(&[("uiScale", "1.5")]);
    daemon.on("set_setting", |_| ());
    let mut world = World::start(daemon);
    world.resize((800.0, 600.0));
    on_desktop(&mut world);
    retype(&mut world, &field("notebook.root"), "~/first");
    press(&mut world, Key::Enter);
    retype(&mut world, &field("notebook.root"), "~/second");
    let find = |world: &World, key: &str| {
        world
            .runtime()
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key}"))
    };
    let body = find(&world, "settings.body").rect;
    world.gesture(|app| {
        app.runtime
            .wheel(body[0] + body[2] / 2.0, body[1] + body[3] / 2.0, 2000.0)
    });
    world.settle();
    let [x, y, w, h] = find(&world, went_to).rect;
    world.gesture(|app| app.click_at((x + w / 2.0, y + h / 2.0)));
    let before = find(&world, &field("notebook.root")).scrolled;
    answer(&mut world);
    world.settle();
    let after = find(&world, &field("notebook.root")).scrolled;
    assert_eq!(before, after, "the page stays where the person went");
    let focus = world.screen().focused().and_then(|n| n.key);
    rest(&mut world);
    focus
}

fn refuse(world: &mut World, request: &serde_json::Value) {
    world.daemon.emit(SettingsUpdatedMessage {
        request_id: request["request_id"].as_str().map(str::to_owned),
        changed_key: Some("notebook.root".into()),
        success: Some(false),
        error: Some("cannot create directory: permission denied".into()),
        ..SettingsUpdatedMessage::new()
    });
}

#[test]
fn a_refusal_that_lands_after_the_person_went_elsewhere_leaves_the_page_where_they_are() {
    let late: fn(&mut World) = |world| {
        let first = world.daemon.sent_of("set_setting").remove(0);
        refuse(world, &first);
    };
    let queued: fn(&mut World) = |world| {
        let first = world.daemon.sent_of("set_setting").remove(0);
        world.daemon.emit(SettingsUpdatedMessage {
            request_id: first["request_id"].as_str().map(str::to_owned),
            changed_key: Some("notebook.root".into()),
            success: Some(true),
            ..SettingsUpdatedMessage::new()
        });
        world.settle();
        let second = world.daemon.sent_of("set_setting").remove(1);
        assert_eq!(
            second["value"], "~/second",
            "the draft queued by leaving is sent"
        );
        refuse(world, &second);
    };
    let lost: fn(&mut World) = |world| world.daemon.disconnect();
    for (how, answer) in [
        ("a late refusal", late),
        ("a queued save", queued),
        ("a lost daemon", lost),
    ] {
        assert_eq!(
            answered_after_going_to("settings.search.field", answer).as_deref(),
            Some("settings.search.field"),
            "{how}: the focus stays in search"
        );
    }
    answered_after_going_to("settings.heading", late);
}

#[test]
fn with_the_home_on_another_machine_browse_lists_the_homes_folders_and_saves_the_one_chosen() {
    let (daemon, _) = accepting(&[("projects_directory", "/home/me/old")]);
    let daemon = daemon.relaying_to("mac", "oma");
    daemon.on("get_recent_locations", |_| {
        message(RecentLocationsResultMessage {
            success: true,
            home_path: Some("/home/me".into()),
            ..RecentLocationsResultMessage::new()
        })
    });
    daemon.on("browse_directory", |r| {
        let directory = match r["input_path"].as_str().unwrap() {
            "~/" | "~" => "/home/me",
            other => other.trim_end_matches('/'),
        };
        message(BrowseDirectoryResultMessage {
            success: true,
            input_path: r["input_path"].as_str().unwrap().into(),
            home_path: Some("/home/me".into()),
            directory: directory.into(),
            entries: ["code", "projects"]
                .iter()
                .map(|name| DirectoryEntry {
                    name: (*name).into(),
                    path: format!("{directory}/{name}"),
                    is_dir: true,
                    is_repo: Some(false),
                })
                .collect(),
            ..BrowseDirectoryResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    click(&mut world, "settings.browse.projects_directory");
    assert_eq!(world.file_dialog(), None);
    assert_eq!(
        named(&world, "new-session.title").as_deref(),
        Some("Select Projects Directory on oma")
    );
    let hosts: Vec<_> = world
        .daemon
        .sent_of("browse_directory")
        .iter()
        .map(|r| r["host"].clone())
        .collect();
    assert!(
        !hosts.is_empty() && hosts.iter().all(serde_json::Value::is_null),
        "{hosts:?}"
    );
    for _ in 0..3 {
        world.gesture(|d| d.press("down"));
    }
    world.gesture(|d| d.press("enter"));

    assert_eq!(
        writes(&world),
        [(
            "projects_directory".to_owned(),
            "/home/me/projects".to_owned()
        )]
    );
    assert!(
        world
            .screen()
            .find(Target::key("new-session.title"))
            .is_none()
    );
    assert_eq!(
        value(&world, &field("projects_directory")),
        "/home/me/projects"
    );
    rest(&mut world);
}

#[test]
fn with_the_home_on_another_machine_escape_leaves_the_folder_chooser_for_settings_saving_nothing() {
    let (daemon, _) = accepting(&[("projects_directory", "/home/me/projects")]);
    let mut world = World::start(daemon.relaying_to("mac", "oma"));
    on_desktop(&mut world);
    assert!(world.screen().to_string().contains("\"home on oma\""));

    click(&mut world, "settings.browse.projects_directory");
    assert!(
        world
            .screen()
            .find(Target::key("new-session.title"))
            .is_some()
    );
    world.gesture(|d| d.press("escape"));
    world.gesture(|d| d.press("escape"));

    assert!(
        world
            .screen()
            .find(Target::key("new-session.title"))
            .is_none()
    );
    assert!(writes(&world).is_empty());
    assert!(
        world
            .screen()
            .find(Target::key("settings.browse.projects_directory"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn with_the_home_on_another_machine_the_editor_is_this_machines_and_saves_on_its_own_daemon() {
    let (daemon, _) = accepting(&[("editor_executable", "home-editor")]);
    let daemon = daemon.relaying_to("mac", "oma");
    let mine: super::Settings = std::sync::Arc::new(std::sync::Mutex::new(
        [("editor_executable".to_owned(), "zed".to_owned())].into(),
    ));
    let machine =
        attn_testkit::Daemon::of_this_machine(&daemon).state(attn_protocol::InitialStateMessage {
            protocol_version: Some(attn_protocol::PROTOCOL_VERSION.into()),
            daemon_instance_id: Some("mac-daemon".into()),
            settings: Some(mine.lock().unwrap().clone()),
            ..attn_protocol::InitialStateMessage::new()
        });
    let kept = mine.clone();
    machine.on("set_setting", move |r| {
        super::write(&kept, &|_: &str, _: &str| None, r)
    });
    let mut world = World::start_relaying(daemon, machine, attn_testkit::VIEWPORT);
    on_desktop(&mut world);
    let editor = field("editor_executable");
    assert_eq!(value(&world, &editor), "zed");

    retype(&mut world, &editor, "zed --wait");
    press(&mut world, Key::Enter);

    assert_eq!(writes(&world), []);
    assert_eq!(
        mine.lock()
            .unwrap()
            .get("editor_executable")
            .map(String::as_str),
        Some("zed --wait")
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.editor_executable"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn with_the_home_on_another_machine_the_folder_chooser_asks_only_the_home_even_for_a_folder_it_lacks()
 {
    let (daemon, _) = accepting(&[
        ("projects_directory", "/home/me/old"),
        ("outposts_enabled", "true"),
    ]);
    let daemon = daemon.relaying_to("mac", "oma");
    daemon.on("get_recent_locations", |_| {
        message(RecentLocationsResultMessage {
            success: true,
            home_path: Some("/home/me".into()),
            ..RecentLocationsResultMessage::new()
        })
    });
    daemon.on("browse_directory", |r| {
        let input = r["input_path"].as_str().unwrap();
        let found = input.starts_with('~');
        message(BrowseDirectoryResultMessage {
            success: found,
            error: (!found).then(|| "no such directory".into()),
            input_path: input.into(),
            home_path: Some("/home/me".into()),
            directory: if found {
                "/home/me".into()
            } else {
                input.trim_end_matches('/').into()
            },
            ..BrowseDirectoryResultMessage::new()
        })
    });
    daemon.on("inspect_path", |r| {
        let path = r["path"].as_str().unwrap();
        message(attn_protocol::InspectPathResultMessage {
            success: true,
            inspection: Some(attn_protocol::PathInspection {
                input_path: path.into(),
                resolved_path: path.into(),
                home_path: Some("/home/me".into()),
                exists: false,
                is_directory: false,
                repo_root: None,
            }),
            ..attn_protocol::InspectPathResultMessage::new()
        })
    });
    let mut world = World::start(daemon);
    world.daemon.emit(attn_protocol::OutpostsUpdatedMessage {
        outposts: vec![attn_protocol::OutpostInfo {
            id: "box".into(),
            name: "box".into(),
            destination: "box:2200".into(),
            state: "connected".into(),
            reach: "reachable".into(),
            state_since: "2026-05-16T16:00:00Z".into(),
            ..attn_protocol::OutpostInfo::default()
        }],
        ..attn_protocol::OutpostsUpdatedMessage::new()
    });
    world.settle();
    on_desktop(&mut world);

    click(&mut world, "settings.browse.projects_directory");
    retype(&mut world, "new-session.path.field", "/srv/absent/");
    world.settle();
    assert!(
        world
            .daemon
            .sent_of("inspect_path")
            .iter()
            .any(|r| r["path"] == "/srv/absent" && r["host"].is_null())
    );

    let elsewhere: Vec<_> = [
        "browse_directory",
        "inspect_path",
        "agent_catalog",
        "get_recent_locations",
    ]
    .into_iter()
    .flat_map(|cmd| world.daemon.sent_of(cmd))
    .filter(|r| !r["host"].is_null())
    .map(|r| (r["cmd"].clone(), r["host"].clone()))
    .collect();
    assert_eq!(
        elsewhere,
        Vec::<(serde_json::Value, serde_json::Value)>::new()
    );
    assert!(
        world
            .daemon
            .sent_of("browse_directory")
            .iter()
            .any(|r| r["input_path"] == "/srv/absent/")
    );
    rest(&mut world);
}
