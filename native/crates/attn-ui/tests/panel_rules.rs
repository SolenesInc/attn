//! The rules the blocks of panels and drawers hold, read from the panel gallery's stories.

use attn_ui::panel_gallery::{PANEL_SUBJECTS, PANEL_VARIANTS};
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Key, KeyEvent, Mods, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
const SCALE: f32 = 2.0;
const WIDTHS: [&str; 6] = [
    "Rest",
    "Long",
    "Narrow",
    "Narrow long",
    "Smallest long",
    "Focus",
];
const SLACK: f32 = 0.5;
/// How much more a measured width may say than its box draws: the box's layout and the text's
/// measure round apart by up to a point on some machines' fonts.
const ONE_POINT: f32 = 1.0 + SLACK;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("panel rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

fn variants(story: &str) -> Vec<&'static str> {
    let extra = PANEL_VARIANTS
        .iter()
        .find(|(name, _)| *name == story)
        .map_or(&[][..], |(_, extra)| extra);
    WIDTHS.iter().chain(extra).copied().collect()
}

fn right(r: [f32; 4]) -> f32 {
    r[0] + r[2]
}

fn bottom(r: [f32; 4]) -> f32 {
    r[1] + r[3]
}

fn centre_y(r: [f32; 4]) -> f32 {
    r[1] + r[3] / 2.0
}

fn holds(outer: [f32; 4], inner: [f32; 4]) -> bool {
    inner[0] >= outer[0] - SLACK
        && inner[1] >= outer[1] - SLACK
        && right(inner) <= right(outer) + SLACK
        && bottom(inner) <= bottom(outer) + SLACK
}

fn within(rt: &Runtime<()>, outer: &ElementInfo) -> Vec<ElementInfo> {
    let prefix = format!("{}/", outer.path);
    rt.elements()
        .iter()
        .filter(|e| e.path.starts_with(&prefix) && e.rect[2] > 0.0 && e.rect[3] > 0.0)
        .cloned()
        .collect()
}

#[test]
fn a_sheets_title_line_and_footer_stay_in_view_whatever_its_body_holds() {
    for variant in variants("Panels/Sheet") {
        let rt = story("Panels/Sheet", variant);
        let sheet = element(&rt, "sheet").rect;
        let header = element(&rt, "sheet.header").rect;
        let body = element(&rt, "sheet.body").rect;
        assert!(
            (header[1] - sheet[1]).abs() <= SLACK,
            "{variant}: the title line starts at {} in a sheet from {}",
            header[1],
            sheet[1]
        );
        assert!(
            body[1] >= bottom(header) - SLACK && body[3] > 0.0,
            "{variant}: the body at {body:?} is not under the title line at {header:?}"
        );
        if let Some(notices) = rt.playground_element("sheet.notices") {
            assert!(
                notices.rect[1] >= bottom(header) - SLACK
                    && bottom(notices.rect) <= body[1] + SLACK,
                "{variant}: the notices at {:?} are not between the title line and the body",
                notices.rect
            );
            assert!(
                holds(notices.rect, element(&rt, "sheet.overwrite").rect),
                "{variant}: the notice's controls leave its band"
            );
        }
        if let Some(footer) = rt.playground_element("sheet.actions") {
            assert!(
                (bottom(footer.rect) - bottom(sheet)).abs() <= SLACK,
                "{variant}: the footer ends at {} in a sheet ending at {}",
                bottom(footer.rect),
                bottom(sheet)
            );
            assert!(
                bottom(body) <= footer.rect[1] + SLACK,
                "{variant}: the body runs under the footer"
            );
        }
        let close = element(&rt, "sheet.close").rect;
        assert!(
            right(close) <= right(sheet) + SLACK,
            "{variant}: the close button leaves the sheet at {close:?}"
        );
    }
}

