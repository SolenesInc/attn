use attn_term::kitty::{
    Described, Format, Picture, Placed, Placements, Quad, Run, Viewport, Virtual, quad,
    source_rect, strip,
};
use attn_term::{Grid, Vt};

fn described(placement_id: u32, screen_row: i32) -> Described {
    Described {
        image_id: 1,
        placement_id,
        generation: 10,
        screen_row,
        pixel_width: 64,
        pixel_height: 64,
        ..Described::default()
    }
}

/// A 40×10 terminal that wrote `line 0` to `line 39`, so 30 rows are history.
fn scrolled() -> Vt {
    let mut vt = Vt::new(40, 10);
    let text: Vec<String> = (0..40).map(|i| format!("line {i}")).collect();
    vt.write(text.join("\r\n").as_bytes());
    assert_eq!(vt.history(), 30);
    vt
}

enum Call {
    Write(&'static str),
    Apply(u32, Vec<Described>),
    Seed(Vec<Described>),
}

#[test]
fn placements_land_on_the_rows_the_worker_put_them_and_each_set_replaces_the_last() {
    use Call::*;
    let virtual_ = |d: Described| Described {
        virtual_: true,
        ..d
    };
    let z = |d: Described, z: i32| Described { z, ..d };
    type Case = (&'static str, Vec<Call>, Vec<bool>, Vec<(u32, &'static str)>);
    let cases: Vec<Case> = vec![
        (
            "anchors a placement to the row the worker put it on",
            vec![Apply(1, vec![described(1, 3)])],
            vec![true],
            vec![(1, "line 33")],
        ),
        (
            "anchors a placement scrolled into the history",
            vec![Apply(1, vec![described(1, -5)])],
            vec![true],
            vec![(1, "line 25")],
        ),
        (
            "culls a placement above the history the client holds",
            vec![Apply(1, vec![described(1, -31), described(2, -30)])],
            vec![true],
            vec![(2, "line 0")],
        ),
        (
            "maps a set against the history of the moment",
            vec![
                Apply(1, vec![described(1, 2)]),
                Write("\r\nmore\r\nmore\r\nmore"),
                Apply(2, vec![described(1, 2)]),
            ],
            vec![true, true],
            vec![(1, "line 35")],
        ),
        (
            "rejects a set older than the one applied",
            vec![
                Apply(5, vec![described(1, 0)]),
                Apply(4, vec![described(2, 0)]),
            ],
            vec![true, false],
            vec![(1, "line 30")],
        ),
        (
            "accepts a set at the same seq, as a resize describes again",
            vec![
                Apply(5, vec![described(1, 0)]),
                Apply(5, vec![described(1, 4)]),
            ],
            vec![true, true],
            vec![(1, "line 34")],
        ),
        (
            "replaces the set rather than merging",
            vec![
                Apply(1, vec![described(1, 0), described(2, 1)]),
                Apply(2, vec![described(3, 2)]),
            ],
            vec![true, true],
            vec![(3, "line 32")],
        ),
        (
            "clears on the empty set",
            vec![Apply(1, vec![described(1, 0)]), Apply(2, vec![])],
            vec![true, true],
            vec![],
        ),
        (
            "skips a virtual placement, which the program draws itself",
            vec![Apply(1, vec![virtual_(described(1, 0)), described(2, 0)])],
            vec![true],
            vec![(2, "line 30")],
        ),
        (
            "orders by z, then placement id",
            vec![Apply(
                1,
                vec![
                    z(described(9, 0), 5),
                    z(described(3, 1), -1),
                    z(described(1, 2), 5),
                ],
            )],
            vec![true],
            vec![(3, "line 31"), (1, "line 32"), (9, "line 30")],
        ),
        (
            "drops what a restore does not carry, and accepts any seq after it",
            vec![
                Apply(7, vec![described(1, 0)]),
                Seed(vec![]),
                Apply(1, vec![described(1, 1)]),
            ],
            vec![true, true],
            vec![(1, "line 31")],
        ),
        (
            "takes a restore as the whole truth",
            vec![Apply(7, vec![described(1, 0)]), Seed(vec![described(2, 1)])],
            vec![true],
            vec![(2, "line 31")],
        ),
    ];
    for (name, calls, accepted, want) in cases {
        let mut vt = scrolled();
        let mut placements = Placements::default();
        let mut taken = Vec::new();
        for call in calls {
            match call {
                Write(bytes) => vt.write(bytes.as_bytes()),
                Apply(seq, set) => taken.push(placements.apply(seq, &set, vt.history())),
                Seed(set) => placements.seed(&set, vt.history()),
            }
        }
        assert_eq!(taken, accepted, "{name}");
        let cols = vt.size().0;
        let got: Vec<(u32, String)> = placements
            .placed()
            .iter()
            .map(|p| {
                let text = vt.row_text(p.row, 0..cols).trim_end().to_owned();
                (p.placement_id, text)
            })
            .collect();
        let want: Vec<(u32, String)> = want.into_iter().map(|(p, t)| (p, t.into())).collect();
        assert_eq!(got, want, "{name}");
    }
}

