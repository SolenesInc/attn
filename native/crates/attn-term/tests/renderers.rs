use std::sync::{Arc, LazyLock};

use attn_term::kitty::{Picture as KittyPicture, Placed};
use attn_term::{Cells, FONT_SIZE, Grid, Image, Metrics, Overlay, OverlayKind, Sprite, Vt};
use attn_ui::colors::TerminalColors;
use rune::compose::DeviceKey;
use rune::render::{EffectSite, Gpu, Instance, OFFSCREEN_FORMAT, Offscreen, Renderer};

const MARK: u32 = 7;
const SHAPE: (u16, u16) = (24, 6);
const FULL: f32 = 2.0;
const SMALL: f32 = 0.35;

static GPU: LazyLock<(Gpu, DeviceKey)> = LazyLock::new(|| {
    let gpu = Gpu::try_with_features(Gpu::instance(), None, Cells::FEATURES)
        .unwrap_or_else(|error| panic!("{error}"));
    let key = Renderer::new(&gpu.device, OFFSCREEN_FORMAT).device_key();
    (gpu, key)
});

fn metrics() -> Metrics {
    Metrics::of(FONT_SIZE)
}

/// The slot a grid of `SHAPE` cells takes at `scale`, in device pixels.
fn slot(scale: f32) -> (u32, u32) {
    let m = metrics();
    (
        (f32::from(SHAPE.0) * m.cell_w * scale).round() as u32,
        (f32::from(SHAPE.1) * m.cell_h * scale).round() as u32,
    )
}

struct Picture {
    width: u32,
    rgba: Vec<u8>,
}

impl Picture {
    fn at(&self, x: u32, y: u32) -> [u8; 4] {
        let at = ((y * self.width + x) * 4) as usize;
        self.rgba[at..at + 4].try_into().expect("four bytes")
    }

    /// The bounds, as left, top, right and bottom, of the pixels that `shows` takes.
    fn bounds(&self, shows: impl Fn([u8; 4]) -> bool) -> Option<[u32; 4]> {
        let height = self.rgba.len() as u32 / 4 / self.width;
        let mut bounds: Option<[u32; 4]> = None;
        for y in 0..height {
            for x in 0..self.width {
                if shows(self.at(x, y)) {
                    let [l, t, r, b] = bounds.unwrap_or([x, y, x, y]);
                    bounds = Some([l.min(x), t.min(y), r.max(x + 1), b.max(y + 1)]);
                }
            }
        }
        bounds
    }

    fn differs_by(&self, other: &Picture) -> u8 {
        assert_eq!(self.rgba.len(), other.rgba.len());
        self.rgba
            .iter()
            .zip(&other.rgba)
            .map(|(a, b)| a.abs_diff(*b))
            .max()
            .unwrap_or(0)
    }
}

/// `grid` drawn by `cells` into a slot of `size` device pixels on a clear target, at full size
/// or shrunk.
fn draw(cells: &mut Cells, grid: &Grid, shrink: Option<f32>, size: (u32, u32)) -> Picture {
    let (gpu, key) = &*GPU;
    let target = Offscreen::with_format(&gpu.device, size.0, size.1, OFFSCREEN_FORMAT);
    cells.prepare(
        &gpu.device,
        &gpu.queue,
        *key,
        FULL,
        shrink,
        metrics(),
        TerminalColors::default(),
        [(MARK, grid)],
    );
    let mut encoder = gpu.device.create_command_encoder(&Default::default());
    {
        let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
            color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                view: &target.view,
                depth_slice: None,
                resolve_target: None,
                ops: wgpu::Operations {
                    load: wgpu::LoadOp::Clear(wgpu::Color::TRANSPARENT),
                    store: wgpu::StoreOp::Store,
                },
            })],
            ..Default::default()
        });
        let instance: Instance = bytemuck::cast([0f32; 32]);
        let instance = Instance {
            rect: [0.0, 0.0, size.0 as f32, size.1 as f32],
            ..instance
        };
        cells.draw(
            &gpu.device,
            &mut pass,
            &EffectSite {
                index: 0,
                instance: &instance,
                mark: MARK,
                data: [0.0; 4],
                scale: FULL,
                format: OFFSCREEN_FORMAT,
                origin: [0.0, 0.0],
                size: [size.0, size.1],
                in_layer: false,
                srgb_blend: false,
                backdrop: None,
            },
        );
    }
    target.copy(&mut encoder);
    gpu.queue.submit([encoder.finish()]);
    Picture {
        width: size.0,
        rgba: target.read(&gpu.device),
    }
}

