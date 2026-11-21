//! The rules the chrome's controls hold, read from the gallery's chrome stories: list rows, icon
//! buttons, chips and group heads.

use attn_ui::Tokens;
use attn_ui::chrome_gallery::CHROME_SHEETS;
use attn_ui::colors::scheme_for;
use attn_ui::system::ListRow;
use rune::render::Instance;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Key, KeyEvent, Mods, Runtime, el};

/// Tall enough for the List row stage at its narrowest, which stands 1034 points.
const WINDOW: (f32, f32) = (1400.0, 1100.0);
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
    let mut reached = Vec::new();
    for _ in 0..2 {
        rt.key(KeyEvent {
            key: Key::Tab,
            mods: Mods::default(),
        });
        rt.frame(1.0);
        let focused = rt.elements().into_iter().find(|e| e.focus_visible);
        reached.push(focused.and_then(|e| e.key).unwrap_or_default());
    }
    assert_eq!(
        reached,
        ["row.crew.start", "row.crew.actions"],
        "Tab reaches what a row keeps in view, then its hover controls, as rows tabbed before the system"
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

/// Every story that draws pieces disabled, chrome and library alike, and the variant that does.
/// The library's Segmented story dims its missing agent in every variant.
const DIMMED: &[(&str, &str)] = &[
    ("Chrome/List row", "Disabled"),
    ("Chrome/Group head", "Disabled"),
    ("Chrome/Icon button", "Disabled"),
    ("Library/Button", "Disabled"),
    ("Library/Switch", "Disabled"),
    ("Library/Segmented", "Rest"),
    ("Library/Segmented", "Disabled"),
    ("Library/Stepper", "Disabled"),
    ("Library/Expander", "Disabled"),
    ("Library/Choice", "Disabled"),
    ("Library/Press area", "Disabled"),
    ("Library/Disclosure", "Disabled"),
    ("Library/Trigger", "Disabled"),
    ("Library/Field box", "Disabled"),
];

/// Disabled pieces the stories give an explanation, so the rule below has something to hold.
const EXPLAINED: [&str; 4] = ["row.agent", "head.empty", "icon.16-in-32", "segment2"];

fn rest_on(rt: &mut Runtime<()>, at: (f32, f32)) -> Option<String> {
    rt.pointer_move(at.0, at.1);
    rt.frame(1.0 / 60.0);
    rt.frame(1.0);
    rt.help_tooltip().map(|(words, _)| words.to_owned())
}

#[test]
fn a_disabled_piece_says_why_under_the_pointer_and_takes_no_press_focus_or_tab_stop() {
    let mut explained = Vec::new();
    for &(name, variant) in DIMMED {
        let shown = story(true, name, variant);
        let pieces: Vec<ElementInfo> = shown
            .elements()
            .into_iter()
            .filter(|e| e.inert && e.help.is_some() && e.rect[2] > 0.0 && e.rect[3] > 0.0)
            .collect();
        for piece in pieces {
            let mut rt = story(true, name, variant);
            assert_eq!(
                rest_on(&mut rt, middle(&piece)),
                piece.help,
                "{name}, {variant}: the pointer resting on disabled {:?} shows no explanation",
                piece.key
            );

            rt.pointer_down(middle(&piece).0, middle(&piece).1);
            rt.frame(1.0 / 60.0);
            let held = rt
                .elements()
                .into_iter()
                .find(|e| e.path == piece.path)
                .expect("the piece is still drawn");
            assert_eq!(
                (held.hover, held.press),
                (0.0, 0.0),
                "{name}, {variant}: disabled {:?} answers the pointer",
                piece.key
            );
            rt.pointer_up(middle(&piece).0, middle(&piece).1);
            rt.frame(1.0 / 60.0);

            rt.set_full_keyboard_access(true);
            for _ in 0..40 {
                let all = rt.elements();
                let inside = all.iter().find(|e| {
                    e.focused
                        && (e.path == piece.path
                            || std::iter::successors(e.parent, |&p| all[p].parent)
                                .any(|p| all[p].path == piece.path))
                });
                assert!(
                    inside.is_none(),
                    "{name}, {variant}: focus lands in disabled {:?} on {:?}",
                    piece.key,
                    inside.and_then(|e| e.key.clone())
                );
                rt.key(KeyEvent {
                    key: Key::Tab,
                    mods: Mods::default(),
                });
                rt.frame(1.0);
            }
            explained.extend(piece.key);
        }
    }
    for key in EXPLAINED {
        assert!(
            explained.iter().any(|k| k == key),
            "disabled {key} carries no explanation, so the rule proves nothing for it; found {explained:?}"
        );
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
            let t = scheme.colors(dark).ui;
            if contrast(solid(t.brand), solid(pill.fill)) >= 4.5 {
                assert!(
                    [ink.r - t.brand.r, ink.g - t.brand.g, ink.b - t.brand.b]
                        .iter()
                        .all(|d| d.abs() < 1e-4),
                    "{}, dark={dark}: brand reads on the pill, yet its words are {ink:?}",
                    scheme.id
                );
            }
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

fn overlaps(a: [f32; 4], b: [f32; 4]) -> bool {
    a[0] < b[0] + b[2] - 0.5
        && b[0] < a[0] + a[2] - 0.5
        && a[1] < b[1] + b[3] - 0.5
        && b[1] < a[1] + a[3] - 0.5
}

#[test]
fn no_drawn_part_of_a_row_lies_under_its_hover_controls_in_any_kind_or_state() {
    let rows = [
        "row.agent",
        "row.tile",
        "row.two",
        "row.keyed",
        "row.select",
        "row.crew",
        "row.wrapped",
    ];
    for variant in ROW_VARIANTS {
        for key in rows {
            for by_keyboard in [false, true] {
                let mut rt = story(true, "Chrome/List row", variant);
                let row = element(&rt, key);
                if by_keyboard {
                    let stop = if key == "row.select" {
                        "row.select.open"
                    } else {
                        key
                    };
                    tab_to(&mut rt, stop);
                } else {
                    rt.pointer_move(row.rect[0] + 12.0, row.rect[1] + 12.0);
                }
                rt.frame(1.0 / 60.0);
                let all = rt.elements();
                let Some(controls) = all.iter().position(|e| {
                    e.key.as_deref() == Some(&format!("{key}.controls.hover"))
                        || (e.key.as_deref() == Some(&format!("{key}.controls"))
                            && !all.iter().any(|o| {
                                o.key.as_deref() == Some(&format!("{key}.controls.hover"))
                            }))
                }) else {
                    panic!("{variant}: {key} shows no hover controls");
                };
                let over = all[controls].visible;
                let row = element(&rt, key);
                let ancestors = |i: usize| std::iter::successors(all[i].parent, |&p| all[p].parent);
                let in_row = |i: usize| ancestors(i).any(|p| all[p].path == row.path);
                for (i, e) in all.iter().enumerate() {
                    if !in_row(i) || i == controls || ancestors(i).any(|p| p == controls) {
                        continue;
                    }
                    let seen = e.opacity * ancestors(i).map(|p| all[p].opacity).product::<f32>();
                    let drawn = e.text.is_some() || e.fill.a > 0.01;
                    let ground = e.text.is_none()
                        && e.visible[0] <= over[0] + 0.5
                        && e.visible[0] + e.visible[2] >= over[0] + over[2] - 0.5;
                    assert!(
                        !(drawn && !ground && seen > 0.01 && overlaps(e.visible, over)),
                        "{variant}, by keyboard={by_keyboard}: {} of {key} at {:?} lies under its hover controls at {over:?}",
                        e.key.as_deref().or(e.text.as_deref()).unwrap_or(&e.path),
                        e.visible
                    );
                }
            }
        }
    }
}

#[test]
fn a_select_rows_title_keeps_its_floor_under_its_controls_at_its_narrowest_real_width() {
    for variant in ROW_VARIANTS {
        let mut rt = story(true, "Chrome/List row", variant);
        let row = element(&rt, "row.select");
        rt.pointer_move(row.rect[0] + 12.0, row.rect[1] + 12.0);
        rt.frame(1.0 / 60.0);
        let title = element(&rt, "row.select.title");
        assert!(
            title.visible[2] >= TITLE_FLOOR,
            "{variant}: the select row's title shows {} points under its controls",
            title.visible[2]
        );
    }
}

fn row_with_asides(asides: &f32, _: &mut Cx<f32>) -> El {
    let t = tokens(true);
    rune::row().child(
        rune::col().w(ROW_WIDTH).child(
            ListRow::new("rule.row", "shop")
                .lead(el().size(16.0, 16.0))
                .title("shop")
                .aside(
                    el().key("rule.asides")
                        .h(10.0)
                        .w(*asides)
                        .shrink()
                        .min_w(0.0),
                )
                .build(&t),
        ),
    )
}

/// A recent place's row at 150% text in the smallest window.
const ROW_WIDTH: f32 = 477.0;

fn asides_width(asides: fn() -> f32) -> (f32, f32) {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("chrome-rules", WINDOW, asides, row_with_asides)
    })
    .headless_runtime(WINDOW, Env::new(true, 1.0));
    rt.frame(0.0);
    let all = rt.elements();
    let key = |k: &str| {
        all.iter()
            .find(|e| e.key.as_deref() == Some(k))
            .unwrap_or_else(|| panic!("no {k}"))
            .rect[2]
    };
    (key("rule.asides"), key("rule.row"))
}

#[test]
fn a_rows_asides_take_at_most_half_of_the_whole_row_and_keep_what_fits_in_it() {
    let (kept, row) = asides_width(|| 227.0);
    assert_eq!(row, ROW_WIDTH);
    assert!(
        kept >= 226.5,
        "asides of 227 in a {row} row are cut to {kept}, short of half the row"
    );
    let (capped, row) = asides_width(|| 400.0);
    assert!(
        capped <= row / 2.0 + 0.5,
        "asides of 400 take {capped} of a {row} row"
    );
}

fn one_line_rows(_: &(), _: &mut Cx<()>) -> El {
    let t = tokens(true);
    let row = |key: &str| {
        ListRow::new(key, "Reload this agent")
            .lead(el().size(14.0, 14.0))
            .title("Reload this agent")
    };
    rune::row().child(
        rune::col()
            .w(ROW_WIDTH)
            .child(row("rule.plain").build(&t))
            .child(
                row("rule.tagged")
                    .tag(attn_ui::system::words(
                        "Restart the agent in its current pane",
                        attn_ui::system::Type::Small,
                        t.text_tertiary,
                    ))
                    .build(&t),
            ),
    )
}

#[test]
fn a_one_line_row_with_a_tag_stands_as_tall_as_one_without() {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("chrome-rules", WINDOW, || (), one_line_rows)
    })
    .headless_runtime(WINDOW, Env::new(true, 1.0));
    rt.frame(0.0);
    let all = rt.elements();
    let height = |k: &str| {
        all.iter()
            .find(|e| e.key.as_deref() == Some(k))
            .unwrap_or_else(|| panic!("no {k}"))
            .rect[3]
    };
    assert_eq!(height("rule.tagged"), height("rule.plain"));
}

