use attn_protocol::{AttachBlock, AttachResultMessage, AttachSnapshot, PtyResizedMessage};
use attn_term::{SNAPSHOT_FORMAT, Vt};
use attn_testkit::{Answer, World};
use base64::Engine;
use rune::PointerButton;
use serde_json::Value;

use super::terminal::{attached, cell, inputs, live_with_modes, one_terminal, output};
use super::terminal_view::{COPY, COPY_COMMAND, copied, view};

/// A shell's prompt, its command line and its output, marked with OSC 133 as an integrated
/// shell marks them, ending on the next prompt.
pub(crate) fn ran(command: &str, output: &str) -> Vec<u8> {
    let url = command.replace(' ', "%20");
    format!(
        "\x1b]133;A\x07$ \x1b]133;B\x07{command}\r\n\x1b]133;C;cmdline_url={url}\x07{output}\x1b]133;D;0\x07\x1b]133;A\x07$ "
    )
    .into_bytes()
}

fn outlined_rows(world: &mut World) -> Vec<(u64, String)> {
    view(world)["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["kind"].as_str().unwrap().starts_with("Outline"))
        .map(|o| {
            (
                o["row"].as_u64().unwrap(),
                o["kind"].as_str().unwrap().to_owned(),
            )
        })
        .collect()
}

fn click(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| app.click_at(at));
}

pub(crate) fn right_click(world: &mut World, col: u16, row: u16) {
    let at = cell(world, col, row);
    world.gesture(|app| {
        app.runtime.pointer_move(at.0, at.1);
        app.runtime
            .button_down(at.0, at.1, PointerButton::Secondary);
        app.runtime.button_up(at.0, at.1, PointerButton::Secondary);
    });
}

/// The open context menu's items, as `(id, enabled)`.
pub(crate) fn menu(world: &World) -> Vec<(String, bool)> {
    let menu = world.runtime().popup_menu().expect("a menu is open");
    [
        "copy",
        "copy-command",
        "copy-output",
        "paste",
        "filter-block",
        "scroll-block-top",
        "scroll-block-bottom",
    ]
    .into_iter()
    .filter_map(|id| menu.find(id))
    .map(|item| (item.id().to_owned(), item.is_enabled()))
    .collect()
}

pub(crate) fn pick(world: &mut World, id: &str) {
    world.gesture(|app| app.runtime.resolve_popup(Some(id)).unwrap());
}

