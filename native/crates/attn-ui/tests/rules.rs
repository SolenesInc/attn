//! The rules the library's controls hold, read from the gallery's stories: rectangles from the
//! element tree, colours and clips from what the frame draws.

use attn_ui::Tokens;
use attn_ui::colors::scheme_for;
use attn_ui::gallery::{FIELD_BOXES, SUBJECTS};
use rune::render::Instance;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1100.0, 800.0);
const SCALE: f32 = 2.0;
const REGULAR_INSET: f32 = 12.0;
const RING_REACH: f32 = 4.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(dark: bool, name: &str, variant: &str) -> Runtime<()> {
    story_at(dark, name, Some(variant))
}

fn story_at(dark: bool, name: &str, variant: Option<&str>) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(dark, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, variant);
    rt
}

fn tokens(dark: bool) -> Tokens {
    scheme_for(None).colors(dark).ui
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

/// The element keyed `key` inside the element keyed `within`.
fn inside(rt: &Runtime<()>, within: &str, key: &str) -> ElementInfo {
    let all = rt.elements();
    let outer = element(rt, within);
    let holds = |e: &ElementInfo| {
        std::iter::successors(e.parent, |&p| all[p].parent).any(|p| all[p].path == outer.path)
    };
    all.iter()
        .find(|e| e.key.as_deref() == Some(key) && holds(e))
        .cloned()
        .unwrap_or_else(|| panic!("no {key} inside {within}"))
}

fn device(rect: [f32; 4]) -> [f32; 4] {
    rect.map(|v| v * SCALE)
}

fn near(a: [f32; 4], b: [f32; 4]) -> bool {
    a.iter().zip(b).all(|(a, b)| (a - b).abs() <= 1.0)
}

/// What the frame paints in the box of `e` over `ground`: every fill drawn at its rectangle, in
/// order, as the hover and pressed layers blend into them.
fn surface(rt: &Runtime<()>, e: &ElementInfo, ground: rune::Color) -> [f32; 3] {
    let ground = [ground.r, ground.g, ground.b].map(rune::theme::srgb_encode);
    rt.instances
        .iter()
        .filter(|i| near(i.rect, device(e.rect)))
        .fold(ground, |below, i| over(i.fill, below))
}

/// `top`, premultiplied as the frame draws it, over `below`.
fn over(top: [f32; 4], below: [f32; 3]) -> [f32; 3] {
    [0, 1, 2].map(|c| top[c] + below[c] * (1.0 - top[3]))
}

fn luminance(srgb: [f32; 3]) -> f32 {
    let [r, g, b] = srgb.map(rune::theme::srgb_decode);
    0.2126 * r + 0.7152 * g + 0.0722 * b
}

fn contrast(a: [f32; 3], b: [f32; 3]) -> f32 {
    let (a, b) = (luminance(a), luminance(b));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

#[test]
fn text_sits_at_one_inset_in_every_field_box() {
    for dark in [true, false] {
        for variant in ["Empty", "Filled", "Long text", "Narrow"] {
            let rt = story(dark, "Library/Field box", variant);
            for key in FIELD_BOXES {
                let field = element(&rt, key);
                let words = element(&rt, &format!("{key}.input"));
                let inset = words.rect[0] - field.rect[0];
                assert!(
                    (inset - REGULAR_INSET).abs() < 0.5,
                    "{variant}, dark={dark}: {key}'s text sits {inset} from its edge, not {REGULAR_INSET}"
                );
            }
        }
    }
}

#[test]
fn every_number_field_takes_one_width_whatever_it_holds() {
    let mut widths = Vec::new();
    for variant in ["Empty", "Filled", "Long text"] {
        let rt = story(true, "Library/Field box", variant);
        widths.push((variant, element(&rt, "field.number").rect[2]));
    }
    let rt = story(true, "Library/Stepper", "Rest");
    widths.push(("stepper", element(&rt, "stepper.number").rect[2]));
    let first = widths[0].1;
    assert!(
        widths.iter().all(|(_, w)| (w - first).abs() < 0.5),
        "number fields at several widths: {widths:?}"
    );
}

#[test]
fn a_steppers_value_keeps_one_width_so_steppers_line_up() {
    let rt = story(true, "Library/Stepper", "Rest");
    let short = element(&rt, "stepper.scale.value");
    let long = element(&rt, "stepper.match.value");
    assert!(
        (short.rect[2] - long.rect[2]).abs() < 0.5,
        "\"100%\" is {} wide and \"Match app\" {}",
        short.rect[2],
        long.rect[2]
    );
    let up = |key: &str| element(&rt, key).rect[0];
    assert!(
        (up("stepper.scale.up") - up("stepper.match.up")).abs() < 0.5,
        "the two steppers' up buttons start at {} and {}",
        up("stepper.scale.up"),
        up("stepper.match.up")
    );
}

/// The warm ring drawn round `e`: outside its edge, or over it for a control in a tight list.
fn ring_of<'a>(rt: &'a Runtime<()>, e: &ElementInfo, t: &Tokens) -> Option<&'a Instance> {
    let warm = [t.warning.r, t.warning.g, t.warning.b].map(rune::theme::srgb_encode);
    let [x, y, w, h] = device(e.rect);
    let reach = RING_REACH * SCALE;
    rt.instances.iter().find(|i| {
        let [ix, iy, iw, ih] = i.rect;
        let around = |inner: f32, outer: f32| (outer - inner).abs() <= reach + 1.0;
        around(x, ix)
            && around(y, iy)
            && around(x + w, ix + iw)
            && around(y + h, iy + ih)
            && (0..3).all(|c| (i.border[c] - warm[c]).abs() < 0.01)
            && i.params[0] > 0.0
    })
}

