//! The rules the reading blocks hold, read from their gallery stories' element trees.

use attn_ui::reading_gallery::READING_SUBJECTS;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1500.0, 1100.0);
const SCALE: f32 = 2.0;
const WIDTHS: [&str; 5] = ["Rest", "Long", "Narrow", "Narrow long", "Smallest long"];
/// The farthest a reading part's box hangs past the text around it: a list row's inset.
const HANG: f32 = 8.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("reading rules", WINDOW, || (), view)
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

fn drawn_under<'a>(
    all: &'a [ElementInfo],
    outer: &ElementInfo,
) -> impl Iterator<Item = &'a ElementInfo> {
    let prefix = format!("{}/", outer.path);
    all.iter()
        .filter(move |e| e.path.starts_with(&prefix) && e.rect[2] > 0.0 && e.rect[3] > 0.0)
}

#[test]
fn nothing_a_reading_block_draws_hangs_past_its_sides_at_any_width() {
    for &(name, keys, _) in READING_SUBJECTS {
        for variant in WIDTHS {
            let rt = story(name, variant);
            let all = rt.elements();
            for key in keys {
                let block = element(&rt, key);
                for part in drawn_under(&all, &block) {
                    assert!(
                        part.rect[0] >= block.rect[0] - HANG - 0.5
                            && part.rect[0] + part.rect[2]
                                <= block.rect[0] + block.rect[2] + HANG + 0.5,
                        "{name} {variant} {key}: {} at {:?} hangs past the block at {:?}",
                        part.path,
                        part.rect,
                        block.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_reading_controls_box_and_ring_take_in_everything_it_draws() {
    for &(name, _, _) in READING_SUBJECTS {
        for variant in WIDTHS {
            let rt = story(name, variant);
            let all = rt.elements();
            for control in all.iter().filter(|e| e.focusable) {
                for part in drawn_under(&all, control) {
                    let r = (control.rect, part.rect);
                    assert!(
                        r.1[0] >= r.0[0] - 0.5
                            && r.1[1] >= r.0[1] - 0.5
                            && r.1[0] + r.1[2] <= r.0[0] + r.0[2] + 0.5
                            && r.1[1] + r.1[3] <= r.0[1] + r.0[3] + 0.5,
                        "{name} {variant}: {} at {:?} reaches outside its control {} at {:?}",
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
fn a_boards_lanes_share_its_width_evenly() {
    for variant in WIDTHS {
        let rt = story("Blocks/Lane", variant);
        let widths: Vec<f32> = (0..3)
            .map(|n| element(&rt, &format!("board.lane.{n}")).rect[2])
            .collect();
        assert!(
            widths.iter().all(|w| (w - widths[0]).abs() < 0.5),
            "{variant}: lanes {widths:?}"
        );
    }
}

#[test]
fn a_reading_story_draws_once_and_rests() {
    for &(name, _, _) in READING_SUBJECTS {
        let mut rt = story(name, "Rest");
        rt.frame(1.0 / 60.0);
        assert!(
            !rt.animating && rt.wake_deadline().is_none(),
            "{name} asks for another frame at rest"
        );
    }
}
