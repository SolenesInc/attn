//! The rules the dialog block and the controls and blocks it carries hold, read from the
//! floating gallery's stories.

use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
const SCALE: f32 = 2.0;
const INSET: f32 = 24.0;
const COMPACT_INSET: f32 = 8.0;
/// The inset of the column the stories stack their controls in.
const STAGE_INSET: f32 = 24.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("floating", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt.frame(1.0);
    rt
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

fn left(e: &ElementInfo) -> f32 {
    e.rect[0]
}

fn right(e: &ElementInfo) -> f32 {
    e.rect[0] + e.rect[2]
}

fn bottom(e: &ElementInfo) -> f32 {
    e.rect[1] + e.rect[3]
}

fn within(rt: &Runtime<()>, outer: &ElementInfo) -> Vec<ElementInfo> {
    rt.elements()
        .into_iter()
        .filter(|e| e.path.starts_with(&outer.path) && e.path != outer.path)
        .filter(|e| e.rect[2] > 0.0 && e.rect[3] > 0.0)
        .collect()
}

const ASKING: [&str; 5] = ["Prompt", "Form", "Failed", "Long", "Smallest long"];

#[test]
fn a_dialogs_title_lead_body_and_actions_keep_its_inset() {
    for variant in ASKING {
        let rt = story("Library/Dialog", variant);
        let dialog = element(&rt, "story-dialog");
        let edge = left(&dialog) + INSET;
        let mut lined = vec!["title", "lead"];
        match variant {
            "Form" | "Failed" => lined.push("story.name.field"),
            "Long" | "Smallest long" => lined.push("story.error"),
            _ => {}
        }
        for key in lined {
            let at = left(&element(&rt, key));
            assert!(
                (at - edge).abs() < 0.5,
                "{variant}: {key} starts at {at}, the dialog's inset at {edge}"
            );
        }
        let confirm = element(&rt, "story-dialog.confirm");
        assert!(
            (right(&confirm) - (right(&dialog) - INSET)).abs() < 0.5,
            "{variant}: the action ends at {}, the dialog's inset at {}",
            right(&confirm),
            right(&dialog) - INSET
        );
        assert!(
            (bottom(&confirm) - (bottom(&dialog) - INSET)).abs() < 0.5,
            "{variant}: the action's foot is at {}, the dialog's inset at {}",
            bottom(&confirm),
            bottom(&dialog) - INSET
        );
    }
}

#[test]
fn a_dialog_draws_its_close_only_when_it_has_no_actions() {
    for variant in ASKING {
        let rt = story("Library/Dialog", variant);
        assert!(
            rt.playground_element("close").is_none(),
            "{variant}: a dialog with Cancel draws a close too"
        );
    }
    let rt = story("Library/Dialog", "Sheet");
    let close = element(&rt, "close");
    let dialog = element(&rt, "story-dialog");
    assert!(
        (right(&close) - (right(&dialog) - INSET)).abs() < 0.5,
        "the close ends at {}, the dialog's inset at {}",
        right(&close),
        right(&dialog) - INSET
    );
}

#[test]
fn nothing_a_dialog_draws_runs_past_its_sides_down_to_the_smallest_window() {
    for variant in ASKING.into_iter().chain(["Sheet"]) {
        let rt = story("Library/Dialog", variant);
        let dialog = element(&rt, "story-dialog");
        for e in within(&rt, &dialog) {
            assert!(
                left(&e) >= left(&dialog) - 0.5 && right(&e) <= right(&dialog) + 0.5,
                "{variant}: {} runs from {} to {}, past the dialog's {}..{}",
                e.path,
                left(&e),
                right(&e),
                left(&dialog),
                right(&dialog)
            );
        }
    }
    let rt = story("Library/Dialog", "Smallest long");
    let dialog = element(&rt, "story-dialog");
    let window = element(&rt, "stage.window");
    assert!(
        right(&dialog) <= right(&window) - 16.0 + 0.5
            && left(&dialog) >= left(&window) + 16.0 - 0.5,
        "the dialog runs {}..{} in a window of {}..{}",
        left(&dialog),
        right(&dialog),
        left(&window),
        right(&window)
    );
}

#[test]
fn a_sheets_columns_start_level_and_stack_only_when_they_run_out_of_room() {
    let rt = story("Library/Dialog", "Sheet");
    let first = element(&rt, "story.Sessions");
    let second = element(&rt, "story.Panes");
    assert!(
        (first.rect[1] - second.rect[1]).abs() < 0.5,
        "the columns start at {} and {}",
        first.rect[1],
        second.rect[1]
    );
    assert!(
        (first.rect[2] - second.rect[2]).abs() < 0.5,
        "the columns are {} and {} wide",
        first.rect[2],
        second.rect[2]
    );
    assert!(left(&second) >= right(&first) + 24.0);
    let rt = story("Library/Dialog", "Sheet smallest");
    let first = element(&rt, "story.Sessions");
    let second = element(&rt, "story.Panes");
    assert!(
        second.rect[1] >= bottom(&first) - 0.5 && (left(&second) - left(&first)).abs() < 0.5,
        "in the smallest window the columns go one under another: {:?} then {:?}",
        first.rect,
        second.rect
    );
}

#[test]
fn a_checks_words_sit_on_the_line_of_the_words_round_it_and_its_box_on_their_first_line() {
    for variant in ["Rest", "Disabled", "Smallest long"] {
        let rt = story("Library/Check", variant);
        for key in ["check.off", "check.on", "check.long"] {
            let check = element(&rt, key);
            let column = rt.elements()[check.parent.expect("the check's column")].clone();
            assert!(
                (left(&check) - (left(&column) + STAGE_INSET - COMPACT_INSET)).abs() < 0.5,
                "{variant}: {key}'s box hangs out to {}, its column's words start at {}",
                left(&check),
                left(&column) + STAGE_INSET
            );
        }
    }
}

#[test]
fn a_composers_words_wrap_inside_it_at_the_compose_line() {
    let rest = story("Library/Composer", "Rest");
    let one_line = element(&rest, "composer").rect[3];
    assert!(
        (one_line - 27.0).abs() < 0.5,
        "an empty composer stands one Compose line high, 27, not {one_line}"
    );
    for variant in ["Long", "Smallest long"] {
        let rt = story("Library/Composer", variant);
        let field = element(&rt, "composer");
        assert!(
            field.rect[3] > 2.0 * one_line - 0.5,
            "{variant}: a long message wraps onto more lines, {}",
            field.rect[3]
        );
        let column = rt.elements()[field.parent.expect("the composer's column")].clone();
        assert!(
            right(&field) <= right(&column) + 0.5,
            "{variant}: the composer runs to {}, past its column's {}",
            right(&field),
            right(&column)
        );
    }
}

#[test]
fn a_dialog_blurs_and_dims_the_whole_window_behind_it() {
    for variant in ASKING {
        let rt = story("Library/Dialog", variant);
        let scrim = element(&rt, "story-scrim");
        let stage = element(&rt, "story-layer");
        assert!(
            scrim.backdrop_blur > 0.0,
            "{variant}: the window behind the dialog is sharp"
        );
        assert!(
            scrim.fill.a > 0.0,
            "{variant}: the window behind the dialog is not dimmed"
        );
        assert_eq!(
            scrim.rect, stage.rect,
            "{variant}: the backdrop covers less than the dialog's layer"
        );
    }
}

/// The smallest window at 150% text: 800 by 600 points shown at 1.5.
const SMALLEST_LARGE_TEXT: (f32, f32) = (800.0 / 1.5, 600.0 / 1.5);

/// A dialog of each size, and how many lines its body says, from one to ten windows' worth.
#[derive(Clone, Copy)]
struct Asking {
    size: attn_ui::blocks::DialogSize,
    lines: usize,
}

thread_local! {
    static ASKED: std::cell::Cell<Option<Asking>> = const { std::cell::Cell::new(None) };
}

fn asking(asking: &Asking, _: &mut Cx<Asking>) -> El {
    use attn_ui::blocks::{Actions, Dialog, Prose};
    use attn_ui::system::Button;
    let t = attn_ui::colors::scheme_for(None).colors(true).ui;
    let mut layer = Vec::new();
    Dialog::new("rule", "Restart shared Codex now?")
        .size(asking.size)
        .on_close(|_: &mut Asking| {})
        .push_all((0..asking.lines).map(|i| {
            Prose::lead(
                format!("rule.line.{i}"),
                "Every Codex tile in this profile goes offline.",
            )
            .build(&t)
        }))
        .actions(
            Actions::new("rule.actions")
                .then_end()
                .push(Button::new("Restart now").build(&t).key("rule.confirm")),
        )
        .build(&t, &mut layer);
    rune::col().w_full().h_full().children(layer)
}

fn asked(case: Asking) -> Runtime<Asking> {
    ASKED.with(|a| a.set(Some(case)));
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new(
            "floating",
            SMALLEST_LARGE_TEXT,
            || ASKED.with(|a| a.get()).expect("a case"),
            asking,
        )
    })
    .headless_runtime(SMALLEST_LARGE_TEXT, Env::new(true, 1.0));
    rt.frame(0.0);
    rt.frame(1.0);
    rt
}

