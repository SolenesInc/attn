use attn_protocol::AttachResultMessage;
use attn_term::{FONT_SIZE, Fonts, SNAPSHOT_FORMAT};
use attn_testkit::{World, message};
use rune::{Key, KeyEvent, Mods, PhysicalInput, WheelEvent};
use serde_json::{Value, json};

use super::terminal::{
    attached, cell, inputs, one_terminal, output, pane, pointed, showing, snapshot_of, typed,
};

/// The terminal's copy key: ⌘C on macOS, Ctrl+Shift+C elsewhere, where Ctrl+C is the shell's.
pub(crate) const COPY: &str = if cfg!(target_os = "macos") {
    "mod+c"
} else {
    "mod+shift+c"
};

/// The copy-command key, which copies a selection as Markdown: ⇧⌘C on macOS, Ctrl+Alt+C
/// elsewhere.
pub(crate) const COPY_COMMAND: &str = if cfg!(target_os = "macos") {
    "mod+shift+c"
} else {
    "ctrl+alt+c"
};

pub(crate) fn view(world: &mut World) -> Value {
    world.terminal_view("r1").expect("r1 is drawn")
}

fn washes(world: &mut World) -> Vec<(u64, [u64; 2])> {
    view(world)["overlays"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|o| o["kind"] == "Wash")
        .map(|o| {
            let cols = o["cols"].as_array().unwrap();
            (
                o["row"].as_u64().unwrap(),
                [cols[0].as_u64().unwrap(), cols[1].as_u64().unwrap()],
            )
        })
        .collect()
}

pub(crate) fn copied(world: &mut World) -> Option<String> {
    world.clipboard.text().map(|t| t.to_string())
}

/// Turns the wheel `rows` rows over terminal r1, positive toward the history.
pub(crate) fn wheel(world: &mut World, rows: f32) {
    let at = cell(world, 1, 1);
    let row = Fonts::default().metrics(FONT_SIZE).cell_h;
    world.gesture(|app| {
        app.runtime
            .wheel_event(WheelEvent::new(0.0, rows * row).at(at.0, at.1))
    });
}

fn drag(world: &mut World, from: (u16, u16), to: (u16, u16)) {
    let (a, b) = (cell(world, from.0, from.1), cell(world, to.0, to.1));
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });
}

fn numbered(from: u32, to: u32) -> Vec<u8> {
    (from..=to)
        .map(|n| format!("line-{n}\r\n"))
        .collect::<String>()
        .into_bytes()
}