/// A field shows focus with its own warm edge, not a ring round it.
fn is_text_editor(e: &ElementInfo) -> bool {
    e.caret.is_some() || e.role.contains("text")
}

#[test]
fn every_focus_ring_is_drawn_whole_and_undimmed() {
    let stories = std::iter::once("Library/Button")
        .chain(SUBJECTS.iter().map(|(name, _)| *name))
        .filter(|name| !matches!(*name, "Library/Tag" | "Library/Icon box"));
    for name in stories {
        for dark in [true, false] {
            let t = tokens(dark);
            let rt = story(dark, name, "Focus");
            let focusable: Vec<ElementInfo> = rt
                .elements()
                .into_iter()
                .filter(|e| e.focusable && !is_text_editor(e))
                .filter(|e| rt.playground_element(&e.path).is_some())
                .collect();
            assert!(!focusable.is_empty(), "{name} draws nothing focusable");
            for e in focusable {
                let ring = ring_of(&rt, &e, &t)
                    .unwrap_or_else(|| panic!("{name}, dark={dark}: {:?} draws no ring", e.key));
                let [x, y, w, h] = ring.rect;
                let [x0, y0, x1, y1] = ring.clip;
                assert!(
                    x0 <= x && y0 <= y && x + w <= x1 && y + h <= y1,
                    "{name}, dark={dark}: {:?}'s ring {:?} is cut by {:?}",
                    e.key,
                    ring.rect,
                    ring.clip
                );
                assert!(
                    ring.border[3] >= 0.999 && e.opacity >= 0.999,
                    "{name}, dark={dark}: {:?}'s ring is dimmed",
                    e.key
                );
            }
        }
    }
}

/// Each control a pointer can press, by story and the key of its pressed surface (`a/b` is `b`
/// inside `a`).
const PRESSABLE: &[(&str, &str)] = &[
    ("Library/Button", "button.primary.regular"),
    ("Library/Button", "button.plain.regular"),
    ("Library/Button", "button.quiet.regular"),
    ("Library/Button", "button.danger.regular"),
    ("Library/Switch", "switch.off/control"),
    ("Library/Switch", "switch.on/control"),
    ("Library/Segmented", "segmented.theme/segment1"),
    ("Library/Stepper", "stepper.scale.up"),
    ("Library/Stepper", "stepper.number.up"),
    ("Library/Expander", "expander"),
    ("Library/Choice", "choice.1"),
    ("Library/Press area", "press-area"),
    ("Library/Disclosure", "disclosure"),
];

/// The steps F5 states for a list row, hover then pressed against rest, in dark and in light.
const ROW_STEPS_DARK: (f32, f32) = (1.20, 1.66);
const ROW_STEPS_LIGHT: (f32, f32) = (1.17, 1.50);
/// Blending in 8-bit steps and the layers' search land a hundredth either side.
const ROUNDING: f32 = 0.01;

