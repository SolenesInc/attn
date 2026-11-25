//! The rules the desktop chrome's blocks hold, read from the gallery's desk stories: a tile
//! header's piece never stands wider than it measures, so the header fits its pieces by what
//! they draw, and nothing it draws reaches past it; the shelf counts its entries' widths as they
//! draw, and what it draws reads on what is behind it in every scheme; the overview's cards share one width and hold what they show.

use attn_ui::Tokens;
use attn_ui::blocks::{CountSide, MemberState, Place, Shelf, ShelfCount, ShelfEntry, ShelfMember};
use attn_ui::colors::scheme_for;
use attn_ui::desk_gallery::header_pieces_of;
use attn_ui::system::{Type, width_of};
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
fn a_shelf_entry_stands_as_wide_as_the_shelf_counts_it_and_draws_nothing_past_its_box() {
    for variant in ["Rest", "Long"] {
        let rt = story_of(true, "Desk/Shelf", variant);
        let counts = [
            ("shelf.count", CountSide::Before, 2, vec![]),
            (
                "shelf.count.needs",
                CountSide::After,
                3,
                vec![shelf_member(Some(MemberState::Approval))],
            ),
        ];
        for (key, side, columns, marks) in counts {
            let drawn = element(&rt, key).rect[2];
            let counted = shelf_count(side, columns, marks).width();
            assert!(
                (drawn - counted).abs() < 0.5,
                "{variant} {key}: drawn {drawn}, counted {counted}"
            );
        }
        for key in [
            "shelf.entry",
            "shelf.entry.stack",
            "shelf.entry.on_screen",
            "shelf.entry.current",
            "shelf.entry.asks",
            "shelf.count",
            "shelf.count.needs",
        ] {
            let entry = element(&rt, key);
            for part in within(&rt, &entry) {
                let [x, y, w, h] = part.visible;
                if w <= 0.0 || h <= 0.0 {
                    continue;
                }
                assert!(
                    x >= entry.rect[0] - 0.5
                        && x + w <= entry.rect[0] + entry.rect[2] + 0.5
                        && y >= entry.rect[1] - 0.5
                        && y + h <= entry.rect[1] + entry.rect[3] + 0.5,
                    "{variant}: {:?} at {:?} runs past {key} at {:?}",
                    part.path,
                    part.visible,
                    entry.rect
                );
            }
        }
    }
}

const SHORT_NAMES: [&str; 3] = ["A", "I", "7"];
const STACKED: usize = 4;

fn named(name: &str, state: MemberState) -> ShelfMember {
    ShelfMember {
        name: name.into(),
        ..shelf_member(Some(state))
    }
}

/// One tile by a one-character name.
fn short_entry(place: Place, name: &str) -> ShelfEntry {
    ShelfEntry::new(vec![named(name, MemberState::Idle)], 0, place, 0)
}

/// A stack where every member but `idle` waits.
fn waiting_stack(place: Place, idle: usize, shown: usize) -> ShelfEntry {
    let state = |i| {
        if i == idle {
            MemberState::Idle
        } else {
            MemberState::Waiting
        }
    };
    let members = (0..STACKED).map(|i| named("builder", state(i))).collect();
    ShelfEntry::new(members, shown, place, 0)
}

fn waiting_stacks(place: Place) -> impl Iterator<Item = (usize, usize, ShelfEntry)> {
    (0..STACKED).flat_map(move |idle| {
        (0..STACKED).map(move |shown| (idle, shown, waiting_stack(place, idle, shown)))
    })
}

/// A row per place of one-character names, and one of stacks at their narrowest.
fn narrow_board(_: &(), _: &mut Cx<()>) -> El {
    let t = tokens(true);
    let rows = PLACES.iter().enumerate().flat_map(|(p, &place)| {
        let names = SHORT_NAMES.map(|name| {
            let entry = short_entry(place, name);
            entry.build(&format!("{p}.{name}"), name, entry.name_width(), &t)
        });
        let stacks = waiting_stacks(place).map(|(idle, shown, entry)| {
            let key = format!("{p}.{idle}.{shown}");
            entry.build(&key, "builder", entry.name_within(0.0), &t)
        });
        [
            Shelf::row(format!("names.{p}")).children(names),
            Shelf::row(format!("stacks.{p}")).children(stacks),
        ]
    });
    rune::col().children(rows)
}

fn narrow_shelf() -> Runtime<()> {
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("narrow shelf", SHELF_WINDOW, || (), narrow_board)
    })
    .headless_runtime(SHELF_WINDOW, Env::new(true, SCALE));
    rt.frame(0.0);
    rt
}

