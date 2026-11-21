//! The rules the library's controls hold, read from the gallery's stories: rectangles from the
//! element tree, colours and clips from what the frame draws.

use attn_ui::Tokens;
use attn_ui::blocks::BannerTone;
use attn_ui::colors::scheme_for;
use attn_ui::gallery::{BANNERS, BUTTONS, FIELD_BOXES, SIZES, SUBJECTS};
use rune::motion_gate::check::paint::{C_FILL, C_TEXT, Paint};
use rune::motion_gate::{Recording, Sampler};
use rune::render::Instance;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Key, KeyEvent, Mods, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
const SCALE: f32 = 2.0;
const REGULAR_INSET: f32 = 12.0;
const RING_REACH: f32 = 4.0;
const LOOSE_GAP: f32 = 8.0;

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
    let warm = [t.focus.r, t.focus.g, t.focus.b].map(rune::theme::srgb_encode);
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

/// How strongly the layers open around `instance` let it show: the product of their opacities.
fn strength_around(rt: &Runtime<()>, instance: &Instance) -> f32 {
    let at = rt
        .instances
        .iter()
        .position(|i| std::ptr::eq(i, instance))
        .expect("the instance is the frame's");
    let mut open: Vec<f32> = Vec::new();
    for i in &rt.instances[..at] {
        if i.params[1] == rune::render::KIND_LAYER {
            open.push(i.fill[3]);
        } else if i.params[1] == rune::render::KIND_LAYER_END {
            open.pop();
        }
    }
    open.iter().product()
}

/// A field shows focus with its own warm edge, not a ring round it.
fn is_text_editor(e: &ElementInfo) -> bool {
    e.caret.is_some() || e.role.contains("text")
}

#[test]
fn every_focus_ring_is_drawn_whole_and_undimmed() {
    let stories = std::iter::once("Library/Button")
        .chain(SUBJECTS.iter().map(|(name, _)| *name))
        .filter(|name| !matches!(*name, "Library/Tag" | "Library/Icon box"))
        .map(|name| (name, "Focus"))
        .chain(std::iter::once(("Library/Button", "Unavailable focus")));
    for (name, variant) in stories {
        let face_fades = variant == "Unavailable focus";
        for dark in [true, false] {
            let t = tokens(dark);
            let mut rt = story(dark, name, variant);
            let painted = paint_one_frame(&mut rt);
            let shown = rt.elements();
            let focusable: Vec<ElementInfo> = rt
                .elements()
                .into_iter()
                .filter(|e| e.focusable && !is_text_editor(e))
                .filter(|e| rt.playground_element(&e.path).is_some())
                .collect();
            assert!(!focusable.is_empty(), "{name} draws nothing focusable");
            for e in focusable {
                let ring = ring_of(&rt, &e, &t).unwrap_or_else(|| {
                    panic!("{name} {variant}, dark={dark}: {:?} draws no ring", e.key)
                });
                let [x, y, w, h] = ring.rect;
                let [x0, y0, x1, y1] = ring.clip;
                assert!(
                    x0 <= x && y0 <= y && x + w <= x1 && y + h <= y1,
                    "{name} {variant}, dark={dark}: {:?}'s ring {:?} is cut by {:?}",
                    e.key,
                    ring.rect,
                    ring.clip
                );
                let shown_at = least_fade_within(&painted, &shown, &e);
                assert!(
                    ring.border[3] >= 0.999
                        && e.opacity >= 0.999
                        && strength_around(&rt, ring) >= 0.999
                        && (face_fades || shown_at >= 0.999),
                    "{name} {variant}, dark={dark}: {:?}'s ring is dimmed (it shows at {shown_at})",
                    e.key
                );
            }
        }
    }
}

/// What every element paints on one frame, with the fades of the layers it sits in.
fn paint_one_frame(rt: &mut Runtime<()>) -> Recording {
    let mut sampler = Sampler::new(rt, 60.0);
    sampler.before(rt);
    let awake = rt.frame(1.0);
    sampler.frame(rt, awake);
    sampler.rec
}

fn painted<'a>(rec: &'a Recording, e: &ElementInfo) -> Option<&'a Paint> {
    rec.tracks
        .iter()
        .find(|track| track.path == e.path)
        .and_then(|track| track.paint_at(0))
}

/// How much of its own colour the least faded part of `control` shows: a fade on `control`
/// reaches all it holds, so nothing inside shows more than the ring does.
fn least_fade_within(rec: &Recording, shown: &[ElementInfo], control: &ElementInfo) -> f32 {
    let inside = format!("{}/", control.path);
    shown
        .iter()
        .filter(|e| e.path == control.path || e.path.starts_with(&inside))
        .filter_map(|e| {
            let paint = painted(rec, e)?;
            let fill = (e.fill.a > 0.01).then(|| paint.colors[C_FILL].a / e.fill.a);
            let ink = e
                .text_color
                .filter(|c| c.a > 0.01)
                .map(|c| paint.colors[C_TEXT].a / c.a);
            fill.into_iter().chain(ink).reduce(f32::max)
        })
        .reduce(f32::max)
        .unwrap_or(0.0)
}