const SELECT_ROWS: [(bool, Option<bool>); 6] = [
    (false, None),
    (true, None),
    (false, Some(false)),
    (true, Some(false)),
    (false, Some(true)),
    (true, Some(true)),
];

fn select_key(selected: bool, checked: Option<bool>) -> String {
    format!("rule.select.{selected}.{checked:?}")
}

fn select_rows(_: &(), _: &mut Cx<()>) -> El {
    let t = tokens(true);
    rune::col()
        .w(ROW_WIDTH)
        .children(SELECT_ROWS.map(|(selected, checked)| {
            let key = select_key(selected, checked);
            let row = ListRow::new(format!("{key}.row"), "Nightly triage")
                .title("Nightly triage")
                .subtitle("Every day at 09:00", &t)
                .description("Every day at 09:00")
                .selected(selected)
                .select(key, "Nightly triage", |_: &mut (), _: &mut rune::Tx| {});
            match checked {
                Some(on) => row.checked(on),
                None => row,
            }
            .build(&t)
        }))
}

fn accessible(node: &serde_json::Value, key: &str) -> Option<serde_json::Value> {
    if node["key"].as_str() == Some(key) {
        return Some(node.clone());
    }
    node["children"]
        .as_array()
        .into_iter()
        .flatten()
        .find_map(|child| accessible(child, key))
}

