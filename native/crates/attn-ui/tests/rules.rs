//! The rules the library's controls hold, read from the gallery's stories: rectangles from the
//! element tree, colours and clips from what the frame draws.

use attn_ui::Tokens;
use attn_ui::blocks::BannerTone;
use attn_ui::colors::scheme_for;
use attn_ui::gallery::{BANNERS, BUTTONS, FIELD_BOXES, SIZES, SUBJECTS};
use rune::render::Instance;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Key, KeyEvent, Mods, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
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
        let red = attn_ui::kit(&t).c.danger.fg;
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
        let body = rune::TextStyle::new(13.0, 400.0);
        let (needed, _) = rt.text.measure("1000000", body, SCALE);
        assert!(
            needed <= room,
            "dark={dark}: 1000000 needs {needed}, the field leaves {room} beside its arrows"
        );
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
const DISABLED: &[(&str, &str)] = &[
    ("Library/Button", "Rest"),
    ("Library/Switch", "Rest"),
    ("Library/Segmented", "Rest"),
    ("Library/Stepper", "Rest"),
    ("Library/Expander", "Closed"),
    ("Library/Choice", "First chosen"),
    ("Library/Press area", "Closed"),
    ("Library/Disclosure", "Closed"),
    ("Library/Trigger", "Rest"),
    ("Library/Field box", "Filled"),
];

/// The parts whose rest look is warm: disabled, they first turn neutral, so they are held to
/// that and not to their rest colour.
const WARM_AT_REST: &[&str] = &[
    "button.primary.regular",
    "button.primary.compact",
    "switch.on",
    "choice.0",
];

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
    for &(name, rest_variant) in DISABLED {
        for dark in [true, false] {
            let t = tokens(dark);
            let rest = story(dark, name, rest_variant);
            let disabled = story(dark, name, "Disabled");
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
                if !within(&disabled, &keys, instance) {
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
const BANNER_VARIANTS: [&str; 8] = [
    "Rest",
    "No action",
    "Sentence only",
    "Notes",
    "Countdown",
    "Two actions",
    "Inline controls",
    "Long",
];

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
fn a_banners_glyph_actions_and_dismiss_sit_on_its_first_line() {
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
            for part in ["action", "second", "dismiss", "countdown"] {
                if let Some(e) = rt.playground_element(&format!("banner.{tone}.{part}")) {
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

/// How the library sets a note: `Type::Body`, 13/400.
const NOTE: rune::TextStyle = rune::TextStyle::new(13.0, 400.0);

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
                let (whole, _) = rune::text::measure(said, &[], NOTE, None);
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
    for &(story, subjects) in attn_ui::block_gallery::BLOCK_SUBJECTS {
        for variant in WIDTHS {
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
    for &(story, _) in attn_ui::block_gallery::BLOCK_SUBJECTS {
        for variant in WIDTHS {
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
