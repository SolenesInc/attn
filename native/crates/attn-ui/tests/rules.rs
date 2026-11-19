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

/// Tripwires under the weakest layer measured: Danger's red ink at 8% and 12% on its fill in
/// light, 1.10:1 hovered and 1.26:1 held down (pressed is measured with the pointer still over
/// it, as a press is).
const HOVER_FLOOR: f32 = 1.09;
const PRESSED_FLOOR: f32 = 1.24;

#[test]
fn hover_and_pressed_show_on_every_control_in_dark_and_light() {
    let mut measured = Vec::new();
    for &(name, subject) in PRESSABLE {
        for dark in [true, false] {
            let t = tokens(dark);
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
                h >= HOVER_FLOOR && p >= PRESSED_FLOOR,
                "{subject}, dark={dark}: hover {h:.2}:1 and pressed {p:.2}:1 against rest"
            );
        }
    }
    println!("{}", measured.join("\n"));
}