/// Each control a pointer can press, by story and the key of its pressed surface (`a/b` is `b`
/// inside `a`).
const PRESSABLE: &[(&str, &str)] = &[
    ("Library/Button", "button.primary.regular"),
    ("Library/Button", "button.plain.regular"),
    ("Library/Button", "button.quiet.regular"),
    ("Library/Button", "button.danger.regular"),
    ("Library/Button", "button.brand.regular"),
    ("Library/Icon button", "icon-button.plain"),
    ("Library/Icon button", "icon-button.edged"),
    ("Library/Switch", "switch.off/control"),
    ("Library/Switch", "switch.on/control"),
    ("Library/Segmented", "segmented.theme/segment1"),
    ("Library/Stepper", "stepper.scale.up"),
    ("Library/Stepper", "stepper.number.up"),
    ("Library/Expander", "expander"),
    ("Library/Choice", "choice.1"),
    ("Library/Press area", "press-area"),
    ("Library/Disclosure", "disclosure"),
    ("Library/Trigger", "trigger.field"),
    ("Library/Trigger", "trigger.quiet"),
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

#[test]
fn in_light_hover_and_pressed_darken_every_control() {
    let t = tokens(false);
    let mut lightened = Vec::new();
    for &(name, subject) in PRESSABLE {
        let mut rt = story_at(false, name, None);
        let find = |rt: &Runtime<()>| match subject.split_once('/') {
            Some((within, key)) => inside(rt, within, key),
            None => element(rt, subject),
        };
        let e = find(&rt);
        let rest = luminance(surface(&rt, &e, t.bg_panel));
        let (x, y) = (e.rect[0] + e.rect[2] / 2.0, e.rect[1] + e.rect[3] / 2.0);
        rt.pointer_move(x, y);
        rt.frame(1.0);
        rt.frame(1.0);
        let hover = luminance(surface(&rt, &find(&rt), t.bg_panel));
        rt.pointer_down(x, y);
        rt.frame(1.0);
        rt.frame(1.0);
        let pressed = luminance(surface(&rt, &find(&rt), t.bg_panel));
        if !(hover < rest && pressed < hover) {
            lightened.push(format!(
                "{subject}: rest {rest:.3}, hover {hover:.3}, pressed {pressed:.3}"
            ));
        }
    }
    assert!(
        lightened.is_empty(),
        "in light, these do not darken from rest to hover to pressed: {lightened:#?}"
    );
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
    let boxes = FIELD_BOXES.map(|key| ("Library/Field box", "Filled", key));
    let held = [
        ("Blocks/Popover", "Rest", "popover.name.box"),
        ("Blocks/Popover", "Refused", "popover.name.box"),
    ];
    for dark in [true, false] {
        let t = tokens(dark);
        for (name, variant, key) in boxes.into_iter().chain(held) {
            let mut rt = if name.starts_with("Blocks/") {
                block_story(dark, name, variant)
            } else {
                story(dark, name, variant)
            };
            let e = element(&rt, key);
            click(&mut rt, &e);
            let field = element(&rt, key);
            let ring = ring_of(&rt, &field, &t)
                .unwrap_or_else(|| panic!("{key}, dark={dark}: focused, draws no ring"));
            let [rx, ry, rw, rh] = ring.rect;
            let [x0, y0, x1, y1] = ring.clip;
            assert!(
                x0 <= rx && y0 <= ry && rx + rw <= x1 && ry + rh <= y1,
                "{name} {variant}, dark={dark}: {key}'s ring {:?} is cut by {:?}",
                ring.rect,
                ring.clip
            );
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

/// Tab, as a person with keyboard navigation on does, until the keyboard is inside `key`.
fn tab_into(rt: &mut Runtime<()>, key: &str) {
    rt.set_full_keyboard_access(true);
    let path = element(rt, key).path;
    let stage = rt
        .elements()
        .into_iter()
        .find(|e| path.starts_with(&e.path) && e.path.ends_with("playground-stage-1"))
        .expect("the story has a stage");
    let (x, y) = (stage.rect[0] + 2.0, stage.rect[1] + 2.0);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.pointer_leave();
    rt.frame(1.0);
    for _ in 0..20 {
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
        if rt
            .elements()
            .iter()
            .any(|e| e.focused && e.path.starts_with(&path))
        {
            rt.frame(1.0);
            return;
        }
    }
    panic!("Tab never reached {key}");
}

/// Whether the frame draws an edge of `ink` exactly on `rect`, in points.
fn edge_of(rt: &Runtime<()>, rect: [f32; 4], ink: rune::Color) -> bool {
    let ink = [ink.r, ink.g, ink.b].map(rune::theme::srgb_encode);
    rt.instances.iter().any(|i| {
        near(i.rect, device(rect))
            && i.params[0] > 0.0
            && i.border[3] > 0.99
            && (0..3).all(|c| (i.border[c] - ink[c]).abs() < 0.01)
    })
}

#[test]
fn a_failed_field_keeps_its_red_edge_inside_the_ring_while_focused() {
    let mut lost = Vec::new();
    for dark in [true, false] {
        let t = tokens(dark);
        let red = t.error;
        for key in FIELD_BOXES {
            let mut rt = story(dark, "Library/Field box", "Failed");
            tab_into(&mut rt, key);
            let field = element(&rt, key);
            let ring = ring_of(&rt, &field, &t).is_some();
            let edge = edge_of(&rt, field.rect, red);
            println!("{key} dark={dark}: ring={ring} red_edge={edge}");
            if !(ring && edge) {
                lost.push(format!("{key} dark={dark}"));
            }
        }
    }
    assert!(
        lost.is_empty(),
        "failed and focused, these lose the ring or the red edge: {lost:?}"
    );
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

/// The glyphs a field draws: its value, or its placeholder when empty.
fn glyphs_in<'a>(rt: &'a Runtime<()>, field: &ElementInfo) -> Vec<(usize, &'a Instance)> {
    let [x, y, w, h] = device(field.rect);
    rt.instances
        .iter()
        .enumerate()
        .filter(|(_, i)| i.params[1] == rune::render::KIND_GLYPH)
        .filter(|(_, i)| {
            i.rect[0] >= x && i.rect[1] >= y && i.rect[1] + i.rect[3] <= y + h && i.rect[0] < x + w
        })
        .collect()
}

/// The widest fade the value of the field keyed `key` draws at its start and at its end, after
/// checking that every glyph of it is visible from inset to inset, `visible` in device px.
fn fades(rt: &Runtime<()>, key: &str, visible: [f32; 4], dark: bool, when: &str) -> [f32; 2] {
    let glyphs = glyphs_in(rt, &element(rt, key));
    assert!(
        !glyphs.is_empty(),
        "{key}, dark={dark}, {when}: no value drawn"
    );
    let mut faded = [0.0f32; 2];
    for (_, g) in glyphs {
        assert!(
            (g.clip[0] - visible[0]).abs() <= 1.0 && (g.clip[2] - visible[1]).abs() <= 1.0,
            "{key}, dark={dark}, {when}: the value is visible over {:?}, not from inset to inset {:?}",
            [g.clip[0], g.clip[2]],
            [visible[0], visible[1]]
        );
        let widths = rune::render::unpack_fade(g.fade).0;
        faded = [faded[0].max(widths[0]), faded[1].max(widths[2])];
    }
    faded
}

#[test]
fn a_long_value_fades_out_at_the_inset_on_both_sides() {
    for dark in [true, false] {
        for key in ["field.line", "field.number"] {
            let mut rt = story(dark, "Library/Field box", "Long text");
            let field = element(&rt, key);
            let visible = device([
                field.rect[0] + REGULAR_INSET,
                field.rect[0] + field.rect[2] - REGULAR_INSET,
                0.0,
                0.0,
            ]);
            let [start, end] = fades(&rt, key, visible, dark, "at rest");
            assert!(
                start == 0.0 && end > 0.0,
                "{key}, dark={dark}: at rest a long value fades at its end alone, not {start} at its start and {end} at its end"
            );
            tab_into(&mut rt, key);
            rt.key(KeyEvent {
                key: Key::End,
                mods: Mods::default(),
            });
            rt.frame(1.0);
            let [start, _] = fades(&rt, key, visible, dark, "scrolled to its end");
            assert!(
                start > 0.0,
                "{key}, dark={dark}: scrolled to its end, a long value fades at its start"
            );
        }
    }
}

#[test]
fn placeholder_value_and_disabled_value_are_inks_a_person_tells_apart() {
    for dark in [true, false] {
        let t = tokens(dark);
        let step = if dark {
            ROW_STEPS_DARK.0
        } else {
            ROW_STEPS_LIGHT.0
        };
        let ink = |variant: &'static str| {
            let rt = story(dark, "Library/Field box", variant);
            let field = element(&rt, "field.line");
            let (n, _) = glyphs_in(&rt, &field)
                .into_iter()
                .next()
                .unwrap_or_else(|| panic!("{variant}, dark={dark}: the field draws no words"));
            (variant, shown(&rt, Part::Fill(n), t.bg_panel))
        };
        let inks = [
            ink("Empty"),
            ink("Filled"),
            ink("Disabled"),
            ink("Disabled empty"),
        ];
        for (i, (a, ink_a)) in inks.iter().enumerate() {
            for (b, ink_b) in &inks[i + 1..] {
                let apart = contrast(*ink_a, *ink_b);
                assert!(
                    apart >= step - ROUNDING,
                    "dark={dark}: {a}'s ink {ink_a:?} and {b}'s {ink_b:?} stand {apart:.2}:1 apart, under the {step}:1 a row's hover takes"
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

#[test]
fn a_number_field_shows_a_seven_digit_value_whole() {
    for dark in [true, false] {
        let mut rt = story(dark, "Library/Stepper", "Wide value");
        let room = element(&rt, "stepper.number.input").rect[2];
        let (needed, _) = rt.text.measure("1000000", roles::BODY, SCALE);
        assert!(
            needed <= room,
            "dark={dark}: 1000000 needs {needed}, the field leaves {room} beside its arrows"
        );
    }
}

/// The segments of the track keyed `track`, in order, each with its words.
fn segments(rt: &Runtime<()>, track: &str) -> Vec<(ElementInfo, ElementInfo)> {
    let all = rt.elements();
    let outer = element(rt, track);
    all.iter()
        .filter(|e| e.role == "radio" && e.parent.is_some_and(|p| all[p].path == outer.path))
        .map(|segment| {
            let words = all
                .iter()
                .find(|w| w.text.is_some() && w.parent.is_some_and(|p| all[p].path == segment.path))
                .cloned()
                .unwrap_or_else(|| panic!("{track}: a segment draws no words"));
            (segment.clone(), words)
        })
        .collect()
}

/// Whether text `e`, as wide as its box, shows its whole first word, or all of it when short.
fn reads_its_first_word(rt: &mut Runtime<()>, e: &ElementInfo) -> bool {
    let all = e.text.clone().unwrap_or_default();
    let first = all.split_whitespace().next().unwrap_or_default();
    let shown = |rt: &mut Runtime<()>, words: &str| rt.text.measure(words, roles::CONTROL, SCALE).0;
    let room = e.rect[2] + 0.25;
    shown(rt, &all) <= room || shown(rt, &format!("{first}…")) <= room
}

#[test]
fn a_segmented_control_stacks_its_segments_where_one_line_would_cut_their_words() {
    for dark in [true, false] {
        let mut rt = story(dark, "Library/Segmented", "Narrow");
        let column = element(&rt, "segmented.column");
        for key in ["segmented.theme", "segmented.agent", "segmented.long"] {
            let track = element(&rt, key);
            let parts = segments(&rt, key);
            for (_, words) in &parts {
                assert!(
                    reads_its_first_word(&mut rt, words),
                    "dark={dark}: {key} cuts {:?} to {} points, short of its first word",
                    words.text,
                    words.rect[2]
                );
            }
            let one_line = parts.iter().all(|(s, _)| s.rect[1] == parts[0].0.rect[1]);
            if key == "segmented.theme" {
                assert!(
                    one_line,
                    "dark={dark}: {key} fits its column and still stacks"
                );
                continue;
            }
            assert!(
                !one_line,
                "dark={dark}: {key} keeps one line in a {}-point column",
                column.rect[2]
            );
            for (segment, _) in &parts {
                assert!(
                    (segment.rect[2] - parts[0].0.rect[2]).abs() < 0.5
                        && segment.rect[0] == parts[0].0.rect[0],
                    "dark={dark}: {key}'s stacked segments stand at {:?}",
                    parts.iter().map(|(s, _)| s.rect).collect::<Vec<_>>()
                );
            }
            assert!(
                track.rect[2] <= column.rect[2] + 0.5,
                "dark={dark}: stacked {key} is {} wide, past its column's {}",
                track.rect[2],
                column.rect[2]
            );
        }
    }
}

#[test]
fn a_stacked_segmented_control_moves_its_choice_with_every_arrow() {
    for dark in [true, false] {
        let mut rt = story(dark, "Library/Segmented", "Narrow");
        let first = tab_to(&mut rt, "segment0");
        assert!(
            first.path.contains("segmented.theme"),
            "Tab lands on {}",
            first.path
        );
        let chosen = |rt: &Runtime<()>| {
            segments(rt, "segmented.long")
                .iter()
                .position(|(s, _)| s.focused)
        };
        while chosen(&rt).is_none() {
            rt.key(KeyEvent {
                key: Key::Tab,
                mods: Mods::default(),
            });
            rt.frame(0.1);
        }
        for (arrow, to) in [
            (Key::Down, Some(1)),
            (Key::Up, Some(0)),
            (Key::Right, Some(1)),
            (Key::Left, Some(0)),
        ] {
            rt.key(KeyEvent {
                key: arrow.clone(),
                mods: Mods::default(),
            });
            rt.frame(0.1);
            assert_eq!(
                chosen(&rt),
                to,
                "dark={dark}: {arrow:?} on the stacked track"
            );
        }
    }
}

#[test]
fn a_segmented_control_stays_inside_its_column() {
    for dark in [true, false] {
        let rt = story(dark, "Library/Segmented", "Narrow");
        let column = element(&rt, "segmented.column");
        let track = device(element(&rt, "segmented.long").rect);
        let segments =
            ["segment0", "segment1"].map(|k| device(inside(&rt, "segmented.long", k).rect));
        for glyph in rt.instances.iter().filter(|i| {
            i.params[1] == rune::render::KIND_GLYPH
                && i.rect[1] >= track[1]
                && i.rect[1] + i.rect[3] <= track[1] + track[3]
                && i.rect[0] + i.rect[2] > track[0]
        }) {
            assert!(
                segments
                    .iter()
                    .any(|&[x, _, w, _]| glyph.rect[0] >= x
                        && glyph.rect[0] + glyph.rect[2] <= x + w),
                "dark={dark}: a label's glyph at {:?} is drawn outside every segment",
                glyph.rect.map(|v| v / SCALE)
            );
        }
        for key in ["segmented.theme", "segmented.agent", "segmented.long"] {
            let control = element(&rt, key);
            assert!(
                control.rect[0] + control.rect[2] <= column.rect[0] + column.rect[2] + 0.5,
                "dark={dark}: {key} ends at {}, past its column's {}",
                control.rect[0] + control.rect[2],
                column.rect[0] + column.rect[2]
            );
        }
    }
}

/// Each story with a Disabled variant, and the variant it dims.
const DISABLED: &[(&str, &str, &str)] = &[
    ("Library/Button", "Rest", "Disabled"),
    ("Library/Button", "Rest", "Unavailable"),
    ("Library/Switch", "Rest", "Disabled"),
    ("Library/Segmented", "Rest", "Disabled"),
    ("Library/Stepper", "Rest", "Disabled"),
    ("Library/Expander", "Closed", "Disabled"),
    ("Library/Choice", "First chosen", "Disabled"),
    ("Library/Press area", "Closed", "Disabled"),
    ("Library/Disclosure", "Closed", "Disabled"),
    ("Library/Trigger", "Rest", "Disabled"),
    ("Library/Field box", "Filled", "Disabled"),
];

/// The parts whose rest look is warm: disabled, they first turn neutral, so they are held to
/// that and not to their rest colour.
const WARM_AT_REST: &[&str] = &["switch.on", "choice.0"];

/// The surface's Primary action keeps its hue while unavailable, so a person sees where the
/// action will be: [`a_disabled_primary_keeps_its_hue_at_half_strength_and_its_label_reads_on_it`].
const KEEPS_ITS_HUE: &[&str] = &["button.primary.regular", "button.primary.compact"];

/// The half strength every unavailable control draws at.
const STRENGTH: f32 = 0.5;
/// Glyph atlases and 8-bit blending land within this of the exact blend.
const BLEND: f32 = 0.02;

/// A part the frame draws: an instance's fill at its centre, or its edge at the middle of its
/// leading side.
#[derive(Clone, Copy)]
enum Part {
    Fill(usize),
    Edge(usize),
}

fn parts(rt: &Runtime<()>) -> Vec<Part> {
    rt.instances
        .iter()
        .enumerate()
        .flat_map(|(n, i)| {
            let kind = i.params[1];
            let fill = (kind == rune::render::KIND_RECT || kind == rune::render::KIND_GLYPH)
                && i.fill[3] > 0.0;
            let edge = kind == rune::render::KIND_RECT && i.params[0] > 0.0 && i.border[3] > 0.0;
            fill.then_some(Part::Fill(n))
                .into_iter()
                .chain(edge.then_some(Part::Edge(n)))
        })
        .collect()
}

fn point_of(rt: &Runtime<()>, part: Part) -> (usize, [f32; 2], [f32; 4]) {
    match part {
        Part::Fill(n) => {
            let [x, y, w, h] = rt.instances[n].rect;
            (n, [x + w / 2.0, y + h / 2.0], rt.instances[n].fill)
        }
        Part::Edge(n) => {
            let i = &rt.instances[n];
            let [x, y, _, h] = i.rect;
            (n, [x + i.params[0] / 2.0, y + h / 2.0], i.border)
        }
    }
}

/// What the frame shows at `part`, over `ground`: every rectangle under its point drawn before
/// it, through the layers open around it, as the renderer blends them.
fn shown(rt: &Runtime<()>, part: Part, ground: rune::Color) -> [f32; 3] {
    let (last, [px, py], top) = point_of(rt, part);
    let ground = [ground.r, ground.g, ground.b].map(rune::theme::srgb_encode);
    let over4 =
        |top: [f32; 4], below: [f32; 4]| [0, 1, 2, 3].map(|c| top[c] + below[c] * (1.0 - top[3]));
    let mut current = [ground[0], ground[1], ground[2], 1.0];
    let mut open: Vec<([f32; 4], f32)> = Vec::new();
    let close = |current: [f32; 4], (below, opacity): ([f32; 4], f32)| {
        over4(current.map(|c| c * opacity), below)
    };
    for (n, i) in rt.instances[..=last].iter().enumerate() {
        let kind = i.params[1];
        if kind == rune::render::KIND_LAYER {
            open.push((current, i.fill[3]));
            current = [0.0; 4];
        } else if kind == rune::render::KIND_LAYER_END {
            let layer = open.pop().expect("a layer ends that began");
            current = close(current, layer);
        } else if n == last {
            current = over4(top, current);
        } else if kind == rune::render::KIND_RECT {
            let [x, y, w, h] = i.rect;
            if px >= x && px <= x + w && py >= y && py <= y + h {
                current = over4(i.fill, current);
            }
        }
    }
    while let Some(layer) = open.pop() {
        current = close(current, layer);
    }
    [current[0], current[1], current[2]]
}

fn same_part(a: &Instance, b: &Instance, edge: bool) -> bool {
    a.params[1] == b.params[1]
        && a.rect.iter().zip(b.rect).all(|(a, b)| (a - b).abs() <= 1.0)
        && (!edge || b.border[3] > 0.0)
        && (edge || b.fill[3] > 0.0)
}

fn within(rt: &Runtime<()>, keys: &[&str], instance: &Instance) -> bool {
    let [x, y, w, h] = instance.rect.map(|v| v / SCALE);
    keys.iter()
        .filter_map(|k| rt.playground_element(k))
        .any(|e| {
            let [ex, ey, ew, eh] = e.rect;
            x >= ex - 1.0 && y >= ey - 1.0 && x + w <= ex + ew + 1.0 && y + h <= ey + eh + 1.0
        })
}

/// The keys of the controls a story draws.
fn subjects(name: &str) -> Vec<String> {
    match name {
        "Library/Button" => BUTTONS
            .iter()
            .flat_map(|(look, _)| {
                SIZES
                    .iter()
                    .map(move |(size, _)| format!("button.{look}.{size}"))
            })
            .collect(),
        "Library/Field box" => FIELD_BOXES.map(str::to_owned).to_vec(),
        _ => SUBJECTS
            .iter()
            .find(|(story, _)| *story == name)
            .map(|(_, keys)| keys.iter().map(|k| (*k).to_owned()).collect())
            .unwrap_or_else(|| panic!("{name} names no subjects")),
    }
}

#[test]
fn disabled_draws_every_part_of_every_control_at_half_strength() {
    for &(name, rest_variant, disabled_variant) in DISABLED {
        for dark in [true, false] {
            let t = tokens(dark);
            let rest = story(dark, name, rest_variant);
            let disabled = story(dark, name, disabled_variant);
            let keys = subjects(name);
            let keys: Vec<&str> = keys.iter().map(String::as_str).collect();
            let ground = [t.bg_panel.r, t.bg_panel.g, t.bg_panel.b].map(rune::theme::srgb_encode);
            let text = [t.text_primary.r, t.text_primary.g, t.text_primary.b]
                .map(rune::theme::srgb_encode);
            let strongest = [0, 1, 2].map(|c| text[c] * STRENGTH + ground[c] * (1.0 - STRENGTH));
            let mut checked = 0;
            for part in parts(&disabled) {
                let (n, _, _) = point_of(&disabled, part);
                let instance = &disabled.instances[n];
                if !within(&disabled, &keys, instance) || within(&disabled, KEEPS_ITS_HUE, instance)
                {
                    continue;
                }
                let edge = matches!(part, Part::Edge(_));
                let seen = shown(&disabled, part, t.bg_panel);
                if within(&disabled, WARM_AT_REST, instance) {
                    let [r, g, b] = seen;
                    assert!(
                        r.max(g).max(b) - r.min(g).min(b) <= 0.06,
                        "{name}, dark={dark}: a disabled warm part still reads warm, {seen:?}"
                    );
                    assert!(
                        contrast(seen, ground) <= contrast(strongest, ground) + ROUNDING,
                        "{name}, dark={dark}: a disabled warm part, {seen:?}, stands out more than text at half strength"
                    );
                    continue;
                }
                let m = rest
                    .instances
                    .iter()
                    .position(|r| same_part(instance, r, edge))
                    .unwrap_or_else(|| {
                        panic!(
                            "{name}, dark={dark}: a disabled part at {:?} has no part at rest to dim",
                            instance.rect.map(|v| v / SCALE)
                        )
                    });
                let at_rest = shown(
                    &rest,
                    if edge { Part::Edge(m) } else { Part::Fill(m) },
                    t.bg_panel,
                );
                let expected =
                    [0, 1, 2].map(|c| at_rest[c] * STRENGTH + ground[c] * (1.0 - STRENGTH));
                if seen == ground && at_rest == ground {
                    continue;
                }
                checked += 1;
                assert!(
                    (0..3).all(|c| (seen[c] - expected[c]).abs() <= BLEND),
                    "{name}, dark={dark}: a disabled {} at {:?} shows {seen:?}; at rest it shows {at_rest:?}, so half strength is {expected:?}",
                    if edge { "edge" } else { "fill" },
                    instance.rect.map(|v| v / SCALE),
                );
            }
            assert!(
                checked > 0,
                "{name}, dark={dark}: no part of the disabled story matched its rest"
            );
        }
    }
}
const BANNER_VARIANTS: [&str; 11] = [
    "Rest",
    "No action",
    "Sentence only",
    "Notes",
    "Countdown",
    "Two actions",
    "Inline controls",
    "Long",
    LONG_LINK,
    LONG_FAILURE,
    MANY_FAILURES,
];

/// The stories of the banners' longest real content at the smallest window, 800 x 600, at the
/// largest text size, 150%.
const LONG_LINK: &str = "Long link, smallest window";
const LONG_FAILURE: &str = "Long failure, smallest window";
const MANY_FAILURES: &str = "Many failures, smallest window";

fn banner_parts(rt: &Runtime<()>, tone: &str) -> (ElementInfo, ElementInfo) {
    let key = format!("banner.{tone}");
    (element(rt, &key), element(rt, &format!("{key}.text")))
}

#[test]
fn a_banners_first_action_starts_one_rhythm_after_its_sentence_at_any_width() {
    for variant in ["Rest", "Notes", "Two actions", "Inline controls", "Long"] {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (_, sentence) = banner_parts(&rt, tone);
            let action = element(&rt, &format!("banner.{tone}.action"));
            let gap = action.rect[0] - (sentence.rect[0] + sentence.rect[2]);
            assert!(
                (gap - 8.0).abs() < 0.5,
                "{variant}: {tone}'s action starts {gap} after its sentence, not 8"
            );
        }
    }
}

/// The height of a compact control, the band a banner's first line centres on.
const BAND: f32 = 26.0;

#[test]
fn a_banners_glyph_and_dismiss_sit_on_its_first_line_and_its_actions_beside_or_under_its_sentence()
{
    let line = |rt: &Runtime<()>, tone: &str| banner_parts(rt, tone).1.rect[3];
    let rest = story(true, "Library/Banner", "Rest");
    for variant in BANNER_VARIANTS {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (bar, sentence) = banner_parts(&rt, tone);
            let band = bar.rect[1] + 6.0 + BAND / 2.0;
            let centre = |e: &ElementInfo| e.rect[1] + e.rect[3] / 2.0;
            let first_line = sentence.rect[1] + line(&rest, tone) / 2.0;
            let mut parts = vec![("sentence's first line".to_owned(), first_line)];
            if let Some(e) = rt.playground_element(&format!("banner.{tone}.dismiss")) {
                parts.push(("dismiss".to_owned(), centre(&e)));
            }
            let first = rt.playground_element(&format!("banner.{tone}.action"));
            let moved = first
                .as_ref()
                .is_some_and(|a| (centre(a) - band).abs() >= 0.5);
            let below = sentence.visible[1] + sentence.visible[3];
            if let Some(a) = first.as_ref().filter(|_| moved) {
                assert!(
                    (a.rect[0] - sentence.rect[0]).abs() < 0.5 && a.rect[1] >= below,
                    "{variant}: {tone}'s actions left the first line for {:?}, not a line of \
                     their own under the sentence at {:?}",
                    a.rect,
                    sentence.rect
                );
            }
            for part in ["action", "second", "countdown"] {
                let Some(e) = rt.playground_element(&format!("banner.{tone}.{part}")) else {
                    continue;
                };
                if moved {
                    assert!(
                        e.rect[1] >= below,
                        "{variant}: {tone}'s {part} at {:?} stays beside the sentence its \
                         actions moved under",
                        e.rect
                    );
                } else {
                    parts.push((part.to_owned(), centre(&e)));
                }
            }
            let all = rt.elements();
            let glyph = all
                .iter()
                .find(|e| e.parent.is_some_and(|p| all[p].path == bar.path))
                .expect("a banner draws its glyph first");
            parts.push(("glyph".to_owned(), centre(glyph)));
            for (part, y) in parts {
                assert!(
                    (y - band).abs() < 0.5,
                    "{variant}: {tone}'s {part} centres at {y}, off its first line at {band}"
                );
            }
        }
    }
}

/// The row that holds `tone`'s notes: the parent of its first note.
fn notes_of(rt: &Runtime<()>, tone: &str) -> Option<ElementInfo> {
    let all = rt.elements();
    let first = ["note.0", "waiting"]
        .iter()
        .find_map(|n| rt.playground_element(&format!("banner.{tone}.{n}")))?;
    let note = all.iter().find(|e| e.path == first.path)?;
    note.parent.map(|p| all[p].clone())
}

#[test]
fn a_banners_notes_follow_its_actions_or_take_a_line_of_their_own_under_its_sentence() {
    for variant in ["Notes", "Two actions", "Inline controls", "Long"] {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (bar, sentence) = banner_parts(&rt, tone);
            let notes = notes_of(&rt, tone).expect("the story's banners have notes");
            let last = ["second", "action"]
                .iter()
                .find_map(|part| rt.playground_element(&format!("banner.{tone}.{part}")))
                .expect("the story's banners act");
            let first_line = (notes.rect[1] - (bar.rect[1] + 6.0)).abs() < 0.5;
            assert!(
                !(first_line && variant == "Inline controls"),
                "{variant}: {tone}'s notes, wider than the bar, stay on its first line"
            );
            if first_line {
                let gap = notes.rect[0] - (last.rect[0] + last.rect[2]);
                assert!(
                    (gap - 8.0).abs() < 0.5,
                    "{variant}: {tone}'s notes start {gap} after its last action, not 8"
                );
            } else {
                assert!(
                    (notes.rect[0] - sentence.rect[0]).abs() < 0.5
                        && notes.rect[1] >= sentence.rect[1] + BAND - 6.0,
                    "{variant}: {tone}'s notes at {:?} are neither on the first line nor on a line \
                     of their own under the sentence at {:?}",
                    notes.rect,
                    sentence.rect
                );
            }
        }
    }
}

#[test]
fn a_banners_dismiss_ends_at_its_inset() {
    for variant in BANNER_VARIANTS
        .into_iter()
        .filter(|v| *v != "Sentence only")
    {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (bar, _) = banner_parts(&rt, tone);
            let dismiss = element(&rt, &format!("banner.{tone}.dismiss"));
            let inset = (bar.rect[0] + bar.rect[2]) - (dismiss.rect[0] + dismiss.rect[2]);
            assert!(
                (inset - 12.0).abs() < 0.5,
                "{variant}: {tone}'s dismiss ends {inset} from the bar's edge, not 12"
            );
        }
    }
}

#[test]
fn a_banner_is_as_tall_in_every_tone_with_or_without_an_action() {
    let mut heights = Vec::new();
    let one_line = ["Rest", "No action", "Sentence only", "Countdown"];
    for variant in one_line {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            heights.push((variant, tone, banner_parts(&rt, tone).0.rect[3]));
        }
    }
    let first = heights[0].2;
    assert!(
        heights.iter().all(|&(_, _, h)| (h - first).abs() < 0.5),
        "banners at several heights: {heights:?}"
    );
}

#[test]
fn every_control_of_a_banner_lies_inside_it_clear_of_the_others() {
    for variant in BANNER_VARIANTS {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (bar, _) = banner_parts(&rt, tone);
            let [bx, by, bw, bh] = bar.rect;
            let all = rt.elements();
            let controls: Vec<&ElementInfo> = all
                .iter()
                .filter(|e| {
                    e.focusable
                        && std::iter::successors(e.parent, |&p| all[p].parent)
                            .any(|p| all[p].path == bar.path)
                })
                .collect();
            for control in &controls {
                let [x, y, w, h] = control.rect;
                assert!(
                    x >= bx - 0.5
                        && y >= by - 0.5
                        && x + w <= bx + bw + 0.5
                        && y + h <= by + bh + 0.5,
                    "{variant}: {tone}'s {:?} at {:?} leaves its bar at {:?}",
                    control.key,
                    control.rect,
                    bar.rect
                );
            }
            for (i, a) in controls.iter().enumerate() {
                for b in &controls[i + 1..] {
                    let apart = |a: [f32; 4], b: [f32; 4]| {
                        a[0] + a[2] <= b[0] + 0.5
                            || b[0] + b[2] <= a[0] + 0.5
                            || a[1] + a[3] <= b[1] + 0.5
                            || b[1] + b[3] <= a[1] + 0.5
                    };
                    assert!(
                        apart(a.rect, b.rect),
                        "{variant}: {tone}'s {:?} at {:?} lies over {:?} at {:?}",
                        a.key,
                        a.rect,
                        b.key,
                        b.rect
                    );
                }
            }
        }
    }
}

/// The element keyed `key`, focused by pressing Tab as a person does: Shift-Tab from the
/// story list onto the stage, then Tab along it.
fn tab_to(rt: &mut Runtime<()>, key: &str) -> ElementInfo {
    rt.set_full_keyboard_access(true);
    rt.key(KeyEvent {
        key: Key::Tab,
        mods: Mods {
            shift: true,
            ..Mods::default()
        },
    });
    rt.frame(0.1);
    for _ in 0..80 {
        let focused = rt
            .elements()
            .into_iter()
            .find(|e| e.focused && e.key.as_deref() == Some(key));
        if let Some(e) = focused {
            return e;
        }
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(0.1);
    }
    panic!("Tab never reaches {key}")
}

#[test]
fn a_long_link_elides_inside_its_bar_with_its_ring_whole_and_its_name_whole() {
    for dark in [true, false] {
        for (tone, _) in BANNERS {
            let rt = story(dark, "Library/Banner", LONG_LINK);
            let more = rt
                .elements()
                .into_iter()
                .find(|e| e.key.as_deref() == Some(&format!("banner.{tone}.more")))
                .and_then(|e| e.text);
            assert_eq!(
                more.as_deref(),
                Some("and 1 more."),
                "dark={dark}: the bar counts the blocker it leaves to Details"
            );
            {
                let link = "go.a";
                let mut rt = story(dark, "Library/Banner", LONG_LINK);
                let key = format!("banner.{tone}.{link}");
                let focused = tab_to(&mut rt, &key);
                let (bar, _) = banner_parts(&rt, tone);
                let ring = ring_around(&rt, &focused)
                    .unwrap_or_else(|| panic!("dark={dark}: {key} draws no focus ring"));
                let [x, y, w, h] = ring.rect;
                let [bx, by, bw, bh] = device(bar.rect);
                assert!(
                    x >= bx && y >= by && x + w <= bx + bw && y + h <= by + bh,
                    "dark={dark}: {key}'s ring {:?} leaves its bar {:?}",
                    ring.rect.map(|v| v / SCALE),
                    bar.rect
                );
                let [x0, y0, x1, y1] = ring.clip;
                assert!(
                    x0 <= x && y0 <= y && x + w <= x1 && y + h <= y1,
                    "dark={dark}: {key}'s ring {:?} is cut by {:?}",
                    ring.rect,
                    ring.clip
                );
                let all = rt.elements();
                let around = ring.rect.map(|v| v / SCALE);
                let inside_link = |e: &ElementInfo| {
                    std::iter::successors(Some(e), |e| e.parent.map(|p| &all[p]))
                        .any(|a| a.path == focused.path)
                };
                for words in all.iter().filter(|e| {
                    e.text.is_some()
                        && !inside_link(e)
                        && std::iter::successors(e.parent, |&p| all[p].parent)
                            .any(|p| all[p].path == bar.path)
                }) {
                    let [x, y, w, h] = words.rect;
                    let apart = x + w <= around[0] + 0.5
                        || around[0] + around[2] <= x + 0.5
                        || y + h <= around[1] + 0.5
                        || around[1] + around[3] <= y + 0.5;
                    assert!(
                        apart,
                        "dark={dark}: {key}'s ring {around:?} crosses {:?} at {:?}",
                        words.text, words.rect
                    );
                }
                let name = focused.label.clone().unwrap_or_default();
                let shown = name.trim_start_matches("Go to ");
                assert!(
                    name.starts_with("Go to ") && shown.chars().count() >= 40,
                    "dark={dark}: {key} is announced as {name:?}, not by its whole name"
                );
                let (whole, _) = rune::text::measure(shown, &[], roles::NAME, None);
                assert!(
                    focused.rect[2] + 0.5 < whole,
                    "dark={dark}: {key} at {} wide draws its name whole at {whole}, which the \
                     bar cannot hold",
                    focused.rect[2]
                );
            }
        }
    }
}

/// The least width a banner's sentence keeps beside its actions: a line of 40 characters, the
/// shortest that still reads as a line, which the real sentences set in Name (6.24 to 6.63 a
/// character) measure 250 across.
const READABLE: f32 = 250.0;

/// The smallest window's height under its title bar at the largest text size, in the points it
/// lays out in: 600 / 1.5 - 32.
const SMALLEST_WORK_AREA: f32 = 368.0;

#[test]
fn a_long_failure_keeps_its_sentence_readable_and_leaves_most_of_the_smallest_window() {
    for variant in [LONG_FAILURE, MANY_FAILURES] {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let (bar, sentence) = banner_parts(&rt, tone);
            assert!(
                sentence.rect[2] + 0.5 >= READABLE,
                "{variant}: {tone}'s sentence is left {} wide, under a readable {READABLE}",
                sentence.rect[2]
            );
            let left = SMALLEST_WORK_AREA - bar.rect[3];
            assert!(
                left >= SMALLEST_WORK_AREA * 2.0 / 3.0,
                "{variant}: {tone}'s banner is {} tall and leaves the work area {left} of \
                 {SMALLEST_WORK_AREA}",
                bar.rect[3]
            );
        }
    }
}

/// How the library sets a banner's sentence: `Type::Name`, 13/600, 16.25 a line.
const SENTENCE_LINE: f32 = 16.25;
/// The most lines a closed sentence shows.
const SENTENCE_LINES: f32 = 4.0;

#[test]
fn a_sentence_longer_than_its_lines_ends_in_an_ellipsis_and_offers_details() {
    let rt = story(true, "Library/Banner", LONG_FAILURE);
    for (tone, _) in BANNERS {
        assert!(
            rt.playground_element(&format!("banner.{tone}.details"))
                .is_none(),
            "{tone}'s sentence fits its lines, yet the banner offers Details"
        );
    }
    for (tone, _) in BANNERS {
        let mut rt = story(true, "Library/Banner", MANY_FAILURES);
        let (_, sentence) = banner_parts(&rt, tone);
        let said = sentence.label.clone().unwrap_or_default();
        let (_, whole) = rune::text::measure(&said, &[], roles::NAME, Some(sentence.rect[2]));
        assert!(
            (sentence.rect[3] - SENTENCE_LINES * SENTENCE_LINE).abs() < 1.0
                && whole > sentence.rect[3],
            "{tone}'s closed sentence is {} tall; its {whole} of text should show {SENTENCE_LINES} lines",
            sentence.rect[3]
        );
        let key = format!("banner.{tone}.details");
        let (bar, _) = banner_parts(&rt, tone);
        let fill = surface(&rt, &bar, tokens(true).bg_panel);
        let all = rt.elements();
        let word = all
            .iter()
            .find(|e| {
                e.text.as_deref() == Some("Details")
                    && std::iter::successors(e.parent, |&p| all[p].parent)
                        .any(|p| all[p].key.as_deref() == Some(key.as_str()))
            })
            .unwrap_or_else(|| panic!("{tone}'s Details draws no words"));
        let reads = contrast(drawn_over(word.text_color.expect("words"), fill), fill);
        assert!(
            reads >= TEXT_READS,
            "{tone}'s Details reads {reads:.2}:1 on its fill"
        );
        let control = tab_to(&mut rt, &key);
        assert_eq!(control.label.as_deref(), Some("Details"), "{tone}");
    }
}

#[test]
fn a_banners_note_ends_in_an_ellipsis_only_when_its_line_is_too_short_for_it() {
    for variant in BANNER_VARIANTS {
        let rt = story(true, "Library/Banner", variant);
        for (tone, _) in BANNERS {
            let Some(notes) = notes_of(&rt, tone) else {
                continue;
            };
            let all = rt.elements();
            let words = all.iter().filter(|e| {
                e.text.is_some()
                    && !e.focusable
                    && e.parent.is_some_and(|p| all[p].path == notes.path)
            });
            for note in words {
                let said = note.text.as_deref().unwrap_or_default();
                let (whole, _) = rune::text::measure(said, &[], roles::BODY, None);
                let shortened = note.rect[2] + 0.5 < whole;
                let alone = note.rect[2] + 0.5 >= notes.rect[2];
                assert!(
                    !shortened || alone,
                    "{variant}: {tone}'s note {said:?} is cut to {} of its {whole} beside other \
                     notes on a line {} wide",
                    note.rect[2],
                    notes.rect[2]
                );
            }
        }
    }
}

/// The least contrast hover stands from rest, in dark and in light, as `attn_ui::system`'s
/// layers measure it.
const HOVER_STEP: (f32, f32) = (1.20, 1.17);
/// WCAG's AA contrast for text below 18 points, and for a focus indicator against what it
/// sits on.
const TEXT_READS: f32 = 4.5;
const RING_READS: f32 = 3.0;

#[test]
fn a_banner_stands_apart_from_the_title_bar_and_its_words_and_ring_read_on_it() {
    for dark in [true, false] {
        let t = tokens(dark);
        let rt = story(dark, "Library/Banner", "Focus");
        let app = [t.bg_app.r, t.bg_app.g, t.bg_app.b].map(rune::theme::srgb_encode);
        let hover = if dark { HOVER_STEP.0 } else { HOVER_STEP.1 };
        for (tone, kind) in BANNERS {
            let (bar, sentence) = banner_parts(&rt, tone);
            let fill = surface(&rt, &bar, t.bg_panel);
            let apart = contrast(fill, app);
            assert!(
                apart >= hover,
                "dark={dark}: {tone}'s fill stands {apart:.2}:1 from the title bar, under a hover step"
            );
            let ink = drawn_over(sentence.text_color.expect("the sentence is text"), fill);
            let reads = contrast(ink, fill);
            assert!(
                reads >= TEXT_READS,
                "dark={dark}: {tone}'s sentence reads {reads:.2}:1 on its fill"
            );
            let note = inside(
                &rt,
                &format!("banner.{tone}"),
                &format!("banner.{tone}.note.0"),
            );
            let quiet = drawn_over(note.text_color.expect("a note is text"), fill);
            let reads = contrast(quiet, fill);
            assert!(
                reads >= TEXT_READS,
                "dark={dark}: {tone}'s notes read {reads:.2}:1 on its fill"
            );
            if kind == BannerTone::Bad {
                for part in ["action", "dismiss"] {
                    let control = element(&rt, &format!("banner.{tone}.{part}"));
                    let ring = ring_around(&rt, &control).unwrap_or_else(|| {
                        panic!("dark={dark}: {tone}'s {part} draws no focus ring")
                    });
                    let edge = [ring.border[0], ring.border[1], ring.border[2]];
                    let shows = contrast(edge, fill);
                    assert!(
                        shows >= RING_READS,
                        "dark={dark}: {tone}'s {part} ring stands {shows:.2}:1 from the fill"
                    );
                }
            }
        }
    }
}

#[test]
fn every_buttons_words_read_on_its_rest_fill_in_dark_and_light() {
    for dark in [true, false] {
        let t = tokens(dark);
        let rt = story(dark, "Library/Button", "Rest");
        let all = rt.elements();
        for (look, _) in BUTTONS.iter().filter(|(look, _)| *look != "danger") {
            for (size, _) in SIZES {
                let key = format!("button.{look}.{size}");
                let button = element(&rt, &key);
                let fill = surface(&rt, &button, t.bg_panel);
                let word = all
                    .iter()
                    .find(|e| {
                        e.text_color.is_some()
                            && std::iter::successors(e.parent, |&p| all[p].parent)
                                .any(|p| all[p].key.as_deref() == Some(key.as_str()))
                    })
                    .unwrap_or_else(|| panic!("{key} draws no words"));
                let reads = contrast(drawn_over(word.text_color.expect("words"), fill), fill);
                assert!(
                    reads >= TEXT_READS,
                    "dark={dark}: {key}'s words read {reads:.2}:1 on its fill"
                );
            }
        }
    }
}

/// Ink of colour `c` as the frame blends it over `below`.
fn drawn_over(c: rune::Color, below: [f32; 3]) -> [f32; 3] {
    let [r, g, b] = [c.r, c.g, c.b].map(rune::theme::srgb_encode);
    over([r * c.a, g * c.a, b * c.a, c.a], below)
}

/// The ring drawn round `e`, in any colour, outside its edge.
fn ring_around<'a>(rt: &'a Runtime<()>, e: &ElementInfo) -> Option<&'a Instance> {
    let [x, y, w, h] = device(e.rect);
    let reach = RING_REACH * SCALE;
    rt.instances.iter().find(|i| {
        let [ix, iy, iw, ih] = i.rect;
        ix < x
            && iy < y
            && x - ix <= reach + 1.0
            && y - iy <= reach + 1.0
            && (ix + iw) - (x + w) <= reach + 1.0
            && (iy + ih) - (y + h) <= reach + 1.0
            && ix + iw > x + w
            && i.params[0] > 0.0
    })
}