fn screen(text: &[u8]) -> (Vt, Grid) {
    let mut vt = Vt::new(SHAPE.0, SHAPE.1);
    vt.write(text);
    let mut grid = Grid::default();
    vt.sync(&mut grid);
    (vt, grid)
}

/// Both renderers of one grid, each drawing it after the other.
#[derive(Default)]
struct Both {
    full: Cells,
    small: Cells,
}

impl Both {
    fn draw(&mut self, grid: &Grid) -> [Picture; 2] {
        [
            draw(&mut self.full, grid, None, slot(FULL)),
            draw(&mut self.small, grid, Some(SMALL), slot(FULL * SMALL)),
        ]
    }
}

const SAME: u8 = 1;

fn find_mark(row: u16) -> Overlay {
    Overlay {
        row,
        cols: (0, 8),
        color: [255, 255, 0],
        alpha: 255,
        kind: OverlayKind::Wash,
    }
}

static SQUARE: rune::Icon = rune::Icon {
    name: "square",
    view: 16.0,
    body: r#"<rect x="2" y="2" width="12" height="12" fill="currentColor"/>"#,
};

fn red_sprite(row: u16, sprite: Sprite) -> Overlay {
    Overlay {
        row,
        cols: (2, 5),
        color: [255, 0, 0],
        alpha: 255,
        kind: OverlayKind::Sprite(sprite),
    }
}

fn red_image(row: u32) -> Image {
    let (cw, ch) = metrics().device_cell(FULL);
    Image {
        picture: Arc::new(KittyPicture {
            id: 1,
            width: 1,
            height: 1,
            rgba: vec![255, 0, 0, 255],
        }),
        placed: Placed {
            image_id: 1,
            placement_id: 1,
            generation: 1,
            z: 0,
            row,
            col: 3,
            offset: [0, 0],
            pixel_width: (cw * 8.0) as u32,
            pixel_height: ch as u32,
            source: [0; 4],
        },
    }
}

/// Both renderers draw `grid` as ones that have drawn nothing do, and `before` when they differ.
fn draws_like_a_fresh_renderer(both: &mut Both, grid: &Grid, what: &str) -> [Picture; 2] {
    let drawn = both.draw(grid);
    let expected = Both::default().draw(grid);
    for (i, size) in ["full size", "small"].iter().enumerate() {
        assert!(
            drawn[i].differs_by(&expected[i]) <= SAME,
            "{size}: {what} draws as a renderer that draws it whole draws it"
        );
    }
    drawn
}

#[test]
fn two_renderers_of_one_grid_both_draw_a_row_that_changed_and_a_grid_replaced_under_their_mark() {
    let (_, replaced) = screen(b"another\r\n\x1b[42mterminal\x1b[0m\r\nentirely");
    let (mut vt, mut grid) = screen(b"one\r\ntwo\r\nthree");
    let mut both = Both::default();
    let before = draws_like_a_fresh_renderer(&mut both, &grid, "the grid");

    vt.write(b"\r\n\x1b[41mfour changed\x1b[0m");
    assert!(vt.sync(&mut grid));
    let after = draws_like_a_fresh_renderer(&mut both, &grid, "a changed row");
    for (i, size) in ["full size", "small"].iter().enumerate() {
        assert!(
            before[i].differs_by(&after[i]) > 60,
            "{size}: the changed row shows"
        );
    }

    grid.set_overlays(vec![find_mark(1)]);
    let marked = draws_like_a_fresh_renderer(&mut both, &grid, "a mark laid over the grid");
    grid.set_images(vec![red_image(4)], 0);
    let pictured = draws_like_a_fresh_renderer(&mut both, &grid, "an image placed on the grid");
    for (i, size) in ["full size", "small"].iter().enumerate() {
        assert!(
            after[i].differs_by(&marked[i]) > 60,
            "{size}: the mark shows"
        );
        assert!(
            marked[i].differs_by(&pictured[i]) > 60,
            "{size}: the image shows"
        );
    }

    draws_like_a_fresh_renderer(&mut both, &replaced, "a grid replaced under the mark");
}

