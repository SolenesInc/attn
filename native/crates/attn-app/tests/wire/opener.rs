use std::collections::BTreeMap;
use std::time::Duration;

use attn_app::{Action, Binding, Keymap};
use attn_protocol::{
    BrowseDirectoryResultMessage, Desktop, DesktopPane, DirectoryEntry, FileActivity,
    FsIndexResultMessage, InitialStateMessage, OpenMarkdownResultMessage, OutpostInfo, Profile,
    RecentFilesResultMessage, Session,
};
use attn_testkit::tree::{pane, row};
use attn_testkit::{Answer, Daemon, Request, Target, World, key_event, message};
use serde_json::{Value, json};

const DESKTOP: &str = "work/desktop_1";
const NOTEBOOK: &str = "/notebook";

fn recent(path: &str, at: &str, host: Option<&str>) -> FileActivity {
    FileActivity {
        path: path.into(),
        last_at: at.into(),
        count: 1,
        source: "open".into(),
        host: host.map(str::to_owned),
        ..FileActivity::default()
    }
}

fn recents() -> Vec<FileActivity> {
    vec![
        recent("/repo/docs/plan.md", "2026-07-24T10:00:00Z", None),
        recent("/other/journal.md", "2026-07-23T10:00:00Z", None),
    ]
}

