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
            .find(|e| e.key.as_deref() == Some(format!("{key}.field").as_str()))
            .unwrap_or_else(|| panic!("{variant}: no {key}.field"));
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

#[derive(Default)]
struct Sibling {
    notice: bool,
    list: bool,
    on: bool,
}

fn sibling_view(state: &Sibling, cx: &mut Cx<Sibling>) -> El {
    use attn_ui::blocks::{Actions, Block, Notice, Page, Section, SettingRow};
    use attn_ui::system::{Button, Type, switch, words};
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    let mut section = Section::new("section", "Pairing");
    if state.notice {
        section =
            section.push(Notice::warn("notice", "The code expired; show a new one.").build(&t));
    }
    if state.list {
        section = section.push(Block::custom(
            "list.block",
            words("A list that comes and goes", Type::Body, t.text_primary),
        ));
    }
    let section = section
        .push(Block::custom(
            "code.block",
            Button::new("Copy")
                .on_press_tx(|_: &mut Sibling, _: &mut rune::Tx| {})
                .build(&t)
                .key("copy")
                .into(),
        ))
        .push(
            Actions::new("actions")
                .push(
                    Button::new("Pair")
                        .on_press_tx(|_: &mut Sibling, _: &mut rune::Tx| {})
                        .build(&t)
                        .key("pair"),
                )
                .build(),
        )
        .push(
            SettingRow::new(
                "row",
                "Auto-approve",
                "Changed here or by another client.",
                switch(
                    "switch",
                    "Auto-approve",
                    state.on,
                    false,
                    false,
                    &t,
                    |state: &mut Sibling, _: &mut rune::Tx| state.on = !state.on,
                ),
            )
            .build(&t),
        )
        .build(&t);
    Page::new().push(section).build("page", &t)
}

fn sibling(dark: bool) -> Runtime<Sibling> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("sibling", (600.0, 400.0), Sibling::default, sibling_view)
    })
    .headless_runtime((600.0, 400.0), Env::new(dark, SCALE));
    while rt.frame(1.0 / 60.0) {}
    rt
}

fn focused(rt: &Runtime<Sibling>) -> Option<String> {
    rt.elements()
        .into_iter()
        .find(|e| e.focused)
        .and_then(|e| e.key)
}

#[test]
fn a_focused_control_keeps_the_keyboard_when_a_block_appears_or_goes_above_it() {
    for (focus, above) in [("pair", "a notice"), ("copy", "a custom block")] {
        let mut rt = sibling(true);
        rt.update(move |_, tx| tx.focus(focus, true));
        while rt.frame(1.0 / 60.0) {}
        assert_eq!(focused(&rt).as_deref(), Some(focus));
        for shown in [true, false] {
            let custom = above == "a custom block";
            rt.update(move |state, _| {
                if custom {
                    state.list = shown;
                } else {
                    state.notice = shown;
                }
            });
            rt.frame(1.0 / 60.0);
            assert_eq!(
                focused(&rt).as_deref(),
                Some(focus),
                "{above} {} above {focus}",
                if shown { "appears" } else { "goes" }
            );
        }
    }
}

#[test]
fn a_switch_moves_its_thumb_and_colours_in_step_and_comes_to_rest() {
    for dark in [true, false] {
        let mut rt = sibling(dark);
        let look = |rt: &Runtime<Sibling>| {
            let all = rt.elements();
            let part = |key: &str| {
                all.iter()
                    .find(|e| e.key.as_deref() == Some(key))
                    .cloned()
                    .unwrap_or_else(|| panic!("the switch's {key} is drawn"))
            };
            let (thumb, track) = (part("thumb"), part("control"));
            let rgb = |c: rune::Color| [c.r, c.g, c.b];
            (thumb.rect[0], rgb(thumb.fill), rgb(track.fill))
        };
        for cause in ["outside", "press", "outside", "press"] {
            let from = look(&rt);
            if cause == "press" {
                let [x, y, w, h] = rt
                    .elements()
                    .into_iter()
                    .find(|e| e.key.as_deref() == Some("switch"))
                    .expect("the switch is drawn")
                    .rect;
                rt.pointer_down(x + w / 2.0, y + h / 2.0);
                rt.pointer_up(x + w / 2.0, y + h / 2.0);
            } else {
                rt.update(|state, _| state.on = !state.on);
            }
            let mut frames = Vec::new();
            for _ in 0..240 {
                let more = rt.frame(1.0 / 60.0);
                frames.push(look(&rt));
                if !more {
                    break;
                }
            }
            assert!(
                !rt.frame(1.0 / 60.0),
                "dark {dark}, {cause}: the switch still repaints after it settles"
            );
            let to = look(&rt);
            let progress = |a: f32, b: f32, now: f32| {
                if (b - a).abs() < 1e-4 {
                    None
                } else {
                    Some((now - a) / (b - a))
                }
            };
            for (n, now) in frames.iter().enumerate() {
                let mut parts: Vec<(&str, f32)> = Vec::new();
                parts.extend(progress(from.0, to.0, now.0).map(|p| ("thumb", p)));
                for c in 0..3 {
                    parts.extend(
                        progress(from.1[c], to.1[c], now.1[c]).map(|p| ("thumb colour", p)),
                    );
                    parts.extend(
                        progress(from.2[c], to.2[c], now.2[c]).map(|p| ("track colour", p)),
                    );
                }
                let done = parts
                    .iter()
                    .filter(|(_, p)| *p >= 0.99)
                    .map(|(n, _)| *n)
                    .collect::<Vec<_>>();
                let unmoved = parts
                    .iter()
                    .filter(|(_, p)| *p <= 0.01)
                    .map(|(n, _)| *n)
                    .collect::<Vec<_>>();
                assert!(
                    done.is_empty() || unmoved.is_empty(),
                    "dark {dark}, {cause}, frame {n}: {done:?} arrived while {unmoved:?} had not started"
                );
            }
        }
    }
}

