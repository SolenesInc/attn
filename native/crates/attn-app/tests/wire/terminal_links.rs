use std::time::Duration;

use attn_protocol::Session;
use attn_testkit::{Daemon, Target, World};
use rune::{Cursor, Mods, PhysicalInput};
use serde_json::{Value, json};

use super::terminal::{cell, inputs, one_terminal, output, pane, pointed, showing};

fn underlines(world: &World) -> Vec<(u64, [u64; 2])> {
    world
        .runtime()
        .state
        .terminals
        .view_state("r1")
        .expect("r1 is drawn")["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["kind"] == "Underline")
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["row"].as_u64().unwrap(),
                [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
            )
        })
        .collect()
}

fn hover(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| app.runtime.pointer_move(at.0, at.1));
}

/// Holds ⌘ on macOS and Ctrl elsewhere, or lets go of every modifier.
fn accelerator(world: &mut World, held: bool) {
    let mods = Mods {
        logo: held && cfg!(target_os = "macos"),
        cmd: held,
        ctrl: held && !cfg!(target_os = "macos"),
        ..Mods::default()
    };
    world.gesture(|app| app.runtime.physical_input(PhysicalInput::Modifiers(mods)));
}

fn click(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| {
        app.runtime.pointer_down(at.0, at.1);
        app.runtime.pointer_up(at.0, at.1);
    });
}

fn opened(world: &mut World) -> Vec<String> {
    world.opened_links()
}

/// A terminal whose session works in `directory`, on an outpost when `host` names one.
fn in_directory(directory: &str, host: Option<&str>) -> Daemon {
    let mut state = showing(
        vec![attn_protocol::DesktopPane {
            session_id: "s1".into(),
            ..pane("t1", "r1")
        }],
        "daemon-1",
    );
    state.sessions = Some(vec![Session {
        id: "s1".into(),
        directory: directory.into(),
        host: host.map(str::to_owned),
        ..Session::default()
    }]);
    Daemon::scripted().state(state)
}

