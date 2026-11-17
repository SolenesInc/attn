//! Box drawing and block elements drawn as rectangles snapped to the cell, as Ghostty's sprite font
//! does, instead of font glyphs that miss the cell edges.

const L: u8 = 1;
const H: u8 = 2;
const D: u8 = 3;

/// Arms (up, right, down, left) of a box-drawing character: 0 none, 1 light, 2 heavy, 3 double.
fn arms(cp: u32) -> Option<[u8; 4]> {
    Some(match cp {
        0x2500 => [0, L, 0, L],
        0x2501 => [0, H, 0, H],
        0x2502 => [L, 0, L, 0],
        0x2503 => [H, 0, H, 0],
        0x250C => [0, L, L, 0],
        0x2510 => [0, 0, L, L],
        0x2514 => [L, L, 0, 0],
        0x2518 => [L, 0, 0, L],
        0x250F => [0, H, H, 0],
        0x2513 => [0, 0, H, H],
        0x2517 => [H, H, 0, 0],
        0x251B => [H, 0, 0, H],
        0x251C => [L, L, L, 0],
        0x2524 => [L, 0, L, L],
        0x252C => [0, L, L, L],
        0x2534 => [L, L, 0, L],
        0x253C => [L, L, L, L],
        0x2523 => [H, H, H, 0],
        0x252B => [H, 0, H, H],
        0x2533 => [0, H, H, H],
        0x253B => [H, H, 0, H],
        0x254B => [H, H, H, H],
        0x2574 => [0, 0, 0, L],
        0x2575 => [L, 0, 0, 0],
        0x2576 => [0, L, 0, 0],
        0x2577 => [0, 0, L, 0],
        0x2550 => [0, D, 0, D],
        0x2551 => [D, 0, D, 0],
        0x2554 => [0, D, D, 0],
        0x2557 => [0, 0, D, D],
        0x255A => [D, D, 0, 0],
        0x255D => [D, 0, 0, D],
        0x2560 => [D, D, D, 0],
        0x2563 => [D, 0, D, D],
        0x2566 => [0, D, D, D],
        0x2569 => [D, D, 0, D],
        0x256C => [D, D, D, D],
        _ => return None,
    })
}

/// Block elements as fractions of the cell: x, y, w, h.
fn blocks(cp: u32) -> Option<&'static [[f32; 4]]> {
    const E: f32 = 1.0 / 8.0;
    Some(match cp {
        0x2580 => &[[0.0, 0.0, 1.0, 0.5]],
        0x2581 => &[[0.0, 7.0 * E, 1.0, E]],
        0x2582 => &[[0.0, 6.0 * E, 1.0, 2.0 * E]],
        0x2583 => &[[0.0, 5.0 * E, 1.0, 3.0 * E]],
        0x2584 => &[[0.0, 0.5, 1.0, 0.5]],
        0x2585 => &[[0.0, 3.0 * E, 1.0, 5.0 * E]],
        0x2586 => &[[0.0, 2.0 * E, 1.0, 6.0 * E]],
        0x2587 => &[[0.0, E, 1.0, 7.0 * E]],
        0x2588 => &[[0.0, 0.0, 1.0, 1.0]],
        0x2589 => &[[0.0, 0.0, 7.0 * E, 1.0]],
        0x258A => &[[0.0, 0.0, 6.0 * E, 1.0]],
        0x258B => &[[0.0, 0.0, 5.0 * E, 1.0]],
        0x258C => &[[0.0, 0.0, 0.5, 1.0]],
        0x258D => &[[0.0, 0.0, 3.0 * E, 1.0]],
        0x258E => &[[0.0, 0.0, 2.0 * E, 1.0]],
        0x258F => &[[0.0, 0.0, E, 1.0]],
        0x2590 => &[[0.5, 0.0, 0.5, 1.0]],
        0x2594 => &[[0.0, 0.0, 1.0, E]],
        0x2595 => &[[7.0 * E, 0.0, E, 1.0]],
        0x2596 => &[[0.0, 0.5, 0.5, 0.5]],
        0x2597 => &[[0.5, 0.5, 0.5, 0.5]],
        0x2598 => &[[0.0, 0.0, 0.5, 0.5]],
        0x259D => &[[0.5, 0.0, 0.5, 0.5]],
        0x2599 => &[[0.0, 0.0, 0.5, 1.0], [0.5, 0.5, 0.5, 0.5]],
        0x259B => &[[0.0, 0.0, 1.0, 0.5], [0.0, 0.5, 0.5, 0.5]],
        0x259C => &[[0.0, 0.0, 1.0, 0.5], [0.5, 0.5, 0.5, 0.5]],
        0x259F => &[[0.5, 0.0, 0.5, 1.0], [0.0, 0.5, 0.5, 0.5]],
        0x259A => &[[0.0, 0.0, 0.5, 0.5], [0.5, 0.5, 0.5, 0.5]],
        0x259E => &[[0.5, 0.0, 0.5, 0.5], [0.0, 0.5, 0.5, 0.5]],
        _ => return None,
    })
}

