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

#[test]
fn a_field_holds_its_input_under_its_own_key_and_no_two_parts_share_a_key() {
    for variant in WIDTHS {
        let rt = block_story("Blocks/Field", variant);
        let all = rt.elements();
        let mut keys: Vec<&str> = all
            .iter()
            .filter_map(|e| e.key.as_deref())
            .filter(|k| k.starts_with("field."))
            .collect();
        keys.sort_unstable();
        let shared: Vec<&&str> = keys
            .windows(2)
            .filter(|w| w[0] == w[1])
            .map(|w| &w[0])
            .collect();
        assert!(shared.is_empty(), "{variant}: keys held twice: {shared:?}");
        for key in ["field.name", "field.path", "field.count"] {
            let field = all
                .iter()
                .find(|e| e.key.as_deref() == Some(format!("{key}.field").as_str()))
                .unwrap_or_else(|| panic!("{variant}: no {key}.field"));
            let input = all
                .iter()
                .find(|e| e.key.as_deref() == Some(key))
                .unwrap_or_else(|| panic!("{variant}: no input keyed {key}"));
            assert!(
                input.path.starts_with(&field.path),
                "{variant}: the input {key} is not inside {key}.field"
            );
        }
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
        let inked: Vec<(f32, f32)> = lines
            .iter()
            .map(|line| {
                let glyphs = glyphs_in(&rt, *line);
                let top = glyphs.iter().map(|g| g[1]).fold(f32::MAX, f32::min);
                let bottom = glyphs.iter().map(|g| g[1] + g[3]).fold(f32::MIN, f32::max);
                (top, bottom)
            })
            .collect();
        let between = inked
            .windows(2)
            .map(|w| (w[1].0 - w[0].1) / SCALE)
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
                "{variant} {key}: the label's ink is {within}pt over its value's, and the facts' ink sits {between}pt apart"
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
        .push(
            Section::new("codex", "Codex")
                .region("Codex models")
                .build(&t),
        )
        .build("page", &t)
}

fn sections() -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("sections", (600.0, 400.0), || (), sections_view)
    })
    .headless_runtime((600.0, 400.0), Env::new(true, SCALE));
    while rt.frame(1.0 / 60.0) {}
    rt
}

#[test]
fn a_section_marked_as_a_form_is_a_form_named_by_its_heading() {
    let rt = sections();
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

#[test]
fn a_section_marked_as_a_region_is_a_landmark_by_its_given_name() {
    let rt = sections();
    let codex = rt
        .elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some("codex"))
        .expect("the codex section");
    assert_eq!(
        (codex.role, codex.label),
        ("region", Some("Codex models".into()))
    );
}

#[test]
fn a_sections_title_is_a_heading_under_the_pages() {
    let rt = sections();
    for (key, title) in [
        ("plain", "Pairing"),
        ("adding", "Add an outpost"),
        ("codex", "Codex"),
    ] {
        let heading = rt
            .elements()
            .into_iter()
            .find(|e| e.key.as_deref() == Some(format!("{key}.heading").as_str()))
            .unwrap_or_else(|| panic!("no heading for {key}"));
        assert_eq!(
            (heading.role, heading.label),
            ("heading", Some(title.into()))
        );
    }
}

const MARKED: &str = "a `b` *c* _d_ \\`e\\`";