struct Setup {
    /// The agent on the desktop and its folder; none leaves the window on its home.
    session: Option<&'static str>,
    notebook: &'static str,
    recents: Vec<FileActivity>,
    index: Option<(Vec<&'static str>, bool)>,
    /// Document tiles beside the agent's pane, and the leaf the screen shows as active.
    tiles: Vec<Value>,
    active: &'static str,
    /// The smallest window at the largest text, where the list scrolls.
    small: bool,
    outposts: Vec<OutpostInfo>,
}

fn studio(reach: &str, state: &str) -> OutpostInfo {
    OutpostInfo {
        id: "studio".into(),
        name: "Studio".into(),
        destination: "studio:2200".into(),
        reach: reach.into(),
        state: state.into(),
        ..OutpostInfo::default()
    }
}

impl Default for Setup {
    fn default() -> Self {
        Setup {
            session: Some("/repo"),
            notebook: NOTEBOOK,
            recents: recents(),
            index: Some((vec!["docs/design.md", "README.md", "docs/plan.md"], false)),
            tiles: Vec::new(),
            active: "p1",
            small: false,
            outposts: vec![studio("reachable", "connected")],
        }
    }
}

fn index_reply(r: &Request, files: &[&str], truncated: bool) -> Value {
    message(FsIndexResultMessage {
        request_id: r["request_id"].as_str().unwrap_or_default().into(),
        success: true,
        root: r["root"].as_str().unwrap_or_default().into(),
        files: files.iter().map(|f| f.to_string()).collect(),
        truncated,
        ..FsIndexResultMessage::new()
    })
}

fn start(setup: Setup) -> World {
    let mut settings = BTreeMap::new();
    if setup.small {
        settings.insert("uiScale".to_owned(), "1.5".to_owned());
    }
    if !setup.outposts.is_empty() {
        settings.insert("outposts_enabled".to_owned(), "true".to_owned());
    }
    if !setup.notebook.is_empty() {
        settings.insert(
            "notebook.root.effective".to_owned(),
            setup.notebook.to_owned(),
        );
    }
    let state = InitialStateMessage {
        selected_profile_id: Some("work".into()),
        profiles: Some(vec![Profile {
            id: "work".into(),
            name: "Work".into(),
            current_desktop_id: DESKTOP.into(),
            ..Profile::default()
        }]),
        settings: Some(settings),
        outposts: Some(setup.outposts),
        sessions: Some(vec![
            Session {
                id: "s1".into(),
                label: "s1".into(),
                directory: setup.session.unwrap_or_default().into(),
                profile_id: "work".into(),
                ..Session::default()
            },
            Session {
                id: "s2".into(),
                label: "s2".into(),
                directory: "/elsewhere".into(),
                profile_id: "work".into(),
                ..Session::default()
            },
        ]),
        desktops: Some(vec![Desktop {
            id: DESKTOP.into(),
            profile_id: "work".into(),
            name: "Desktop 1".into(),
            shortcut_slot: Some(1),
            order_key: "1".into(),
            tree_json: row(std::iter::once(pane("p1")).chain(setup.tiles)).to_string(),
            active_pane_id: setup.active.into(),
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
    };
    let daemon = Daemon::scripted().state(state);
    let files = setup.recents;
    daemon.on("recent_files", move |r| {
        message(RecentFilesResultMessage {
            request_id: r["request_id"].as_str().unwrap_or_default().into(),
            success: true,
            files: files.clone(),
            ..RecentFilesResultMessage::new()
        })
    });
    match setup.index {
        Some((files, truncated)) => {
            daemon.on("fs_index", move |r| index_reply(r, &files, truncated));
        }
        None => {
            daemon.answer_in_turn("fs_index", [Answer::Hold]);
        }
    }
    daemon.on("browse_directory", |r| {
        message(BrowseDirectoryResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            input_path: r["input_path"].as_str().unwrap_or_default().into(),
            directory: "/home/victor/notes".into(),
            home_path: Some("/home/victor".into()),
            entries: vec![
                DirectoryEntry {
                    name: "archive".into(),
                    path: "/home/victor/notes/archive".into(),
                    is_dir: true,
                    ..DirectoryEntry::default()
                },
                DirectoryEntry {
                    name: "ideas.md".into(),
                    path: "/home/victor/notes/ideas.md".into(),
                    is_dir: false,
                    ..DirectoryEntry::default()
                },
            ],
            ..BrowseDirectoryResultMessage::new()
        })
    });
    daemon.on("open_markdown", |r| {
        message(OpenMarkdownResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            path: r["path"].as_str().unwrap_or_default().into(),
            desktop_id: Some(DESKTOP.into()),
            tile_id: Some("tile-opened".into()),
            ..OpenMarkdownResultMessage::new()
        })
    });
    daemon.on("desktop_show_leaf", |r| {
        json!({"event": "desktop_show_leaf_result", "request_id": r["request_id"], "success": true})
    });
    let size = if setup.small {
        (800.0, 600.0)
    } else {
        (1600.0, 900.0)
    };
    let mut world = World::start_sized(daemon, size);
    if setup.session.is_some() {
        world.open_desktop();
    }
    open_opener(&mut world);
    world
}

/// Presses the opener's shortcut, ⌘P on macOS.
fn open_opener(world: &mut World) {
    let ev = match Keymap::from_setting(None, attn_testkit::platform()).binding(&Action::FileOpen) {
        Some(Binding::Chord(chord)) => key_event(&chord),
        other => panic!("the opener has no single chord: {other:?}"),
    };
    world.gesture(|app| app.key(ev));
}

/// The opener's rows as assistive tech reads them: the file's name, then where it is.
fn rows(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter(|n| {
            n.key
                .as_deref()
                .is_some_and(|k| k.starts_with("opener.row."))
        })
        .filter_map(|n| n.name)
        .collect()
}

fn is_open(world: &World) -> bool {
    world
        .screen()
        .find(Target::role("dialog").name("Open a markdown file"))
        .is_some()
}

fn empty(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("opener.empty"))
        .and_then(|n| n.name)
}

/// Replaces the query with `text` and lets a typed path's listing come back.
fn search(world: &mut World, text: &str) {
    world.gesture(|app| {
        app.press("mod+a");
        app.type_text(text);
    });
    world.advance(Duration::from_millis(300));
}

fn press(world: &mut World, chord: &str) {
    world.gesture(|app| app.press(chord));
}

fn sent(world: &World, cmd: &str) -> Vec<Request> {
    world.daemon.sent_of(cmd)
}

/// What each listed row says at its end, by the part's key suffix: its machine, `.offline` and
/// its touch.
fn row_parts(world: &World, part: &str) -> Vec<Option<String>> {
    let screen = world.screen();
    screen
        .all(Target::role("list-box-option"))
        .into_iter()
        .filter_map(|n| n.key.filter(|k| k.starts_with("opener.row.")))
        .map(|key| {
            screen
                .find(Target::key(format!("{key}.{part}")))
                .map(|n| n.name.unwrap_or_default())
        })
        .collect()
}

/// A machine name wider than the smallest window's opener, as the screenshot tour's worst case
/// makes them; its chip must leave the field room to take keys.
const LONG_LAB: &str = "lab-build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary-build-and-release-platform-本番の権限を移行する作業ツリー-tokyo-region-primary";

/// The machine the opener lists, when it lists one machine's files.
fn scope(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("opener.scope"))
        .and_then(|n| n.name)
}

