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
        ("Components/Dialog", "dialog"),
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