/// The rounded corners ╭ ╮ ╰ ╯ as the way their horizontal arm runs (1 right, -1 left) and
/// their vertical arm runs (1 down, -1 up).
fn rounded(cp: u32) -> Option<[f32; 2]> {
    Some(match cp {
        0x256D => [1.0, 1.0],
        0x256E => [-1.0, 1.0],
        0x2570 => [1.0, -1.0],
        0x256F => [-1.0, -1.0],
        _ => return None,
    })
}

/// `cp` as an A8 coverage mask of its `w` by `h` device-pixel cell, or None when the font draws
/// it.
pub fn mask(cp: u32, w: u32, h: u32) -> Option<Vec<u8>> {
    if let Some(arms) = rounded(cp) {
        return Some(arc(arms, w, h));
    }
    Some(fill(&rects(cp, w as f32, h as f32)?, w, h))
}

/// A rounded corner as Ghostty draws one: its two light arms run from the cell's edges and meet
/// on a quarter circle half the cell's narrower side across, antialiased.
fn arc([sx, sy]: [f32; 2], w: u32, h: u32) -> Vec<u8> {
    let (cw, ch) = (w as f32, h as f32);
    let vertical = rects(0x2502, cw, ch).expect("a light vertical line")[0];
    let horizontal = rects(0x2500, cw, ch).expect("a light horizontal line")[0];
    let (vx, hy) = (
        vertical[0] + vertical[2] * 0.5,
        horizontal[1] + horizontal[3] * 0.5,
    );
    let half = vertical[2] * 0.5;
    let r = (cw * 0.5).min(ch * 0.5);
    let (ox, oy) = (vx + sx * r, hy + sy * r);
    let (x_end, y_end) = (
        if sx > 0.0 { cw } else { 0.0 },
        if sy > 0.0 { ch } else { 0.0 },
    );
    let beyond = |v: f32, a: f32, b: f32| (a.min(b) - v).max(v - a.max(b)).max(0.0);
    let mut m = vec![0u8; (w * h) as usize];
    for y in 0..h {
        for x in 0..w {
            let (px, py) = (x as f32 + 0.5, y as f32 + 0.5);
            let down_arm = (px - vx).abs().hypot(beyond(py, oy, y_end));
            let across_arm = (py - hy).abs().hypot(beyond(px, ox, x_end));
            let (dx, dy) = (px - ox, py - oy);
            let on_curve = if dx * sx <= 0.0 && dy * sy <= 0.0 {
                (dx.hypot(dy) - r).abs()
            } else {
                f32::INFINITY
            };
            let d = down_arm.min(across_arm).min(on_curve);
            m[(y * w + x) as usize] = ((half + 0.5 - d).clamp(0.0, 1.0) * 255.0).round() as u8;
        }
    }
    m
}

/// Rects in device pixels relative to the cell's top-left, or None when the font draws `cp`.
fn rects(cp: u32, cw: f32, ch: f32) -> Option<Vec<[f32; 4]>> {
    if let Some(b) = blocks(cp) {
        return Some(
            b.iter()
                .map(|r| snap([r[0] * cw, r[1] * ch, r[2] * cw, r[3] * ch]))
                .collect(),
        );
    }
    let a = arms(cp)?;
    let light = (cw / 9.0).round().max(1.0);
    let (cx, cy) = ((cw * 0.5).floor(), (ch * 0.5).floor());
    let mut out = Vec::new();
    let any_double = a.contains(&D);
    for (i, &w) in a.iter().enumerate() {
        if w == 0 {
            continue;
        }
        let t = if w == H { light * 2.0 } else { light };
        let lines: &[f32] = if w == D { &[-light, light] } else { &[0.0] };
        for &off in lines {
            let reach = if any_double { light } else { t * 0.5 };
            let r = match i {
                0 => [
                    cx - t * 0.5 + off,
                    0.0,
                    t,
                    cy + reach + if w == D { t * 0.5 } else { 0.0 },
                ],
                1 => [cx - reach + 0.0, cy - t * 0.5 + off, cw - cx + reach, t],
                2 => [cx - t * 0.5 + off, cy - reach, t, ch - cy + reach],
                _ => [0.0, cy - t * 0.5 + off, cx + reach, t],
            };
            out.push(snap(r));
        }
    }
    Some(out)
}

fn snap(r: [f32; 4]) -> [f32; 4] {
    let (x0, y0) = (r[0].round(), r[1].round());
    let (x1, y1) = ((r[0] + r[2]).round(), (r[1] + r[3]).round());
    [x0, y0, (x1 - x0).max(1.0), (y1 - y0).max(1.0)]
}

/// The rects rasterized into an A8 coverage mask `w` by `h`.
fn fill(rects: &[[f32; 4]], w: u32, h: u32) -> Vec<u8> {
    let mut m = vec![0u8; (w * h) as usize];
    for r in rects {
        let (x0, y0) = (r[0].max(0.0) as u32, r[1].max(0.0) as u32);
        let (x1, y1) = (((r[0] + r[2]) as u32).min(w), ((r[1] + r[3]) as u32).min(h));
        for y in y0..y1 {
            for x in x0..x1 {
                m[(y * w + x) as usize] = 255;
            }
        }
    }
    m
}