#[test]
fn hovering_a_url_underlines_it_and_output_reads_the_link_under_the_resting_pointer_again() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"see https://example.test/page. then\r\n");
    world.settle();

    hover(&mut world, 10, 0);
    assert_eq!(underlines(&world), [(0, [4, 29])]);
    let overlay = &world.runtime().state.terminals.view_state("r1").unwrap()["overlays"][0];
    assert_eq!(
        overlay["alpha"],
        Value::from(204),
        "the text's color at 0.8"
    );

    hover(&mut world, 1, 0);
    assert_eq!(underlines(&world), []);

    hover(&mut world, 10, 0);
    output(&world, 2, b"more");
    world.settle();
    assert_eq!(
        underlines(&world),
        [(0, [4, 29])],
        "output below leaves the link under the pointer"
    );
    output(&world, 3, b"\x1b[2J\x1b[Hplain words here\r\n");
    world.settle();
    assert_eq!(underlines(&world), [], "output replaced the link");
    output(&world, 4, b"\x1b[Hgo https://example.test/next\r\n");
    world.settle();
    assert_eq!(
        underlines(&world),
        [(0, [3, 28])],
        "output put a link under the pointer"
    );

    hover(&mut world, 11, 0);
    let away = cell(&world, 10, 0);
    world.gesture(|app| app.runtime.pointer_move(away.0, -50.0));
    assert_eq!(underlines(&world), []);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_click_with_the_accelerator_opens_the_url_and_a_plain_click_does_not() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"see https://example.test/page. then\r\n");
    world.settle();

    click(&mut world, 10, 0);
    assert_eq!(opened(&mut world), Vec::<String>::new());
    world.advance(Duration::from_secs(1));

    hover(&mut world, 10, 0);
    assert_eq!(world.runtime().cursor(), Cursor::Text);
    accelerator(&mut world, true);
    assert_eq!(world.runtime().cursor(), Cursor::Pointer);
    click(&mut world, 10, 0);
    assert_eq!(opened(&mut world), ["https://example.test/page"]);

    accelerator(&mut world, false);
    assert_eq!(world.runtime().cursor(), Cursor::Text);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_accelerated_click_on_a_link_opens_it_instead_of_telling_a_program_that_tracks_the_mouse() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(
        &world,
        1,
        b"https://example.test/x\r\n\x1b[?1000h\x1b[?1006h",
    );
    world.settle();

    click(&mut world, 30, 0);
    assert_eq!(
        inputs(&world),
        [pointed("\x1b[<0;31;1M"), pointed("\x1b[<3;31;1m")]
    );

    accelerator(&mut world, true);
    click(&mut world, 3, 0);
    assert_eq!(opened(&mut world), ["https://example.test/x"]);
    assert_eq!(inputs(&world).len(), 2, "the program heard nothing more");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn an_osc_8_hyperlink_underlines_its_whole_label_and_opens_its_hidden_uri() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(
        &world,
        1,
        b"go \x1b]8;;https://hidden.test/a\x1b\\read the docs\x1b]8;;\x1b\\ now\r\n",
    );
    world.settle();

    hover(&mut world, 9, 0);
    assert_eq!(underlines(&world), [(0, [3, 16])]);
    accelerator(&mut world, true);
    click(&mut world, 4, 0);
    assert_eq!(opened(&mut world), ["https://hidden.test/a"]);

    output(
        &world,
        2,
        b"\x1b]8;;smb://share.test/x\x1b\\share\x1b]8;;\x1b\\\r\n",
    );
    world.settle();
    world.advance(Duration::from_secs(1));
    click(&mut world, 2, 1);
    assert_eq!(
        opened(&mut world),
        Vec::<String>::new(),
        "only web, mail and phone links open, as the Tauri app's opener allows"
    );

    output(
        &world,
        3,
        b"\x1b]8;;tel:+15550100\x1b\\call\x1b]8;;\x1b\\\r\n",
    );
    world.settle();
    world.advance(Duration::from_secs(1));
    click(&mut world, 2, 2);
    assert_eq!(opened(&mut world), ["tel:+15550100"]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_url_that_wraps_underlines_both_rows_and_opens_whole() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let cols = world.terminal_size("r1").0;
    let url = format!("https://example.test/{}", "a".repeat(usize::from(cols)));
    output(&world, 1, format!("{url}\r\n").as_bytes());
    world.settle();

    hover(&mut world, 2, 1);
    assert_eq!(
        underlines(&world),
        [
            (0, [0, u64::from(cols)]),
            (1, [0, url.len() as u64 - u64::from(cols)])
        ]
    );
    accelerator(&mut world, true);
    click(&mut world, 2, 1);
    assert_eq!(opened(&mut world), [url]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_path_that_exists_under_the_sessions_directory_opens_and_one_that_does_not_is_no_link() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::create_dir(dir.path().join("src")).unwrap();
    std::fs::write(dir.path().join("src/main.go"), "").unwrap();
    std::fs::write(dir.path().join("my notes.md"), "").unwrap();
    let root = dir.path().to_str().unwrap();
    let mut world = World::start(in_directory(root, None));
    world.open_desktop();
    output(&world, 1, b"(src/main.go:12:3), src/gone.go\r\n");
    world.settle();

    hover(&mut world, 5, 0);
    assert_eq!(underlines(&world), [(0, [1, 17])]);
    hover(&mut world, 25, 0);
    assert_eq!(underlines(&world), [], "no such file");
    std::fs::write(dir.path().join("src/gone.go"), "").unwrap();
    output(&world, 2, b"\r\n");
    world.settle();
    assert_eq!(
        underlines(&world),
        [(0, [20, 31])],
        "a file made after it was printed links once the screen draws again"
    );

    accelerator(&mut world, true);
    click(&mut world, 5, 0);
    assert_eq!(opened(&mut world), [format!("file://{root}/src/main.go")]);

    output(
        &world,
        3,
        format!("\x1b]8;;file://{root}/my%20notes.md\x1b\\notes\x1b]8;;\x1b\\").as_bytes(),
    );
    world.settle();
    click(&mut world, 1, 2);
    assert_eq!(
        opened(&mut world),
        [format!("file://{root}/my%20notes.md")],
        "a markdown path opens with the system until the app shows markdown"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_session_on_an_outpost_links_its_urls_but_not_its_paths() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(dir.path().join("here.go"), "").unwrap();
    let mut world = World::start(in_directory(dir.path().to_str().unwrap(), Some("box")));
    world.open_desktop();
    output(&world, 1, b"here.go https://example.test\r\n");
    world.settle();

    hover(&mut world, 2, 0);
    assert_eq!(underlines(&world), []);
    hover(&mut world, 12, 0);
    assert_eq!(underlines(&world), [(0, [8, 28])]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_file_link_on_an_outpost_stays_shut_and_says_why() {
    let dir = tempfile::tempdir().unwrap();
    let mut world = World::start(in_directory(dir.path().to_str().unwrap(), Some("box")));
    world.open_desktop();
    output(
        &world,
        1,
        b"\x1b]8;;file:///etc/hosts\x1b\\hosts\x1b]8;;\x1b\\ file:///etc/passwd\r\n",
    );
    world.settle();

    accelerator(&mut world, true);
    click(&mut world, 1, 0);
    click(&mut world, 10, 0);
    accelerator(&mut world, false);
    assert_eq!(opened(&mut world), Vec::<String>::new());
    let notice = world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name);
    assert_eq!(
        notice.as_deref(),
        Some("/etc/passwd is on box; opening files on an outpost isn't supported yet")
    );
    world.advance(Duration::from_secs(6));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_url_after_wide_characters_underlines_the_columns_it_draws_in() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, "日本 https://x.test/日\r\n".as_bytes());
    world.settle();

    hover(&mut world, 8, 0);
    assert_eq!(underlines(&world), [(0, [5, 22])]);
    accelerator(&mut world, true);
    click(&mut world, 8, 0);
    assert_eq!(opened(&mut world), ["https://x.test/日"]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn automation_holds_the_accelerator_for_a_press_that_opens_a_link_and_lets_it_go() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"https://example.test/held\r\n");
    world.settle();
    let terminal = json!({"key": "terminal.r1"});
    let on_link = json!({"target": terminal, "at": [0.05, 0.01]});

    world.automate("hold", json!({"mods": "mod"})).unwrap();
    world.automate("press", on_link.clone()).unwrap();
    assert_eq!(opened(&mut world), ["https://example.test/held"]);

    world.advance(Duration::from_secs(1));
    world.automate("hold", json!({"mods": ""})).unwrap();
    world.automate("press", on_link).unwrap();
    assert_eq!(opened(&mut world), Vec::<String>::new());
    assert_eq!(
        world
            .automate("hold", json!({"mods": "mod+meta"}))
            .unwrap_err(),
        "hold: \"mod+meta\": no modifier \"meta\"; modifiers: mod, ctrl, alt, shift, super"
    );
    assert!(world.runtime_is_at_rest());
}