/// Every block that takes text, keyed `key`, saying `said`.
fn every_block(
    key: &str,
    said: attn_ui::system::Said,
    t: &attn_ui::Tokens,
    cx: &Cx<()>,
) -> Vec<El> {
    use attn_ui::blocks::{Banner, BannerTone, Inspector};
    use attn_ui::system::{
        Button, Choice, GroupHead, Segment, Segmented, Type, expander, keys_hint,
    };
    let held = |name: &str, e: El| rune::col().key(format!("{key}.{name}")).child(e);
    use attn_ui::blocks::{
        Disclosure, Facts, Field, Head, List, Notice, Page, Prose, Refusal, Section, SettingRow,
        Status,
    };
    use attn_ui::system::{ListRow, Tone, disclosure, tag, words};
    let page = |name: &str, block: attn_ui::blocks::Block| {
        Page::new().push(block).build(format!("{key}.{name}"), t)
    };
    let refusal = Refusal::new(format!("{key}.refusal.line"), said.clone()).build(t);
    vec![
        page(
            "section",
            Section::new(format!("{key}.section.part"), said.clone()).build(t),
        ),
        page(
            "aside",
            Section::new(format!("{key}.aside.part"), "Title")
                .aside(said.clone())
                .build(t),
        ),
        page(
            "notice",
            Notice::warn(format!("{key}.notice.line"), said.clone()).build(t),
        ),
        page(
            "status",
            Status::new(format!("{key}.status.line"), said.clone(), true).build(t),
        ),
        page(
            "refusal",
            attn_ui::blocks::Block::custom(format!("{key}.refusal.slot"), refusal),
        ),
        page(
            "prose",
            Prose::hint(format!("{key}.prose.line"), said.clone()).build(t),
        ),
        page(
            "facts",
            Facts::new(format!("{key}.facts.list"))
                .fact(format!("{key}.facts.fact"), "Path", said.clone())
                .build(t),
        ),
        page(
            "field",
            Field::new(format!("{key}.field.part"), "Name")
                .value(said.clone())
                .build(t),
        ),
        page(
            "row",
            SettingRow::new(
                format!("{key}.row.part"),
                said.clone(),
                "Copy",
                words("x", attn_ui::system::Type::Small, t.text_primary),
            )
            .build(t),
        ),
        page(
            "list",
            List::new(format!("{key}.list.part"), "List")
                .empty(format!("{key}.list.empty"), said.clone(), None)
                .build(t),
        ),
        page(
            "disclosure",
            Disclosure::new(
                format!("{key}.disclosure.part"),
                said.clone(),
                false,
                |_: &mut (), _| {},
            )
            .build(t),
        ),
        Page::new()
            .head(Head::new(format!("{key}.head.title"), said.clone()))
            .build(format!("{key}.head"), t),
        rune::col().key(format!("{key}.listrow")).child(
            ListRow::new(format!("{key}.listrow.row"), "row")
                .title(said.clone())
                .build(t),
        ),
        rune::col().key(format!("{key}.subtitle")).child(
            ListRow::new(format!("{key}.subtitle.row"), "row")
                .title("Row")
                .subtitle(said.clone(), t)
                .build(t),
        ),
        rune::col().key(format!("{key}.tag")).child(El::from(tag(
            format!("{key}.tag.word"),
            said.clone(),
            Tone::Neutral,
            t,
        ))),
        rune::col()
            .key(format!("{key}.control"))
            .child(El::from(disclosure(
                format!("{key}.control.line"),
                said.clone(),
                false,
                None,
                &[],
                false,
                false,
                t,
                |_: &mut (), _| {},
            ))),
        held(
            "banner",
            Banner::new(format!("{key}.banner.bar"), "Banner", BannerTone::Calm)
                .says(format!("{key}.banner.said"), said.clone())
                .build(t, cx),
        ),
        held(
            "bannernote",
            Banner::new(format!("{key}.bannernote.bar"), "Banner", BannerTone::Calm)
                .says(format!("{key}.bannernote.said"), "Sentence")
                .note(format!("{key}.bannernote.note"), said.clone())
                .build(t, cx),
        ),
        held(
            "inspector",
            Inspector::new(format!("{key}.inspector.column"), said.clone()).build(t),
        ),
        held(
            "listnote",
            ListRow::note(
                format!("{key}.listnote.line"),
                said.clone(),
                Type::Body,
                t.text_primary,
            ),
        ),
        held("button", El::from(Button::new(said.clone()).build(t))),
        held(
            "segment",
            El::from(
                Segmented::new(format!("{key}.segment.group"), "Segments")
                    .option(Segment::new(format!("{key}.segment.one"), said.clone()))
                    .build(t),
            ),
        ),
        held(
            "choice",
            El::from(
                Choice::new(format!("{key}.choice.one"), said.clone()).build(
                    (0, 1),
                    t,
                    |_: &mut (), _| {},
                ),
            ),
        ),
        held(
            "choicenote",
            El::from(
                Choice::new(format!("{key}.choicenote.one"), "Name")
                    .note(said.clone())
                    .build((0, 1), t, |_: &mut (), _| {}),
            ),
        ),
        held(
            "expander",
            El::from(expander(
                format!("{key}.expander.control"),
                said.clone(),
                false,
                false,
                t,
                |_: &mut (), _| {},
            )),
        ),
        held(
            "grouphead",
            GroupHead::new(format!("{key}.grouphead.head"), "Group")
                .name(said.clone())
                .build(t),
        ),
        held(
            "groupsection",
            GroupHead::new(format!("{key}.groupsection.head"), "Group")
                .section(said.clone())
                .build(t),
        ),
        held("keyshint", keys_hint(["K"], said.clone(), t)),
        page(
            "listheading",
            List::new(format!("{key}.listheading.list"), said.clone())
                .heading()
                .row(
                    ListRow::new(format!("{key}.listheading.row"), "Row")
                        .still()
                        .title("Row")
                        .build(t),
                )
                .build(t),
        ),
    ]
}