#[test]
fn the_wheel_scrolls_the_view_through_the_history_and_a_scrolled_view_keeps_its_rows() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 200));
    world.settle();
    let bottom = world.terminal_text("r1");
    assert_eq!(view(&mut world)["scrolled_back"], 0);
    let rows = world.terminal_size("r1").1;
    assert_eq!(view(&mut world)["cursor"], json!([0, rows - 1]));

    wheel(&mut world, 10.0);
    assert_eq!(view(&mut world)["scrolled_back"], 10);
    assert_eq!(
        view(&mut world)["cursor"],
        Value::Null,
        "the cursor's row is below the view"
    );
    wheel(&mut world, -5.0);
    assert_eq!(view(&mut world)["cursor"], Value::Null);
    wheel(&mut world, 5.0);
    let scrolled = world.terminal_text("r1");
    assert_eq!(scrolled[10], bottom[0], "the view moved ten rows back");

    output(&world, 2, &numbered(201, 205));
    world.settle();
    assert_eq!(
        world.terminal_text("r1"),
        scrolled,
        "output leaves a scrolled-back view on its rows"
    );
    assert_eq!(view(&mut world)["scrolled_back"], 15);

    wheel(&mut world, -100.0);
    assert_eq!(view(&mut world)["scrolled_back"], 0);
    output(&world, 3, b"line-206\r\n");
    world.settle();
    let rows = world.terminal_text("r1");
    assert!(
        rows.iter().any(|r| r == "line-206"),
        "a view at the screen follows output: {rows:?}"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_covered_window_reports_the_scroll_and_the_selection_its_input_made_without_drawing() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 200));
    world.settle();
    world.cover(true);
    let drawn = world.terminal_frames();

    wheel(&mut world, 10.0);
    assert_eq!(view(&mut world)["scrolled_back"], 10);
    drag(&mut world, (0, 2), (5, 3));
    assert!(
        view(&mut world)["overlays"]
            .as_array()
            .unwrap()
            .iter()
            .any(|o| o["kind"] == "Wash"),
        "the drag's selection is over the view"
    );
    assert_eq!(
        world.terminal_frames(),
        drawn,
        "the covered window drew nothing"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_cursor_moves_with_its_row_as_the_view_scrolls() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 100));
    output(&world, 2, b"\x1b[6A");
    world.settle();
    let rows = world.terminal_size("r1").1;
    assert_eq!(view(&mut world)["cursor"], json!([0, rows - 7]));
    wheel(&mut world, 3.0);
    assert_eq!(view(&mut world)["cursor"], json!([0, rows - 4]));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn output_that_clears_the_history_brings_a_scrolled_view_back_to_the_screen() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 100));
    world.settle();
    wheel(&mut world, 10.0);
    output(&world, 2, b"\x1b[3J");
    world.settle();
    assert_eq!(view(&mut world)["scrolled_back"], 0);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_restored_snapshot_keeps_its_history_and_keys_follow_the_modes_set_after_it() {
    let daemon = one_terminal();
    let history: Vec<u8> = (1..=40)
        .map(|n| format!("row-{n:04} tail\r\n"))
        .collect::<String>()
        .into_bytes();
    daemon.on("attach_session", move |request| {
        message(AttachResultMessage {
            id: request["id"].as_str().unwrap().into(),
            last_seq: Some(10),
            snapshot: Some(snapshot_of(&history, SNAPSHOT_FORMAT)),
            ..attached()
        })
    });
    let mut world = World::start(daemon);
    world.open_desktop();
    assert_eq!(inputs(&world), []);
    wheel(&mut world, 100_000.0);
    assert_eq!(world.terminal_text("r1")[0], "row-0001 tail");

    output(&world, 11, b"m\x1b[?1h");
    world.settle();
    world.gesture(|app| {
        app.key(KeyEvent {
            key: Key::Up,
            mods: Mods::default(),
        })
    });
    assert_eq!(inputs(&world), [typed("\x1bOA")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_history_keeps_far_more_than_libghosttys_default() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 5000));
    world.settle();
    wheel(&mut world, 100_000.0);
    assert_eq!(world.terminal_text("r1")[0], "line-1");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_drag_selects_washes_and_copies_its_text_and_the_copy_key_copies_it_again() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"alpha beta\r\n  gamma delta\r\n");
    world.settle();
    let (a, b) = (cell(&world, 0, 0), cell(&world, 6, 1));
    world.gesture(|app| app.runtime.pointer_down(a.0, a.1));
    let commits = world.runtime().stats.commits;
    world.gesture(|app| {
        app.runtime.pointer_move(b.0, b.1);
        app.runtime.pointer_up(b.0, b.1);
    });

    assert_eq!(copied(&mut world).as_deref(), Some("alpha beta\ngamma"));
    let cols = u64::from(world.terminal_size("r1").0);
    assert_eq!(washes(&mut world), [(0, [0, cols]), (1, [0, 7])]);
    assert_eq!(view(&mut world)["copies"], "alpha beta\ngamma");
    assert_eq!(
        world.runtime().stats.commits,
        commits,
        "dragging a selection runs no view"
    );

    world.clipboard.set_text("something else");
    world.gesture(|app| app.press(COPY));
    assert_eq!(copied(&mut world).as_deref(), Some("alpha beta\ngamma"));
    assert_eq!(inputs(&world), [], "the copy key reaches no program");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn copied_lines_lose_the_indent_they_share() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"    one\r\n      two\r\n    three\r\n");
    world.settle();
    drag(&mut world, (0, 0), (20, 2));
    assert_eq!(copied(&mut world).as_deref(), Some("one\n  two\nthree"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_copy_command_key_copies_a_selection_as_markdown_with_its_styles() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let cols = usize::from(world.terminal_size("r1").0);
    let filler = "w".repeat(cols - 1);
    let styled = format!(
        "Use \x1b[32mattn\x1b[0m and \x1b[1mbold\x1b[0m and \x1b[3mit\x1b[0m\r\n{filler} \x1b[1mtail\x1b[0m\r\n"
    );
    output(&world, 1, styled.as_bytes());
    world.settle();
    drag(&mut world, (0, 0), (10, 2));
    assert_eq!(
        copied(&mut world),
        Some(format!("Use attn and bold and it\n{filler}\ntail")),
        "plain copy keeps the wrapped rows apart"
    );

    world.gesture(|app| app.press(COPY_COMMAND));
    assert_eq!(
        copied(&mut world),
        Some(format!(
            "Use `attn` and **bold** and *it*\n{filler} **tail**"
        ))
    );
    assert_eq!(
        inputs(&world),
        [],
        "the copy-command key reaches no program"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_click_that_does_not_drag_clears_the_selection_and_copies_nothing() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"alpha beta\r\n");
    world.settle();
    drag(&mut world, (0, 0), (4, 0));
    let a = cell(&world, 2, 0);
    world.clipboard.set_text("kept");
    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_up(a.0, a.1);
    });

    assert_eq!(washes(&mut world), []);
    assert_eq!(copied(&mut world).as_deref(), Some("kept"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_double_click_after_a_wide_character_selects_the_word_under_the_pointer() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, "😀hello world\r\n".as_bytes());
    world.settle();
    for col in [2, 6] {
        let at = cell(&world, col, 0);
        world.gesture(|app| {
            for _ in 0..2 {
                app.runtime.pointer_down(at.0, at.1);
                app.runtime.pointer_up(at.0, at.1);
            }
        });
        assert_eq!(copied(&mut world).as_deref(), Some("hello"), "column {col}");
        assert_eq!(washes(&mut world), [(0, [2, 7])], "column {col}");
        world.clipboard.set_text("");
    }
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_double_click_selects_the_word_and_a_triple_click_the_row() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"git push --force-with-lease origin\r\n");
    world.settle();
    let a = cell(&world, 12, 0);
    world.gesture(|app| {
        for _ in 0..2 {
            app.runtime.pointer_down(a.0, a.1);
            app.runtime.pointer_up(a.0, a.1);
        }
    });
    assert_eq!(copied(&mut world).as_deref(), Some("--force-with-lease"));
    assert_eq!(washes(&mut world), [(0, [9, 27])]);

    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_up(a.0, a.1);
    });
    assert_eq!(
        copied(&mut world).as_deref(),
        Some("git push --force-with-lease origin")
    );

    world.gesture(|app| {
        app.runtime.pointer_down(a.0, a.1);
        app.runtime.pointer_up(a.0, a.1);
    });
    assert_eq!(washes(&mut world), [], "a fourth click starts over");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_program_that_tracks_the_mouse_hears_the_drag_unless_alt_holds_it_for_a_selection() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"alpha beta\r\n\x1b[?1000h\x1b[?1006h");
    world.settle();
    drag(&mut world, (0, 0), (4, 0));
    assert_eq!(washes(&mut world), []);
    assert_eq!(copied(&mut world), None);
    assert_eq!(
        inputs(&world),
        [pointed("\x1b[<0;1;1M"), pointed("\x1b[<3;5;1m")]
    );

    world.gesture(|app| {
        app.runtime.physical_input(PhysicalInput::Modifiers(Mods {
            alt: true,
            ..Mods::default()
        }))
    });
    drag(&mut world, (0, 0), (4, 0));
    assert_eq!(copied(&mut world).as_deref(), Some("alpha"));
    assert_eq!(inputs(&world).len(), 2, "the program heard nothing more");
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_selection_stays_on_its_rows_as_the_view_scrolls() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, &numbered(1, 100));
    world.settle();
    drag(&mut world, (0, 2), (6, 2));
    assert_eq!(washes(&mut world), [(2, [0, 7])]);
    let selected = copied(&mut world).unwrap();

    wheel(&mut world, 3.0);
    assert_eq!(washes(&mut world), [(5, [0, 7])]);
    assert_eq!(world.terminal_text("r1")[5], selected);

    let rows = world.terminal_text("r1").len() as f32;
    wheel(&mut world, rows);
    assert_eq!(
        washes(&mut world),
        [],
        "a row scrolled out of view keeps no wash at the edge"
    );
    wheel(&mut world, -rows);
    assert_eq!(washes(&mut world), [(5, [0, 7])]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_selection_on_a_programs_screen_follows_its_text_when_the_wheel_scrolls_the_program() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(
        &world,
        1,
        b"\x1b[?1049h\x1b[Hheading\r\nselected first\r\nfooter",
    );
    world.settle();
    drag(&mut world, (0, 1), (13, 1));
    assert_eq!(copied(&mut world).as_deref(), Some("selected first"));

    wheel(&mut world, 1.0);
    assert_eq!(inputs(&world), [typed("\x1b[A")]);
    output(
        &world,
        2,
        b"\x1b[2J\x1b[Holder\r\nheading\r\nselected first\r\nfooter",
    );
    world.settle();
    assert_eq!(washes(&mut world), [(2, [0, 14])]);

    output(&world, 3, b"\x1b[2J\x1b[Hsomething else");
    world.settle();
    assert_eq!(washes(&mut world), [], "the wash goes when its text does");
    world.clipboard.set_text("other");
    world.gesture(|app| app.press(COPY));
    assert_eq!(copied(&mut world).as_deref(), Some("selected first"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn the_copy_key_with_nothing_selected_never_reaches_the_program() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"alpha\r\n\x1b[>1u");
    world.settle();
    world.clipboard.set_text("kept");
    world.gesture(|app| app.press(COPY));
    assert_eq!(inputs(&world), []);
    assert_eq!(copied(&mut world).as_deref(), Some("kept"));
    assert!(world.runtime_is_at_rest());
}

#[cfg(not(target_os = "macos"))]
#[test]
fn ctrl_shift_v_pastes_into_the_terminal_on_linux() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    world.clipboard.set_text("echo pasted\nls");
    world.gesture(|app| app.press("mod+shift+v"));
    assert_eq!(inputs(&world), [typed("echo pasted\rls")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_view_with_nothing_over_it_reports_no_overlays() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    assert_eq!(
        view(&mut world),
        json!({"cursor": [0, 0], "overlays": [], "images": [], "scrolled_back": 0, "copies": null, "block": null, "annotations": null})
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_drag_that_starts_on_the_tile_header_selects_nothing_in_the_terminal() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"alpha beta\r\n");
    world.settle();
    let (slot, _) = world.runtime().presented("terminal.r1").unwrap();
    let header = (slot[0] + 4.0, slot[1] - 4.0);
    let inside = cell(&world, 6, 0);
    world.gesture(|app| {
        app.runtime.pointer_down(header.0, header.1);
        app.runtime.pointer_move(inside.0, inside.1);
        app.runtime.pointer_up(inside.0, inside.1);
    });
    assert_eq!(washes(&mut world), []);
    assert_eq!(copied(&mut world), None);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_divider_drag_that_ends_in_the_terminal_selects_nothing_in_it() {
    let panes = vec![pane("t1", "r1"), pane("t2", "r2")];
    let mut world =
        World::start(attn_testkit::Daemon::scripted().state(showing(panes, "daemon-1")));
    world.resize((1600.0, 1000.0));
    world.open_desktop();
    output(&world, 1, b"alpha beta\r\n");
    world.settle();
    let divider = world
        .runtime()
        .elements()
        .into_iter()
        .find_map(|e| e.key.filter(|k| k.starts_with("divider.")))
        .expect("the two terminals have a divider between them");
    let (strip, _) = world.runtime().presented(&divider).unwrap();
    let over_the_terminal = (strip[0] + 1.0, strip[1] + strip[3] / 2.0);
    let inside = cell(&world, 6, 0);
    world.gesture(|app| {
        app.runtime
            .pointer_down(over_the_terminal.0, over_the_terminal.1);
        app.runtime.pointer_move(inside.0, inside.1);
        app.runtime.pointer_up(inside.0, inside.1);
    });
    assert_eq!(washes(&mut world), []);
    assert_eq!(copied(&mut world), None);
}

#[test]
fn a_program_writes_the_clipboard_with_osc_52_and_a_query_reads_nothing() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"\x1b]52;c;Y29w");
    output(&world, 2, b"aWVk\x07after");
    world.settle();
    assert_eq!(copied(&mut world).as_deref(), Some("copied"));
    assert_eq!(world.terminal_text("r1")[0], "after");

    world.clipboard.set_text("kept");
    output(&world, 3, b"\x1b]52;c;?\x07");
    world.settle();
    assert_eq!(copied(&mut world).as_deref(), Some("kept"));
    assert_eq!(
        inputs(&world),
        [],
        "the terminal answers no clipboard query"
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn pasting_an_image_sends_the_ctrl_v_byte_for_the_agent_to_read_it_itself() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    output(&world, 1, b"\x1b[?2004h");
    world.settle();
    let png: Vec<u8> = [
        &b"\x89PNG\r\n\x1a\n\0\0\0\rIHDR"[..],
        &1u32.to_be_bytes(),
        &1u32.to_be_bytes(),
        &[0u8; 8],
    ]
    .concat();
    let image = rune::ClipboardImage::from_png(png).expect("a PNG header");
    world.gesture(|app| {
        app.runtime
            .paste_content(rune::ClipboardContent::new("ignored").with_image(image))
    });
    assert_eq!(inputs(&world), [typed("\x16")]);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_selection_stays_on_its_text_when_the_oldest_history_is_dropped() {
    let mut world = World::start(one_terminal());
    world.open_desktop();
    let line = |n: u32| format!("line-{n:07}-{}\r\n", "x".repeat(60));
    let fill = |from: u32, to: u32| (from..to).map(line).collect::<String>().into_bytes();
    let full = 8 * 1024 * 1024 / 70 + 10_000;
    for (seq, from) in (0..full).step_by(10_000).enumerate() {
        output(
            &world,
            seq as u32 + 1,
            &fill(from, (from + 10_000).min(full)),
        );
    }
    world.settle();
    drag(&mut world, (0, 10), (11, 10));
    assert_eq!(washes(&mut world), [(10, [0, 12])]);
    let selected = copied(&mut world).expect("the drag copies");
    output(&world, 10_000, &fill(full, full + 600));
    world.settle();
    wheel(&mut world, 600.0);
    let rows = world.terminal_text("r1");
    assert!(
        rows[10].starts_with(&selected),
        "the view is back on the selected row: {:?}",
        &rows[8..12]
    );
    let washed: Vec<&str> = washes(&mut world)
        .iter()
        .map(|(row, cols)| &rows[*row as usize][cols[0] as usize..cols[1] as usize])
        .collect();
    assert_eq!(
        washed,
        [selected.as_str()],
        "the wash stays on the text it selected"
    );
    assert!(world.runtime_is_at_rest());
}
