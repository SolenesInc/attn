//! The rules the desktop chrome's blocks hold, read from the gallery's desk stories: a tile
//! header's piece never stands wider than it measures, so the header fits its pieces by what
//! they draw, and nothing it draws reaches past it; the shelf counts its chips' widths as they
//! draw; the overview's cards share one width and hold what they show.

use attn_ui::Tokens;
use attn_ui::colors::scheme_for;
use attn_ui::desk_gallery::header_pieces_of;
use rune::{App, AppSpec, Cx, El, ElementInfo, Env, Runtime, el};

const WINDOW: (f32, f32) = (900.0, 500.0);
const SCALE: f32 = 2.0;
/// How far a piece's drawn width may fall short of its measure: the runtime lays each run of
/// words out on whole points, so a piece of three runs draws up to a point and a half narrower.
const ROUNDING: f32 = 1.5;

fn view(_: &(), _: &mut Cx<()>) -> El {
    el()
}

fn story(dark: bool, variant: &str) -> Runtime<()> {
    story_of(dark, "Desk/Header piece", variant)
}

fn story_of(dark: bool, name: &str, variant: &str) -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("desk-rules", WINDOW, || (), view)
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

#[test]
fn a_header_piece_stands_as_wide_as_it_measures_and_draws_nothing_past_its_box() {
    for dark in [true, false] {
        for (variant, long) in [("Rest", false), ("Long", true)] {
            let rt = story(dark, variant);
            for (key, piece) in header_pieces_of(long, &tokens(dark)) {
                let measured = if key == "piece.line" {
                    piece.width()
                } else {
                    piece.button_width()
                };
                let drawn = element(&rt, key);
                let [x, y, w, h] = drawn.rect;
                assert!(
                    w <= measured + 0.5 && w >= measured - ROUNDING,
                    "{variant} {key}: drawn {w}, measured {measured}"
                );
                for part in within(&rt, &drawn) {
                    let [px, py, pw, ph] = part.rect;
                    assert!(
                        px >= x - 0.5
                            && px + pw <= x + w + 0.5
                            && py >= y - 0.5
                            && py + ph <= y + h + 0.5,
                        "{variant} {key}: {:?} at {:?} runs past the piece at {:?}",
                        part.key,
                        part.rect,
                        drawn.rect
                    );
                }
            }
        }
    }
}

#[test]
fn a_piece_cut_short_keeps_its_words_within_their_room() {
    let rt = story(true, "Long");
    let label = element(&rt, "piece.plot.label");
    assert!(
        label.rect[2] <= 84.0 + 0.5,
        "the plot's label keeps to 84 points, drew {}",
        label.rect[2]
    );
}

#[test]
fn a_shelf_chip_of_marks_stands_as_wide_as_the_shelf_counts_it() {
    use attn_ui::blocks::Shelf;
    use attn_ui::system::DotSize;
    for variant in ["Rest", "Long"] {
        let rt = story_of(true, "Desk/Shelf", variant);
        for (key, marks) in [("shelf.dot", 1), ("shelf.dot.current", 3)] {
            let drawn = element(&rt, key).rect[2];
            let counted = Shelf::marks_chip_width(marks, DotSize::Small).max(20.0);
            assert!(
                (drawn - counted).abs() < 0.5,
                "{variant} {key}: drawn {drawn}, counted {counted}"
            );
        }
        let chip = element(&rt, "shelf.chip");
        for part in within(&rt, &chip) {
            let [x, _, w, _] = part.rect;
            assert!(
                x >= chip.rect[0] - 0.5 && x + w <= chip.rect[0] + chip.rect[2] + 0.5,
                "{variant}: {:?} runs past its chip",
                part.key
            );
        }
    }
}

#[test]
fn the_overviews_cards_share_one_width_and_a_tiles_name_stays_inside_it() {
    for variant in ["Rest", "Long"] {
        let rt = story_of(true, "Desk/Overview", variant);
        let widths: Vec<f32> = ["card.current", "card.empty", "card.one"]
            .iter()
            .map(|k| element(&rt, k).rect[2])
            .collect();
        assert!(
            widths.iter().all(|w| (w - widths[0]).abs() < 0.5),
            "{variant}: cards {widths:?}"
        );
        let current = element(&rt, "card.current");
        for part in within(&rt, &current) {
            let [x, y, w, h] = part.rect;
            let [cx, cy, cw, ch] = current.rect;
            assert!(
                x >= cx - 0.5 && x + w <= cx + cw + 0.5 && y >= cy - 0.5 && y + h <= cy + ch + 0.5,
                "{variant}: {:?} at {:?} runs past its card at {:?}",
                part.key,
                part.rect,
                current.rect
            );
        }
    }
}

#[test]
fn a_link_on_a_header_line_reaches_over_none_of_the_words_beside_it() {
    for variant in ["Rest", "Long"] {
        let rt = story(true, variant);
        let line = element(&rt, "piece.line");
        let link = element(&rt, "piece.line.link");
        let inside_link = within(&rt, &link);
        let reach = attn_ui::system::RING_REACH;
        let [lx, _, lw, _] = link.rect;
        let (lx, lw) = (lx - reach, lw + 2.0 * reach);
        for part in within(&rt, &line) {
            if part.path == link.path
                || inside_link.iter().any(|e| e.path == part.path)
                || within(&rt, &part).iter().any(|e| e.path == link.path)
            {
                continue;
            }
            let [x, _, w, _] = part.rect;
            assert!(
                x + w <= lx + 0.5 || x >= lx + lw - 0.5,
                "{variant}: {:?} at {:?} sits under the ring of the link at {:?}",
                part.key,
                part.rect,
                link.rect
            );
        }
    }
}