#[test]
fn a_select_rows_button_is_checked_or_unchecked_only_when_told_and_says_what_it_is_in_every_state()
{
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("chrome-rules", WINDOW, || (), select_rows)
    })
    .headless_runtime(WINDOW, Env::new(true, 1.0));
    rt.set_full_keyboard_access(true);
    rt.frame(0.0);
    for focused in [false, true] {
        for (selected, checked) in SELECT_ROWS {
            let key = select_key(selected, checked);
            if focused {
                for _ in 0..SELECT_ROWS.len() {
                    if rt
                        .elements()
                        .iter()
                        .any(|e| e.focused && e.key.as_deref() == Some(&key))
                    {
                        break;
                    }
                    rt.key(KeyEvent {
                        key: Key::Tab,
                        mods: Mods::default(),
                    });
                    rt.frame(1.0);
                }
            }
            let node = accessible(&rt.accessibility_json(), &key)
                .unwrap_or_else(|| panic!("{key}: no select button in the tree"));
            let states: Vec<&str> = node["states"]
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(|s| s.as_str())
                .collect();
            let toggled: Vec<&str> = states
                .iter()
                .copied()
                .filter(|s| ["checked", "unchecked"].contains(s))
                .collect();
            let told: Vec<&str> = match checked {
                Some(true) => vec!["checked"],
                Some(false) => vec!["unchecked"],
                None => vec![],
            };
            assert_eq!(
                toggled, told,
                "{key}, focused={focused}: the select button is {states:?}"
            );
            assert_eq!(
                states.contains(&"focused"),
                focused,
                "{key}: focus is {states:?}"
            );
            assert_eq!(
                node["description"].as_str(),
                Some("Every day at 09:00"),
                "{key}, focused={focused}: the select button's description"
            );
        }
    }
}