fn placed(row: u32, col: i32) -> Placed {
    Placed {
        image_id: 1,
        placement_id: 1,
        generation: 5,
        z: 0,
        row,
        col,
        offset: [0, 0],
        pixel_width: 100,
        pixel_height: 80,
        source: [0; 4],
    }
}

fn viewport(device_per_unit: f32) -> Viewport {
    Viewport {
        top: 100,
        cell: (10.0, 20.0),
        size: (800.0, 480.0),
        device_per_unit,
    }
}

fn close(a: f32, b: f32) -> bool {
    (a - b).abs() < 1e-4
}

#[test]
fn a_placement_draws_at_its_cell_and_size_clipped_to_the_grid_without_squashing() {
    type Case = (
        &'static str,
        Placed,
        f32,
        Option<([f32; 4], [f32; 2], [f32; 2])>,
    );
    let cases: Vec<Case> = vec![
        (
            "a fully visible image draws at its cell, at its size",
            placed(103, 4),
            1.0,
            Some(([40.0, 60.0, 100.0, 80.0], [0.0, 1.0], [0.0, 1.0])),
        ),
        (
            "an image above the view is cropped, not squashed",
            placed(98, 0),
            1.0,
            Some(([0.0, 0.0, 100.0, 40.0], [0.0, 1.0], [0.5, 1.0])),
        ),
        (
            "an image past the right edge is cropped",
            placed(100, 74),
            1.0,
            Some(([740.0, 0.0, 60.0, 80.0], [0.0, 0.6], [0.0, 1.0])),
        ),
        (
            "an image past the bottom is cropped",
            placed(123, 0),
            1.0,
            Some(([0.0, 460.0, 100.0, 20.0], [0.0, 1.0], [0.0, 0.25])),
        ),
        (
            "an image scrolled out above draws nothing",
            placed(95, 0),
            1.0,
            None,
        ),
        (
            "an image below the view draws nothing",
            placed(124, 0),
            1.0,
            None,
        ),
        (
            "an image that just pokes into the view draws its last rows",
            placed(97, 0),
            1.0,
            Some(([0.0, 0.0, 100.0, 20.0], [0.0, 1.0], [0.75, 1.0])),
        ),
        (
            "a retina placement draws at half its device-pixel size",
            placed(103, 4),
            2.0,
            Some(([40.0, 60.0, 50.0, 40.0], [0.0, 1.0], [0.0, 1.0])),
        ),
        (
            "a retina placement is clipped in grid units",
            placed(100, 77),
            2.0,
            Some(([770.0, 0.0, 30.0, 40.0], [0.0, 0.6], [0.0, 1.0])),
        ),
    ];
    for (name, placed, scale, want) in cases {
        let got = quad(&placed, viewport(scale));
        match (got, want) {
            (None, None) => {}
            (Some(q), Some((rect, u, v))) => {
                let got = [q.x, q.y, q.width, q.height, q.u[0], q.u[1], q.v[0], q.v[1]];
                let want = [rect[0], rect[1], rect[2], rect[3], u[0], u[1], v[0], v[1]];
                assert!(
                    got.iter().zip(want).all(|(a, b)| close(*a, b)),
                    "{name}: {got:?} != {want:?}"
                );
            }
            (got, want) => panic!("{name}: {got:?} != {want:?}"),
        }
    }
}