#[test]
fn a_dialogs_actions_stay_whole_in_the_window_and_its_body_scrolls_however_long() {
    use attn_ui::blocks::DialogSize;
    let (w, h) = SMALLEST_LARGE_TEXT;
    for size in [
        DialogSize::Prompt,
        DialogSize::Form,
        DialogSize::Sheet,
        DialogSize::Tour,
    ] {
        for lines in [1, 4, 12, 24, 120] {
            let rt = asked(Asking { size, lines });
            let all = rt.elements();
            let find = |key: &str| {
                all.iter()
                    .find(|e| e.key.as_deref() == Some(key))
                    .unwrap_or_else(|| panic!("no {key}"))
                    .clone()
            };
            let confirm = find("rule.confirm");
            let [x, y, cw, ch] = confirm.rect;
            assert!(
                x >= 0.0 && y >= 0.0 && x + cw <= w && y + ch <= h,
                "{size:?}, {lines} lines: the action at {:?} is not whole in the {w}x{h} window",
                confirm.rect
            );
            assert_eq!(
                confirm.visible, confirm.rect,
                "{size:?}, {lines} lines: the action is cut"
            );
            let body = find("rule.body");
            let last = find(&format!("rule.line.{}", lines - 1));
            let shown = last.visible[3] >= last.rect[3] - 0.5;
            let fits = bottom(&last) <= bottom(&body) + 0.5;
            assert_eq!(
                shown, fits,
                "{size:?}, {lines} lines: the last line is shown {shown} though it fits {fits}"
            );
            assert!(
                bottom(&body) <= y,
                "{size:?}, {lines} lines: the body ends at {} below the action at {y}",
                bottom(&body)
            );
        }
    }
}