#[test]
fn a_wrapping_rows_tag_keeps_clear_of_its_kept_controls() {
    for variant in ROW_VARIANTS {
        let rt = story(true, "Chrome/List row", variant);
        let tag = element(&rt, "row.kept.tag");
        let retry = element(&rt, "row.kept.retry");
        let title = element(&rt, "row.kept.title");
        assert!(
            !overlaps(tag.visible, retry.visible),
            "{variant}: the tag {:?} lies under the kept control {:?}",
            tag.visible,
            retry.visible
        );
        assert!(
            tag.rect[2] >= 40.0 && tag.rect[1] >= title.rect[1] + title.rect[3] - 0.5,
            "{variant}: the tag {:?} is squeezed beside the title {:?} instead of under it",
            tag.rect,
            title.rect
        );
    }
}

#[test]
fn a_hovered_rows_title_is_whole_up_to_eight_points_before_its_controls_and_never_under_them() {
    for variant in ROW_VARIANTS {
        for key in ["row.agent", "row.tile", "row.two", "row.keyed", "row.crew"] {
            let mut rt = story(true, "Chrome/List row", variant);
            let row = element(&rt, key);
            rt.pointer_move(row.rect[0] + 12.0, row.rect[1] + 12.0);
            rt.frame(1.0 / 60.0);
            let title = device(element(&rt, &format!("{key}.title")).rect);
            let all = rt.elements();
            let first = all
                .iter()
                .filter(|e| {
                    e.role == "button"
                        && e.key
                            .as_deref()
                            .is_some_and(|k| k.starts_with(&format!("{key}.")))
                })
                .map(|e| e.rect[0])
                .fold(f32::INFINITY, f32::min)
                * SCALE;
            let glyphs = rt.instances.iter().filter(|i| {
                i.uv != [0.0; 4]
                    && rune::render::unpack_fade(i.fade).0[2] != 0.0
                    && i.rect[0] >= title[0] - 1.0
                    && i.rect[1] >= title[1] - 1.0
                    && i.rect[1] + i.rect[3] <= title[1] + title[3] + 1.0
            });
            for glyph in glyphs {
                let end = glyph.rect[0] + glyph.rect[2];
                let (widths, _) = rune::render::unpack_fade(glyph.fade);
                let whole_until = glyph.clip[2] - widths[2].abs();
                assert!(
                    glyph.clip[2] <= first + 0.5,
                    "{variant}: {key}'s title draws to {} under its first control at {first}",
                    glyph.clip[2]
                );
                if end <= first - 8.0 * SCALE {
                    assert!(
                        end <= whole_until + 0.5,
                        "{variant}: {key}'s glyph ending {}pt before its controls fades from {} over {:?}",
                        (first - end) / SCALE,
                        whole_until / SCALE,
                        widths
                    );
                }
            }
        }
    }
}

const COMING_AND_GOING: [&str; 3] = ["hover", "badge", "tag"];

type RowKind = fn(ListRow, &Tokens) -> ListRow;