const BLOCKS: [&str; 29] = [
    "section",
    "aside",
    "notice",
    "status",
    "refusal",
    "prose",
    "facts",
    "field",
    "row",
    "list",
    "disclosure",
    "head",
    "listrow",
    "subtitle",
    "tag",
    "control",
    "banner",
    "bannernote",
    "inspector",
    "listnote",
    "button",
    "segment",
    "choice",
    "choicenote",
    "expander",
    "grouphead",
    "groupsection",
    "keyshint",
    "listheading",
];

fn every_block_view(_: &(), cx: &mut Cx<()>) -> El {
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    let data = attn_ui::system::Said::from(MARKED.to_owned());
    let plain = attn_ui::system::Said::from(MARKED.replace('`', ""));
    rune::col()
        .w_full()
        .children(every_block("data", data, &t, cx))
        .children(every_block("plain", plain, &t, cx))
        .children(every_block("copy", MARKED.into(), &t, cx))
}

#[test]
fn every_block_draws_and_reads_text_from_data_as_written_and_parses_its_own_copy() {
    let size = (900.0, 9000.0);
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("every block", size, || (), every_block_view)
    })
    .headless_runtime(size, Env::new(true, SCALE));
    while rt.frame(1.0 / 60.0) {}
    let all = rt.elements();
    let rect = |key: &str| {
        all.iter()
            .find(|e| e.key.as_deref() == Some(key))
            .unwrap_or_else(|| panic!("no {key}"))
            .rect
    };
    let glyphs = |key: &str| glyphs_in(&rt, rect(key)).len();
    let inside = |outer: [f32; 4], inner: [f32; 4]| {
        inner[0] >= outer[0] - 0.5
            && inner[1] >= outer[1] - 0.5
            && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
            && inner[1] + inner[3] <= outer[1] + outer[3] + 0.5
    };
    let backticks = MARKED.matches('`').count();
    for block in BLOCKS {
        let (data, plain, copy) = (
            glyphs(&format!("data.{block}")),
            glyphs(&format!("plain.{block}")),
            glyphs(&format!("copy.{block}")),
        );
        assert_eq!(
            data - plain,
            backticks,
            "{block} draws {data} glyphs for {MARKED:?} as data and {plain} without its backticks"
        );
        assert!(
            copy <= plain,
            "{block} draws {copy} glyphs for {MARKED:?} as its own copy, so it did not read its backticks"
        );
        let outer = rect(&format!("data.{block}"));
        assert!(
            all.iter()
                .any(|e| e.label.as_deref().is_some_and(|l| l.contains(MARKED))
                    && inside(outer, e.rect)),
            "{block} is not read as {MARKED:?}"
        );
    }
}

/// Every public text input of attn_ui, found in its source: `(path::fn(param), said)`. A
/// parameter is text when its type names `Said`, `String` or `str`, directly or through a
/// generic bound. Keys, ids, accessibility roles and glyph names are names, not text; colour
/// settings and the appearance setting are parsed, not drawn.
/// `text` cut at its commas that lie outside any brackets.
fn split_top(text: &str) -> Vec<String> {
    let mut parts = Vec::new();
    let (mut depth, mut current) = (0, String::new());
    for c in text.chars() {
        match c {
            '<' | '(' | '[' => depth += 1,
            '>' | ')' | ']' => depth -= 1,
            _ => {}
        }
        if c == ',' && depth == 0 {
            parts.push(std::mem::take(&mut current));
        } else {
            current.push(c);
        }
    }
    parts.push(current);
    parts
}