#[test]
fn hover_and_pressed_show_on_every_control_in_dark_and_light() {
    let mut measured = Vec::new();
    for &(name, subject) in PRESSABLE {
        for dark in [true, false] {
            let t = tokens(dark);
            let (hover_floor, pressed_floor) = if dark {
                ROW_STEPS_DARK
            } else {
                ROW_STEPS_LIGHT
            };
            let mut rt = story_at(dark, name, None);
            let find = |rt: &Runtime<()>| match subject.split_once('/') {
                Some((within, key)) => inside(rt, within, key),
                None => element(rt, subject),
            };
            let e = find(&rt);
            let rest = surface(&rt, &e, t.bg_panel);
            let (x, y) = (e.rect[0] + e.rect[2] / 2.0, e.rect[1] + e.rect[3] / 2.0);
            rt.pointer_move(x, y);
            rt.frame(1.0);
            rt.frame(1.0);
            let hover = surface(&rt, &find(&rt), t.bg_panel);
            rt.pointer_down(x, y);
            rt.frame(1.0);
            rt.frame(1.0);
            let pressed = surface(&rt, &find(&rt), t.bg_panel);
            let (h, p) = (contrast(rest, hover), contrast(rest, pressed));
            measured.push(format!(
                "{subject} dark={dark}: hover {h:.2}:1, pressed {p:.2}:1"
            ));
            assert!(
                h >= hover_floor - ROUNDING && p >= pressed_floor - ROUNDING,
                "{subject}, dark={dark}: hover {h:.2}:1 and pressed {p:.2}:1 against rest"
            );
        }
    }
    println!("{}", measured.join("\n"));
}

fn click(rt: &mut Runtime<()>, e: &ElementInfo) {
    let (x, y) = (e.rect[0] + e.rect[2] / 2.0, e.rect[1] + e.rect[3] / 2.0);
    rt.pointer_move(x, y);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(1.0);
    rt.frame(1.0);
}

fn hover(rt: &mut Runtime<()>, e: &ElementInfo) {
    rt.pointer_move(e.rect[0] + e.rect[2] / 2.0, e.rect[1] + e.rect[3] / 2.0);
    rt.frame(1.0);
    rt.frame(1.0);
}

/// Every box the frame draws for `e` itself, ring aside: whatever sits within a point or two of
/// its rectangle, in points.
fn drawn_edges(rt: &Runtime<()>, e: &ElementInfo) -> Vec<[f32; 4]> {
    let [x, y, w, h] = e.rect;
    rt.instances
        .iter()
        .map(|i| i.rect.map(|v| v / SCALE))
        .filter(|[ix, iy, iw, ih]| {
            (ix - x).abs() <= 1.5
                && (iy - y).abs() <= 1.5
                && (ix + iw - x - w).abs() <= 1.5
                && (iy + ih - y - h).abs() <= 1.5
        })
        .collect()
}

#[test]
fn focus_and_every_other_state_leave_a_fields_edge_where_it_was() {
    for dark in [true, false] {
        let rest = story(dark, "Library/Field box", "Filled");
        for key in FIELD_BOXES {
            let at_rest = element(&rest, key).rect;
            let mut states: Vec<(&str, Runtime<()>)> = Vec::new();
            for variant in ["Filled", "Failed", "Disabled", "Disabled empty"] {
                states.push((variant, story(dark, "Library/Field box", variant)));
            }
            let mut hovered = story(dark, "Library/Field box", "Filled");
            let e = element(&hovered, key);
            hover(&mut hovered, &e);
            states.push(("Hover", hovered));
            let mut focused = story(dark, "Library/Field box", "Filled");
            let e = element(&focused, key);
            click(&mut focused, &e);
            states.push(("Focus", focused));
            for (state, rt) in &states {
                let e = element(rt, key);
                assert!(
                    near(e.rect, at_rest),
                    "{key} {state}, dark={dark}: laid out at {:?}, at rest {at_rest:?}",
                    e.rect
                );
                for edge in drawn_edges(rt, &e) {
                    assert!(
                        edge.iter().zip(at_rest).all(|(a, b)| (a - b).abs() < 0.26),
                        "{key} {state}, dark={dark}: draws an edge at {edge:?}, its box is {at_rest:?}"
                    );
                }
            }
        }
    }
}