/// Wide enough for a block story at its widest page.
const BLOCK_WINDOW: (f32, f32) = (1500.0, 1100.0);

fn block_story(dark: bool, name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("rules", BLOCK_WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(BLOCK_WINDOW, Env::new(dark, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt
}

const ROWS: [&str; 4] = ["row.stepper", "row.number", "row.switch", "row.button"];
const PAGE_VARIANTS: [&str; 6] = [
    "Rest",
    "Long",
    "Narrow",
    "Narrow long",
    "Smallest long",
    "Refused",
];
const WIDTHS: [&str; 5] = ["Rest", "Long", "Narrow", "Narrow long", "Smallest long"];

/// Each width every block story draws at, and the states a story draws only at its smallest.
fn widths(story: &str) -> Vec<&'static str> {
    let smallest_states: &[&str] = match story {
        "Blocks/Table" => &["Empty smallest long", "Empty off smallest long"],
        _ => &[],
    };
    WIDTHS.iter().chain(smallest_states).copied().collect()
}

fn right(e: &ElementInfo) -> f32 {
    e.rect[0] + e.rect[2]
}

fn bottom(e: &ElementInfo) -> f32 {
    e.rect[1] + e.rect[3]
}

#[test]
fn a_page_has_one_right_edge() {
    for variant in PAGE_VARIANTS {
        let rt = block_story(true, "Blocks/Page", variant);
        let edge = right(&element(&rt, "page.section"));
        for key in ["page.layout"].into_iter().chain(ROWS) {
            let e = element(&rt, key);
            assert!(
                (right(&e) - edge).abs() < 0.5,
                "{variant}: {key} ends at {}, the page at {edge}",
                right(&e)
            );
        }
        if variant.starts_with("Narrow") || variant.starts_with("Smallest") {
            continue;
        }
        for (row, last) in [
            ("row.stepper", "row.stepper.reset"),
            ("row.switch", "row.switch.control"),
            ("row.button", "row.button.control"),
        ] {
            let inside = right(&element(&rt, row)) - 16.0;
            let end = right(&element(&rt, last));
            assert!(
                (end - inside).abs() < 0.5,
                "{variant}: {last} ends at {end}, its row's inset at {inside}"
            );
        }
    }
}

#[test]
fn a_control_moved_under_its_words_starts_on_their_edge() {
    for variant in ["Narrow", "Narrow long"] {
        let rt = block_story(true, "Blocks/Setting row", variant);
        let row = element(&rt, "row.stepper");
        let down = element(&rt, "row.stepper.control.down");
        let words_edge = row.rect[0] + 16.0;
        assert!(
            down.rect[1] > row.rect[1] + 40.0,
            "{variant}: the stepper did not move under its words"
        );
        assert!(
            (down.rect[0] - words_edge).abs() < 0.5,
            "{variant}: the stepper starts at {}, its words at {words_edge}",
            down.rect[0]
        );
    }
}

#[test]
fn blocks_are_spaced_by_what_they_are() {
    for variant in PAGE_VARIANTS {
        let rt = block_story(true, "Blocks/Page", variant);
        let between =
            |above: &str, below: &str| element(&rt, below).rect[1] - bottom(&element(&rt, above));
        let layout = element(&rt, "page.layout");
        let all = rt.elements();
        let heading = all
            .iter()
            .find(|e| e.parent.is_some_and(|p| all[p].path == layout.path))
            .expect("a section has a head");
        let sections = heading.rect[1] - bottom(&element(&rt, "page.section"));
        let rule = between("page.section", "page.layout");
        assert!(
            (sections - 32.0).abs() < 0.5 && (rule - 16.0).abs() < 0.5,
            "{variant}: sections are {sections} apart with the rule {rule} down, not 32 and 16"
        );
        let rows = between("row.switch", "row.button");
        assert!(
            (rows - 8.0).abs() < 0.5,
            "{variant}: setting rows are {rows} apart, not 8"
        );
        let lead = between("page.prose", "page.section");
        assert!(
            (lead - 16.0).abs() < 0.5,
            "{variant}: a section sits {lead} under the prose, not 16"
        );
    }
}

#[test]
fn reading_text_wraps_at_the_reading_measure() {
    for variant in ["Long", "Narrow long"] {
        let rt = block_story(true, "Blocks/Page", variant);
        let prose = element(&rt, "page.prose");
        assert!(
            prose.rect[2] <= 480.5,
            "{variant}: prose runs {} wide",
            prose.rect[2]
        );
        let row = element(&rt, "row.number");
        assert!(row.rect[2] > 480.0 || variant.starts_with("Narrow"));
    }
}

#[test]
fn a_page_title_wraps_short_of_its_aside() {
    for variant in PAGE_VARIANTS {
        let rt = block_story(true, "Blocks/Page", variant);
        let switch = device(inside(&rt, "page.switch", "control").rect);
        let under = rt.instances.iter().find(|i| {
            i.params[1] == rune::render::KIND_GLYPH
                && i.rect[0] < switch[0] + switch[2]
                && i.rect[0] + i.rect[2] > switch[0]
                && i.rect[1] < switch[1] + switch[3]
                && i.rect[1] + i.rect[3] > switch[1]
        });
        assert!(
            under.is_none(),
            "{variant}: a glyph at {:?} is drawn under the title's switch at {:?}",
            under.map(|g| g.rect.map(|v| v / SCALE)),
            switch.map(|v| v / SCALE)
        );
    }
}

fn apart(a: [f32; 4], b: [f32; 4]) -> bool {
    a[0] + a[2] <= b[0] || b[0] + b[2] <= a[0] || a[1] + a[3] <= b[1] || b[1] + b[3] <= a[1]
}

/// Whether `inner` stays between `outer`'s sides, but for a quiet control hung left by its
/// inset so its words line up with the text above it.
fn holds_across(outer: [f32; 4], inner: [f32; 4]) -> bool {
    inner[0] >= outer[0] - REGULAR_INSET - 0.5 && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
}

/// The narrowest a title gets beside the aside before the aside moves under it.
const TITLE_FLOOR: f32 = 288.0;

#[test]
fn a_heads_aside_gives_way_to_its_title_and_stays_on_the_page() {
    for variant in PAGE_VARIANTS {
        let rt = block_story(true, "Blocks/Page", variant);
        let page = element(&rt, "page.section").rect;
        let title = element(&rt, "page.heading").rect;
        assert!(
            title[2] >= TITLE_FLOOR.min(page[2]) - 0.5,
            "{variant}: the title keeps {} of the page's {}",
            title[2],
            page[2]
        );
        let aside = ["page.switch", "page.pills"]
            .into_iter()
            .filter(|key| rt.elements().iter().any(|e| e.key.as_deref() == Some(*key)));
        for key in aside {
            let e = element(&rt, key).rect;
            assert!(
                apart(title, e),
                "{variant}: {key} at {e:?} covers the title at {title:?}"
            );
            assert!(
                e[0] + e[2] <= page[0] + page[2] + 0.5,
                "{variant}: {key} ends at {}, past the page's {}",
                e[0] + e[2],
                page[0] + page[2]
            );
        }
    }
}

#[test]
fn no_part_of_a_block_runs_past_its_sides_at_any_width() {
    for &(story, subjects) in attn_ui::block_gallery::BLOCK_SUBJECTS
        .iter()
        .chain(attn_ui::settings_gallery::SETTINGS_SUBJECTS)
    {
        for variant in widths(story) {
            let rt = block_story(true, story, variant);
            for key in subjects {
                let block = element(&rt, key);
                for part in rt.elements().iter().filter(|e| {
                    e.path.starts_with(&format!("{}/", block.path))
                        && e.rect[2] > 0.0
                        && e.rect[3] > 0.0
                }) {
                    assert!(
                        holds_across(block.rect, part.rect),
                        "{story} {variant} {key}: {} at {:?} leaves the block at {:?}",
                        part.path,
                        part.rect,
                        block.rect
                    );
                }
            }
        }
    }
}

/// The longest word of a colour scheme's name, "Catppuccin", in `Name`.
const LONGEST_NAME_WORD: f32 = 72.5;

#[test]
fn a_choices_name_keeps_its_longest_word_whole_at_every_width() {
    for variant in WIDTHS {
        let rt = block_story(true, "Blocks/Choices", variant);
        let options: Vec<ElementInfo> = rt
            .elements()
            .into_iter()
            .filter(|e| e.role == "radio")
            .collect();
        assert!(!options.is_empty(), "{variant}: no choices drawn");
        for option in options {
            let prefix = format!("{}/", option.path);
            for text in rt
                .elements()
                .into_iter()
                .filter(|e| e.path.starts_with(&prefix) && e.text.is_some())
            {
                assert!(
                    text.rect[2] >= LONGEST_NAME_WORD,
                    "{variant}: {:?} gets {} points, under a whole word's {LONGEST_NAME_WORD}",
                    text.text,
                    text.rect[2]
                );
                assert!(
                    holds_across(option.rect, text.rect),
                    "{variant}: {:?} at {:?} leaves its choice at {:?}",
                    text.text,
                    text.rect,
                    option.rect
                );
            }
        }
    }
}

fn holds_all(outer: [f32; 4], inner: [f32; 4]) -> bool {
    inner[0] >= outer[0] - 0.5
        && inner[1] >= outer[1] - 0.5
        && inner[0] + inner[2] <= outer[0] + outer[2] + 0.5
        && inner[1] + inner[3] <= outer[1] + outer[3] + 0.5
}

#[test]
fn a_controls_box_and_ring_take_in_everything_it_draws_at_any_width() {
    for &(story, _) in attn_ui::block_gallery::BLOCK_SUBJECTS
        .iter()
        .chain(attn_ui::settings_gallery::SETTINGS_SUBJECTS)
    {
        for variant in widths(story) {
            let rt = block_story(true, story, variant);
            let elements = rt.elements();
            for control in elements.iter().filter(|e| e.focusable) {
                let prefix = format!("{}/", control.path);
                for part in elements
                    .iter()
                    .filter(|e| e.path.starts_with(&prefix) && e.rect[2] > 0.0 && e.rect[3] > 0.0)
                {
                    assert!(
                        holds_all(control.rect, part.rect),
                        "{story} {variant}: {} at {:?} reaches outside its control {} at {:?}",
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

/// A tag's words, `Tag` text on its 14-point line, with 2 points above and below: about 4 points
/// over a tag's capitals and 4.5 under them, the room they had at 10 points, measured on the
/// tag sheet at 2x.
const TAG_HEIGHT: f32 = 18.0;

#[test]
fn a_tag_is_one_height_with_its_words_centred_in_it() {
    for variant in ["Rest", "Long"] {
        let rt = story(true, "Library/Tag", variant);
        for tone in ["neutral", "warn", "bad", "good", "accent"] {
            let key = format!("tag.{tone}");
            let tag = element(&rt, &key);
            let all = rt.elements();
            let said = all
                .iter()
                .find(|e| e.text.is_some() && e.parent.is_some_and(|p| all[p].path == tag.path))
                .or_else(|| (tag.text.is_some()).then_some(&tag))
                .unwrap_or_else(|| panic!("{key} holds no words"));
            assert!(
                (tag.rect[3] - TAG_HEIGHT).abs() < 0.5,
                "{variant}: {key} is {} high, not {TAG_HEIGHT}",
                tag.rect[3]
            );
            let above = said.rect[1] - tag.rect[1];
            let below = (tag.rect[1] + tag.rect[3]) - (said.rect[1] + said.rect[3]);
            assert!(
                (above - below).abs() < 0.5,
                "{variant}: {key}'s words sit {above} from its top and {below} from its bottom"
            );
        }
    }
}

/// An icon-only button's side: a `Large` icon, 18, with 4 points on each side, the room the
/// 14-point icon had in the 22-point button before, and the height of a compact control.
const ICON_BUTTON: f32 = 26.0;
const LARGE_ICON: f32 = 18.0;

#[test]
fn an_icon_only_button_gives_its_large_icon_four_points_on_every_side() {
    let rt = story(true, "Library/Banner", "Rest");
    let all = rt.elements();
    for (tone, _) in BANNERS {
        let dismiss = element(&rt, &format!("banner.{tone}.dismiss"));
        assert!(
            (dismiss.rect[2] - ICON_BUTTON).abs() < 0.5
                && (dismiss.rect[3] - ICON_BUTTON).abs() < 0.5,
            "{tone}'s dismiss is {:?}, not {ICON_BUTTON} square",
            dismiss.rect
        );
        let glyph = all
            .iter()
            .find(|e| e.parent.is_some_and(|p| all[p].path == dismiss.path))
            .expect("a dismiss draws its icon");
        let room = [
            glyph.rect[0] - dismiss.rect[0],
            glyph.rect[1] - dismiss.rect[1],
            (dismiss.rect[0] + dismiss.rect[2]) - (glyph.rect[0] + glyph.rect[2]),
            (dismiss.rect[1] + dismiss.rect[3]) - (glyph.rect[1] + glyph.rect[3]),
        ];
        assert!(
            (glyph.rect[2] - LARGE_ICON).abs() < 0.5 && room.iter().all(|r| (r - 4.0).abs() < 0.5),
            "{tone}'s dismiss draws a {:?} icon with {room:?} round it",
            glyph.rect
        );
    }
}

#[test]
fn a_tag_beside_a_choices_name_sits_on_its_line_after_it() {
    for dark in [true, false] {
        let rt = story(dark, "Library/Choice", "Recommended");
        let all = rt.elements();
        let tag = inside(&rt, "choice.0", "choice.0.recommended");
        let index = all.iter().position(|e| e.path == tag.path).unwrap();
        let name = all
            .iter()
            .find(|e| e.parent == all[index].parent && e.path != tag.path)
            .expect("the tag shares its line with the choice's name");
        let middle = |r: [f32; 4]| r[1] + r[3] / 2.0;
        assert!(
            (middle(name.rect) - middle(tag.rect)).abs() <= 1.0,
            "the tag's middle is at {}, the name's at {}",
            middle(tag.rect),
            middle(name.rect)
        );
        assert!(
            tag.rect[0] >= name.rect[0] + name.rect[2],
            "the tag starts at {}, before the name ends at {}",
            tag.rect[0],
            name.rect[0] + name.rect[2]
        );
    }
}

const SMALL_ICON: f32 = 14.0;
const REGULAR_HEIGHT: f32 = 32.0;

#[test]
fn a_steppers_minus_and_plus_are_small_marks_on_square_buttons_in_its_tab_order() {
    for dark in [true, false] {
        let mut rt = story(dark, "Library/Stepper", "Rest");
        let all = rt.elements();
        for (dir, mark) in [("down", "−"), ("up", "+")] {
            let button = element(&rt, &format!("stepper.scale.{dir}"));
            let drawn = all
                .iter()
                .find(|e| {
                    e.text.as_deref() == Some(mark)
                        && std::iter::successors(e.parent, |&p| all[p].parent)
                            .any(|p| all[p].path == button.path)
                })
                .unwrap_or_else(|| panic!("stepper.scale.{dir} draws no {mark}"));
            assert!(
                (drawn.rect[3] - SMALL_ICON).abs() < 0.25,
                "dark={dark}: {mark} is drawn {} high, not IconSize::Small",
                drawn.rect[3]
            );
            assert!(
                [button.rect[2], button.rect[3]]
                    .iter()
                    .all(|side| (side - REGULAR_HEIGHT).abs() < 0.25),
                "dark={dark}: stepper.scale.{dir} presses over {:?}, not {REGULAR_HEIGHT} square",
                button.rect
            );
            let centre = |r: [f32; 4]| [r[0] + r[2] / 2.0, r[1] + r[3] / 2.0];
            let (a, b) = (centre(drawn.rect), centre(button.rect));
            assert!(
                (a[0] - b[0]).abs() < 0.5 && (a[1] - b[1]).abs() < 0.5,
                "dark={dark}: {mark} is centred at {a:?} in a button centred at {b:?}"
            );
        }
        let stepper = element(&rt, "stepper.scale");
        assert!(
            (stepper.rect[2] - (2.0 * REGULAR_HEIGHT + 2.0 * 4.0 + 80.0)).abs() < 0.25
                && (stepper.rect[3] - REGULAR_HEIGHT).abs() < 0.25,
            "dark={dark}: the stepper is {:?}",
            stepper.rect
        );
        tab_into(&mut rt, "stepper.scale.down");
        let mut stops = vec!["stepper.scale.down".to_owned()];
        for _ in 0..3 {
            rt.key(KeyEvent {
                key: Key::Tab,
                mods: Mods::default(),
            });
            rt.frame(1.0);
            let focused = rt.elements().into_iter().find(|e| e.focused);
            stops.push(focused.and_then(|e| e.key).unwrap_or_default());
        }
        assert_eq!(
            stops,
            [
                "stepper.scale.down",
                "stepper.scale.up",
                "stepper.match.down",
                "stepper.match.up"
            ],
            "dark={dark}: Tab through the steppers"
        );
    }
}

const CONTROL_EDGE: f32 = 3.0;

#[test]
fn an_unchosen_choices_mark_stands_a_controls_step_from_its_ground() {
    for dark in [true, false] {
        let t = tokens(dark);
        for (variant, key) in [("First chosen", "choice.1"), ("Second chosen", "choice.0")] {
            let rt = story(dark, "Library/Choice", variant);
            let choice = element(&rt, key);
            let mark = element(&rt, &format!("{key}.mark"));
            let ground = surface(&rt, &choice, t.bg_panel);
            let ring = rt
                .instances
                .iter()
                .find(|i| near(i.rect, device(mark.rect)) && i.params[0] > 0.0)
                .unwrap_or_else(|| panic!("{key}, dark={dark}: the mark draws no ring"))
                .border;
            let apart = contrast([ring[0], ring[1], ring[2]], ground);
            assert!(
                apart >= CONTROL_EDGE - ROUNDING,
                "{key}, dark={dark}: the mark's ring stands {apart:.2}:1 from its ground, under {CONTROL_EDGE}:1"
            );
        }
    }
}

/// How strongly an unavailable control draws: half strength.
const UNAVAILABLE: f32 = 0.5;

#[test]
fn a_disabled_primary_keeps_its_hue_at_half_strength_and_its_label_reads_on_it() {
    for (dark, variant) in [true, false]
        .into_iter()
        .flat_map(|dark| ["Disabled", "Unavailable"].map(|variant| (dark, variant)))
    {
        let t = tokens(dark);
        let rt = story(dark, "Library/Button", variant);
        let ground = [t.bg_panel.r, t.bg_panel.g, t.bg_panel.b].map(rune::theme::srgb_encode);
        let primary = [t.primary.r, t.primary.g, t.primary.b].map(rune::theme::srgb_encode);
        let half = [0, 1, 2].map(|c| primary[c] * UNAVAILABLE + ground[c] * (1.0 - UNAVAILABLE));
        for size in ["regular", "compact"] {
            let key = format!("button.primary.{size}");
            let button = element(&rt, &key);
            let fill = rt
                .instances
                .iter()
                .position(|i| near(i.rect, device(button.rect)) && i.fill[3] > 0.0)
                .unwrap_or_else(|| panic!("{key} {variant}, dark={dark}: draws no fill"));
            let drawn = shown(&rt, Part::Fill(fill), t.bg_panel);
            assert!(
                (0..3).all(|c| (drawn[c] - half[c]).abs() < 0.02),
                "{key} {variant}, dark={dark}: a disabled Primary is drawn {drawn:?}, not its hue at half strength {half:?}"
            );
            let (glyph, _) = *glyphs_in(&rt, &button)
                .first()
                .unwrap_or_else(|| panic!("{key} {variant}, dark={dark}: draws no label"));
            let label = shown(&rt, Part::Fill(glyph), t.bg_panel);
            let apart = contrast(label, drawn);
            assert!(
                apart >= CONTROL_EDGE - ROUNDING,
                "{key} {variant}, dark={dark}: the label stands {apart:.2}:1 on the fill, under {CONTROL_EDGE}:1"
            );
        }
    }
}

const TABLE_ROWS: [&str; 4] = [
    "table.builder",
    "table.orchestrator",
    "table.reviewer",
    "table.fallback",
];
const RAIL: f32 = 2.0;
const FIELD: f32 = 504.0;
const GLYPH_SLACK: f32 = 1.5;

/// The glyphs drawn in `within` that some part of shows: past the clip each glyph carries and the
/// clip of every layer it is drawn in.
fn shown_glyphs(rt: &Runtime<()>, within: [f32; 4]) -> Vec<[f32; 4]> {
    use rune::render::{KIND_GLYPH, KIND_LAYER, KIND_LAYER_END};
    let area = device(within);
    let mut layers: Vec<[f32; 4]> = Vec::new();
    let mut shown = Vec::new();
    for i in &rt.instances {
        let kind = i.params[1];
        if kind == KIND_LAYER {
            layers.push(i.clip);
        } else if kind == KIND_LAYER_END {
            layers.pop();
        } else if kind == KIND_GLYPH {
            let [x, y, w, h] = i.rect;
            let seen = layers
                .iter()
                .chain([&i.clip])
                .fold([x, y, x + w, y + h], |r, c| {
                    [
                        r[0].max(c[0]),
                        r[1].max(c[1]),
                        r[2].min(c[2]),
                        r[3].min(c[3]),
                    ]
                });
            let (cx, cy) = (x + w / 2.0, y + h / 2.0);
            let within = cx >= area[0]
                && cx <= area[0] + area[2]
                && cy >= area[1]
                && cy <= area[1] + area[3];
            if seen[0] < seen[2] && seen[1] < seen[3] && within {
                shown.push(i.rect.map(|v| v / SCALE));
            }
        }
    }
    shown
}

fn words_left(rt: &Runtime<()>, e: &ElementInfo) -> f32 {
    shown_glyphs(rt, e.rect)
        .iter()
        .map(|g| g[0])
        .fold(f32::INFINITY, f32::min)
}

fn middle(e: &ElementInfo) -> f32 {
    e.rect[1] + e.rect[3] / 2.0
}

#[test]
fn a_table_rows_parts_share_its_first_line() {
    for variant in [
        "Rest",
        "Long",
        "Narrow",
        "Narrow long",
        "Open",
        "Smallest long",
    ] {
        let rt = block_story(true, "Blocks/Table", variant);
        let trailing = right(&element(&rt, "table")) - ROW_INSET;
        for row in TABLE_ROWS {
            let who = element(&rt, row);
            let model = element(&rt, &format!("{row}.model"));
            let beside = model.rect[0] > right(&who);
            let band = if beside { who.rect[1] } else { model.rect[1] } + 16.0;
            if beside {
                assert!(
                    (middle(&model) - band).abs() < 0.5,
                    "{variant} {row}: its control centres at {} off its first line at {band}",
                    middle(&model)
                );
            }
            let more = element(&rt, &format!("{row}.more"));
            let under = !beside && more.rect[1] >= bottom(&model);
            assert!(
                (middle(&more) - band).abs() < 0.5 || under,
                "{variant} {row}: its more button centres at {}, neither on its control's line \
                 at {band} nor under it",
                middle(&more)
            );
            assert!(
                (right(&more) - trailing).abs() < 0.5 && (under || more.rect[0] > right(&model)),
                "{variant} {row}: its more button ends at {}, not after its control at the row's \
                 trailing edge {trailing}",
                right(&more)
            );
        }
    }
}

/// How far a table's rows keep their parts from its sides.
const ROW_INSET: f32 = 16.0;

const ALTERNATIVES: [&str; 2] = ["table.alt.fast", "table.alt.unnamed"];

#[test]
fn an_alternatives_remove_button_ends_its_model_line_or_the_block_under_it() {
    for variant in ["Open", "Open narrow", "Open smallest long"] {
        let rt = block_story(true, "Blocks/Table", variant);
        let trailing = right(&element(&rt, "table")) - ROW_INSET;
        for alt in ALTERNATIVES {
            let model = element(&rt, &format!("{alt}.model"));
            let remove = element(&rt, &format!("{alt}.remove"));
            let on_its_line = (middle(&remove) - middle(&model)).abs() < 0.5;
            let under_it = remove.rect[1] >= bottom(&model);
            assert!(
                on_its_line || under_it,
                "{variant} {alt}: its remove button at {:?} is neither on its model's line nor \
                 under it, the model at {:?}",
                remove.rect,
                model.rect
            );
            assert!(
                (right(&remove) - trailing).abs() < 0.5,
                "{variant} {alt}: its remove button ends at {}, not at the trailing edge {trailing}",
                right(&remove)
            );
        }
    }
}

#[test]
fn a_closed_alternatives_condition_wraps_to_two_lines_and_no_further() {
    for variant in ["Open", "Open narrow", "Open smallest long"] {
        let rt = block_story(true, "Blocks/Table", variant);
        let one_line = element(&rt, "table.alt.fast.note.label").rect[3];
        let note = element(&rt, "table.alt.fast.note");
        let lines = (note.rect[3] / one_line).round();
        let glyph_rows = {
            let mut centres: Vec<f32> = shown_glyphs(&rt, note.rect)
                .iter()
                .map(|g| g[1] + g[3] / 2.0)
                .collect();
            centres.sort_by(f32::total_cmp);
            centres.dedup_by(|a, b| (*a - *b).abs() < one_line / 2.0);
            centres.len()
        };
        assert!(
            lines <= 2.0 && glyph_rows as f32 == lines,
            "{variant}: the condition stands {lines} lines high and draws {glyph_rows}, past 2"
        );
        if variant == "Open smallest long" {
            assert_eq!(
                lines, 2.0,
                "{variant}: a long condition keeps one line where it has room for two"
            );
        }
    }
}

#[test]
fn a_tables_control_column_has_one_left_edge() {
    for (variant, keys) in [
        (
            "Rest",
            &[
                "table.builder.model",
                "table.builder.alternatives",
                "table.orchestrator.model",
                "table.reviewer.model",
                "table.fallback.model",
            ][..],
        ),
        (
            "Open",
            &[
                "table.builder.model",
                "table.alt.fast.model",
                "table.alt.unnamed.model",
                "table.builder.add-alternative",
                "table.orchestrator.model",
            ][..],
        ),
    ] {
        let rt = block_story(true, "Blocks/Table", variant);
        let edges: Vec<(&str, f32)> = keys
            .iter()
            .map(|k| (*k, words_left(&rt, &element(&rt, k))))
            .collect();
        let first = edges[0].1;
        for (key, edge) in &edges {
            assert!(
                (edge - first).abs() <= GLYPH_SLACK,
                "{variant}: {key}'s words start at {edge}, off the column's edge at {first}"
            );
        }
    }
}

#[test]
fn a_sub_row_starts_twelve_from_its_rail() {
    for variant in ["Open", "Sub-row open", "Open narrow"] {
        let rt = block_story(true, "Blocks/Table", variant);
        let column = words_left(&rt, &element(&rt, "table.builder.instructions.label"));
        for sub in ["table.alt.fast", "table.alt.unnamed"] {
            let at = words_left(&rt, &element(&rt, sub));
            let want = column + RAIL + 12.0;
            assert!(
                (at - want).abs() <= GLYPH_SLACK,
                "{variant} {sub}: its words start at {at}, not 12 past the rail at {}",
                column + RAIL
            );
        }
    }
}

#[test]
fn an_open_rows_details_end_with_the_name_column() {
    for variant in ["Open", "Sub-row open", "Open narrow"] {
        let rt = block_story(true, "Blocks/Table", variant);
        let who = element(&rt, "table.builder");
        let model = element(&rt, "table.builder.model");
        let names_end = if model.rect[0] > right(&who) {
            model.rect[0]
        } else {
            right(&element(&rt, "table.builder.more"))
        };
        for key in [
            "table.builder.instructions.field",
            "table.alt.fast.name.field",
        ] {
            let Some(details) = rt
                .elements()
                .iter()
                .find(|e| e.key.as_deref() == Some(key))
                .cloned()
            else {
                continue;
            };
            assert!(
                details.rect[2] <= FIELD + 0.5,
                "{variant} {key}: {} wide, past the field measure {FIELD}",
                details.rect[2]
            );
            assert!(
                right(&details) <= names_end + 0.5,
                "{variant} {key}: ends at {}, past the name column's end at {names_end}",
                right(&details)
            );
        }
    }
}

#[test]
fn a_narrow_tables_head_names_only_the_columns_on_its_line() {
    for (variant, letters) in [("Rest", "ROLERUNSON".len()), ("Narrow", "ROLE".len())] {
        let rt = block_story(true, "Blocks/Table", variant);
        let head = element(&rt, "table.head");
        let shown = shown_glyphs(&rt, head.rect);
        assert_eq!(
            shown.len(),
            letters,
            "{variant}: the head shows {} letters, not the {letters} of the columns on its line",
            shown.len()
        );
        let cut = shown.iter().find(|g| g[1] + g[3] > bottom(&head) + 0.5);
        assert!(
            cut.is_none(),
            "{variant}: a head label at {cut:?} runs past the head's edge"
        );
    }
}

#[test]
fn a_tables_footer_starts_on_the_name_column() {
    for variant in ["Rest", "Narrow", "Open"] {
        let rt = block_story(true, "Blocks/Table", variant);
        let names = words_left(&rt, &element(&rt, "table.head"));
        let footer = words_left(&rt, &element(&rt, "table.add"));
        assert!(
            (footer - names).abs() <= GLYPH_SLACK,
            "{variant}: the footer's words start at {footer}, off the name column at {names}"
        );
    }
}

/// How far two neighbouring glyphs' boxes reach into each other in a word, as kerning sets them.
const KERN: f32 = 2.0;

#[test]
fn a_fields_label_and_note_never_overlap() {
    for variant in ["Rest", "Long", "Narrow", "Narrow long"] {
        let rt = block_story(true, "Blocks/Field", variant);
        for key in ["field.name.label", "field.path.label"] {
            let glyphs = shown_glyphs(&rt, element(&rt, key).rect);
            for (i, a) in glyphs.iter().enumerate() {
                for b in &glyphs[i + 1..] {
                    let across = (a[0] + a[2]).min(b[0] + b[2]) - a[0].max(b[0]);
                    let down = (a[1] + a[3]).min(b[1] + b[3]) - a[1].max(b[1]);
                    let apart = across <= KERN || down <= KERN;
                    assert!(apart, "{variant} {key}: glyphs at {a:?} and {b:?} overlap");
                }
            }
        }
    }
}

#[test]
fn a_switched_off_empty_table_draws_what_it_offers_as_switched_on() {
    for dark in [true, false] {
        let t = tokens(dark);
        let off = block_story(dark, "Blocks/Table", "Empty off");
        let on = block_story(dark, "Blocks/Table", "Empty");
        let empty = device(element(&off, "table.empty").rect);
        let mut checked = 0;
        for part in parts(&off) {
            let (n, _, _) = point_of(&off, part);
            if !holds_all(empty, off.instances[n].rect) {
                continue;
            }
            let edge = matches!(part, Part::Edge(_));
            let m = on
                .instances
                .iter()
                .position(|r| same_part(&off.instances[n], r, edge))
                .expect("the empty state draws the same parts switched on");
            let seen = shown(&off, part, t.bg_panel);
            let lit = shown(
                &on,
                if edge { Part::Edge(m) } else { Part::Fill(m) },
                t.bg_panel,
            );
            checked += 1;
            assert!(
                (0..3).all(|c| (seen[c] - lit[c]).abs() <= BLEND),
                "dark={dark}: the off table's empty state shows {seen:?} at {:?}, and {lit:?} switched on",
                off.instances[n].rect.map(|v| v / SCALE)
            );
        }
        assert!(checked > 0, "dark={dark}: the empty state draws nothing");
    }
}

/// Rows of the Table story that carry a tag, by the key of the name that opens the row and the
/// key of the tag.
const TAGGED_ROWS: [(&str, &str); 2] = [
    ("table.builder", "table.builder.tag.Attn"),
    ("table.orchestrator", "table.orchestrator.tag.Needs a model"),
];

/// How far the tag keyed `key` draws its edge from what the frame paints under the tag.
fn tag_edge_step(rt: &Runtime<()>, key: &str, ground: rune::Color) -> f32 {
    let tag = device(element(rt, key).rect);
    let (at, edge) = rt
        .instances
        .iter()
        .enumerate()
        .find(|(_, i)| near(i.rect, tag) && i.border[3] > 0.0 && i.params[0] > 0.0)
        .unwrap_or_else(|| panic!("{key} draws no edge"));
    let ground = [ground.r, ground.g, ground.b].map(rune::theme::srgb_encode);
    let under = rt.instances[..at]
        .iter()
        .filter(|i| holds_all(i.rect, tag))
        .fold(ground, |below, i| over(i.fill, below));
    contrast(over(edge.border, under), under)
}

#[test]
fn a_tags_edge_stands_a_hover_step_from_every_fill_it_sits_on() {
    for dark in [true, false] {
        let t = tokens(dark);
        let step = if dark {
            ROW_STEPS_DARK.0
        } else {
            ROW_STEPS_LIGHT.0
        } - ROUNDING;
        for (name, tag) in TAGGED_ROWS {
            for variant in ["Rest", "Open"] {
                let mut rt = block_story(dark, "Blocks/Table", variant);
                let mut seen = vec![(variant, tag_edge_step(&rt, tag, t.bg_app))];
                let at = element(&rt, name).rect;
                let (x, y) = (at[0] + at[2] / 2.0, at[1] + at[3] / 2.0);
                rt.pointer_move(x, y);
                rt.frame(1.0);
                rt.frame(1.0);
                seen.push(("hovered", tag_edge_step(&rt, tag, t.bg_app)));
                rt.pointer_down(x, y);
                rt.frame(1.0);
                rt.frame(1.0);
                seen.push(("pressed", tag_edge_step(&rt, tag, t.bg_app)));
                for (on, measured) in seen {
                    assert!(
                        measured >= step,
                        "dark={dark}: {tag}'s edge on its {variant} row {on} stands {measured:.2}:1 from what it sits on, under the hover step {step:.2}:1"
                    );
                }
            }
        }
    }
}

/// The table's widths a window from the smallest to the widest Settings page gives it.
const TABLE_SWEEP: std::ops::RangeInclusive<u32> = 160..=800;

#[test]
fn a_tables_head_names_details_and_wrapped_controls_start_on_one_edge_at_every_width() {
    let mut rt = block_story(true, "Blocks/Table", "Open, window wide");
    let page_sides = 2.0 * 32.0;
    for content in TABLE_SWEEP {
        let width = content as f32 + page_sides;
        rt.set_viewport((width, BLOCK_WINDOW.1), SCALE);
        rt.frame(1.0);
        rt.frame(1.0);
        let elements = rt.elements();
        let within = |key: &str, text: &str| {
            let at = element(&rt, key);
            elements
                .iter()
                .find(|e| {
                    e.path.starts_with(&format!("{}/", at.path)) && e.text.as_deref() == Some(text)
                })
                .unwrap_or_else(|| panic!("at {content}: no {text:?} in {key}"))
                .rect[0]
        };
        let head = within("table.head", "ROLE");
        let title = within("table.builder", "Builder");
        let details = element(&rt, "table.builder.instructions.value").rect[0];
        assert!(
            (title - head).abs() < 0.5 && (details - head).abs() < 0.5,
            "at {content} wide: the head starts at {head}, the title at {title}, the details at {details}"
        );
        for (row, control, said) in [
            (
                "table.builder",
                "table.builder.model",
                "Claude · Opus · high",
            ),
            ("table.alt.fast", "table.alt.fast.model", "Codex · gpt-5"),
            (
                "table.alt.unnamed",
                "table.alt.unnamed.model",
                "Codex · gpt-5",
            ),
        ] {
            if element(&rt, control).rect[1] > element(&rt, row).rect[1] + 0.5 {
                let words = within(control, said);
                assert!(
                    (words - title).abs() < 0.5,
                    "at {content} wide: {control} wraps under its row with its words at {words}, \
                     off the names' edge at {title}"
                );
            }
        }
    }
}

/// Each switched-off state of the Table story beside the same state switched on, with every
/// control's focus ring showing.
const OFF_BESIDE_ON: [(&str, &str); 2] =
    [("Off, focus", "Focus"), ("Off, open, focus", "Open, focus")];

#[test]
fn a_switched_off_table_fades_what_its_rows_say_and_draws_its_controls_whole() {
    for dark in [true, false] {
        let t = tokens(dark);
        for (off_variant, on_variant) in OFF_BESIDE_ON {
            let off = block_story(dark, "Blocks/Table", off_variant);
            let on = block_story(dark, "Blocks/Table", on_variant);
            let table = format!("{}/", element(&off, "table").path);
            let elements: Vec<ElementInfo> = off
                .elements()
                .into_iter()
                .filter(|e| e.path.starts_with(&table))
                .collect();
            let names: Vec<&ElementInfo> = elements
                .iter()
                .filter(|e| {
                    e.key.as_ref().is_some_and(|k| {
                        elements
                            .iter()
                            .any(|r| r.key.as_deref() == Some(&format!("{k}.row")))
                    })
                })
                .collect();
            let controls: Vec<[f32; 4]> = elements
                .iter()
                .filter(|e| e.focusable && !names.iter().any(|n| n.path == e.path))
                .map(|e| device(e.rect))
                .collect();
            assert!(!controls.is_empty(), "{off_variant} draws no control");
            let same_as_on = |part: Part, what: &str| {
                let (n, _, _) = point_of(&off, part);
                let edge = matches!(part, Part::Edge(_));
                let m = on
                    .instances
                    .iter()
                    .position(|r| same_part(&off.instances[n], r, edge))
                    .unwrap_or_else(|| panic!("{off_variant}: {what} has no part switched on"));
                let seen = shown(&off, part, t.bg_panel);
                let lit = shown(
                    &on,
                    if edge { Part::Edge(m) } else { Part::Fill(m) },
                    t.bg_panel,
                );
                assert!(
                    (0..3).all(|c| (seen[c] - lit[c]).abs() <= BLEND),
                    "dark={dark}, {off_variant}: {what} at {:?} shows {seen:?}, and {lit:?} switched on",
                    off.instances[n].rect.map(|v| v / SCALE)
                );
            };
            for part in parts(&off) {
                let (n, _, _) = point_of(&off, part);
                if controls
                    .iter()
                    .any(|&c| holds_all(c, off.instances[n].rect))
                {
                    same_as_on(part, "a control's part");
                }
            }
            for e in elements.iter().filter(|e| e.focusable) {
                let ring = ring_of(&off, e, &t)
                    .unwrap_or_else(|| panic!("{off_variant}: {:?} draws no ring", e.key));
                let n = off
                    .instances
                    .iter()
                    .position(|i| std::ptr::eq(i, ring))
                    .expect("the ring is one of the frame's instances");
                same_as_on(Part::Edge(n), "a focus ring");
            }
            let ground = [t.bg_panel.r, t.bg_panel.g, t.bg_panel.b].map(rune::theme::srgb_encode);
            let orchestrator = element(&off, "table.orchestrator");
            let name = device(orchestrator.rect);
            let mut words = 0;
            for part in parts(&off) {
                let (n, _, _) = point_of(&off, part);
                let i = &off.instances[n];
                if i.params[1] != rune::render::KIND_GLYPH || !holds_all(name, i.rect) {
                    continue;
                }
                let m = on
                    .instances
                    .iter()
                    .position(|r| same_part(i, r, false))
                    .expect("a name's glyph is drawn switched on");
                let lit = shown(&on, Part::Fill(m), t.bg_panel);
                let half = [0, 1, 2].map(|c| lit[c] * STRENGTH + ground[c] * (1.0 - STRENGTH));
                let seen = shown(&off, part, t.bg_panel);
                words += 1;
                assert!(
                    (0..3).all(|c| (seen[c] - half[c]).abs() <= BLEND),
                    "dark={dark}, {off_variant}: a glyph of {:?} shows {seen:?}, not half of {lit:?}",
                    orchestrator.key
                );
            }
            assert!(
                words > 0,
                "{off_variant}: the Orchestrator's name draws no glyph"
            );
        }
    }
}

/// Each text size the app offers, 0.7 to 1.5 in tenths: the view zooms by it, which lays it out
/// in a window `1 / size` as many points across, at `size` times the display's scale.
fn at_text_size(rt: &mut Runtime<()>, size: f32, scale: f32) {
    rt.set_viewport((BLOCK_WINDOW.0 / size, BLOCK_WINDOW.1 / size), scale * size);
    rt.frame(0.0);
}

#[test]
fn a_notices_actions_share_a_line_wherever_its_line_holds_them_at_every_text_size() {
    for variant in ["Rest", "Narrow"] {
        let mut rt = block_story(true, "Blocks/Notice and status", variant);
        for scale in [1.0, 2.0] {
            for tenths in 7..=15 {
                let size = tenths as f32 / 10.0;
                at_text_size(&mut rt, size, scale);
                let notice = element(&rt, "notice.conflict").rect;
                let reload = element(&rt, "notice.conflict.reload").rect;
                let overwrite = element(&rt, "notice.conflict.overwrite").rect;
                let line = notice[0] + notice[2] - REGULAR_INSET - reload[0];
                let both = reload[2] + LOOSE_GAP + overwrite[2];
                if both <= line {
                    assert_eq!(
                        reload[1], overwrite[1],
                        "{variant} at {size} text on a {scale}x display: Overwrite anyway sits under Reload from disk though the line's {line} holds their {both}"
                    );
                }
            }
        }
    }
}

const FACTS: &[&str] = &["facts.session.state.changed", "facts.pr.updated"];
#[test]
fn an_end_columns_numbers_and_title_share_its_right_edge() {
    for variant in ["Rest", "Focus"] {
        let rt = block_story(true, "Blocks/Diagnostics table", variant);
        for column in 0..2 {
            let right = |e: &ElementInfo| e.rect[0] + e.rect[2];
            let edge = right(&element(&rt, &format!("facts.head.{column}")));
            for row in FACTS {
                let at = right(&element(&rt, &format!("{row}.cell.{column}")));
                assert!(
                    (at - edge).abs() <= 0.5,
                    "{variant} {row}: column {column}'s number ends at {at}, off its title's end \
                     at {edge}"
                );
            }
        }
    }
}

#[test]
fn an_identifier_wraps_only_after_its_dots() {
    let line = roles::MONO.line_px();
    for variant in ["Long", "Narrow long", "Smallest long"] {
        let rt = block_story(true, "Blocks/Diagnostics table", variant);
        let row = "facts.workspace.terminal.layout.snapshot.persisted.across.every.profile";
        let parts: Vec<ElementInfo> = (0..8)
            .map(|i| element(&rt, &format!("{row}.name.{i}")))
            .collect();
        for (i, part) in parts.iter().enumerate() {
            assert!(
                part.rect[3] <= line + 0.5,
                "{variant}: part {i} is {} tall, more than one {line} line: it broke inside",
                part.rect[3]
            );
        }
        let lines = parts
            .windows(2)
            .filter(|w| w[1].rect[1] > w[0].rect[1] + 0.5)
            .count();
        assert!(
            variant == "Long" || lines > 0,
            "{variant}: the long identifier stands on one line"
        );
    }
}

#[test]
fn a_compact_tables_one_line_row_is_as_tall_as_its_text() {
    let line = roles::MONO.line_px();
    for variant in ["Rest", "Focus"] {
        let rt = block_story(true, "Blocks/Diagnostics table", variant);
        let row = element(&rt, "facts.pr.updated");
        let want = line + 2.0 * 8.0;
        assert!(
            row.rect[3] <= want + 0.5,
            "{variant}: a one-line row is {} tall, past its text and its room at {want}",
            row.rect[3]
        );
    }
}

#[test]
fn a_tables_column_title_shows_wherever_its_values_show() {
    for variant in ["Rest", "Long", "Narrow", "Narrow long", "Smallest long"] {
        let rt = block_story(true, "Blocks/Diagnostics table", variant);
        for column in 0..2 {
            let title = shown_glyphs(&rt, element(&rt, &format!("facts.head.{column}")).rect);
            let suffix = format!(".cell.{column}");
            let cells: Vec<_> = rt
                .elements()
                .into_iter()
                .filter(|e| e.key.as_deref().is_some_and(|k| k.ends_with(&suffix)))
                .collect();
            assert!(!cells.is_empty(), "{variant}: column {column} has no cells");
            for cell in cells {
                let value = shown_glyphs(&rt, cell.rect);
                assert!(
                    value.is_empty() || !title.is_empty(),
                    "{variant} {:?}: column {column}'s value shows and its title does not",
                    cell.key
                );
            }
        }
    }
}

/// The type roles as these tests state them, apart from the library, so a test fails when the
/// library sets a role differently.
#[allow(clippy::disallowed_methods)]
mod roles {
    /// `Type::Body`, 13/400, as the library sets a note.
    pub const BODY: rune::TextStyle = rune::TextStyle::new(13.0, 400.0);
    /// How a segment sets its words: `Type::Control`, 12/600.
    pub const CONTROL: rune::TextStyle = rune::TextStyle::new(12.0, 600.0);
    /// How the library sets a link in a note: `Type::Name`, 13/600.
    pub const NAME: rune::TextStyle = rune::TextStyle::new(13.0, 600.0);
    /// `Type::Mono`'s size and weight, 12/400.
    pub const MONO: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
}