#[test]
fn a_placement_samples_its_source_rect_cropped_as_its_quad_was() {
    let sub = |p: Placed| Placed {
        source: [10, 20, 50, 40],
        ..p
    };
    let cases: Vec<(&str, Placed, [f32; 4])> = vec![
        (
            "the whole texture when the placement names no source rect",
            placed(100, 0),
            [0.0, 0.0, 200.0, 160.0],
        ),
        (
            "the sub-rect a placement names",
            sub(placed(100, 0)),
            [10.0, 20.0, 50.0, 40.0],
        ),
        (
            "the named sub-rect cropped as the quad was",
            sub(placed(98, 0)),
            [10.0, 40.0, 50.0, 20.0],
        ),
        (
            "the whole texture cropped as the quad was",
            placed(98, 0),
            [0.0, 80.0, 200.0, 80.0],
        ),
    ];
    for (name, placed, want) in cases {
        let q: Quad = quad(&placed, viewport(1.0)).expect("the placement shows");
        let got = source_rect(&placed, &q, (200, 160));
        assert!(
            got.iter().zip(want).all(|(a, b)| close(*a, b)),
            "{name}: {got:?} != {want:?}"
        );
    }
}

#[test]
fn a_stored_image_draws_as_rgba_whatever_layout_the_daemon_kept_it_in() {
    type Case = (&'static str, Format, (u32, u32), Vec<u8>, Option<Vec<u8>>);
    let cases: Vec<Case> = vec![
        (
            "rgb gains an opaque alpha",
            Format::Rgb,
            (2, 1),
            vec![1, 2, 3, 4, 5, 6],
            Some(vec![1, 2, 3, 255, 4, 5, 6, 255]),
        ),
        (
            "rgba stays as it is",
            Format::Rgba,
            (1, 1),
            vec![1, 2, 3, 4],
            Some(vec![1, 2, 3, 4]),
        ),
        (
            "gray and alpha spreads the gray",
            Format::GrayAlpha,
            (2, 1),
            vec![7, 8, 9, 10],
            Some(vec![7, 7, 7, 8, 9, 9, 9, 10]),
        ),
        (
            "gray spreads the gray, opaque",
            Format::Gray,
            (1, 2),
            vec![7, 9],
            Some(vec![7, 7, 7, 255, 9, 9, 9, 255]),
        ),
        (
            "pixels short of the size are no picture",
            Format::Rgb,
            (2, 1),
            vec![1, 2, 3],
            None,
        ),
        (
            "pixels past the size are no picture",
            Format::Gray,
            (1, 1),
            vec![1, 2],
            None,
        ),
        (
            "an empty image is no picture",
            Format::Rgba,
            (0, 3),
            vec![],
            None,
        ),
    ];
    for (name, format, (width, height), pixels, want) in cases {
        let got = Picture::new(1, width, height, format, &pixels).map(|p| p.rgba);
        assert_eq!(got, want, "{name}");
    }
}

/// U+10EEEE with the diacritics for `marks` (row, column, image id's high byte): 0 is U+0305,
/// 1 U+030D, 2 U+030E, 3 U+0310.
fn placeholder(marks: &[usize]) -> String {
    const DIACRITICS: [char; 4] = ['\u{0305}', '\u{030D}', '\u{030E}', '\u{0310}'];
    std::iter::once('\u{10EEEE}')
        .chain(marks.iter().map(|&m| DIACRITICS[m]))
        .collect()
}

fn run(col: u16, width: u16, image_id: u32, image_row: u32, image_col: u32) -> Run {
    Run {
        col,
        width,
        image_id,
        placement_id: 0,
        image_row,
        image_col,
    }
}

#[test]
fn placeholder_cells_group_into_runs_as_kitty_reads_them_and_draw_no_glyph() {
    let p = placeholder;
    type Case = (&'static str, String, Vec<(u16, Run)>);
    let cases: Vec<Case> = vec![
        (
            "a row of cells naming each row and column is one run",
            format!("\x1b[38;5;7m{}{}{}", p(&[1, 0]), p(&[1, 1]), p(&[1, 2])),
            vec![(0, run(0, 3, 7, 1, 0))],
        ),
        (
            "cells leaving out the column continue the cell to their left",
            format!("ab\x1b[38;5;7m{}{}{}", p(&[2, 1]), p(&[]), p(&[])),
            vec![(0, run(2, 3, 7, 2, 1))],
        ),
        (
            "a bare first cell starts at the image's first row and column",
            format!("\x1b[38;5;7m{}{}", p(&[]), p(&[])),
            vec![(0, run(0, 2, 7, 0, 0))],
        ),
        (
            "a column that does not follow on starts a new run",
            format!("\x1b[38;5;7m{}{}", p(&[0, 0]), p(&[0, 3])),
            vec![(0, run(0, 1, 7, 0, 0)), (0, run(1, 1, 7, 0, 3))],
        ),
        (
            "another image starts a new run",
            format!("\x1b[38;5;7m{}\x1b[38;5;8m{}", p(&[0, 0]), p(&[0, 1])),
            vec![(0, run(0, 1, 7, 0, 0)), (0, run(1, 1, 8, 0, 1))],
        ),
        (
            "text between cells ends the run",
            format!("\x1b[38;5;7m{}x{}", p(&[0, 0]), p(&[0, 1])),
            vec![(0, run(0, 1, 7, 0, 0)), (0, run(2, 1, 7, 0, 1))],
        ),
        (
            "a truecolor foreground is the image id's low 24 bits, a third diacritic its high byte",
            format!("\x1b[38;2;1;2;3m{}", p(&[0, 0, 2])),
            vec![(0, run(0, 1, 0x0201_0203, 0, 0))],
        ),
        (
            "the underline color names the placement",
            format!("\x1b[38;5;7;58;5;9m{}", p(&[0, 0])),
            vec![(
                0,
                Run {
                    placement_id: 9,
                    ..run(0, 1, 7, 0, 0)
                },
            )],
        ),
        (
            "each row keeps its own runs",
            format!("\x1b[38;5;7m{}\r\n{}", p(&[0, 0]), p(&[1, 0])),
            vec![(0, run(0, 1, 7, 0, 0)), (1, run(0, 1, 7, 1, 0))],
        ),
    ];
    for (name, bytes, want) in cases {
        let mut vt = Vt::new(20, 3);
        vt.write(bytes.as_bytes());
        let mut grid = Grid::default();
        vt.sync(&mut grid);
        let got: Vec<(u16, Run)> = grid.placeholders().map(|(row, r)| (row, *r)).collect();
        assert_eq!(got, want, "{name}");
        for y in 0..3 {
            assert!(
                !grid.row_text(y).contains('\u{10EEEE}'),
                "{name}: row {y} draws a placeholder glyph"
            );
        }
    }
}

#[test]
fn rewriting_a_row_drops_its_placeholder_runs() {
    let mut vt = Vt::new(20, 3);
    let mut grid = Grid::default();
    vt.write(format!("\x1b[38;5;7m{}", placeholder(&[0, 0])).as_bytes());
    vt.sync(&mut grid);
    assert_eq!(grid.placeholders().count(), 1);
    vt.write(b"\x1b[0m\r\x1b[2Kplain");
    vt.sync(&mut grid);
    assert_eq!(grid.placeholders().count(), 0);
    assert_eq!(grid.row_text(0), "plain");
}

/// Ghostty's own cases for a 500×306 image in 36×80 cells (`graphics_unicode.zig`, "unicode
/// render placement"), and the edges they leave out.
#[test]
fn a_run_draws_its_strip_of_the_image_fitted_and_centred_in_the_placement_cells() {
    let target = |columns: u32, rows: u32| Virtual {
        image_id: 1,
        placement_id: 0,
        generation: 4,
        columns,
        rows,
    };
    type Case = (
        &'static str,
        Virtual,
        Run,
        Option<([u32; 2], [u32; 2], [u32; 4])>,
    );
    let cases: Vec<Case> = vec![
        (
            "4x2, top row: the image is centred down, its top half shows",
            target(4, 2),
            run(0, 4, 1, 0, 0),
            Some(([0, 36], [144, 44], [0, 0, 500, 153])),
        ),
        (
            "4x2, bottom row: its bottom half shows from the cell's top",
            target(4, 2),
            run(0, 4, 1, 1, 0),
            Some(([0, 0], [144, 44], [0, 153, 500, 153])),
        ),
        (
            "2x2 written four cells wide still fits the placement's two columns",
            target(2, 2),
            run(0, 4, 1, 0, 0),
            Some(([0, 58], [72, 22], [0, 0, 500, 153])),
        ),
        (
            "1x1: the whole image in one cell",
            target(1, 1),
            run(0, 4, 1, 0, 0),
            Some(([0, 29], [36, 22], [0, 0, 500, 306])),
        ),
        (
            "a row past the image's rows shows nothing",
            target(4, 2),
            run(0, 4, 1, 2, 0),
            None,
        ),
        (
            "no size asked fits the image to its size rounded up to whole cells, 14x4",
            target(0, 0),
            run(0, 14, 1, 0, 0),
            Some(([0, 6], [504, 74], [0, 0, 500, 74])),
        ),
    ];
    for (name, target, run, want) in cases {
        let got = strip(&run, &target, (500, 306), (36, 80), 12)
            .map(|p| (p.offset, [p.pixel_width, p.pixel_height], p.source));
        assert_eq!(got, want, "{name}");
        if let Some(p) = strip(&run, &target, (500, 306), (36, 80), 12) {
            assert_eq!((p.row, p.col, p.generation, p.z), (12, 0, 4, -1), "{name}");
        }
    }
    let sliver = |image_row: u32| {
        strip(
            &run(0, 10, 1, image_row, 0),
            &target(10, 10),
            (15, 26),
            (16, 34),
            0,
        )
    };
    assert_eq!(
        sliver(0),
        None,
        "a row the image covers by under half an image pixel shows nothing, not the whole image"
    );
    assert!(sliver(1).is_some());
}

#[test]
fn virtual_placements_are_kept_apart_and_found_by_the_runs_that_name_them() {
    let virtual_ = |placement_id: u32, image_id: u32| Described {
        image_id,
        placement_id,
        generation: 3,
        virtual_: true,
        grid_cols: 4,
        grid_rows: 2,
        ..Described::default()
    };
    let mut placements = Placements::default();
    placements.apply(1, &[virtual_(5, 1), virtual_(6, 1), described(2, 0)], 0);
    assert_eq!(
        placements
            .placed()
            .iter()
            .map(|p| p.placement_id)
            .collect::<Vec<_>>(),
        [2]
    );
    let named = Run {
        placement_id: 6,
        ..run(0, 1, 1, 0, 0)
    };
    assert_eq!(placements.target(&named).map(|v| v.placement_id), Some(6));
    assert_eq!(
        placements
            .target(&run(0, 1, 1, 0, 0))
            .map(|v| (v.columns, v.rows)),
        Some((4, 2)),
        "a run naming no placement shows any of its image's"
    );
    assert_eq!(placements.target(&run(0, 1, 9, 0, 0)), None);
    let mut images: Vec<(u32, u64)> = placements.images().collect();
    images.sort_unstable();
    assert_eq!(images, [(1, 3), (1, 3), (1, 10)]);
}
