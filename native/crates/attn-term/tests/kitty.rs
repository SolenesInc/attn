use attn_term::Vt;
use attn_term::kitty::{
    Described, Format, Picture, Placed, Placements, Quad, Viewport, quad, source_rect,
};

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
