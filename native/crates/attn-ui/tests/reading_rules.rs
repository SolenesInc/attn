//! The rules the reading blocks hold, read from their gallery stories' element trees.

use attn_ui::reading_gallery::{KEY_HINTS, KEY_TABS, READING_SUBJECTS};
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1500.0, 1100.0);
const SCALE: f32 = 2.0;
const WIDTHS: [&str; 5] = ["Rest", "Long", "Narrow", "Narrow long", "Smallest long"];
/// The farthest a reading part's box hangs past the text around it: a list row's inset.
const HANG: f32 = 8.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("reading rules", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

/// What `outer` holds that is drawn: the parts its ancestors' clips leave visible.
fn drawn_under<'a>(
    all: &'a [ElementInfo],
    outer: &ElementInfo,
) -> impl Iterator<Item = &'a ElementInfo> {
    let prefix = format!("{}/", outer.path);
    all.iter()
        .filter(move |e| e.path.starts_with(&prefix) && e.visible[2] > 0.0 && e.visible[3] > 0.0)
}

#[test]
fn nothing_a_reading_block_draws_hangs_past_its_sides_at_any_width() {
    for &(name, keys, _) in READING_SUBJECTS {
        for variant in WIDTHS {
            let rt = story(name, variant);
            let all = rt.elements();
            for key in keys {
                let block = element(&rt, key);
                for part in drawn_under(&all, &block) {
                    assert!(
                        part.visible[0] >= block.rect[0] - HANG - 0.5
                            && part.visible[0] + part.visible[2]
                                <= block.rect[0] + block.rect[2] + HANG + 0.5,
                        "{name} {variant} {key}: {} at {:?} hangs past the block at {:?}",
                        part.path,
                        part.visible,
                        block.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_reading_controls_box_and_ring_take_in_everything_it_draws() {
    for &(name, _, _) in READING_SUBJECTS {
        for variant in WIDTHS {
            let rt = story(name, variant);
            let all = rt.elements();
            for control in all.iter().filter(|e| e.focusable) {
                for part in drawn_under(&all, control) {
                    let r = (control.rect, part.visible);
                    assert!(
                        r.1[0] >= r.0[0] - 0.5
                            && r.1[1] >= r.0[1] - 0.5
                            && r.1[0] + r.1[2] <= r.0[0] + r.0[2] + 0.5
                            && r.1[1] + r.1[3] <= r.0[1] + r.0[3] + 0.5,
                        "{name} {variant}: {} at {:?} reaches outside its control {} at {:?}",
                        part.path,
                        part.visible,
                        control.path,
                        control.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_boards_lanes_share_its_width_evenly() {
    for variant in WIDTHS {
        let rt = story("Blocks/Lane", variant);
        let widths: Vec<f32> = (0..3)
            .map(|n| element(&rt, &format!("board.lane.{n}")).rect[2])
            .collect();
        assert!(
            widths.iter().all(|w| (w - widths[0]).abs() < 0.5),
            "{variant}: lanes {widths:?}"
        );
    }
}

#[test]
fn a_reading_story_draws_once_and_rests() {
    for &(name, _, _) in READING_SUBJECTS {
        let mut rt = story(name, "Rest");
        rt.frame(1.0 / 60.0);
        assert!(
            !rt.animating && rt.wake_deadline().is_none(),
            "{name} asks for another frame at rest"
        );
    }
}

#[test]
fn a_heads_short_title_keeps_its_control_beside_it_in_a_docked_reader_and_a_long_one_wraps_it_under()
 {
    for (variant, beside) in [("Narrow", true), ("Narrow long", false)] {
        let rt = story("Blocks/Reader", variant);
        let title = element(&rt, "reader.title").rect;
        let open = element(&rt, "reader.open").rect;
        assert_eq!(
            open[1] < title[1] + title[3],
            beside,
            "{variant}: Open as tile at {open:?}, the title at {title:?}"
        );
    }
}

#[test]
fn every_lanes_title_sits_on_one_line_whether_or_not_it_has_a_control_beside_it() {
    for variant in WIDTHS {
        let rt = story("Blocks/Lane", variant);
        let all = rt.elements();
        let tops: Vec<f32> = (0..3)
            .map(|n| {
                let lane = element(&rt, &format!("board.lane.{n}"));
                drawn_under(&all, &lane)
                    .find(|e| e.role == "heading")
                    .unwrap_or_else(|| panic!("lane {n} has no title"))
                    .rect[1]
            })
            .collect();
        assert!(
            tops.iter().all(|y| (y - tops[0]).abs() < 0.5),
            "{variant}: lane titles at {tops:?}"
        );
    }
}

#[test]
fn a_sections_short_title_keeps_its_aside_on_its_line_and_a_long_one_wraps_beside_it() {
    for variant in WIDTHS {
        let rt = story("Blocks/Section", variant);
        let title = element(&rt, "section.second.heading").rect;
        let reset = element(&rt, "section.second.reset").rect;
        assert!(
            reset[1] < title[1] + title[3] && reset[0] >= title[0] + title[2],
            "{variant}: Reset at {reset:?}, the title at {title:?}"
        );
    }
}

#[test]
fn a_fixed_pane_keeps_its_width_and_a_share_gives_way_but_not_below_its_least() {
    for variant in WIDTHS {
        let rt = story("Blocks/Pane", variant);
        let fixed = element(&rt, "panes.fixed").rect[2];
        let share = element(&rt, "panes.share").rect[2];
        assert!((fixed - 120.0).abs() < 0.5, "{variant}: fixed pane {fixed}");
        assert!(share >= 80.0 - 0.5, "{variant}: share pane {share}");
    }
}

#[test]
fn a_remarks_words_move_under_who_said_them_before_they_get_too_narrow_to_read() {
    for variant in WIDTHS {
        let rt = story("Blocks/Remark", variant);
        for n in 0..4 {
            let said = element(&rt, &format!("remarks.{n}.said")).rect;
            assert!(
                said[2] >= 160.0 - 0.5,
                "{variant}: remark {n}'s words are {} wide",
                said[2]
            );
        }
    }
}

#[test]
fn a_separator_between_facts_never_ends_or_starts_a_line() {
    for name in ["Blocks/Byline", "Blocks/Card", "Blocks/Lane"] {
        for variant in WIDTHS {
            let rt = story(name, variant);
            let all = rt.elements();
            let drawn = |e: &&ElementInfo| e.visible[2] > 0.0 && e.visible[3] > 0.0;
            let words: Vec<&ElementInfo> = all
                .iter()
                .filter(drawn)
                .filter(|e| e.text.as_deref().is_some_and(|t| t != "·" && !t.is_empty()))
                .collect();
            for dot in all
                .iter()
                .filter(drawn)
                .filter(|e| e.text.as_deref() == Some("·"))
            {
                let [x, y, w, h] = dot.visible;
                let on_line = |e: &&&ElementInfo| {
                    let [_, ey, _, eh] = e.visible;
                    ey < y + h && ey + eh > y
                };
                let before = words
                    .iter()
                    .filter(on_line)
                    .any(|e| e.visible[0] + e.visible[2] <= x + 0.5);
                let after = words
                    .iter()
                    .filter(on_line)
                    .any(|e| e.visible[0] >= x + w - 0.5);
                assert!(
                    before && after,
                    "{name} {variant}: a separator at {:?} has {} on its line",
                    dot.visible,
                    if before {
                        "nothing after it"
                    } else {
                        "nothing before it"
                    }
                );
            }
        }
    }
}

/// How wide and how tall the words of the control keyed `key` draw.
fn control_words(rt: &Runtime<()>, key: &str) -> [f32; 2] {
    let all = rt.elements();
    let control = element(rt, key);
    drawn_under(&all, &control)
        .find(|e| e.text.is_some())
        .map(|e| [e.visible[2], e.visible[3]])
        .unwrap_or_else(|| panic!("{key} draws no words"))
}

#[test]
fn a_cards_action_and_a_lanes_control_draw_their_words_whole_at_every_width() {
    for (name, key) in [
        ("Blocks/Card", "cards.resting.open"),
        ("Blocks/Lane", "board.lane.2.show"),
    ] {
        let [whole, line] = control_words(&story(name, "Rest"), key);
        for variant in WIDTHS {
            let [w, h] = control_words(&story(name, variant), key);
            assert!(
                w >= whole - 0.5 || h > line + 0.5,
                "{name} {variant}: {key}'s words draw {w} of their {whole} on one line"
            );
        }
    }
}

#[test]
fn a_key_lines_tabs_are_set_off_only_from_hints_on_their_own_line() {
    for (variant, set_off) in [("Rest", true), ("Narrow", false), ("Smallest long", false)] {
        let rt = story("Blocks/KeyLine", variant);
        let all = rt.elements();
        let tabs = all
            .iter()
            .find(|e| e.role == "tablist")
            .unwrap_or_else(|| panic!("{variant}: no tabs"));
        let last = all
            .iter()
            .rfind(|e| e.role == "tab")
            .unwrap_or_else(|| panic!("{variant}: no tab"));
        let room = (tabs.rect[0] + tabs.rect[2]) - (last.rect[0] + last.rect[2]);
        assert_eq!(
            room > 4.0,
            set_off,
            "{variant}: the tabs end {room} past their last tab"
        );
    }
}

#[test]
fn a_key_lines_measured_widths_are_never_narrower_than_it_draws_and_within_a_point() {
    let rt = story("Blocks/KeyLine", "Long");
    let drawn = |key: &str| element(&rt, key).rect[2];
    for (keys, name) in KEY_TABS {
        let key = format!("keys.tab.{keys}");
        let measured = attn_ui::blocks::key_choice_width(keys, name);
        assert!(
            (0.0..=1.0).contains(&(measured - drawn(&key))),
            "{key} draws {} wide, measured {measured}",
            drawn(&key)
        );
    }
    for (keys, what) in KEY_HINTS {
        let key = format!("keys.hint.{what}");
        let measured = attn_ui::system::keys_hint_width(keys.iter().copied(), what);
        assert!(
            (0.0..=1.0).contains(&(measured - drawn(&key))),
            "{key} draws {} wide, measured {measured}",
            drawn(&key)
        );
    }
}

#[test]
fn a_figures_node_centres_its_words_on_both_axes() {
    for variant in WIDTHS {
        let rt = story("Blocks/Figure", variant);
        for n in 0..2 {
            let node = element(&rt, &format!("figure.node.{n}")).rect;
            let name = element(&rt, &format!("figure.node.{n}.name")).rect;
            let centre = |r: [f32; 4]| [r[0] + r[2] / 2.0, r[1] + r[3] / 2.0];
            let (a, b) = (centre(node), centre(name));
            assert!(
                (a[0] - b[0]).abs() < 0.5 && (a[1] - b[1]).abs() < 0.5,
                "{variant}: node {n} at {node:?} holds its name at {name:?}"
            );
        }
    }
}

#[test]
fn a_split_controls_halves_share_one_edge_and_stand_as_one_height() {
    for variant in WIDTHS {
        let rt = story("Blocks/Split", variant);
        for (action, more) in [
            ("split.send", "split.send.menu"),
            ("split.failed", "split.failed.menu"),
        ] {
            let (a, m) = (element(&rt, action).rect, element(&rt, more).rect);
            assert!(
                (a[0] + a[2] - 1.0 - m[0]).abs() < 0.01,
                "{variant}: {more} at {m:?} does not lie on {action}'s edge at {a:?}"
            );
            assert!(
                (a[1] - m[1]).abs() < 0.01 && (a[3] - m[3]).abs() < 0.01,
                "{variant}: {action} {a:?} and {more} {m:?} stand apart"
            );
        }
    }
}

#[test]
fn a_toolbars_buttons_stand_on_one_line_with_a_rule_only_between_its_groups() {
    for variant in WIDTHS {
        let rt = story("Blocks/Toolbar", variant);
        let all = rt.elements();
        let toolbar = element(&rt, "toolbar");
        assert_eq!(toolbar.role, "toolbar", "{variant}");
        let buttons: Vec<&ElementInfo> = drawn_under(&all, &toolbar)
            .filter(|e| e.focusable)
            .collect();
        let line = buttons.first().expect("a toolbar holds buttons").rect;
        let strip: Vec<&ElementInfo> = buttons
            .iter()
            .copied()
            .filter(|b| (b.rect[1] - line[1]).abs() < 0.01)
            .collect();
        assert_eq!(
            strip.len(),
            5,
            "{variant}: the strip's buttons share a line"
        );
        let gaps: Vec<f32> = strip
            .windows(2)
            .map(|w| w[1].rect[0] - (w[0].rect[0] + w[0].rect[2]))
            .collect();
        let within = gaps[0];
        assert!(
            gaps[..3].iter().all(|g| (g - within).abs() < 0.01),
            "{variant}: a group's buttons stand evenly apart: {gaps:?}"
        );
        assert!(
            gaps[3] > within + 1.0,
            "{variant}: the rule sets the last group apart: {gaps:?}"
        );
    }
}

#[test]
fn a_trays_parts_line_up_with_its_panels_bar_and_its_rule_spans_the_panel() {
    for variant in ["Rest", "Narrow", "Smallest long"] {
        let rt = story("Blocks/Tray", variant);
        let panel = element(&rt, "tray.panel").rect;
        let tray = element(&rt, "tray").rect;
        let first = element(&rt, "tray.0").rect;
        let path = element(&rt, "tray.path").rect[0];
        assert!(
            (tray[0] - panel[0]).abs() < 0.5 && (tray[2] - panel[2]).abs() < 0.5,
            "{variant}: the tray {tray:?} does not span its panel {panel:?}"
        );
        assert!(
            (first[0] - path).abs() < 0.5,
            "{variant}: the tray's first part starts at {}, its bar's at {path}",
            first[0]
        );
    }
}
