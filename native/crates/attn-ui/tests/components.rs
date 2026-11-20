use attn_ui::{STORIES, Showcase};
use rune::{Env, Runtime};
use serde_json::Value;

fn nodes(node: &Value, out: &mut Vec<(String, Option<String>)>) {
    if let Some(role) = node["role"].as_str() {
        out.push((role.to_owned(), node["name"].as_str().map(str::to_owned)));
    }
    for child in node["children"].as_array().into_iter().flatten() {
        nodes(child, out);
    }
}

#[test]
fn every_shared_component_has_its_role_and_a_name_in_dark_and_light() {
    let expected = [
        ("Components/List row", "list-item"),
        ("Components/Button", "button"),
        ("Components/Field", "text-input"),
        ("Components/Menu", "menu"),
        ("Components/Popover", "dialog"),
        ("Components/Toast", "status"),
        ("Components/Tooltip", "tooltip"),
    ];
    assert_eq!(STORIES.len(), expected.len());
    for (&(story, view), (name, role)) in STORIES.iter().zip(expected) {
        assert_eq!(story, name);
        for light in [false, true] {
            let env = Env {
                system_dark: !light,
                scale: 2.0,
            };
            let mut runtime = Runtime::new(Showcase::default(), view, (800.0, 600.0), env);
            runtime.frame(0.0);
            let mut found = Vec::new();
            nodes(&runtime.accessibility_json(), &mut found);
            assert!(
                found
                    .iter()
                    .any(|(r, n)| r == role && n.as_deref().is_some_and(|n| !n.is_empty())),
                "{story} shows no named {role}: {found:?}"
            );
        }
    }
}

fn over(top: rune::Color, bottom: rune::Color) -> rune::Color {
    use rune::theme::{srgb_decode, srgb_encode};
    let mix = |t: f32, b: f32| srgb_decode(srgb_encode(t) * top.a + srgb_encode(b) * (1.0 - top.a));
    rune::Color {
        r: mix(top.r, bottom.r),
        g: mix(top.g, bottom.g),
        b: mix(top.b, bottom.b),
        a: 1.0,
    }
}