fn glyphs_in(rt: &Runtime<()>, rect: [f32; 4]) -> Vec<[f32; 4]> {
    let [x, y, w, h] = rect.map(|v| v * SCALE);
    rt.instances
        .iter()
        .filter(|i| i.params[1] == KIND_GLYPH)
        .map(|i| i.rect)
        .filter(|g| {
            g[0] >= x && g[0] + g[2] <= x + w + 1.0 && g[1] >= y && g[1] + g[3] <= y + h + 1.0
        })
        .collect()
}

fn rect_of(rt: &Runtime<()>, key: &str, variant: &str) -> [f32; 4] {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("{variant}: no {key} on the stage"))
        .rect
}

#[test]
fn a_facts_label_sits_nearer_its_own_value_than_the_next_fact() {
    let facts = [
        "facts.install",
        "facts.build",
        "facts.host",
        "facts.available",
        "facts.path",
        "facts.folder",
    ];
    for variant in WIDTHS {
        let rt = block_story("Blocks/Facts", variant);
        let lines: Vec<[f32; 4]> = facts.iter().map(|k| rect_of(&rt, k, variant)).collect();
        let between = lines
            .windows(2)
            .map(|w| w[1][1] - (w[0][1] + w[0][3]))
            .fold(f32::MAX, f32::min);
        for (key, line) in facts.iter().zip(&lines) {
            let glyphs = glyphs_in(&rt, *line);
            let first_top = glyphs.iter().map(|g| g[1]).fold(f32::MAX, f32::min);
            let label_bottom = glyphs
                .iter()
                .filter(|g| g[1] < first_top + 4.0)
                .map(|g| g[1] + g[3])
                .fold(f32::MIN, f32::max);
            let stacked = glyphs
                .iter()
                .any(|g| g[0] < glyphs[0][0] + 1.0 && g[1] > label_bottom);
            if !stacked {
                continue;
            }
            let value_top = glyphs
                .iter()
                .filter(|g| g[1] > label_bottom)
                .map(|g| g[1])
                .fold(f32::MAX, f32::min);
            let within = (value_top - label_bottom) / SCALE;
            assert!(
                within < between,
                "{variant} {key}: the label is {within}pt over its value, and facts sit {between}pt apart"
            );
        }
    }
}

#[test]
fn a_facts_label_lines_up_with_the_words_of_a_control_beside_it() {
    for variant in ["Rest", "Long"] {
        let rt = block_story("Blocks/Facts", variant);
        let control = rect_of(&rt, "facts.folder.copy", variant);
        let line = rect_of(&rt, "facts.folder", variant);
        let label = glyphs_in(&rt, [line[0], line[1], control[0] - line[0], line[3]]);
        let words = glyphs_in(&rt, control);
        let middle = |g: &[[f32; 4]]| {
            let top = g.iter().map(|g| g[1]).fold(f32::MAX, f32::min);
            let bottom = g.iter().map(|g| g[1] + g[3]).fold(f32::MIN, f32::max);
            (top + bottom) / 2.0 / SCALE
        };
        assert!(
            !label.is_empty() && !words.is_empty(),
            "{variant}: no label or no words beside it"
        );
        let off = (middle(&label) - middle(&words)).abs();
        assert!(
            off <= 2.0,
            "{variant}: the label's middle is {off}pt off the control's words"
        );
    }
}

fn sections_view(_: &(), cx: &mut Cx<()>) -> El {
    use attn_ui::blocks::{Page, Section};
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    Page::new()
        .push(Section::new("plain", "Pairing").build(&t))
        .push(Section::new("adding", "Add an outpost").form().build(&t))
        .build("page", &t)
}

#[test]
fn a_section_marked_as_a_form_is_a_form_named_by_its_heading() {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("sections", (600.0, 400.0), || (), sections_view)
    })
    .headless_runtime((600.0, 400.0), Env::new(true, SCALE));
    while rt.frame(1.0 / 60.0) {}
    let section = |key: &str| {
        let e = rt
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no section {key}"));
        (e.role, e.label)
    };
    assert_eq!(section("adding"), ("form", Some("Add an outpost".into())));
    assert_eq!(section("plain"), ("group", Some("Pairing".into())));
}