fn keyed(rt: &Runtime<()>, key: &str) -> ElementInfo {
    rt.elements()
        .into_iter()
        .find(|e| e.key.as_deref() == Some(key))
        .unwrap_or_else(|| panic!("{key} is drawn"))
}

#[test]
fn a_name_whose_own_width_fits_is_drawn_whole_however_short_it_is() {
    let rt = narrow_shelf();
    for (p, &place) in PLACES.iter().enumerate() {
        for name in SHORT_NAMES {
            let entry = keyed(&rt, &format!("{p}.{name}"));
            let said = within(&rt, &entry)
                .into_iter()
                .find(|e| e.text.as_deref() == Some(name))
                .unwrap_or_else(|| panic!("{place:?} {name:?} is drawn"));
            assert_eq!(said.visible, said.rect, "{place:?} {name:?} is not cut");
            assert!(
                said.rect[2] >= width_of(name, Type::Control) - 0.5,
                "{place:?} {name:?} has {} of {} points",
                said.rect[2],
                width_of(name, Type::Control)
            );
        }
    }
}

#[test]
fn a_stack_at_its_narrowest_draws_every_dot_whole_whichever_member_it_shows() {
    let rt = narrow_shelf();
    for (p, &place) in PLACES.iter().enumerate() {
        for (idle, shown, entry) in waiting_stacks(place) {
            let key = format!("{p}.{idle}.{shown}");
            let pill = keyed(&rt, &key);
            let [x, y, width, height] = pill.rect;
            let first = waiting_stack(place, idle, 0).width(0.0);
            assert_eq!(entry.width(0.0), first, "{key}: the pill keeps its width");
            assert!(
                (width - first).abs() < 0.5,
                "{key}: drawn {width}, counted {first}"
            );
            for member in (0..STACKED).filter(|&m| m != shown) {
                let side = if member < shown { "above" } else { "below" };
                let dot = keyed(&rt, &format!("{key}.{side}.{member}"));
                let [dx, dy, dw, dh] = dot.visible;
                let whole = if member == idle { 4.0 } else { 10.0 };
                assert!(
                    (dw - whole).abs() < 0.5
                        && dx >= x + pill.radius
                        && dx + dw <= x + width - pill.radius,
                    "{key}: {side}.{member} draws {dw} of {whole} points at {} in {width}",
                    dx - x
                );
                assert!(dy >= y && dy + dh <= y + height, "{key}: {side}.{member}");
                let found = entry.dot_at(dx + dw / 2.0 - x, dy + dh / 2.0 - y);
                assert_eq!(found, Some(member), "{key}: the pointer on {side}.{member}");
            }
        }
    }
}

const SHELF_WINDOW: (f32, f32) = (1400.0, 700.0);
const STATES: [Option<MemberState>; 7] = [
    None,
    Some(MemberState::Idle),
    Some(MemberState::Working),
    Some(MemberState::Waiting),
    Some(MemberState::Approval),
    Some(MemberState::Scheduled),
    Some(MemberState::Unknown),
];
const PLACES: [Place; 3] = [Place::OffScreen, Place::OnScreen, Place::Current];

thread_local! {
    static SHELF_TOKENS: std::cell::Cell<Option<Tokens>> = const { std::cell::Cell::new(None) };
}

fn shelf_tokens() -> Tokens {
    SHELF_TOKENS
        .with(|cell| cell.get())
        .expect("the shelf's tokens")
}

fn shelf_member(state: Option<MemberState>) -> ShelfMember {
    ShelfMember {
        name: "plan".into(),
        icon: (&attn_ui::icon::AGENT).into(),
        state,
    }
}

fn shelf_count(side: CountSide, columns: usize, marks: Vec<ShelfMember>) -> ShelfCount {
    ShelfCount {
        side,
        columns,
        needs_user: !marks.is_empty(),
        marks,
    }
}