#[test]
fn a_focused_field_draws_the_ring_every_control_draws() {
    for dark in [true, false] {
        let t = tokens(dark);
        for key in FIELD_BOXES {
            let mut rt = story(dark, "Library/Field box", "Filled");
            let e = element(&rt, key);
            click(&mut rt, &e);
            let field = element(&rt, key);
            let ring = ring_of(&rt, &field, &t)
                .unwrap_or_else(|| panic!("{key}, dark={dark}: focused, draws no ring"));
            let [x, y, w, h] = ring.rect.map(|v| v / SCALE);
            let reach = field.rect[0] - x;
            assert!(
                (reach - RING_REACH).abs() < 0.5
                    && (field.rect[1] - y - RING_REACH).abs() < 0.5
                    && (x + w - field.rect[0] - field.rect[2] - RING_REACH).abs() < 0.5
                    && (y + h - field.rect[1] - field.rect[3] - RING_REACH).abs() < 0.5,
                "{key}, dark={dark}: ring {:?} does not stand {RING_REACH} outside {:?}",
                [x, y, w, h],
                field.rect
            );
        }
    }
}

#[test]
fn a_field_beside_a_regular_button_shares_its_top_and_bottom() {
    for dark in [true, false] {
        let rt = story(dark, "Library/Baseline", "Rest");
        let button = element(&rt, "baseline.plain");
        for key in [
            "baseline.primary",
            "baseline.quiet",
            "baseline.line",
            "baseline.number",
        ] {
            let e = element(&rt, key);
            let drawn = drawn_edges(&rt, &e);
            let (top, bottom) = drawn
                .iter()
                .fold((e.rect[1], e.rect[1] + e.rect[3]), |(t, b), r| {
                    (t.min(r[1]), b.max(r[1] + r[3]))
                });
            assert!(
                (top - button.rect[1]).abs() < 0.26
                    && (bottom - button.rect[1] - button.rect[3]).abs() < 0.26,
                "dark={dark}: {key} spans {top}..{bottom}, a Regular button {}..{}",
                button.rect[1],
                button.rect[1] + button.rect[3]
            );
        }
    }
}

#[test]
fn a_long_value_ends_at_the_inset() {
    for dark in [true, false] {
        for variant in ["Long text", "Narrow"] {
            let rt = story(dark, "Library/Field box", variant);
            for key in ["field.line", "field.number"] {
                let field = element(&rt, key);
                let value = inside(&rt, key, "value");
                let gap = field.rect[0] + field.rect[2] - (value.rect[0] + value.rect[2]);
                let trailing = rt.elements().iter().any(|e| {
                    e.key.as_deref() == Some("trailing")
                        && std::iter::successors(e.parent, |&p| rt.elements()[p].parent)
                            .any(|p| rt.elements()[p].path == field.path)
                });
                assert!(
                    trailing || (gap - REGULAR_INSET).abs() < 0.5,
                    "{variant}, dark={dark}: {key}'s value ends {gap} from its edge, not {REGULAR_INSET}"
                );
            }
        }
    }
}

#[test]
fn a_segmented_control_beside_words_keeps_its_own_width() {
    for dark in [true, false] {
        let rt = story(dark, "Library/Segmented", "Rest");
        let alone = element(&rt, "segmented.theme");
        let row = element(&rt, "segmented.row");
        let words = element(&rt, "segmented.description");
        let control = element(&rt, "segmented.beside");
        let ends = |e: &ElementInfo| e.rect[0] + e.rect[2];
        assert!(
            (ends(&control) - ends(&row)).abs() < 0.5 && words.rect[2] >= row.rect[2] / 2.0,
            "dark={dark}: the words take {} of the row's {}, the control {:?}",
            words.rect[2],
            row.rect[2],
            control.rect
        );
        assert!(
            ends(&words) <= control.rect[0],
            "dark={dark}: the control at {:?} covers the words ending at {}",
            control.rect,
            ends(&words)
        );
        assert!(
            alone.rect[2] < row.rect[2],
            "the control alone fills its column"
        );
    }
}