fn rows_that_change(shown: &u8, _: &mut Cx<u8>) -> El {
    let t = tokens(true);
    let kinds: [(&str, RowKind); 4] = [
        ("rule.line", |row, _| row),
        ("rule.wrap", |row, _| row.wrap()),
        ("rule.two", |row, t| row.subtitle("Waiting since noon", t)),
        ("rule.select", |row, _| {
            row.select("rule.select.open", "Open shop", |_: &mut u8, _| {})
        }),
    ];
    let has = |part: usize| shown & (1 << part) != 0;
    let mut list = rune::col().w(ROW_WIDTH);
    for (key, kind) in kinds {
        let row = kind(
            ListRow::new(key, "shop")
                .lead(el().size(16.0, 16.0))
                .title("shop"),
            &t,
        )
        .controls(
            [attn_ui::system::icon_button(
                format!("{key}.start"),
                "Start",
                &attn_ui::icon::HOME,
                attn_ui::system::IconSize::Small,
            )
            .build(&t)
            .into()],
            attn_ui::system::Reveal::Always,
        );
        let row = if has(0) {
            row.hover_controls([attn_ui::system::icon_button(
                format!("{key}.more"),
                "Actions",
                &attn_ui::icon::HOME,
                attn_ui::system::IconSize::Small,
            )])
        } else {
            row
        };
        let row = if has(1) {
            row.badge(el().size(8.0, 8.0))
        } else {
            row
        };
        let row = if has(2) {
            row.tag(attn_ui::system::words(
                "Restart",
                attn_ui::system::Type::Small,
                t.text_tertiary,
            ))
        } else {
            row
        };
        list = list.child(row.build(&t));
    }
    rune::row().child(list)
}

#[test]
fn a_focused_kept_control_keeps_the_focus_while_its_rows_other_parts_come_and_go() {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("chrome-rules", WINDOW, || 0b111u8, rows_that_change)
    })
    .headless_runtime(WINDOW, Env::new(true, 1.0));
    rt.set_full_keyboard_access(true);
    rt.frame(0.0);
    let focused = |rt: &Runtime<u8>| {
        rt.elements()
            .into_iter()
            .find(|e| e.focused)
            .and_then(|e| e.key)
    };
    for kind in ["rule.line", "rule.wrap", "rule.two", "rule.select"] {
        let kept = format!("{kind}.start");
        for from in 0..8u8 {
            for to in 0..8u8 {
                if from == to {
                    continue;
                }
                rt.update(|s, _| *s = from);
                rt.frame(1.0);
                let mut tabs = 0;
                while focused(&rt).as_deref() != Some(kept.as_str()) {
                    assert!(tabs < 40, "Tab never reached {kept}");
                    rt.key(KeyEvent {
                        key: Key::Tab,
                        mods: Mods::default(),
                    });
                    rt.frame(1.0);
                    tabs += 1;
                }
                rt.update(|s, _| *s = to);
                rt.frame(1.0);
                let shown = |m: u8| {
                    COMING_AND_GOING
                        .iter()
                        .enumerate()
                        .filter(|(i, _)| m & (1 << i) != 0)
                        .map(|(_, p)| *p)
                        .collect::<Vec<_>>()
                };
                assert_eq!(
                    focused(&rt).as_deref(),
                    Some(kept.as_str()),
                    "{kept} loses the focus when its row goes from {:?} to {:?}",
                    shown(from),
                    shown(to)
                );
            }
        }
    }
}

#[test]
fn the_keyboards_row_keeps_a_chosen_rows_ground_and_carries_the_bar_a_hovered_row_never_draws() {
    let bar = |rt: &Runtime<()>, key: &str| {
        rt.elements()
            .iter()
            .any(|e| e.key.as_deref() == Some(&format!("{key}.in-view")))
    };
    for dark in [true, false] {
        let t = tokens(dark);
        let mut rt = story(dark, "Chrome/List row", "Rest");
        let e = element(&rt, "row.cursor");
        let (x, y) = on_words(&e);
        rt.pointer_move(x, y);
        rt.frame(1.0);
        assert!(
            !bar(&rt, "row.cursor"),
            "dark={dark}: a hovered row draws the bar"
        );
        let rt = story(dark, "Chrome/List row", "In view");
        let keyboards = surface(&rt, &element(&rt, "row.cursor"), t.bg_panel);
        let chosen = surface(&rt, &element(&rt, "row.chosen"), t.bg_panel);
        assert_eq!(
            keyboards, chosen,
            "dark={dark}: the keyboard's row stands on another ground than a chosen row"
        );
        assert!(
            bar(&rt, "row.cursor"),
            "dark={dark}: the keyboard's row has no bar"
        );
    }
}

#[test]
fn a_row_in_view_looks_the_same_with_the_keyboards_cursor_on_it_or_not() {
    for dark in [true, false] {
        let t = tokens(dark);
        let rt = story(dark, "Chrome/List row", "In view");
        let shown = surface(&rt, &element(&rt, "row.shown"), t.bg_panel);
        let with_cursor = surface(&rt, &element(&rt, "row.shown-cursor"), t.bg_panel);
        assert_eq!(
            shown, with_cursor,
            "dark={dark}: the keyboard's cursor changes a row in view"
        );
    }
}