#[test]
fn a_small_grid_fills_its_slot_at_its_own_proportions_and_stands_centered_in_the_rest() {
    let m = metrics();
    let (w, h) = (f32::from(SHAPE.0) * m.cell_w, f32::from(SHAPE.1) * m.cell_h);
    let text = format!("\x1b[44m{}", " ".repeat(usize::from(SHAPE.0 * SHAPE.1)));
    let (_, grid) = screen(text.as_bytes());
    for scale in [0.2f32, 0.35, 0.7, 1.3] {
        let shrink = Some(scale / FULL);
        let mut cells = Cells::default();
        for (wide, tall) in [(1.0, 1.0), (1.5, 1.0), (1.0, 2.0)] {
            let size = ((w * scale * wide) as u32, (h * scale * tall) as u32);
            let picture = draw(&mut cells, &grid, shrink, size);
            let fit = (size.0 as f32 / w).min(size.1 as f32 / h);
            let (gw, gh) = (w * fit, h * fit);
            let (left, top) = ((size.0 as f32 - gw) / 2.0, (size.1 as f32 - gh) / 2.0);
            let lit = picture.bounds(|p| p != [0; 4]).expect("the grid draws");
            for (edge, want) in lit.iter().zip([left, top, left + gw, top + gh]) {
                assert!(
                    (*edge as f32 - want).abs() <= 1.5,
                    "at {scale} in {size:?} the grid stands at {lit:?}, not {left},{top} {gw}x{gh}"
                );
            }
        }
    }
}

#[test]
fn an_image_stands_in_a_small_grid_where_and_as_large_as_in_a_full_size_one() {
    let (_, mut grid) = screen(b"");
    grid.set_images(vec![red_image(1)], 0);
    let red = |picture: &Picture| {
        picture
            .bounds(|[r, g, b, _]| r > 128 && g < 128 && b < 128)
            .expect("the image draws")
    };
    let both = Both::default().draw(&grid);
    let (full, small) = (red(&both[0]), red(&both[1]));
    for (edge, full) in small.iter().zip(full) {
        let want = full as f32 * SMALL;
        assert!(
            (*edge as f32 - want).abs() <= 1.5,
            "small {small:?} beside full {full:?}"
        );
    }
}

#[test]
fn a_sprite_stands_centered_in_place_of_its_cells_text_which_comes_back_when_it_goes() {
    let text = b"\r\nx ag-7yd2rk x\r\nx ag-7yd2rk x";
    let (_, bare) = screen(text);
    let shows_red = |[r, g, b, _]: [u8; 4]| r > 128 && g < 64 && b < 64;
    for sprite in [Sprite::Icon(&SQUARE), Sprite::Letter('M')] {
        let (_, mut grid) = screen(text);
        let mut both = Both::default();
        both.draw(&grid);
        for row in [1, 2] {
            grid.set_overlays(vec![red_sprite(row, sprite)]);
            let drawn = draws_like_a_fresh_renderer(&mut both, &grid, "a sprite");
            let blank: &[u8] = match row {
                1 => b"\r\nx    7yd2rk x\r\nx ag-7yd2rk x",
                _ => b"\r\nx ag-7yd2rk x\r\nx    7yd2rk x",
            };
            let (_, mut blank) = screen(blank);
            blank.set_overlays(vec![red_sprite(row, sprite)]);
            let over_blank = Both::default().draw(&blank);
            for (i, scale) in [FULL, FULL * SMALL].into_iter().enumerate() {
                assert!(
                    drawn[i].differs_by(&over_blank[i]) <= SAME,
                    "{sprite:?} at {scale} shows none of its cells' text"
                );
                let m = metrics();
                let (cw, ch) = (m.cell_w * scale, m.cell_h * scale);
                let [l, t, r, b] = drawn[i].bounds(shows_red).expect("the sprite draws");
                let middle = [(l + r) as f32 / 2.0, (t + b) as f32 / 2.0];
                let wants = [cw * 3.5, ch * (f32::from(row) + 0.5)];
                assert!(
                    (middle[0] - wants[0]).abs() <= cw * 0.2
                        && (middle[1] - wants[1]).abs() <= ch * 0.2,
                    "{sprite:?} at {scale} stands around {middle:?}, not {wants:?}"
                );
            }
        }
        grid.set_overlays(vec![]);
        let after = both.draw(&grid);
        for (after, bare) in after.iter().zip(Both::default().draw(&bare)) {
            assert!(
                after.differs_by(&bare) <= SAME,
                "{sprite:?} leaves its text"
            );
        }
    }
}

#[test]
fn every_underline_and_strike_still_shows_at_a_fifth_of_full_size() {
    let size = slot(FULL * 0.2);
    for (style, name) in [("4", "underline"), ("9", "strike")] {
        let line = format!("\x1b[{style};38;2;255;0;0m        \x1b[0m");
        let (_, grid) = screen(vec![line; usize::from(SHAPE.1)].join("\r\n").as_bytes());
        let picture = draw(&mut Cells::default(), &grid, Some(0.2), size);
        let row = size.1 as f32 / f32::from(SHAPE.1);
        for y in 0..u32::from(SHAPE.1) {
            let (top, bottom) = ((y as f32 * row) as u32, ((y + 1) as f32 * row) as u32);
            let shows = (top..bottom).any(|y| picture.at(2, y)[0] > 120);
            assert!(shows, "{name} of row {y}");
        }
    }
}