thread_local! {
    static OFFERED: std::cell::Cell<usize> = const { std::cell::Cell::new(0) };
}

fn offering(lines: &usize, _: &mut Cx<usize>) -> El {
    use attn_ui::blocks::{Actions, Popover, Prose};
    use attn_ui::system::Button;
    use rune_kit::Placement;
    use rune_kit::popover::anchored;
    let t = attn_ui::colors::scheme_for(None).colors(true).ui;
    let popover = Popover::new("rule", "Set up Captain")
        .push_all((0..*lines).map(|i| {
            Prose::lead(format!("rule.line.{i}"), "Where the Chief keeps its notes.").build(&t)
        }))
        .actions(
            Actions::new("rule.actions")
                .then_end()
                .push(Button::new("Start Captain").build(&t).key("rule.confirm")),
        )
        .build(&t);
    let row = el().key("rule.row").absolute(8.0, 40.0).size(200.0, 32.0);
    let layer = vec![anchored(
        attn_ui::kit(&t),
        popover,
        "rule.row",
        Placement::RightStart,
        10.0,
    )];
    rune_kit::root(rune::col().w_full().h_full().child(row), layer)
}

#[test]
fn a_popovers_actions_stay_whole_in_the_window_and_its_body_scrolls_however_long() {
    let (w, h) = SMALLEST_LARGE_TEXT;
    for lines in [1, 4, 12, 24, 120] {
        OFFERED.with(|o| o.set(lines));
        let mut rt = App::new(AppSpec {
            fonts: attn_ui::FONTS,
            families: attn_ui::FAMILIES,
            ..AppSpec::new(
                "floating",
                SMALLEST_LARGE_TEXT,
                || OFFERED.with(|o| o.get()),
                offering,
            )
        })
        .headless_runtime(SMALLEST_LARGE_TEXT, Env::new(true, 1.0));
        rt.frame(0.0);
        for _ in 0..30 {
            rt.frame(1.0 / 30.0);
        }
        let all = rt.elements();
        let confirm = all
            .iter()
            .find(|e| e.key.as_deref() == Some("rule.confirm"))
            .expect("the action is drawn");
        let [x, y, cw, ch] = confirm.rect;
        assert!(
            x >= 0.0 && y >= 0.0 && x + cw <= w && y + ch <= h,
            "{lines} lines: the action at {:?} is not whole in the {w}x{h} window",
            confirm.rect
        );
        assert_eq!(
            confirm.visible, confirm.rect,
            "{lines} lines: the action is cut"
        );
    }
}