/// The public text inputs `source`, the file of `module`, declares, each with whether it takes
/// `Said`.
fn text_inputs_in(source: &str, module: &str) -> Vec<(String, bool)> {
    let mut inputs = Vec::new();
    let mut owner: Option<String> = None;
    let mut at = 0;
    for line in source.split_inclusive('\n') {
        let start = at;
        at += line.len();
        let trimmed = line.trim_start();
        if line.starts_with('}') {
            owner = None;
        }
        if line.starts_with("impl") && line.trim_end().ends_with('{') {
            let head = line.trim_end().trim_end_matches('{');
            let head = head.rsplit(" for ").next().unwrap();
            let head = head.trim_start_matches("impl").trim();
            let head = if head.starts_with('<') {
                &head[head.find('>').unwrap() + 1..]
            } else {
                head
            };
            owner = Some(head.trim().split(['<', ' ']).next().unwrap().to_owned());
            continue;
        }
        let Some(rest) = trimmed.strip_prefix("pub fn ") else {
            continue;
        };
        let fn_name: String = rest
            .chars()
            .take_while(|c| c.is_alphanumeric() || *c == '_')
            .collect();
        let open = start + line.len() - rest.len() + fn_name.len();
        let bytes = source.as_bytes();
        let mut generics = String::new();
        let mut i = open;
        if bytes[i] == b'<' {
            let mut depth = 0;
            loop {
                match bytes[i] {
                    b'<' => depth += 1,
                    b'>' => depth -= 1,
                    _ => {}
                }
                generics.push(bytes[i] as char);
                i += 1;
                if depth == 0 {
                    break;
                }
            }
        }
        assert_eq!(
            bytes[i], b'(',
            "{module}::{fn_name}: no parameters where expected"
        );
        let mut depth = 0;
        let mut params = String::new();
        loop {
            let c = bytes[i] as char;
            match c {
                '(' => depth += 1,
                ')' => depth -= 1,
                _ => {}
            }
            i += 1;
            if depth == 0 {
                break;
            }
            if !(c == '(' && depth == 1) {
                params.push(c);
            }
        }
        let mut tail = String::new();
        let mut depth = 0;
        while i < bytes.len() {
            let c = bytes[i] as char;
            match c {
                '<' | '(' | '[' => depth += 1,
                '>' | ')' | ']' => depth -= 1,
                '{' | ';' if depth == 0 => break,
                _ => {}
            }
            tail.push(c);
            i += 1;
        }
        let inline = generics
            .strip_prefix('<')
            .and_then(|g| g.strip_suffix('>'))
            .unwrap_or("");
        let clauses = tail.split_once("where").map_or("", |(_, w)| w);
        let bounds: Vec<(String, String)> = split_top(inline)
            .into_iter()
            .chain(split_top(clauses))
            .filter_map(|g| {
                g.split_once(':')
                    .map(|(name, bound)| (name.trim().to_owned(), bound.to_owned()))
            })
            .collect();
        let mut parts = Vec::new();
        let (mut depth, mut current) = (0, String::new());
        for c in params.chars() {
            match c {
                '<' | '(' | '[' => depth += 1,
                '>' | ')' | ']' => depth -= 1,
                _ => {}
            }
            if c == ',' && depth == 0 {
                parts.push(std::mem::take(&mut current));
            } else {
                current.push(c);
            }
        }
        parts.push(current);
        for part in parts {
            let Some((param, ty)) = part.split_once(':') else {
                continue;
            };
            let param = param.trim().trim_start_matches("mut ").trim();
            let ty = ty.split_whitespace().collect::<Vec<_>>().join(" ");
            if param.contains("self")
                || ["key", "id", "role", "select_role", "mark"].contains(&param)
                || param.ends_with("_key")
                || param.ends_with("_id")
                || ty.contains("Fn(")
                || ty.contains("Tokens")
            {
                continue;
            }
            let bound = bounds
                .iter()
                .filter(|(g, _)| {
                    ty.split(|c: char| !c.is_alphanumeric() && c != '_')
                        .any(|w| w == g)
                })
                .map(|(_, b)| b.as_str())
                .collect::<Vec<_>>()
                .join(" ");
            let seen = format!("{ty} {bound}");
            let text = ["Said", "String", "str"]
                .iter()
                .any(|w| seen.split(|c: char| !c.is_alphanumeric()).any(|x| x == *w));
            if !text {
                continue;
            }
            let said = seen.contains("Said") || seen.contains("'static str");
            let owner = owner.as_ref().map_or(String::new(), |o| format!("{o}::"));
            inputs.push((format!("{module}::{owner}{fn_name}({param})"), said));
        }
    }
    inputs
}

