//! The rules the blocks a desktop's tiles draw over and around their terminals hold, read from
//! the tile gallery's stories.

use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (1400.0, 800.0);
const SCALE: f32 = 2.0;
/// Where the terminal's scroll bar runs, which a bar over the terminal keeps clear of.
const SCROLL_BAR_ROOM: f32 = 14.0;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("tiles", WINDOW, || (), view)
    })
    .playground(attn_ui::playground::<()>().enabled(true))
    .headless_runtime(WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt.open_playground(name, Some(variant));
    rt.frame(1.0);
    rt
}

fn element(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.playground_element(key)
        .unwrap_or_else(|| panic!("no element {key} on the stage"))
}

fn left(e: &ElementInfo) -> f32 {
    e.rect[0]
}

fn right(e: &ElementInfo) -> f32 {
    e.rect[0] + e.rect[2]
}

fn top(e: &ElementInfo) -> f32 {
    e.rect[1]
}

fn bottom(e: &ElementInfo) -> f32 {
    e.rect[1] + e.rect[3]
}

fn middle(e: &ElementInfo) -> f32 {
    e.rect[1] + e.rect[3] / 2.0
}

fn within(rt: &Runtime<()>, outer: &ElementInfo) -> Vec<ElementInfo> {
    rt.elements()
        .into_iter()
        .filter(|e| e.path.starts_with(&outer.path) && e.path != outer.path)
        .filter(|e| e.rect[2] > 0.0 && e.rect[3] > 0.0)
        .collect()
}

const STORIES: [(&str, &[&str]); 5] = [
    (
        "Tiles/Find bar",
        &["Rest", "Case on", "Long", "Smallest long"],
    ),
    ("Tiles/Filter", &["Rest", "Empty", "Long", "Smallest long"]),
    ("Tiles/Notices", &["Rest", "Long", "Smallest long"]),
    ("Tiles/Status", &["Rest", "Long", "Smallest long"]),
    ("Tiles/Drag", &["Rest", "Long"]),
];

#[test]
fn nothing_drawn_over_a_terminal_runs_past_its_sides_down_to_the_smallest_window() {
    for (name, variants) in STORIES {
        for variant in variants {
            let rt = story(name, variant);
            let tile = element(&rt, "story.tile");
            for e in within(&rt, &tile) {
                let below_its_foot = bottom(&e) > bottom(&tile) + 0.5 && name != "Tiles/Notices";
                assert!(
                    left(&e) >= left(&tile) - 0.5
                        && right(&e) <= right(&tile) + 0.5
                        && top(&e) >= top(&tile) - 0.5
                        && !below_its_foot,
                    "{name} {variant}: {:?} at {:?} runs past the tile at {:?}",
                    e.key,
                    e.rect,
                    tile.rect
                );
            }
        }
    }
}

#[test]
fn a_terminal_bar_keeps_clear_of_the_scroll_bar_and_centres_its_parts_on_one_line() {
    for variant in ["Rest", "Case on", "Long", "Smallest long"] {
        let rt = story("Tiles/Find bar", variant);
        let tile = element(&rt, "story.tile");
        let bar = element(&rt, "story.tile.find");
        assert!(
            right(&bar) <= right(&tile) - SCROLL_BAR_ROOM + 0.5,
            "{variant}: the bar ends at {}, over the scroll bar past {}",
            right(&bar),
            right(&tile) - SCROLL_BAR_ROOM
        );
        let line = middle(&element(&rt, "story.tile.find.field"));
        for part in ["case", "count", "prev", "next", "close"] {
            let at = middle(&element(&rt, &format!("story.tile.find.{part}")));
            assert!(
                (at - line).abs() < 0.5,
                "{variant}: {part} centres at {at}, the field at {line}"
            );
        }
    }
}

#[test]
fn a_filters_list_stands_under_its_bar_and_ends_on_its_edge() {
    for variant in ["Rest", "Empty", "Long", "Smallest long"] {
        let rt = story("Tiles/Filter", variant);
        let bar = element(&rt, "story.tile.filter");
        let list = element(&rt, "story.tile.filter.results");
        assert!(
            top(&list) > bottom(&bar),
            "{variant}: the list starts at {}, over its bar's foot at {}",
            top(&list),
            bottom(&bar)
        );
        assert!(
            (right(&list) - right(&bar)).abs() < 2.0,
            "{variant}: the list ends at {} ({:?}), its bar at {} ({:?})",
            right(&list),
            list.rect,
            right(&bar),
            bar.rect
        );
    }
}

#[test]
fn a_notices_action_and_dismiss_stand_inside_it() {
    for variant in ["Rest", "Long", "Smallest long"] {
        let rt = story("Tiles/Notices", variant);
        let notice = element(&rt, "story.tile.stale");
        for key in ["story.tile.stale.reload", "story.tile.stale.dismiss"] {
            let part = element(&rt, key);
            assert!(
                left(&part) >= left(&notice)
                    && right(&part) <= right(&notice)
                    && bottom(&part) <= bottom(&notice),
                "{variant}: {key} at {:?} stands outside its notice at {:?}",
                part.rect,
                notice.rect
            );
        }
    }
}

#[test]
fn a_long_tab_gives_way_before_it_crowds_the_others_out() {
    for variant in ["Rest", "Long", "Smallest long"] {
        let rt = story("Tiles/Tabs", variant);
        let first = element(&rt, "story.tabs.tab.0");
        let second = element(&rt, "story.tabs.tab.1");
        assert!(
            first.rect[2] <= 220.0 + 16.5,
            "{variant}: the first tab stands {} wide",
            first.rect[2]
        );
        assert!(
            left(&second) >= right(&first),
            "{variant}: the second tab starts at {}, inside the first ending at {}",
            left(&second),
            right(&first)
        );
    }
}
