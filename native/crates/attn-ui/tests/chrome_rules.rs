//! The rules the chrome's controls hold, read from the gallery's chrome stories: list rows, icon
//! buttons, chips and group heads.

use attn_ui::Tokens;
use attn_ui::chrome_gallery::CHROME_SHEETS;
use attn_ui::colors::scheme_for;
use attn_ui::system::ListRow;
use rune::render::Instance;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Key, KeyEvent, Mods, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
const SCALE: f32 = 2.0;
const RING_REACH: f32 = 4.0;
const ROWS: [&str; 4] = ["row.agent", "row.nav", "row.tile", "row.two"];
const ONE_LINE: [&str; 3] = ["row.agent", "row.nav", "row.tile"];
const KINDS: [&str; 5] = [
    "row.select",
    "row.quiet",
    "row.crew",
    "row.tree",
    "row.dimmed",
];
const ROW_VARIANTS: [&str; 4] = ["Rest", "In view", "Long", "Narrow long"];

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(dark: bool, name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("chrome-rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(dark, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt.frame(0.0);
    rt
}

fn tokens(dark: bool) -> Tokens {
    scheme_for(None).colors(dark).ui
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

fn within(rt: &Runtime<()>, outer: &ElementInfo) -> Vec<ElementInfo> {
    let all = rt.elements();
    all.iter()
        .filter(|e| {
            std::iter::successors(e.parent, |&p| all[p].parent).any(|p| all[p].path == outer.path)
        })
        .cloned()
        .collect()
}

fn device(rect: [f32; 4]) -> [f32; 4] {
    rect.map(|v| v * SCALE)
}

fn near(a: [f32; 4], b: [f32; 4]) -> bool {
    a.iter().zip(b).all(|(a, b)| (a - b).abs() <= 1.0)
}

fn surface(rt: &Runtime<()>, e: &ElementInfo, ground: rune::Color) -> [f32; 3] {
    let ground = [ground.r, ground.g, ground.b].map(rune::theme::srgb_encode);
    rt.instances
        .iter()
        .filter(|i| near(i.rect, device(e.rect)))
        .fold(ground, |below, i| over(i.fill, below))
}

fn over(top: [f32; 4], below: [f32; 3]) -> [f32; 3] {
    [0, 1, 2].map(|c| top[c] + below[c] * (1.0 - top[3]))
}

fn contrast(a: [f32; 3], b: [f32; 3]) -> f32 {
    let luminance = |c: [f32; 3]| {
        let [r, g, b] = c.map(rune::theme::srgb_decode);
        0.2126 * r + 0.7152 * g + 0.0722 * b
    };
    let (a, b) = (luminance(a), luminance(b));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

fn middle(e: &ElementInfo) -> (f32, f32) {
    (e.rect[0] + e.rect[2] / 2.0, e.rect[1] + e.rect[3] / 2.0)
}

/// A point over a control's words rather than its end, which a row's hover controls may cover.
fn on_words(e: &ElementInfo) -> (f32, f32) {
    (e.rect[0] + e.rect[2] / 4.0, e.rect[1] + e.rect[3] / 2.0)
}

/// Whether the control keyed `key` takes a press or the keyboard: not a heading that only names
/// its group.
fn pressable(rt: &Runtime<()>, key: &str) -> bool {
    let e = element(rt, key);
    e.role != "heading" && (e.focusable || within(rt, &e).iter().any(|part| part.focusable))
}

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

fn tab_to(rt: &mut Runtime<()>, key: &str) {
    rt.set_full_keyboard_access(true);
    let target = element(rt, key);
    let stage = rt
        .elements()
        .into_iter()
        .find(|e| target.path.starts_with(&e.path) && e.path.ends_with("playground-stage-1"))
        .expect("the story has a stage");
    let (x, y) = (stage.rect[0] + 2.0, stage.rect[1] + 2.0);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.pointer_leave();
    rt.frame(1.0);
    for _ in 0..40 {
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
        if rt
            .elements()
            .iter()
            .any(|e| e.focus_visible && e.path == target.path)
        {
            return;
        }
    }
    panic!("Tab never reached {key}");
}

/// The key of what a pointer or the keyboard acts on in the control keyed `key`: the control,
/// or the button inside it when it is an item that holds one.
fn target_of(rt: &Runtime<()>, key: &str) -> String {
    let own = element(rt, key);
    if own.focusable {
        return key.to_owned();
    }
    within(rt, &own)
        .into_iter()
        .find(|e| e.focusable)
        .and_then(|e| e.key)
        .unwrap_or_else(|| panic!("{key} holds nothing focusable"))
}

#[test]
fn every_list_row_of_one_line_is_one_height_and_its_title_starts_on_one_line() {
    for dark in [true, false] {
        for variant in ROW_VARIANTS {
            let rt = story(dark, "Chrome/List row", variant);
            let heights: Vec<f32> = ONE_LINE.iter().map(|k| element(&rt, k).rect[3]).collect();
            assert!(
                heights.iter().all(|h| (h - heights[0]).abs() < 0.01),
                "{variant}, dark={dark}: one-line rows stand {heights:?} high"
            );
            let starts: Vec<f32> = ROWS
                .iter()
                .map(|k| element(&rt, &format!("{k}.title")).rect[0] - element(&rt, k).rect[0])
                .collect();
            assert!(
                starts.iter().all(|x| (x - starts[0]).abs() < 0.01),
                "{variant}, dark={dark}: titles start {starts:?} into their rows"
            );
        }
    }
}

#[test]
fn nothing_a_row_draws_runs_past_its_sides_and_the_title_gives_way() {
    for variant in ROW_VARIANTS {
        let rt = story(true, "Chrome/List row", variant);
        for key in ROWS.iter().chain(&KINDS) {
            let row = element(&rt, key);
            let [x, _, w, _] = row.rect;
            for part in within(&rt, &row) {
                let [px, _, pw, _] = part.rect;
                if pw == 0.0 {
                    continue;
                }
                assert!(
                    px >= x - 0.01 && px + pw <= x + w + 0.01,
                    "{variant}: {key}'s {:?} at {px}..{} runs past {x}..{}",
                    part.key,
                    px + pw,
                    x + w
                );
            }
        }
    }
}

#[test]
fn hover_and_pressed_take_the_row_step_on_every_chrome_control() {
    let subjects: Vec<(&str, &str)> = CHROME_SHEETS
        .iter()
        .filter(|sheet| sheet.story != "Chrome/Chip")
        .flat_map(|sheet| sheet.keys.iter().map(move |k| (sheet.story, *k)))
        .collect();
    let mut measured = Vec::new();
    for dark in [true, false] {
        let t = tokens(dark);
        let (hover_floor, pressed_floor) = if dark { (1.20, 1.66) } else { (1.17, 1.50) };
        for &(name, key) in &subjects {
            let mut rt = story(dark, name, "Rest");
            if !pressable(&rt, key) {
                continue;
            }
            let key = &target_of(&rt, key);
            let e = element(&rt, key);
            let rest = surface(&rt, &e, t.bg_panel);
            let (x, y) = on_words(&e);
            rt.pointer_move(x, y);
            rt.frame(1.0);
            let hover = surface(&rt, &element(&rt, key), t.bg_panel);
            rt.pointer_down(x, y);
            rt.frame(1.0);
            rt.frame(1.0);
            let pressed = surface(&rt, &element(&rt, key), t.bg_panel);
            let (h, p) = (contrast(rest, hover), contrast(rest, pressed));
            measured.push(format!(
                "{key} dark={dark}: hover {h:.2}:1, pressed {p:.2}:1"
            ));
            assert!(
                h >= hover_floor - 0.01 && p >= pressed_floor - 0.01,
                "{key}, dark={dark}: hover {h:.2}:1 and pressed {p:.2}:1 against rest"
            );
        }
    }
    println!("{}", measured.join("\n"));
}

#[test]
fn a_rows_hover_controls_show_whole_on_the_first_frame_on_its_ground_with_its_words_ending_before_them()
 {
    for dark in [true, false] {
        for variant in ROW_VARIANTS {
            for key in ["row.agent", "row.tile", "row.two", "row.keyed"] {
                let mut rt = story(dark, "Chrome/List row", variant);
                let row = element(&rt, key);
                let at_rest = element(&rt, &format!("{key}.title")).visible;
                let controls = format!("{key}.controls");
                assert!(
                    element(&rt, &controls).opacity < 0.01,
                    "{variant}: {key}'s controls show before the pointer is over it"
                );
                let (x, y) = middle(&row);
                rt.pointer_move(x, y);
                rt.frame(1.0 / 60.0);
                assert!(
                    !rt.frame(1.0 / 60.0),
                    "{variant}, dark={dark}: {key} asks for frames after its hover showed: something moves"
                );
                let first = element(&rt, &controls);
                assert!(
                    first.opacity > 0.999,
                    "{variant}, dark={dark}: {key}'s controls are at {} one frame after hover: they move",
                    first.opacity
                );
                assert!(
                    first.fill.a < 0.001,
                    "{variant}, dark={dark}: {key}'s controls lie on a ground of their own, {:?}",
                    first.fill
                );
                let title = element(&rt, &format!("{key}.title"));
                let controls_start = rt
                    .elements()
                    .into_iter()
                    .filter(|e| {
                        e.key.as_deref().is_some_and(|k| {
                            k.starts_with(&format!("{key}.")) && e.role == "button"
                        })
                    })
                    .map(|e| e.rect[0])
                    .fold(f32::INFINITY, f32::min);
                let words_end = title.visible[0] + title.visible[2];
                assert!(
                    words_end <= controls_start,
                    "{variant}, dark={dark}: {key}'s words reach {words_end} under its controls at {controls_start}"
                );
                if key != "row.keyed" {
                    assert!(
                        title.visible[2] >= TITLE_FLOOR,
                        "{variant}, dark={dark}: {key}'s title shows {} points under its controls",
                        title.visible[2]
                    );
                }
                rt.pointer_leave();
                rt.frame(1.0 / 60.0);
                let left = element(&rt, &format!("{key}.title")).visible;
                assert!(
                    near(left, at_rest),
                    "{variant}, dark={dark}: {key}'s words show {left:?} one frame after the pointer left, {at_rest:?} at rest: they move"
                );
            }
        }
    }
}

#[test]
fn every_chrome_control_draws_its_ring_whole_and_undimmed() {
    for dark in [true, false] {
        let t = tokens(dark);
        for sheet in CHROME_SHEETS.iter().filter(|s| s.story != "Chrome/Chip") {
            for key in sheet.keys {
                let mut rt = story(dark, sheet.story, "Rest");
                if !pressable(&rt, key) {
                    continue;
                }
                let target = target_of(&rt, key);
                tab_to(&mut rt, &target);
                let e = element(&rt, &target);
                let ring = ring_of(&rt, &e, &t)
                    .unwrap_or_else(|| panic!("{key}, dark={dark}: draws no ring"));
                let [x, y, w, h] = ring.rect;
                let [x0, y0, x1, y1] = ring.clip;
                assert!(
                    x0 <= x && y0 <= y && x + w <= x1 && y + h <= y1,
                    "{key}, dark={dark}: its ring {:?} is cut by {:?}",
                    ring.rect,
                    ring.clip
                );
                assert!(
                    ring.border[3] >= 0.999,
                    "{key}, dark={dark}: its ring is dimmed"
                );
            }
        }
    }
}

#[test]
fn an_icon_only_button_gives_its_icon_one_room_on_every_side_and_a_count_sits_beside_it() {
    let rt = story(true, "Chrome/Icon button", "Rest");
    for key in ["icon.18-in-26", "icon.16-in-32"] {
        let button = element(&rt, key);
        let [bx, by, bw, bh] = button.rect;
        assert!((bw - bh).abs() < 0.01, "{key} is {bw} by {bh}, not square");
        let icon = within(&rt, &button)
            .into_iter()
            .filter(|e| e.rect[2] > 0.0 && e.rect[2] < bw)
            .min_by(|a, b| a.rect[2].total_cmp(&b.rect[2]))
            .unwrap_or_else(|| panic!("{key} draws no icon"));
        let [ix, iy, iw, ih] = icon.rect;
        let sides = [ix - bx, iy - by, bx + bw - ix - iw, by + bh - iy - ih];
        assert!(
            sides.iter().all(|s| (s - sides[0]).abs() < 0.51),
            "{key}'s icon sits {sides:?} from its sides"
        );
    }
    for variant in ["Rest", "Long"] {
        let rt = story(true, "Chrome/Icon button", variant);
        let pill = element(&rt, "icon.18-counted");
        let parts: Vec<ElementInfo> = within(&rt, &pill)
            .into_iter()
            .filter(|e| e.rect[2] > 0.0 && e.rect[2] < pill.rect[2] - 1.0)
            .collect();
        for a in &parts {
            for b in &parts {
                let apart = a.rect[0] + a.rect[2] <= b.rect[0] + 0.01
                    || b.rect[0] + b.rect[2] <= a.rect[0] + 0.01;
                let nested = a.path.starts_with(&b.path) || b.path.starts_with(&a.path);
                assert!(
                    apart || nested,
                    "{variant}: the count and the icon overlap: {:?} and {:?}",
                    a.rect,
                    b.rect
                );
            }
        }
    }
}

#[test]
fn a_two_line_rows_lead_and_controls_centre_on_its_titles_line() {
    for variant in ROW_VARIANTS {
        let mut rt = story(true, "Chrome/List row", variant);
        let row = element(&rt, "row.two");
        let (x, y) = middle(&row);
        rt.pointer_move(x, y);
        rt.frame(1.0);
        let title = element(&rt, "row.two.title");
        let centre = |e: &ElementInfo| e.rect[1] + e.rect[3] / 2.0;
        let line = centre(&title);
        let lead = within(&rt, &row)
            .into_iter()
            .find(|e| e.key.is_none() && e.rect[2] == 8.0)
            .expect("row.two leads with its dot");
        for (what, e) in [
            ("lead", lead),
            ("rename", element(&rt, "row.two.rename")),
            ("select's run", element(&rt, "row.select.run")),
        ] {
            let off = if what == "select's run" {
                centre(&e) - centre(&element(&rt, "row.select.title"))
            } else {
                centre(&e) - line
            };
            assert!(
                off.abs() < 0.51,
                "{variant}: the {what} centres {off} off its title's line"
            );
        }
    }
}

#[test]
fn quiet_controls_are_faint_until_their_row_is_selected_and_a_tree_row_steps_in() {
    let rest = story(true, "Chrome/List row", "Rest");
    let selected = story(true, "Chrome/List row", "In view");
    let faint = element(&rest, "row.quiet.controls").opacity;
    let full = element(&selected, "row.quiet.controls").opacity;
    assert!(
        faint < 0.75 && full > 0.999,
        "quiet controls stand at {faint} at rest and {full} on the selected row"
    );
    let start = |rt: &Runtime<()>, key: &str| {
        element(rt, &format!("{key}.title")).rect[0] - element(rt, key).rect[0]
    };
    let step = start(&rest, "row.tree") - start(&rest, "row.nav");
    assert!(
        (step - 24.0).abs() < 0.01,
        "a row two levels deep starts its title {step} further in, not two steps of 12"
    );
}

#[test]
fn a_rows_hover_controls_take_its_badges_room_and_leave_its_kept_controls_in_view_and_tab_after_them()
 {
    for dark in [true, false] {
        for variant in ROW_VARIANTS {
            let mut rt = story(dark, "Chrome/List row", variant);
            let (x, y) = middle(&element(&rt, "row.agent"));
            rt.pointer_move(x, y);
            rt.frame(1.0 / 60.0);
            let badges = element(&rt, "row.agent.badges");
            assert!(
                badges.opacity < 0.01,
                "{variant}, dark={dark}: row.agent's badges show at {} under its hover controls",
                badges.opacity
            );

            let mut rt = story(dark, "Chrome/List row", variant);
            let (x, y) = middle(&element(&rt, "row.crew"));
            rt.pointer_move(x, y);
            rt.frame(1.0 / 60.0);
            let over = element(&rt, "row.crew.controls.hover");
            let stays = element(&rt, "row.crew.start");
            assert!(
                over.rect[0] + over.rect[2] <= stays.rect[0] + 0.5,
                "{variant}, dark={dark}: row.crew's hover controls end at {} over what it keeps from {}",
                over.rect[0] + over.rect[2],
                stays.rect[0]
            );
        }
    }
    let mut rt = story(true, "Chrome/List row", "Rest");
    tab_to(&mut rt, "row.crew");
    rt.key(KeyEvent {
        key: Key::Tab,
        mods: Mods::default(),
    });
    rt.frame(1.0);
    let focused = rt.elements().into_iter().find(|e| e.focus_visible);
    assert_eq!(
        focused.and_then(|e| e.key).as_deref(),
        Some("row.crew.start"),
        "Tab reaches the control a row keeps in view before its hover controls"
    );
}

#[test]
fn a_still_row_takes_no_press_or_focus_and_a_wrapped_row_grows_with_its_lead_on_its_first_line() {
    for dark in [true, false] {
        let t = tokens(dark);
        let mut rt = story(dark, "Chrome/List row", "Narrow long");
        let still = element(&rt, "row.still");
        assert_eq!(still.role, "listitem");
        assert!(!still.focusable, "dark={dark}: a still row takes the focus");
        let rest = surface(&rt, &still, t.bg_panel);
        let (x, y) = middle(&still);
        rt.pointer_move(x, y);
        rt.frame(1.0);
        let hovered = surface(&rt, &element(&rt, "row.still"), t.bg_panel);
        assert!(
            contrast(rest, hovered) < 1.01,
            "dark={dark}: a still row lights under the pointer"
        );
        let wrap = element(&rt, "row.wrap");
        assert!(
            wrap.rect[3] > ListRow::two_line_height() + 1.0,
            "dark={dark}: a wrapped row of long words stays {} high",
            wrap.rect[3]
        );
        let line = element(&rt, "row.wrap.subtitle");
        assert!(
            line.visible[2] >= line.rect[2] - 0.01,
            "dark={dark}: a wrapped row's line is cut"
        );
        let lead = element(&rt, "row.wrap.lead");
        let title = element(&rt, "row.wrap.title");
        let (lead_y, title_y) = (
            lead.rect[1] + lead.rect[3] / 2.0,
            title.rect[1] + title.rect[3] / 2.0,
        );
        assert!(
            (lead_y - title_y).abs() < 1.0,
            "dark={dark}: a wrapped row's lead centres at {lead_y}, its title's first line at {title_y}"
        );
    }
}

/// The narrowest a row's title may get: "Nightly" in `Name`, 46 points as the review of landing 1
/// measured it, so a name keeps its first word in the narrowest sidebar.
const TITLE_FLOOR: f32 = 46.0;

fn inside(inner: [f32; 4], outer: [f32; 4]) -> bool {
    inner[0] >= outer[0] - 0.01
        && inner[1] >= outer[1] - 0.01
        && inner[0] + inner[2] <= outer[0] + outer[2] + 0.01
        && inner[1] + inner[3] <= outer[1] + outer[3] + 0.01
}

#[test]
fn a_select_rows_state_box_holds_every_part_and_its_tag_drops_under_before_its_title_is_cut() {
    for variant in ROW_VARIANTS {
        let mut rt = story(true, "Chrome/List row", variant);
        let row = element(&rt, "row.select");
        let open = element(&rt, "row.select.open");
        assert!(
            near(open.rect, row.rect),
            "{variant}: the row's button {:?} is not the row {:?}",
            open.rect,
            row.rect
        );
        for part in ["row.select.title", "row.select.tag", "row.select.run"] {
            let e = element(&rt, part);
            assert!(
                inside(e.rect, open.rect),
                "{variant}: {part} {:?} lies outside the row's state box {:?}",
                e.rect,
                open.rect
            );
        }
        let title = element(&rt, "row.select.title");
        let tag = element(&rt, "row.select.tag");
        let on_line =
            ((tag.rect[1] + tag.rect[3] / 2.0) - (title.rect[1] + title.rect[3] / 2.0)).abs() < 0.6;
        let under = tag.rect[1] >= title.rect[1] + title.rect[3] - 0.01;
        assert!(
            on_line || under,
            "{variant}: the tag {:?} is neither on the title's line nor under it {:?}",
            tag.rect,
            title.rect
        );
        assert!(
            title.rect[2] >= TITLE_FLOOR,
            "{variant}: the title gets {} points",
            title.rect[2]
        );
        let corner = (row.rect[0] + 2.0, row.rect[1] + row.rect[3] - 2.0);
        click(&mut rt, corner);
        assert_eq!(
            presses(&rt),
            1,
            "{variant}: a press on the row's padding opens nothing"
        );
    }
}

#[test]
fn a_rows_hover_fade_reaches_only_the_line_its_controls_stand_on() {
    for variant in ROW_VARIANTS {
        let mut rt = story(true, "Chrome/List row", variant);
        let row = element(&rt, "row.two");
        let rest = element(&rt, "row.two.subtitle").visible;
        let (x, y) = middle(&row);
        rt.pointer_move(x, y);
        rt.frame(1.0 / 60.0);
        let hovered = element(&rt, "row.two.subtitle").visible;
        assert!(
            near(rest, hovered),
            "{variant}: hovering cuts the line under the controls from {rest:?} to {hovered:?}"
        );
    }
}

const DISABLED: [(&str, &[&str]); 3] = [
    ("Chrome/List row", &["row.agent", "row.select"]),
    ("Chrome/Group head", &["head.empty", "head.toggle"]),
    ("Chrome/Icon button", &["icon.16-in-32"]),
];

fn presses(rt: &Runtime<()>) -> u32 {
    element(rt, "stage.presses")
        .label
        .and_then(|said| said.parse().ok())
        .expect("the stage counts its presses")
}

fn click(rt: &mut Runtime<()>, (x, y): (f32, f32)) {
    rt.pointer_move(x, y);
    rt.frame(1.0 / 60.0);
    rt.pointer_down(x, y);
    rt.pointer_up(x, y);
    rt.frame(1.0 / 60.0);
}

#[test]
fn a_disabled_piece_takes_no_tab_stop_and_no_press_on_any_part_of_it() {
    for (name, keys) in DISABLED {
        for key in keys {
            let mut rt = story(true, name, "Rest");
            let piece = element(&rt, key);
            click(&mut rt, middle(&piece));
            assert!(
                presses(&rt) > 0,
                "{key} takes no press while enabled, so the probe proves nothing"
            );

            let mut rt = story(true, name, "Disabled");
            let piece = element(&rt, key);
            rt.pointer_move(middle(&piece).0, middle(&piece).1);
            rt.frame(1.0 / 60.0);
            let parts: Vec<ElementInfo> = std::iter::once(piece.clone())
                .chain(within(&rt, &piece))
                .filter(|e| e.path == piece.path || e.role == "button" || e.focusable)
                .collect();
            for part in &parts {
                click(&mut rt, middle(part));
                assert_eq!(
                    presses(&rt),
                    0,
                    "disabled {key}: a click on {:?} ran its press",
                    part.key
                );
            }

            rt.set_full_keyboard_access(true);
            for _ in 0..40 {
                rt.key(KeyEvent {
                    key: Key::Tab,
                    mods: Mods::default(),
                });
                rt.frame(1.0);
                let all = rt.elements();
                let inside = all.iter().find(|e| {
                    e.focused
                        && (e.path == piece.path
                            || std::iter::successors(e.parent, |&p| all[p].parent)
                                .any(|p| all[p].path == piece.path))
                });
                assert!(
                    inside.is_none(),
                    "disabled {key}: Tab stops on {:?}",
                    inside.and_then(|e| e.key.clone())
                );
            }
        }
    }
}

thread_local! {
    static PILL_TOKENS: std::cell::Cell<Option<Tokens>> = const { std::cell::Cell::new(None) };
}

fn pill_tokens() -> Tokens {
    PILL_TOKENS
        .with(|cell| cell.get())
        .expect("the pill's tokens")
}

fn pill_view(t: &Tokens, _: &mut Cx<Tokens>) -> El {
    rune::row()
        .items_start()
        .child(attn_ui::system::pill("pill", "Set up ⏎", t))
}

#[test]
fn a_pill_stands_on_its_own_ground_and_its_words_read_on_it_in_every_scheme() {
    for scheme in attn_ui::colors::SCHEMES.iter() {
        for dark in [true, false] {
            PILL_TOKENS.with(|cell| cell.set(Some(scheme.colors(dark).ui)));
            let mut rt = App::new(AppSpec {
                fonts: attn_ui::FONTS,
                families: attn_ui::FAMILIES,
                ..AppSpec::new("pill", WINDOW, pill_tokens, pill_view)
            })
            .headless_runtime(WINDOW, Env::new(dark, SCALE));
            rt.frame(0.0);
            let pill = rt
                .elements()
                .into_iter()
                .find(|e| e.key.as_deref() == Some("pill"))
                .expect("the pill");
            assert!(
                pill.fill.a > 0.999,
                "{}, dark={dark}: the pill's ground lets the row show through at {}",
                scheme.id,
                pill.fill.a
            );
            assert!(
                pill.rect[3] <= ListRow::height(),
                "the pill stands {} tall, past a row's {}",
                pill.rect[3],
                ListRow::height()
            );
            let solid = |c: rune::Color| [c.r, c.g, c.b].map(rune::theme::srgb_encode);
            let ink = pill.text_color.expect("the pill has words");
            let ratio = contrast(solid(ink), solid(pill.fill));
            assert!(
                ratio >= 4.5,
                "{}, dark={dark}: the pill's words read at {ratio:.2}",
                scheme.id
            );
        }
    }
}

#[test]
fn a_rows_tag_is_drawn_whole_and_clear_of_its_controls_in_every_state() {
    for variant in ROW_VARIANTS {
        for state in ["rest", "hover", "tab"] {
            let mut rt = story(true, "Chrome/List row", variant);
            let row = element(&rt, "row.select");
            match state {
                "hover" => {
                    let (x, y) = middle(&row);
                    rt.pointer_move(x, y);
                    rt.frame(1.0 / 60.0);
                }
                "tab" => tab_to(&mut rt, "row.select.open"),
                _ => {}
            }
            let tag = element(&rt, "row.select.tag");
            assert!(
                tag.opacity > 0.999,
                "{variant}, {state}: the tag shows at {}",
                tag.opacity
            );
            assert!(
                near(tag.visible, tag.rect),
                "{variant}, {state}: the tag shows {:?} of {:?}",
                tag.visible,
                tag.rect
            );
            let overlaps = |a: [f32; 4], b: [f32; 4]| {
                a[0] < b[0] + b[2] && b[0] < a[0] + a[2] && a[1] < b[1] + b[3] && b[1] < a[1] + a[3]
            };
            for control in rt.elements().into_iter().filter(|e| {
                e.role == "button"
                    && e.key
                        .as_deref()
                        .is_some_and(|k| k.starts_with("row.select.") && k != "row.select.open")
            }) {
                assert!(
                    !overlaps(tag.rect, control.rect),
                    "{variant}, {state}: the tag {:?} lies under {:?} {:?}",
                    tag.rect,
                    control.key,
                    control.rect
                );
            }
        }
    }
}

#[test]
fn a_lit_row_stands_apart_from_a_raised_surface() {
    for dark in [true, false] {
        let t = tokens(dark);
        let rt = story(dark, "Chrome/List row", "Rest");
        let row = element(&rt, "row.raised");
        let lit = surface(&rt, &row, t.bg_elevated);
        let raised =
            [t.bg_elevated.r, t.bg_elevated.g, t.bg_elevated.b].map(rune::theme::srgb_encode);
        assert!(
            contrast(lit, raised) > 1.05,
            "dark={dark}: the lit row {lit:?} is the raised surface {raised:?}"
        );
    }
}

#[test]
fn a_wrapped_rows_title_keeps_its_controls_room_at_rest_and_never_fades() {
    for variant in ROW_VARIANTS {
        let mut rt = story(true, "Chrome/List row", variant);
        let row = element(&rt, "row.wrapped");
        let rest = element(&rt, "row.wrapped.title");
        let (x, y) = middle(&row);
        rt.pointer_move(x, y);
        rt.frame(1.0 / 60.0);
        let hovered = element(&rt, "row.wrapped.title");
        assert!(
            near(rest.visible, hovered.visible) && near(rest.rect, hovered.rect),
            "{variant}: hovering changes the wrapped title from {:?} to {:?}",
            rest.visible,
            hovered.visible
        );
        let controls = element(&rt, "row.wrapped.controls");
        assert!(
            rest.rect[0] + rest.rect[2] <= controls.rect[0] + 0.5,
            "{variant}: the wrapped title runs to {} under its controls from {}",
            rest.rect[0] + rest.rect[2],
            controls.rect[0]
        );
    }
}