fn public_text_inputs() -> Vec<(String, bool)> {
    fn files(dir: &std::path::Path, out: &mut Vec<std::path::PathBuf>) {
        let mut entries: Vec<_> = std::fs::read_dir(dir)
            .unwrap()
            .map(|e| e.unwrap().path())
            .collect();
        entries.sort();
        for path in entries {
            if path.is_dir() {
                files(&path, out);
            } else if path.extension().is_some_and(|e| e == "rs") {
                out.push(path);
            }
        }
    }
    let src = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("src");
    let mut paths = Vec::new();
    files(&src, &mut paths);
    let mut inputs = Vec::new();
    for path in paths {
        let name = path.strip_prefix(&src).unwrap().with_extension("");
        let module = name.to_string_lossy().replace('/', "::");
        if module.contains("gallery")
            || module == "playground"
            || module.starts_with("colors")
            || module == "lib"
            || module == "icon"
        {
            continue;
        }
        let source = std::fs::read_to_string(&path).unwrap();
        inputs.extend(text_inputs_in(&source, &module));
    }
    inputs
}

/// How each public text input draws: `Said` takes authored copy parsed and data as written;
/// `Plain` draws whatever it is given as written. Every input whose words went through
/// `words()` on main, and so parsed, is `Said`; keys and key labels, paths given as data and
/// names read only by assistive tools are `Plain`. The table is checked against the `pub fn`
/// signatures in attn-ui's source, inline and where-clause bounds included; it does not see
/// text that reaches a draw another way, which `Said` guards: only authored copy is parsed.
const TEXT_INPUTS: &[(&str, bool)] = &[
    ("blocks::banner::Banner::new(label)", false),
    ("blocks::banner::Banner::value(value)", false),
    ("blocks::banner::Banner::says(sentence)", true),
    ("blocks::banner::Banner::note(said)", true),
    ("blocks::banner::Banner::link(shown)", true),
    ("blocks::banner::Banner::link(label)", false),
    ("blocks::banner::Banner::note_button(label)", false),
    ("blocks::banner::Banner::dismiss(label)", false),
    ("blocks::choices::Choices::new(label)", false),
    ("blocks::dialog::Dialog::new(title)", true),
    ("blocks::dialog::Dialog::lead(lead)", true),
    ("blocks::disclosure::Disclosure::new(label)", true),
    ("blocks::disclosure::Disclosure::region(name)", false),
    ("blocks::disclosure::Disclosure::beside(said)", false),
    ("blocks::disclosure::Disclosure::tags(tags)", true),
    ("blocks::facts::Facts::fact(label)", true),
    ("blocks::facts::Facts::fact(value)", true),
    ("blocks::facts::Facts::code(label)", true),
    ("blocks::facts::Facts::code(value)", true),
    ("blocks::facts::Facts::item(label)", true),
    ("blocks::facts::Facts::control(label)", true),
    ("blocks::facts::Facts::list(name)", false),
    ("blocks::field::Field::new(label)", true),
    ("blocks::field::Field::value(value)", true),
    ("blocks::field::Field::note(note)", true),
    ("blocks::field::Field::hint(hint)", true),
    ("blocks::field::Field::error(error)", true),
    ("blocks::inspector::Inspector::new(title)", true),
    ("blocks::inspector::Inspector::side(label)", false),
    ("blocks::inspector::value(said)", true),
    ("blocks::inspector::aside(said)", true),
    ("blocks::list::List::new(label)", true),
    ("blocks::list::List::chooser(chosen)", false),
    ("blocks::list::List::empty(said)", true),
    ("blocks::menu::Menu::new(label)", false),
    ("blocks::notice::Notice::error(said)", true),
    ("blocks::notice::Notice::warn(said)", true),
    ("blocks::notice::Notice::info(said)", true),
    ("blocks::notice::Status::new(said)", true),
    ("blocks::notice::Status::aside(aside)", true),
    ("blocks::notice::Refusal::new(reason)", true),
    ("blocks::page::Page::status(said)", true),
    ("blocks::page::Page::group(label)", false),
    ("blocks::page::Head::new(title)", true),
    ("blocks::page::Head::lead(lead)", true),
    ("blocks::popover::Popover::new(title)", true),
    ("blocks::prose::Prose::lead(said)", true),
    ("blocks::prose::Prose::empty(said)", true),
    ("blocks::prose::Prose::hint(said)", true),
    ("blocks::prose::Prose::code(value)", false),
    ("blocks::section::Section::new(title)", true),
    ("blocks::section::Section::description(said)", true),
    ("blocks::section::Section::aside(said)", true),
    ("blocks::section::Section::region(name)", false),
    ("blocks::setting_row::SettingRow::new(title)", true),
    ("blocks::setting_row::SettingRow::new(copy)", true),
    ("blocks::sheet::Sheet::new(title)", true),
    ("blocks::sheet::Sheet::article(label)", false),
    ("dialog::Dialog::new(title)", false),
    ("dialog::Dialog::subtitle(subtitle)", false),
    ("finder::empty(message)", true),
    ("finder::path_option(title)", false),
    ("finder::path_option(path)", false),
    ("finder::Hint::keys(before)", true),
    ("finder::Hint::keys(keys)", false),
    ("finder::Hint::keys(after)", true),
    ("finder::Hint::plain(said)", true),
    ("finder::Hint::pieces(pieces)", true),
    ("finder::footer(finder)", false),
    ("finder::title(said)", true),
    ("finder::ghost(typed)", false),
    ("finder::ghost(rest)", false),
    ("finder::chip(said)", true),
    ("finder::grouped(label)", false),
    ("glyph_button::GlyphButton::new(label)", false),
    ("glyph_button::GlyphButton::help(help)", false),
    ("keycap::Keys::new(caps)", false),
    ("keycap::Keys::raised(caps)", false),
    ("keycap::Keys::outlined(caps)", false),
    ("keycap::Keys::ledged(caps)", false),
    ("system::attachment::file_chip(name)", true),
    ("system::attachment::file_chip(kind)", true),
    ("system::attachment::file_chip(detail)", true),
    ("system::attachment::image_tile(name)", false),
    ("system::attachment::drop_overlay(said)", true),
    ("system::button::Button::new(words)", true),
    ("system::button::Button::help(help)", false),
    ("system::button::Button::shortcut(keys)", false),
    ("system::check::Check::new(name)", true),
    ("system::check::Check::note(note)", true),
    ("system::chip::keycaps(keys)", false),
    ("system::chip::keys_hint(keys)", false),
    ("system::chip::keys_hint(what)", true),
    ("system::chip::slot_badge(slot)", false),
    ("system::chip::chip_button(label)", false),
    ("system::choice::Choice::new(name)", true),
    ("system::choice::Choice::note(note)", true),
    ("system::choice::checks(label)", false),
    ("system::choice::choices(label)", false),
    ("system::composer::composer(placeholder)", false),
    ("system::control::Control::label(label)", false),
    ("system::disclosure::disclosure(label)", true),
    ("system::disclosure::disclosure(beside)", false),
    ("system::disclosure::disclosure(chips)", true),
    ("system::drop_zone::drop_zone(said)", true),
    ("system::expander::expander(said)", true),
    ("system::group_head::GroupHead::new(label)", false),
    ("system::group_head::GroupHead::name(name)", true),
    ("system::group_head::GroupHead::section(name)", true),
    ("system::group_head::GroupHead::keys(keys)", false),
    ("system::icon_button::icon_button(label)", false),
    ("system::icon_button::IconButton::count(count)", false),
    ("system::icon_button::IconButton::keys(keys)", false),
    ("system::icon_button::IconButton::help(help)", false),
    ("system::list_row::ListRow::new(label)", false),
    ("system::list_row::ListRow::note(said)", true),
    ("system::list_row::ListRow::title(title)", true),
    ("system::list_row::ListRow::quiet_title(title)", true),
    ("system::list_row::ListRow::subtitle(subtitle)", true),
    ("system::list_row::ListRow::subtitle_keyed(subtitle)", true),
    ("system::list_row::ListRow::keys(keys)", false),
    ("system::list_row::ListRow::select(label)", false),
    ("system::list_row::ListRow::item_value(value)", false),
    ("system::list_row::ListRow::value(value)", false),
    ("system::list_row::ListRow::description(description)", false),
    ("system::pager::pager((key, dot))", false),
    ("system::press_area::press_area(label)", false),
    ("system::said::Said::then_code(code)", false),
    ("system::segmented::Segment::new(name)", true),
    ("system::segmented::Segment::help(help)", false),
    ("system::segmented::Segmented::new(label)", false),
    ("system::stepper::stepper(what)", false),
    ("system::stepper::stepper(value)", true),
    ("system::switch::switch(label)", false),
    ("system::tag::pill(said)", true),
    ("system::tag::tag(said)", true),
    ("system::trigger::Trigger::new(label)", false),
    ("system::trigger::Trigger::value(value)", false),
    ("system::type_role::words(words)", true),
    ("type_::upper(label)", false),
    ("type_::caps(label)", false),
    ("type_::sans_caps(label)", false),
];

