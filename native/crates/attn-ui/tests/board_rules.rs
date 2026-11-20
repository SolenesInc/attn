//! The rules a board of cards holds, read from the gallery's board story.

use attn_ui::board_gallery::CARD_LEAST;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 1100.0);
const GAP: f32 = 16.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(dark: bool, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("board-rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(dark, 2.0));
    rt.frame(0.0);
    rt.open_playground("Blocks/Board", Some(variant));
    rt.frame(0.0);
    rt
}

fn rect(rt: &Runtime<()>, key: &str) -> [f32; 4] {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
        .rect
}

fn descendants(rt: &Runtime<()>, key: &str) -> Vec<ElementInfo> {
    let outer = rt
        .playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"));
    let all = rt.elements();
    all.iter()
        .filter(|e| {
            std::iter::successors(e.parent, |&p| all[p].parent).any(|p| all[p].path == outer.path)
        })
        .cloned()
        .collect()
}

#[test]
fn the_cards_of_one_row_stand_side_by_side_at_one_height_and_stack_when_two_do_not_fit() {
    for dark in [true, false] {
        let rt = story(dark, "Rest");
        let (a, b) = (rect(&rt, "board.sessions"), rect(&rt, "board.prs"));
        assert_eq!(a[1], b[1], "one row: {a:?} {b:?}");
        assert_eq!(a[3], b[3], "one height: {a:?} {b:?}");
        assert!(
            (b[0] - (a[0] + a[2]) - GAP).abs() < 0.5,
            "{GAP} apart: {a:?} {b:?}"
        );
        assert!(a[3] >= CARD_LEAST.1 && a[2] >= CARD_LEAST.0);

        let rt = story(dark, "Narrow");
        let (a, b) = (rect(&rt, "board.sessions"), rect(&rt, "board.prs"));
        assert_eq!(a[0], b[0], "stacked on one edge: {a:?} {b:?}");
        assert!(
            (b[1] - (a[1] + a[3]) - GAP).abs() < 0.5,
            "{GAP} apart: {a:?} {b:?}"
        );
        assert_eq!(a[2], b[2], "one width: {a:?} {b:?}");
    }
}

#[test]
fn a_cards_heading_and_its_groups_headings_start_on_one_line() {
    for variant in ["Rest", "Long", "Narrow long"] {
        let rt = story(true, variant);
        let card = rect(&rt, "board.sessions.header");
        let heading = descendants(&rt, "board.sessions.header")
            .into_iter()
            .find(|e| e.role == "heading")
            .expect("the card's band holds its heading")
            .rect;
        let group = descendants(&rt, "board.group.head")
            .into_iter()
            .filter(|e| e.text.is_some())
            .map(|e| e.rect[0])
            .fold(f32::MAX, f32::min);
        assert!(
            (heading[0] - group).abs() < 0.5,
            "{variant}: the card's heading at {}, its group's at {group}, in {card:?}",
            heading[0]
        );
    }
}

#[test]
fn nothing_a_board_draws_runs_past_its_sides_at_the_smallest_window() {
    for dark in [true, false] {
        let rt = story(dark, "Smallest long");
        let board = rect(&rt, "board");
        for e in descendants(&rt, "board") {
            let [x, _, w, _] = e.rect;
            if w <= 0.0 {
                continue;
            }
            assert!(
                x >= board[0] - 0.5 && x + w <= board[0] + board[2] + 0.5,
                "{:?} at {:?} runs past the board at {board:?}",
                e.key,
                e.rect
            );
        }
    }
}