fn contrast(a: rune::Color, b: rune::Color) -> f32 {
    let (a, b) = (a.luminance(), b.luminance());
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

#[test]
fn primary_button_text_reads_at_aa_contrast_on_every_enabled_fill_in_dark_and_light() {
    for dark in [true, false] {
        let kit = attn_ui::kit(&attn_ui::colors::scheme_for(None).colors(dark).ui);
        let c = &kit.c;
        let fill = c.bg_icon_button_contrast;
        let fills = [
            ("rest", fill),
            ("hover", over(kit.on(c.submit_hover, fill), fill)),
            ("highlight", over(kit.on(c.submit_highlight, fill), fill)),
            ("pressed", over(kit.on(c.contrast_pressed, fill), fill)),
        ];
        for (state, behind) in fills {
            for (part, ink) in [("text", c.submit_fg), ("icon", c.submit_icon)] {
                let ratio = contrast(ink, behind);
                assert!(
                    ratio >= 4.5,
                    "dark={dark}: primary button {part} on its {state} fill {} is {ratio:.2}:1",
                    behind.hex()
                );
            }
        }
    }
}

#[test]
fn accent_badge_text_is_the_schemes_ink_on_its_accent_and_reads_at_aa_in_classic_light() {
    fn badge(&(scheme, dark): &(usize, bool), _: &mut rune::Cx<(usize, bool)>) -> rune::El {
        let kit = attn_ui::kit(&attn_ui::colors::SCHEMES[scheme].colors(dark).ui);
        rune::row().child(
            rune_kit::Badge::new("Customized")
                .variant(rune_kit::BadgeVariant::Accent)
                .build(kit)
                .key("badge"),
        )
    }
    for (scheme, colors) in attn_ui::colors::SCHEMES.iter().enumerate() {
        for dark in [true, false] {
            let ui = &colors.colors(dark).ui;
            let mut runtime =
                Runtime::new((scheme, dark), badge, (300.0, 100.0), Env::new(dark, 2.0));
            runtime.frame(0.0);
            let elements = runtime.elements();
            let find = |path: &str| {
                elements
                    .iter()
                    .find(|e| e.path.ends_with(path))
                    .unwrap_or_else(|| panic!("no {path}"))
            };
            let fill = find("badge").fill;
            let ink = find("badge/label").text_color.expect("the badge's ink");
            let id = colors.id;
            assert_eq!((fill, ink), (ui.accent, ui.on_accent), "{id} dark={dark}");
            if id == "classic" {
                let ratio = contrast(ink, fill);
                assert!(
                    ratio >= 4.5,
                    "{id} dark={dark}: accent badge text {} on {} is {ratio:.2}:1",
                    ink.hex(),
                    fill.hex()
                );
            }
        }
    }
}

struct Opening {
    open: bool,
    mark_alone: bool,
}

fn opening_view(state: &Opening, _: &mut rune::Cx<Opening>) -> rune::El {
    let t = attn_ui::colors::scheme_for(None).colors(true).ui;
    let panel = rune::col().token_motion(attn_ui::kit(&t).motion.overlay);
    if state.mark_alone {
        return panel.child(attn_ui::system::opens_in_place(state.open, &t));
    }
    panel
        .child(
            attn_ui::system::expander(
                "expander",
                "Models",
                state.open,
                false,
                &t,
                |_: &mut Opening, _| {},
            )
            .into(),
        )
        .child(
            attn_ui::system::disclosure(
                "disclosure",
                "Agents",
                state.open,
                None,
                &[],
                false,
                false,
                &t,
                |_: &mut Opening, _| {},
            )
            .into(),
        )
        .child(
            attn_ui::blocks::Page::new()
                .push(
                    attn_ui::blocks::Disclosure::new(
                        "block",
                        "Models",
                        state.open,
                        |_: &mut Opening, _| {},
                    )
                    .push(attn_ui::blocks::Prose::lead("block.prose", "Inside").build(&t))
                    .build(&t),
                )
                .build("page", &t),
        )
}

fn opening(mark_alone: bool, reduced_motion: bool) -> Runtime<Opening> {
    let mut rt = rune::App::new(rune::AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..rune::AppSpec::new(
            "opening",
            (400.0, 200.0),
            || Opening {
                open: false,
                mark_alone: false,
            },
            opening_view,
        )
    })
    .headless_runtime((400.0, 200.0), Env::new(true, 2.0));
    rt.update(move |state, _| state.mark_alone = mark_alone);
    rt.set_system_reduced_motion(reduced_motion);
    while rt.frame(1.0 / 60.0) {}
    rt
}

fn chevrons_pointing_right(rt: &Runtime<Opening>) -> Result<usize, f32> {
    let closed = -std::f32::consts::FRAC_PI_2;
    let mut right = 0;
    for instance in &rt.instances {
        let turn = instance.params[0];
        if instance.params[1] != rune::render::KIND_GLYPH || turn == 0.0 {
            continue;
        }
        if turn != closed {
            return Err(turn);
        }
        right += 1;
    }
    Ok(right)
}

#[test]
fn the_opens_in_place_chevron_turns_at_once_and_asks_for_no_further_frame() {
    for reduced_motion in [false, true] {
        let mut controls = opening(false, reduced_motion);
        let mut mark = opening(true, reduced_motion);
        assert_eq!(chevrons_pointing_right(&controls), Ok(3));
        assert_eq!(chevrons_pointing_right(&mark), Ok(1));
        for open in [true, false] {
            let case = format!("open {open}, reduced motion {reduced_motion}");
            controls.update(move |state, _| state.open = open);
            controls.frame(1.0 / 60.0);
            let right = if open { 0 } else { 3 };
            assert_eq!(chevrons_pointing_right(&controls), Ok(right), "{case}");
            mark.update(move |state, _| state.open = open);
            let more = mark.frame(1.0 / 60.0);
            assert_eq!(chevrons_pointing_right(&mark), Ok(right / 3), "{case}");
            assert!(!more, "{case}: the mark repaints after its turn");
        }
    }
}