#[test]
fn every_public_text_input_has_a_row_saying_how_it_draws() {
    let found = public_text_inputs();
    let table: Vec<(String, bool)> = TEXT_INPUTS
        .iter()
        .map(|(name, said)| ((*name).to_owned(), *said))
        .collect();
    let missing: Vec<_> = found.iter().filter(|row| !table.contains(row)).collect();
    let stale: Vec<_> = table.iter().filter(|row| !found.contains(row)).collect();
    assert!(
        missing.is_empty() && stale.is_empty(),
        "public text inputs without their row (said = parses copy):\n{}\nrows with no such input:\n{}",
        missing
            .iter()
            .map(|(n, s)| format!("    (\"{n}\", {s}),"))
            .collect::<Vec<_>>()
            .join("\n"),
        stale
            .iter()
            .map(|(n, s)| format!("    (\"{n}\", {s}),"))
            .collect::<Vec<_>>()
            .join("\n")
    );
    assert_eq!(table.len(), found.len());
}

fn named_facts_view(_: &(), cx: &mut Cx<()>) -> El {
    use attn_ui::blocks::{Facts, Page};
    let t = attn_ui::colors::scheme_for(None)
        .colors(cx.system_dark())
        .ui;
    let facts = |key: &str| {
        Facts::new(key)
            .list("Capabilities")
            .fact(format!("{key}.resume"), "Resume", "Supported")
            .code(format!("{key}.pin"), "Model pin", "Unavailable")
    };
    Page::new()
        .push(facts("lines").build(&t))
        .push(facts("tiles").tiles().build(&t))
        .build("page", &t)
}