fn far_and_here() -> Vec<FileActivity> {
    let mut edited = recent("/far/notes.md", "2026-07-24T10:00:00Z", Some("studio"));
    edited.source = "edited".into();
    vec![
        edited,
        recent("/repo/docs/plan.md", "2026-07-23T10:00:00Z", None),
    ]
}

fn notices(world: &World) -> Vec<String> {
    world
        .screen()
        .all(Target::role("status"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

#[test]
fn the_opener_lists_recent_files_at_once_relative_to_the_agents_folder_and_takes_the_keyboard() {
    let world = start(Setup::default());
    assert_eq!(
        rows(&world),
        ["plan.md docs/plan.md", "journal.md /other/journal.md"]
    );
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("opener.field")
    );
    assert_eq!(sent(&world, "recent_files").len(), 1);
    let index = &sent(&world, "fs_index")[0];
    assert_eq!(
        (&index["root"], &index["extensions"]),
        (&json!("/repo"), &json!(["md"]))
    );
}

#[test]
fn recents_show_before_the_index_answers_then_both_rank_in_one_list_each_file_once() {
    let mut world = start(Setup {
        index: None,
        ..Setup::default()
    });
    assert_eq!(rows(&world).len(), 2);
    let held = sent(&world, "fs_index").remove(0);
    world.daemon.reply_to(
        &held,
        index_reply(&held, &["docs/design.md", "docs/plan.md"], false),
    );
    world.settle();
    search(&mut world, "d");
    let listed = rows(&world);
    assert!(
        listed.contains(&"plan.md docs/plan.md".to_owned()),
        "{listed:?}"
    );
    assert!(
        listed.contains(&"design.md docs/design.md".to_owned()),
        "{listed:?}"
    );
    assert_eq!(
        listed
            .iter()
            .filter(|r| *r == "plan.md docs/plan.md")
            .count(),
        1
    );
}

#[test]
fn enter_opens_the_highlighted_file_by_its_absolute_path_for_the_agent_and_shows_it() {
    let mut world = start(Setup::default());
    for key in ["down", "down", "up", "down"] {
        press(&mut world, key);
    }
    assert_eq!(
        world
            .screen()
            .active_option()
            .and_then(|n| n.name)
            .as_deref(),
        Some("journal.md /other/journal.md"),
        "the field names the highlighted file"
    );
    press(&mut world, "enter");
    let opened = sent(&world, "open_markdown");
    assert_eq!(opened.len(), 1);
    assert_eq!(
        (&opened[0]["path"], &opened[0]["session_id"]),
        (&json!("/other/journal.md"), &json!("s1"))
    );
    assert!(!is_open(&world));
    assert_eq!(
        sent(&world, "desktop_show_leaf")[0]["leaf_id"],
        "tile-opened"
    );
}

#[test]
fn arrows_show_each_file_whole_in_the_smallest_window_at_the_largest_text_and_enter_opens_it() {
    const FILES: [&str; 40] = [
        "a01.md", "a02.md", "a03.md", "a04.md", "a05.md", "a06.md", "a07.md", "a08.md", "a09.md",
        "a10.md", "a11.md", "a12.md", "a13.md", "a14.md", "a15.md", "a16.md", "a17.md", "a18.md",
        "a19.md", "a20.md", "a21.md", "a22.md", "a23.md", "a24.md", "a25.md", "a26.md", "a27.md",
        "a28.md", "a29.md", "a30.md", "a31.md", "a32.md", "a33.md", "a34.md", "a35.md", "a36.md",
        "a37.md", "a38.md", "a39.md", "a40.md",
    ];
    let mut world = start(Setup {
        recents: Vec::new(),
        index: Some((FILES.to_vec(), false)),
        small: true,
        ..Setup::default()
    });
    world.gesture(|app| app.type_text("a"));
    super::arrows_show_each_option(&mut world, "opener.list");
    for _ in 0..30 {
        press(&mut world, "down");
    }
    super::typing_shows_the_active_option(&mut world, "opener.list", ".");
    let highlighted = world.screen().active_option().and_then(|n| n.name);
    press(&mut world, "enter");
    let opened = sent(&world, "open_markdown");
    assert_eq!(opened.len(), 1);
    let path = opened[0]["path"].as_str().unwrap_or_default().to_owned();
    assert!(
        highlighted
            .as_deref()
            .is_some_and(|name| name.starts_with(path.trim_start_matches("/repo/"))),
        "Enter opened {path}, not the highlighted {highlighted:?}"
    );
}

#[test]
fn editing_the_path_to_another_folder_shows_its_listing_from_the_top_with_its_first_row_highlighted()
 {
    let mut world = start(Setup {
        small: true,
        ..Setup::default()
    });
    world.daemon.on("browse_directory", |r| {
        let input = r["input_path"].as_str().unwrap_or_default().to_owned();
        let folder = input
            .trim_start_matches("~/")
            .trim_end_matches('/')
            .to_owned();
        message(BrowseDirectoryResultMessage {
            request_id: r["request_id"].as_str().map(str::to_owned),
            success: true,
            input_path: input,
            directory: format!("/home/victor/{folder}"),
            home_path: Some("/home/victor".into()),
            entries: (1..=30)
                .map(|i| DirectoryEntry {
                    name: format!("{folder}-{i:02}.md"),
                    path: format!("/home/victor/{folder}/{folder}-{i:02}.md"),
                    is_dir: false,
                    ..DirectoryEntry::default()
                })
                .collect(),
            ..BrowseDirectoryResultMessage::new()
        })
    });
    search(&mut world, "~/one/");
    for _ in 0..29 {
        press(&mut world, "down");
    }
    let scrolled = |world: &World| world.screen().scrolled(Target::key("opener.list"));
    assert!(scrolled(&world) > Some(0.0), "the arrows scrolled the list");

    search(&mut world, "~/two/");
    assert_eq!(
        world
            .screen()
            .active_option()
            .and_then(|n| n.key)
            .as_deref(),
        Some("opener.row./home/victor/two/two-01.md")
    );
    assert_eq!(scrolled(&world), Some(0.0), "the new listing shows its top");
    press(&mut world, "enter");
    assert_eq!(
        sent(&world, "open_markdown")[0]["path"],
        "/home/victor/two/two-01.md"
    );
}

#[test]
fn an_outposts_recent_file_shows_its_machine_apart_from_the_same_path_here_and_opens_there() {
    let mut world = start(Setup {
        recents: vec![
            recent("/repo/docs/plan.md", "2026-07-24T10:00:00Z", Some("studio")),
            recent("/repo/docs/plan.md", "2026-07-23T10:00:00Z", None),
        ],
        ..Setup::default()
    });
    assert_eq!(
        rows(&world),
        [
            "plan.md /repo/docs/plan.md on Studio",
            "plan.md docs/plan.md"
        ]
    );
    press(&mut world, "enter");
    let opened = &sent(&world, "open_markdown")[0];
    assert_eq!(
        (&opened["path"], &opened["session_id"], &opened["host"]),
        (&json!("/repo/docs/plan.md"), &json!("s1"), &json!("studio"))
    );
}

#[test]
fn the_highlight_stays_on_its_file_as_the_list_shrinks_and_enter_opens_nothing_when_nothing_matches()
 {
    let mut world = start(Setup::default());
    search(&mut world, "md");
    press(&mut world, "down");
    press(&mut world, "down");
    search(&mut world, "docs/d");
    press(&mut world, "enter");
    let opened = sent(&world, "open_markdown");
    assert_eq!(opened[0]["path"], "/repo/docs/design.md");

    open_opener(&mut world);
    search(&mut world, "zzz");
    assert_eq!(
        empty(&world).as_deref(),
        Some("No files match. Type a path to browse.")
    );
    press(&mut world, "enter");
    assert_eq!(sent(&world, "open_markdown").len(), 1);
}

#[test]
fn a_query_with_no_match_in_a_capped_index_says_the_index_is_capped() {
    let mut world = start(Setup {
        index: Some((vec![], true)),
        ..Setup::default()
    });
    search(&mut world, "zzzz");
    assert!(
        empty(&world).is_some_and(|e| e.contains("index is capped")),
        "{:?}",
        empty(&world)
    );
}

#[test]
fn a_typed_folder_lists_its_files_and_folders_opening_files_and_descending_into_folders() {
    let mut world = start(Setup::default());
    search(&mut world, "docs/plan");
    assert!(sent(&world, "browse_directory").is_empty());

    search(&mut world, "~/notes/");
    let browsed = sent(&world, "browse_directory");
    let last = browsed.last().unwrap();
    assert_eq!(
        (&last["input_path"], &last["extensions"]),
        (&json!("~/notes/"), &json!(["md"]))
    );
    assert_eq!(
        rows(&world),
        ["archive/ ~/notes/archive", "ideas.md ~/notes/ideas.md"]
    );

    press(&mut world, "enter");
    assert_eq!(
        world
            .screen()
            .get(Target::key("opener.field"))
            .value
            .as_deref(),
        Some("~/notes/archive/")
    );
    assert!(sent(&world, "open_markdown").is_empty());

    search(&mut world, "~/notes/");
    press(&mut world, "down");
    press(&mut world, "enter");
    assert_eq!(
        sent(&world, "open_markdown")[0]["path"],
        "/home/victor/notes/ideas.md"
    );
}

#[test]
fn a_relative_path_browses_against_the_agents_folder() {
    let mut world = start(Setup::default());
    search(&mut world, "./docs/");
    assert_eq!(
        sent(&world, "browse_directory").last().unwrap()["input_path"],
        "/repo/docs/"
    );
}

#[test]
fn escape_or_a_press_outside_closes_the_opener_and_a_press_inside_does_not() {
    let mut world = start(Setup::default());
    press(&mut world, "escape");
    assert!(!is_open(&world));

    open_opener(&mut world);
    world.gesture(|app| app.click(Target::key("opener.field")));
    assert!(is_open(&world));
    world.gesture(|app| app.click_at((20.0, 880.0)));
    assert!(!is_open(&world));
}

#[test]
fn with_no_agent_behind_the_screen_the_opener_indexes_the_notebook() {
    let world = start(Setup {
        session: None,
        ..Setup::default()
    });
    assert_eq!(sent(&world, "fs_index")[0]["root"], NOTEBOOK);
    assert_eq!(
        rows(&world),
        ["plan.md /repo/docs/plan.md", "journal.md /other/journal.md"]
    );
}

#[test]
fn a_file_opened_from_home_takes_the_window_to_its_desktop() {
    let mut world = start(Setup {
        session: None,
        ..Setup::default()
    });
    press(&mut world, "enter");
    assert_eq!(
        sent(&world, "desktop_show_leaf")[0]["leaf_id"],
        "tile-opened"
    );
    assert!(world.screen().find(Target::key("home")).is_none());
}

#[test]
fn an_agent_with_no_folder_indexes_the_notebook_and_still_opens_for_that_agent() {
    let mut world = start(Setup {
        session: Some(""),
        ..Setup::default()
    });
    press(&mut world, "enter");
    assert_eq!(sent(&world, "fs_index")[0]["root"], NOTEBOOK);
    assert_eq!(sent(&world, "open_markdown")[0]["session_id"], "s1");
}

fn document_tile(id: &str, path: &str, session: Option<&str>) -> Value {
    json!({
        "type": "tile",
        "tile_id": id,
        "tile_kind": "markdown",
        "tile_params": json!({ "path": path }).to_string(),
        "tile_session_id": session,
    })
}

#[test]
fn over_a_document_tile_the_opener_indexes_the_folder_of_the_agent_it_was_opened_for() {
    let tiles = || {
        vec![
            document_tile("tile-plan", "/elsewhere/plan.md", Some("s2")),
            document_tile("tile-loose", "/tmp/loose.md", None),
        ]
    };
    let mut world = start(Setup {
        tiles: tiles(),
        active: "tile-plan",
        ..Setup::default()
    });
    assert_eq!(sent(&world, "fs_index")[0]["root"], "/elsewhere");
    press(&mut world, "enter");
    assert_eq!(sent(&world, "open_markdown")[0]["session_id"], "s2");

    let world = start(Setup {
        tiles: tiles(),
        active: "tile-loose",
        ..Setup::default()
    });
    assert_eq!(sent(&world, "fs_index")[0]["root"], NOTEBOOK);
}

#[test]
fn with_no_folder_and_no_notebook_the_opener_lists_recents_without_an_index() {
    let world = start(Setup {
        session: None,
        notebook: "",
        ..Setup::default()
    });
    assert!(sent(&world, "fs_index").is_empty());
    assert_eq!(rows(&world).len(), 2);
}

#[test]
fn each_recent_file_names_its_machine_and_its_latest_touch_only_while_an_outpost_exists() {
    let world = start(Setup {
        recents: far_and_here(),
        ..Setup::default()
    });
    assert_eq!(
        rows(&world),
        ["notes.md /far/notes.md on Studio", "plan.md docs/plan.md"]
    );
    assert_eq!(
        row_parts(&world, "machine"),
        [Some("Studio".to_owned()), Some("This Mac".to_owned())]
    );
    let touched = row_parts(&world, "touched");
    assert!(
        touched[0]
            .as_deref()
            .is_some_and(|t| t.starts_with("edited "))
            && touched[1]
                .as_deref()
                .is_some_and(|t| t.starts_with("opened ")),
        "{touched:?}"
    );

    let alone = start(Setup {
        recents: far_and_here(),
        outposts: Vec::new(),
        ..Setup::default()
    });
    assert_eq!(
        rows(&alone),
        ["plan.md docs/plan.md"],
        "with outposts off, an outpost's files are not listed"
    );
    assert_eq!(row_parts(&alone, "machine"), [None]);
}

#[test]
fn tab_and_shift_tab_cycle_the_machine_listed_and_backspace_on_an_empty_field_lists_every_machine()
{
    let mut lab = studio("reachable", "connected");
    lab.id = "lab".into();
    lab.name = LONG_LAB.into();
    let mut files = far_and_here();
    files.push(recent("/lab/log.md", "2026-07-22T10:00:00Z", Some("lab")));
    let mut world = start(Setup {
        recents: files,
        outposts: vec![studio("reachable", "connected"), lab],
        small: true,
        ..Setup::default()
    });
    let every = [
        "notes.md /far/notes.md on Studio".to_owned(),
        "plan.md docs/plan.md".to_owned(),
        format!("log.md /lab/log.md on {LONG_LAB}"),
    ];
    assert_eq!(scope(&world), None);

    press(&mut world, "tab");
    assert_eq!(scope(&world).as_deref(), Some("This Mac"));
    assert_eq!(rows(&world), ["plan.md docs/plan.md"]);
    assert_eq!(
        row_parts(&world, "machine"),
        [None],
        "one machine's rows carry no tag"
    );

    press(&mut world, "tab");
    assert_eq!(scope(&world).as_deref(), Some("Studio"));
    assert_eq!(rows(&world), ["notes.md /far/notes.md on Studio"]);

    press(&mut world, "tab");
    assert_eq!(scope(&world).as_deref(), Some(LONG_LAB));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("opener.field"),
        "a long machine's chip leaves the field room to keep the keyboard"
    );
    press(&mut world, "tab");
    assert_eq!(scope(&world), None);
    assert_eq!(rows(&world), every);

    press(&mut world, "shift+tab");
    assert_eq!(scope(&world).as_deref(), Some(LONG_LAB));
    press(&mut world, "shift+tab");
    assert_eq!(scope(&world).as_deref(), Some("Studio"));
    search(&mut world, "notes");
    press(&mut world, "backspace");
    assert_eq!(
        scope(&world).as_deref(),
        Some("Studio"),
        "Backspace in typed text edits it"
    );
    press(&mut world, "mod+a");
    press(&mut world, "backspace");
    assert_eq!(scope(&world).as_deref(), Some("Studio"));
    press(&mut world, "backspace");
    assert_eq!(scope(&world), None);
    assert_eq!(rows(&world), every);
}

#[test]
fn an_offline_outposts_files_are_marked_offline_and_opening_one_says_so_and_opens_nothing() {
    let mut world = start(Setup {
        recents: far_and_here(),
        outposts: vec![studio("unreachable", "lost")],
        ..Setup::default()
    });
    assert_eq!(
        row_parts(&world, "offline"),
        [Some("offline".to_owned()), None]
    );
    press(&mut world, "enter");
    assert!(sent(&world, "open_markdown").is_empty());
    assert!(is_open(&world), "the opener stays open for another file");
    assert!(
        notices(&world)
            .iter()
            .any(|n| n.contains("Could not open notes.md: Studio is offline")),
        "{:?}",
        notices(&world)
    );
}

#[test]
fn a_typed_path_browses_the_machine_listed_and_its_file_opens_on_that_machine() {
    let mut world = start(Setup {
        recents: far_and_here(),
        ..Setup::default()
    });
    search(&mut world, "~/notes/");
    let asked = |world: &World| {
        sent(world, "browse_directory")
            .last()
            .map(|r| r["host"].clone())
    };
    assert_eq!(asked(&world), Some(Value::Null));

    press(&mut world, "shift+tab");
    assert_eq!(scope(&world).as_deref(), Some("Studio"));
    assert_eq!(
        asked(&world),
        Some(json!("studio")),
        "Tab lists the typed folder again on the new machine at once"
    );
    assert_eq!(
        rows(&world),
        [
            "archive/ ~/notes/archive on Studio",
            "ideas.md ~/notes/ideas.md on Studio"
        ]
    );
    press(&mut world, "down");
    press(&mut world, "enter");
    let opened = &sent(&world, "open_markdown")[0];
    assert_eq!(
        (&opened["path"], &opened["host"]),
        (&json!("/home/victor/notes/ideas.md"), &json!("studio"))
    );

    open_opener(&mut world);
    press(&mut world, "shift+tab");
    search(&mut world, "./docs/");
    assert_eq!(
        empty(&world).as_deref(),
        Some("No folder to resolve this path against."),
        "a relative path has no folder on an outpost"
    );
}