#[test]
fn no_part_of_a_panel_runs_past_its_sides_at_any_width() {
    for &(story_name, subjects) in PANEL_SUBJECTS {
        for variant in variants(story_name) {
            let rt = story(story_name, variant);
            for key in subjects {
                let panel = element(&rt, key);
                for part in within(&rt, &panel) {
                    assert!(
                        part.rect[0] >= panel.rect[0] - SLACK
                            && right(part.rect) <= right(panel.rect) + SLACK,
                        "{story_name} {variant}: {} at {:?} leaves {key} at {:?}",
                        part.path,
                        part.rect,
                        panel.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_controls_box_takes_in_everything_it_draws_at_any_width() {
    for &(story_name, _) in PANEL_SUBJECTS {
        for variant in variants(story_name) {
            let rt = story(story_name, variant);
            for control in rt.elements().iter().filter(|e| e.focusable) {
                for part in within(&rt, control)
                    .into_iter()
                    .filter(|part| part.visible[2] > 0.0 && part.visible[3] > 0.0)
                {
                    assert!(
                        holds(control.rect, part.visible),
                        "{story_name} {variant}: {} draws at {:?}, outside its control {} at {:?}",
                        part.path,
                        part.visible,
                        control.path,
                        control.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_rows_controls_centre_on_its_names_line() {
    for variant in WIDTHS {
        let rt = story("Panels/List", variant);
        for i in 0..3 {
            let main = element(&rt, &format!("list.row{i}.title")).rect;
            for control in [format!("list.run{i}"), format!("list.edit{i}")] {
                let control = element(&rt, &control).rect;
                if control[1] >= bottom(main) - SLACK {
                    continue;
                }
                assert!(
                    (centre_y(control) - centre_y(main)).abs() <= SLACK,
                    "{variant} row {i}: a control centres at {} beside a name whose line centres at {}",
                    centre_y(control),
                    centre_y(main)
                );
            }
        }
    }
}

#[test]
fn what_a_row_says_under_it_starts_on_its_names_edge() {
    for variant in ["Failed", "Rest", "Narrow long", "Smallest long"] {
        let rt = story("Panels/List", variant);
        let Some(error) = rt.playground_element("list.error1") else {
            continue;
        };
        let main = element(&rt, "list.select1");
        let name = within(&rt, &main)
            .into_iter()
            .map(|e| e.rect[0])
            .fold(f32::INFINITY, f32::min);
        let note = element(&rt, "list.note1").rect[0];
        for (what, x) in [("error", error.rect[0]), ("note", note)] {
            assert!(
                (x - name).abs() <= SLACK,
                "{variant}: the {what} starts at {x}, the name at {name}"
            );
        }
    }
}

#[test]
fn a_bare_lists_rows_hang_their_words_on_the_column_s_line() {
    let rt = story("Panels/List", "Bare");
    let line = element(&rt, "list").rect[0];
    for i in 0..3 {
        let row = element(&rt, &format!("list.row{i}"));
        let words = within(&rt, &element(&rt, &format!("list.select{i}")))
            .into_iter()
            .filter(|e| e.role == "label" || e.role == "text")
            .map(|e| e.rect[0])
            .fold(f32::INFINITY, f32::min);
        assert!(
            (words - line).abs() <= SLACK,
            "row {i}: its words start at {words}, the column's line is {line}"
        );
        assert!(
            row.rect[0] < line - SLACK,
            "row {i}: its box starts at {}, not out past the column's line {line}",
            row.rect[0]
        );
    }
}

#[test]
fn a_control_in_an_inspector_sets_its_words_where_a_facts_words_start() {
    for variant in WIDTHS {
        let rt = story("Panels/Inspector", variant);
        let starts = |key: &str| -> Vec<f32> {
            within(&rt, &element(&rt, key))
                .into_iter()
                .filter(|e| e.role == "label" || e.role == "text")
                .map(|e| e.rect[0])
                .collect()
        };
        let value = starts("inspector.profile")
            .into_iter()
            .fold(f32::NEG_INFINITY, f32::max);
        let words = starts("inspector.seed.open")
            .into_iter()
            .fold(f32::INFINITY, f32::min);
        assert!(
            (words - value).abs() <= SLACK,
            "{variant}: the link's words start at {words}, a fact's value at {value}"
        );
    }
}

#[test]
fn every_tab_stop_in_a_panel_has_a_key_and_a_name() {
    for (story_name, _) in PANEL_SUBJECTS {
        for variant in variants(story_name) {
            let mut rt = story(story_name, variant);
            rt.set_full_keyboard_access(true);
            let stage = rt
                .elements()
                .into_iter()
                .find(|e| e.path.ends_with("playground-stage-1"))
                .expect("the story has a stage");
            let (x, y) = (stage.rect[0] + 2.0, stage.rect[1] + 2.0);
            rt.pointer_down(x, y);
            rt.pointer_up(x, y);
            rt.pointer_leave();
            rt.frame(1.0);
            let mut seen: Vec<String> = Vec::new();
            for _ in 0..40 {
                rt.key(KeyEvent {
                    key: Key::Tab,
                    mods: Mods::default(),
                });
                rt.frame(1.0);
                let Some(stop) = rt
                    .elements()
                    .into_iter()
                    .find(|e| e.focused && e.path.starts_with(&stage.path))
                else {
                    continue;
                };
                if seen.contains(&stop.path) {
                    break;
                }
                assert!(
                    stop.key.is_some() && stop.label.as_deref().is_some_and(|l| !l.is_empty()),
                    "{story_name} {variant}: a Tab stop with no key or no name: {} {:?} {:?}",
                    stop.role,
                    stop.key,
                    stop.label
                );
                seen.push(stop.path);
            }
        }
    }
}

fn fitting(lines: &usize, cx: &mut Cx<usize>) -> El {
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    let said = (0..*lines)
        .map(|n| format!("Line {n} of what the sheet holds"))
        .collect::<Vec<_>>()
        .join("\n");
    let sheet = attn_ui::blocks::Sheet::new("fit", "Notifications")
        .fits(520.0)
        .push(attn_ui::blocks::Block::custom(
            "fit.words",
            attn_ui::system::words(said, attn_ui::system::Type::Body, t.text_primary).wrap(),
        ))
        .build(&t);
    rune::col()
        .key("room")
        .w(360.0)
        .max_h(FITTING_ROOM)
        .child(sheet)
}

const FITTING_WINDOW: (f32, f32) = (533.0, 400.0);

static FITTING_LINES: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(1);

fn fitting_lines() -> usize {
    FITTING_LINES.load(std::sync::atomic::Ordering::Relaxed)
}
const FITTING_ROOM: f32 = 352.0;

#[test]
fn a_fitting_sheet_keeps_its_body_inside_its_frame_and_its_frame_inside_its_room() {
    let line = 20.0;
    let most = (10.0 * FITTING_WINDOW.1 / line) as usize;
    for lines in [1, 2, 5, 10, 20, most / 2, most] {
        FITTING_LINES.store(lines, std::sync::atomic::Ordering::Relaxed);
        let mut rt = App::new(AppSpec {
            fonts: attn_ui::FONTS,
            families: attn_ui::FAMILIES,
            ..AppSpec::new("fitting", FITTING_WINDOW, fitting_lines, fitting)
        })
        .headless_runtime(FITTING_WINDOW, Env::new(true, SCALE));
        while rt.frame(1.0 / 60.0) {}
        let all = rt.elements();
        let rect = |key: &str| {
            all.iter()
                .find(|e| e.key.as_deref() == Some(key))
                .unwrap_or_else(|| panic!("{lines} lines: no {key}"))
                .rect
        };
        let (room, frame, body) = (rect("room"), rect("fit"), rect("fit.body"));
        let window = [0.0, 0.0, FITTING_WINDOW.0, FITTING_WINDOW.1];
        assert!(
            holds(window, room),
            "{lines} lines: the room {room:?} runs past the window"
        );
        assert!(
            holds(room, frame),
            "{lines} lines: the sheet {frame:?} runs past its room {room:?}"
        );
        assert!(
            holds(frame, body),
            "{lines} lines: the body {body:?} runs past the sheet {frame:?}"
        );
    }
}

static SCROLLING: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);

fn scrolling_variant() -> usize {
    SCROLLING.load(std::sync::atomic::Ordering::Relaxed)
}

fn scrolling_list(variant: &usize, cx: &mut Cx<usize>) -> El {
    use attn_ui::blocks::List;
    use attn_ui::system::ListRow;
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    let list = (0..40).fold(List::new("rows", "Rows"), |list, i| {
        list.row(ListRow::new(format!("rows.{i}"), format!("Row {i}")).build(&t))
    });
    let shown = match variant {
        0 => list.scrolling(&t),
        1 => list
            .heading()
            .count(40)
            .find(Some(attn_ui::system::words(
                "Find",
                attn_ui::system::Type::Body,
                t.text_primary,
            )))
            .scrolling(&t),
        2 => list.scrolling_up_to(200.0, &t),
        _ => list
            .scrolls_as("files", "navigation", "Files")
            .scrolling(&t),
    };
    rune::col().w(360.0).h(300.0).child(shown)
}

#[test]
fn a_scrolling_list_s_key_names_the_node_that_scrolls() {
    let window = (600.0, 400.0);
    for (variant, key, role) in [
        (0, "rows", "list"),
        (1, "rows", "list"),
        (2, "rows", "list"),
        (3, "files", "navigation"),
    ] {
        SCROLLING.store(variant, std::sync::atomic::Ordering::Relaxed);
        let mut rt = App::new(AppSpec {
            fonts: attn_ui::FONTS,
            families: attn_ui::FAMILIES,
            ..AppSpec::new("scrolling", window, scrolling_variant, scrolling_list)
        })
        .headless_runtime(window, Env::new(true, SCALE));
        while rt.frame(1.0 / 60.0) {}
        let node = rt
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("variant {variant}: no {key}"));
        assert_eq!(
            node.role, role,
            "variant {variant}: {key} is a {}",
            node.role
        );
        assert!(
            rt.accessibility_action(key, rune::accesskit::Action::ScrollDown, None),
            "variant {variant}: {key} does not scroll down though its 40 rows overflow"
        );
    }
}

#[test]
fn an_ask_chip_never_draws_wider_than_it_says_with_its_mark_a_step_in_from_every_edge() {
    use attn_ui::panel_gallery::{Drawer, Panels, ask_chip};
    let cases = [
        ("Rest", Drawer::Wide, false, false),
        ("Long", Drawer::Wide, true, false),
        ("Narrow", Drawer::Narrow, false, false),
        ("Narrow long", Drawer::Narrow, true, false),
        ("Smallest long", Drawer::Smallest, true, false),
        ("Several", Drawer::Wide, false, true),
    ];
    for (variant, size, long, several) in cases {
        let rt = story("Panels/Ask chip", variant);
        let chip = element(&rt, "askchip");
        let said = ask_chip(&Panels {
            size,
            long,
            several,
            ..Panels::default()
        })
        .width();
        assert!(
            chip.rect[2] <= said + SLACK && said - chip.rect[2] <= ONE_POINT,
            "{variant}: the chip draws {} wide and says {said}",
            chip.rect[2]
        );
        let mark = within(&rt, &chip)
            .into_iter()
            .min_by(|a, b| a.rect[0].total_cmp(&b.rect[0]))
            .expect("the chip draws its mark");
        let start = mark.rect[0] - chip.rect[0];
        let top = mark.rect[1] - chip.rect[1];
        let end = bottom(chip.rect) - bottom(mark.rect);
        assert!(
            (start - top).abs() <= SLACK && (top - end).abs() <= SLACK,
            "{variant}: the mark stands {start} in from the start, {top} from the top and {end} from the bottom"
        );
    }
}

#[test]
fn a_scroll_bar_never_lies_over_what_it_scrolls() {
    let mut bars = 0;
    for &(story_name, _) in PANEL_SUBJECTS {
        for variant in variants(story_name) {
            let rt = story(story_name, variant);
            let elements = rt.elements();
            for bar in elements
                .iter()
                .filter(|e| e.path.ends_with("/scroll-bar") && e.rect[2] > 0.0)
            {
                bars += 1;
                let area = bar.path.trim_end_matches("/scroll-bar");
                for part in elements.iter().filter(|e| {
                    e.path.starts_with(&format!("{area}/"))
                        && !e.path.starts_with(&bar.path)
                        && e.role == "text"
                        && e.rect[2] > 0.0
                }) {
                    assert!(
                        right(part.rect) <= bar.rect[0] + SLACK,
                        "{story_name} {variant}: {} at {:?} lies under its scroll bar at {:?}",
                        part.path,
                        part.rect,
                        bar.rect
                    );
                }
            }
        }
    }
    assert!(
        bars > 0,
        "no panel story shows a scroll bar to hold this to"
    );
}

#[test]
fn a_notices_actions_share_a_line_where_its_line_holds_them_at_any_text_size() {
    for variant in [
        "Notice",
        "Narrow notice",
        "Notice at 150%",
        "Smallest notice",
        "Smallest notice at 150%",
    ] {
        let rt = story("Panels/Sheet", variant);
        let notice = element(&rt, "sheet.conflict");
        let reload = element(&rt, "sheet.reload");
        let overwrite = element(&rt, "sheet.overwrite");
        let inset = 12.0;
        let line = notice.rect[0] + notice.rect[2] - inset - reload.rect[0];
        let both = overwrite.rect[0] + overwrite.rect[2] - reload.rect[0];
        assert!(
            both <= line,
            "{variant}: the two actions need {both} on a line of {line}"
        );
        assert_eq!(
            reload.rect[1], overwrite.rect[1],
            "{variant}: Overwrite anyway sits under Reload from disk though its line holds both: {:?} {:?}",
            reload.rect, overwrite.rect
        );
    }
}

#[test]
fn a_notice_too_long_for_its_sheet_scrolls_its_sentence_and_keeps_its_actions_whole() {
    let mut rt = story("Panels/Sheet", "Smallest notice at 150%");
    let band = element(&rt, "sheet.notices");
    let notice = element(&rt, "sheet.conflict");
    let view = element(&rt, "sheet.conflict.sentence");
    let body = element(&rt, "sheet.body");
    let inside = |e: &ElementInfo, of: &ElementInfo| {
        e.rect[1] >= of.rect[1] && e.rect[1] + e.rect[3] <= of.rect[1] + of.rect[3]
    };
    for action in ["sheet.reload", "sheet.overwrite"] {
        let action = element(&rt, action);
        assert!(
            inside(&action, &band),
            "{:?} lies outside the band {:?}",
            action.rect,
            band.rect
        );
    }
    assert!(
        inside(&view, &notice),
        "the sentence's view {:?} runs out of its notice {:?}",
        view.rect,
        notice.rect
    );
    let sentence = |rt: &Runtime<()>| {
        rt.elements()
            .into_iter()
            .find(|e| {
                e.text
                    .as_deref()
                    .is_some_and(|t| t.starts_with("This automation changed"))
            })
            .expect("the notice's sentence")
            .rect
    };
    let [_, top, _, tall] = sentence(&rt);
    assert!(
        tall > view.rect[3],
        "the sentence, {tall} tall, fits its view {:?}: the story no longer gives way",
        view.rect
    );
    rt.accessibility_action(
        "sheet.conflict.sentence",
        rune::accesskit::Action::ScrollDown,
        None,
    );
    for _ in 0..60 {
        rt.frame(1.0 / 60.0);
    }
    assert!(
        sentence(&rt)[1] < top,
        "the sentence scrolls in its view: its top stays at {top}"
    );
    assert!(
        band.rect[1] + band.rect[3] <= body.rect[1],
        "{:?} {:?}",
        band.rect,
        body.rect
    );
}