#[test]
fn a_click_selects_the_command_block_under_it_and_the_copy_keys_copy_it_whole_or_its_command() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &ran("echo hi", "hi\r\nthere\r\n"));
    world.settle();

    click(&mut world, 1, 2);
    assert_eq!(
        outlined_rows(&mut world),
        [
            (0, "Outline { top: true, bottom: false }".into()),
            (1, "Outline { top: false, bottom: false }".into()),
            (2, "Outline { top: false, bottom: true }".into()),
        ]
    );
    world.gesture(|app| app.press(COPY));
    assert_eq!(copied(&mut world).as_deref(), Some("echo hi\nhi\nthere"));
    world.gesture(|app| app.press(COPY_COMMAND));
    assert_eq!(copied(&mut world).as_deref(), Some("echo hi"));
    assert_eq!(inputs(&world), [], "the copy keys reach no program");

    click(&mut world, 1, 3);
    assert_eq!(
        outlined_rows(&mut world),
        [],
        "a click past the blocks selects none"
    );
    assert_eq!(view(&mut world)["block"], Value::Null);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_click_on_the_command_line_selects_the_command_the_shell_sent() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &ran("ls -la", "total 0\r\n"));
    world.settle();

    click(&mut world, 4, 0);
    assert_eq!(view(&mut world)["copies"], "ls -la");
    world.gesture(|app| app.press(COPY));
    assert_eq!(copied(&mut world).as_deref(), Some("ls -la"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_copy_command_key_without_a_block_keeps_the_clipboard_and_passes_the_key_on() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"plain\r\n");
    world.settle();

    world.clipboard.set_text("kept");
    world.gesture(|app| app.press(COPY_COMMAND));
    assert_eq!(copied(&mut world).as_deref(), Some("kept"));
    if cfg!(target_os = "macos") {
        assert_eq!(
            inputs(&world),
            [],
            "command chords never reach the program on macOS"
        );
    } else {
        assert_eq!(inputs(&world).len(), 1, "the program hears ctrl+alt+c");
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_context_menu_selects_the_block_under_it_and_copies_or_scrolls_to_its_parts() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let lines: String = (1..=100).map(|n| format!("out-{n}\r\n")).collect();
    output(&world, 1, &ran("seq 100", &lines));
    world.settle();
    let rows = world.terminal_size("r1").1;

    right_click(&mut world, 1, rows - 3);
    assert_eq!(
        menu(&world),
        [
            ("copy".into(), true),
            ("copy-command".into(), true),
            ("copy-output".into(), true),
            ("paste".into(), true),
            ("filter-block".into(), true),
            ("scroll-block-top".into(), true),
            ("scroll-block-bottom".into(), true),
        ]
    );
    assert_ne!(
        view(&mut world)["block"],
        Value::Null,
        "the menu selects the block"
    );
    pick(&mut world, "copy-output");
    assert_eq!(
        copied(&mut world).as_deref(),
        Some(lines.trim_end().replace("\r\n", "\n").as_str())
    );

    right_click(&mut world, 1, rows - 3);
    pick(&mut world, "scroll-block-top");
    assert_eq!(world.terminal_text("r1")[0], "$ seq 100");

    right_click(&mut world, 1, 1);
    pick(&mut world, "scroll-block-bottom");
    let text = world.terminal_text("r1");
    assert_eq!(text[usize::from(rows) - 1], "out-100");

    right_click(&mut world, 1, 1);
    pick(&mut world, "copy");
    assert_eq!(
        copied(&mut world).as_deref(),
        Some(format!("seq 100\n{}", lines.trim_end().replace("\r\n", "\n")).as_str())
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_block_stays_on_its_text_when_the_daemon_makes_the_screen_shorter() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let filler = "filler\r\n".repeat(20);
    let mut stream = filler.into_bytes();
    stream.extend(ran("echo hello", "hello\r\nworld\r\n"));
    output(&world, 1, &stream);
    world.settle();
    let (cols, _) = world.terminal_size("r1");

    world.daemon.emit(PtyResizedMessage {
        id: "r1".into(),
        cols: i64::from(cols) + 20,
        rows: 12,
        ..PtyResizedMessage::new()
    });
    world.settle();
    let rows = world.terminal_text("r1");
    let hello = rows
        .iter()
        .position(|r| r == "hello")
        .expect("the output shows");

    right_click(&mut world, 1, hello as u16);
    pick(&mut world, "copy-output");
    assert_eq!(copied(&mut world).as_deref(), Some("hello\nworld"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_programs_own_screen_has_no_blocks_to_select_or_outline() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &ran("echo hi", "hi\r\n"));
    world.settle();
    click(&mut world, 1, 1);
    assert_ne!(view(&mut world)["block"], Value::Null);

    output(&world, 2, b"\x1b[?1049hediting");
    world.settle();
    assert_eq!(
        outlined_rows(&mut world),
        [],
        "the outline stays with the shell's screen"
    );
    click(&mut world, 6, 0);
    assert_eq!(view(&mut world)["block"], Value::Null);
    right_click(&mut world, 6, 0);
    assert_eq!(menu(&world)[2], ("copy-output".into(), false));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_context_menu_off_the_blocks_offers_only_paste_and_pastes() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"plain\r\n");
    world.settle();

    right_click(&mut world, 1, 0);
    assert_eq!(
        menu(&world),
        [
            ("copy".into(), false),
            ("copy-command".into(), false),
            ("copy-output".into(), false),
            ("paste".into(), true),
            ("filter-block".into(), false),
            ("scroll-block-top".into(), false),
            ("scroll-block-bottom".into(), false),
        ]
    );
    world.clipboard.set_text("pasted");
    pick(&mut world, "paste");
    assert_eq!(inputs(&world), [("pasted".into(), "user".into())]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_program_that_tracks_the_mouse_takes_the_right_button_and_no_menu_opens() {
    let mut world = live_with_modes(b"\x1b[?1000h\x1b[?1006h");
    right_click(&mut world, 1, 1);
    assert!(world.runtime().popup_menu().is_none());
    assert_eq!(
        inputs(&world).len(),
        2,
        "the program hears the press and release"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_restored_screen_brings_back_the_blocks_the_daemon_kept() {
    let daemon = one_terminal();
    daemon.answer_in_turn("attach_session", [Answer::Hold]);
    let mut world = World::start(daemon);
    world.open_desktop();
    let (cols, rows) = world.terminal_size("r1");
    let mut vt = Vt::new(cols, rows);
    vt.write(b"$ make\r\nbuilt\r\n$ ");
    let snapshot = vt.snapshot().expect("a screen at rest encodes");
    let request = world.daemon.sent_of("attach_session")[0].clone();
    world.daemon.reply_to(
        &request,
        AttachResultMessage {
            last_seq: Some(1),
            snapshot: Some(AttachSnapshot {
                blocks: Some(vec![AttachBlock {
                    id: 7,
                    pending: false,
                    prompt_row: 0,
                    input_row: Some(0),
                    input_col: Some(2),
                    output_start_row: Some(1),
                    end_row: Some(2),
                    command: Some("make".into()),
                    exit_code: Some(0),
                }]),
                cols: i64::from(cols),
                rows: i64::from(rows),
                format: Some(SNAPSHOT_FORMAT.into()),
                snapshot_b64: base64::engine::general_purpose::STANDARD.encode(snapshot),
                ..AttachSnapshot::default()
            }),
            ..attached()
        },
    );
    world.settle();

    click(&mut world, 1, 1);
    assert_eq!(view(&mut world)["block"], 7);
    world.gesture(|app| app.press(COPY));
    assert_eq!(copied(&mut world).as_deref(), Some("make\nbuilt"));

    output(&world, 2, &ran("ls", "a\r\n"));
    world.settle();
    click(&mut world, 1, 3);
    assert_eq!(
        view(&mut world)["block"],
        8,
        "the next block takes the next id"
    );
    assert!(world.runtime_is_at_rest());
}