/// Every entry the shelf draws, a row per place: one tile in each state, a stack that shows each
/// state between one more of every state on both edges, and the counts with and without marks.
fn shelf_board(t: &Tokens, _: &mut Cx<Tokens>) -> El {
    let rows = PLACES.iter().enumerate().map(|(p, &place)| {
        let flat = STATES.iter().map(|&state| vec![shelf_member(state)]);
        let stacks = STATES.iter().map(|&shown| {
            let all = || STATES.iter().map(|&state| shelf_member(state));
            all().chain([shelf_member(shown)]).chain(all()).collect()
        });
        let entries = flat
            .map(|members| (members, 0))
            .chain(stacks.map(|members| (members, STATES.len())))
            .enumerate()
            .map(|(i, (members, shown))| {
                let entry = ShelfEntry::new(members, shown, place, 1);
                entry.build(&format!("entry.{p}.{i}"), "plan", entry.name_width(), t)
            });
        let framed = (place != Place::OffScreen).then(|| {
            [
                Shelf::frame(0.0, 1390.0, t),
                Shelf::frame_edge(0.0, 1390.0, t),
            ]
        });
        Shelf::row(format!("row.{p}"))
            .children(framed.into_iter().flatten())
            .children(entries)
    });
    let needing = STATES.map(shelf_member);
    let counts = [false, true].into_iter().flat_map(|open| {
        [&needing[..], &[]]
            .into_iter()
            .map(move |needing| (open, needing.to_vec()))
    });
    let counts = counts.enumerate().map(|(i, (open, needing))| {
        shelf_count(CountSide::After, 3, needing).build(format!("count.{i}"), "3 more", open, t)
    });
    rune::col()
        .w_full()
        .h_full()
        .fill(t.bg_panel)
        .children(rows)
        .child(Shelf::row("counts").children(counts))
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

fn luminance(c: rune::Color) -> f32 {
    0.2126 * c.r + 0.7152 * c.g + 0.0722 * c.b
}

fn contrast(a: rune::Color, b: rune::Color) -> f32 {
    let (a, b) = (luminance(a), luminance(b));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

/// What the board shows that reads too little against what is directly behind it: the last
/// opaque surface drawn before it under its middle.
fn faults(t: Tokens, dark: bool) -> (usize, Vec<String>) {
    SHELF_TOKENS.with(|cell| cell.set(Some(t)));
    let mut rt = App::new(AppSpec {
        fonts: attn_ui::FONTS,
        families: attn_ui::FAMILIES,
        ..AppSpec::new("shelf", SHELF_WINDOW, shelf_tokens, shelf_board)
    })
    .headless_runtime(SHELF_WINDOW, Env::new(dark, SCALE));
    rt.frame(0.0);
    let all = rt.elements();
    let behind = |at: usize| {
        let [x, y, w, h] = all[at].visible;
        let (x, y) = (x + w / 2.0, y + h / 2.0);
        all[..at]
            .iter()
            .rev()
            .find(|e| {
                let [ex, ey, ew, eh] = e.visible;
                e.fill.a > 0.999 && x >= ex && x <= ex + ew && y >= ey && y <= ey + eh
            })
            .expect("the board's own ground")
            .fill
    };
    let mut found = vec![];
    let mut seen = 0;
    let mut check = |what: &str, ink: rune::Color, ground: rune::Color, floor: f32| {
        seen += 1;
        let reads = contrast(ink, ground);
        if reads < floor - 0.01 {
            found.push(format!("{what} reads {reads:.2}:1, under {floor}:1"));
        }
    };
    for (i, e) in all.iter().enumerate().skip(1) {
        let [_, _, w, h] = e.visible;
        if w <= 0.0 || h <= 0.0 {
            continue;
        }
        let ground = behind(i);
        match (e.role, e.text_color) {
            ("icon", Some(ink)) => check(&e.path, ink, ground, 3.0),
            (_, Some(ink)) if e.text.is_some() => check(&e.path, ink, ground, 4.5),
            _ => {}
        }
        let dot = h == 4.0 && e.fill.a > 0.0;
        let cut_out = all
            .iter()
            .filter(|part| part.parent == Some(i) && part.role == "icon")
            .any(|icon| contrast(icon.text_color.expect("an icon's ink"), ground) < 1.01);
        if dot || (e.fill.a > 0.0 && cut_out) {
            check(&e.path, e.fill, ground, 3.0);
        }
        if e.key.as_deref() == Some("shelf.frame.edge") {
            let frame = e.rect.map(|v| v * SCALE);
            let edge = rt
                .instances
                .iter()
                .find(|drawn| {
                    drawn.border[3] > 0.0
                        && drawn
                            .rect
                            .iter()
                            .zip(frame)
                            .all(|(a, b)| (a - b).abs() <= 1.0)
                })
                .expect("the frame's edge");
            let [r, g, b] = [0, 1, 2].map(|c| rune::theme::srgb_decode(edge.border[c]));
            check(&e.path, rune::Color { r, g, b, a: 1.0 }, ground, 3.0);
        }
    }
    (seen, found)
}

#[test]
fn the_shelfs_marks_and_names_read_on_what_is_behind_them_in_every_scheme() {
    for scheme in attn_ui::colors::SCHEMES.iter() {
        for dark in [true, false] {
            let t = scheme.colors(dark).ui;
            let (seen, found) = faults(t, dark);
            println!("{}, dark={dark}: {seen} marks and words read", scheme.id);
            assert!(seen > 400, "only {seen} marks and words were found");
            assert!(
                found.is_empty(),
                "{}, dark={dark}:\n{}",
                scheme.id,
                found.join("\n")
            );
        }
    }
}
