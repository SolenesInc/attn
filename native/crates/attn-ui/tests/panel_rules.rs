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
                for part in within(&rt, control) {
                    assert!(
                        holds(control.rect, part.rect),
                        "{story_name} {variant}: {} at {:?} reaches outside its control {} at {:?}",
                        part.path,
                        part.rect,
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