fn a11y_node<'a>(node: &'a serde_json::Value, key: &str) -> Option<&'a serde_json::Value> {
    if node["key"].as_str() == Some(key) {
        return Some(node);
    }
    node["children"]
        .as_array()
        .into_iter()
        .flatten()
        .find_map(|child| a11y_node(child, key))
}

#[test]
fn named_facts_are_a_list_of_their_facts_as_lines_and_as_tiles() {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("named facts", (900.0, 900.0), || (), named_facts_view)
    })
    .headless_runtime((900.0, 900.0), Env::new(true, SCALE));
    while rt.frame(1.0 / 60.0) {}
    let tree = rt.accessibility_json();
    for arrangement in ["lines", "tiles"] {
        let list = a11y_node(&tree, arrangement)
            .unwrap_or_else(|| panic!("no {arrangement} facts in {tree:#}"));
        let items: Vec<(&str, Option<&str>)> = list["children"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|item| (item["role"].as_str().unwrap_or(""), item["name"].as_str()))
            .collect();
        assert_eq!(
            (list["role"].as_str(), list["name"].as_str(), items),
            (
                Some("list"),
                Some("Capabilities"),
                vec![
                    ("list-item", Some("Resume Supported")),
                    ("list-item", Some("Model pin Unavailable")),
                ]
            ),
            "{arrangement}"
        );
    }
}
