//! The rules the blocks hold for the Settings pages, read from the gallery's stories.

use attn_ui::block_gallery::BLOCK_SUBJECTS;
use attn_ui::settings_gallery::SETTINGS_SUBJECTS;
use rune::render::KIND_GLYPH;
use rune::{App, AppSpec, Cx, El, Env, Runtime, el};

const WINDOW: (f32, f32) = (1500.0, 1100.0);
const SCALE: f32 = 2.0;
const WIDTHS: [&str; 5] = ["Rest", "Long", "Narrow", "Narrow long", "Smallest long"];

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn block_story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("settings rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt
}

#[test]
fn no_word_is_drawn_past_the_side_of_its_page_at_any_width() {
    for &(story, subjects) in BLOCK_SUBJECTS.iter().chain(SETTINGS_SUBJECTS) {
        for variant in WIDTHS {
            let rt = block_story(story, variant);
            let rects: Vec<[f32; 4]> = subjects
                .iter()
                .map(|key| {
                    rt.playground_element(key)
                        .unwrap_or_else(|| panic!("{story} {variant}: no {key} on the stage"))
                        .rect
                        .map(|v| v * SCALE)
                })
                .collect();
            let left = rects.iter().map(|r| r[0]).fold(f32::MAX, f32::min) - 1.0;
            let right = rects.iter().map(|r| r[0] + r[2]).fold(f32::MIN, f32::max) + 1.0;
            let top = rects.iter().map(|r| r[1]).fold(f32::MAX, f32::min);
            let bottom = rects.iter().map(|r| r[1] + r[3]).fold(f32::MIN, f32::max);
            let side = rt
                .elements()
                .into_iter()
                .find(|e| e.key.as_deref() == Some("playground-side"))
                .map_or(f32::MAX, |e| e.rect[0] * SCALE);
            let on_stage =
                |r: &[f32; 4]| r[0] >= left && r[0] < side && r[1] >= top && r[1] + r[3] <= bottom;
            for glyph in rt
                .instances
                .iter()
                .filter(|i| i.params[1] == KIND_GLYPH && on_stage(&i.rect))
            {
                let [x, _, w, _] = glyph.rect;
                assert!(
                    x + w <= right,
                    "{story} {variant}: a glyph at x {x}..{} y {} is drawn past the page's sides {left}..{right}",
                    x + w,
                    glyph.rect[1]
                );
            }
        }
    }
}

#[test]
fn a_fields_label_runs_to_the_fields_measure_before_it_wraps() {
    for (variant, key) in [
        ("Long", "field.path"),
        ("Narrow long", "field.path"),
        ("Rest", "field.count"),
        ("Narrow", "field.count"),
    ] {
        let rt = block_story("Blocks/Field", variant);
        let all = rt.elements();
        let field = all
            .iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("{variant}: no {key}"));
        let label = all
            .iter()
            .find(|e| e.key.as_deref() == Some(format!("{key}.label").as_str()))
            .unwrap_or_else(|| panic!("{variant}: no {key}.label"));
        let words = all
            .iter()
            .find(|e| e.parent.is_some_and(|p| all[p].path == label.path) && e.role == "text")
            .unwrap_or_else(|| panic!("{variant}: the label holds no words"));
        let field_right = field.rect[0] + field.rect[2];
        let words_right = words.rect[0] + words.rect[2];
        let one_line = words.rect[3] < 20.0;
        assert!(
            one_line || field_right - words_right < 40.0,
            "{variant} {key}: the label wraps at {words_right}, short of the field's {field_right}"
        );
    }
}
