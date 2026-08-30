use gpui::{Bounds, Pixels, Point, point, size};

/// Tiles `count` cards into `area`: a near-square grid whose last row stretches to fill the width.
pub fn tiles(area: Bounds<Pixels>, count: usize, gap: Pixels) -> Vec<Bounds<Pixels>> {
    if count == 0 {
        return Vec::new();
    }
    let columns = (count as f32).sqrt().ceil() as usize;
    let rows = count.div_ceil(columns);
    let row_height = (area.size.height - gap * (rows as f32 - 1.)) / rows as f32;
    let mut out = Vec::with_capacity(count);
    for row in 0..rows {
        let first = row * columns;
        let in_row = (count - first).min(columns);
        let width = (area.size.width - gap * (in_row as f32 - 1.)) / in_row as f32;
        for column in 0..in_row {
            out.push(Bounds {
                origin: point(
                    area.origin.x + (width + gap) * column as f32,
                    area.origin.y + (row_height + gap) * row as f32,
                ),
                size: size(width, row_height),
            });
        }
    }
    out
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Direction {
    Left,
    Right,
    Up,
    Down,
}

/// The tile whose center is nearest in `direction` from `current`, judged along that axis first.
pub fn neighbor(rects: &[Bounds<Pixels>], current: usize, direction: Direction) -> Option<usize> {
    let from = center(&rects[current]);
    let mut best: Option<(f32, usize)> = None;
    for (index, rect) in rects.iter().enumerate() {
        if index == current {
            continue;
        }
        let to = center(rect);
        let (dx, dy) = (f32::from(to.x - from.x), f32::from(to.y - from.y));
        let (along, across) = match direction {
            Direction::Left => (-dx, dy.abs()),
            Direction::Right => (dx, dy.abs()),
            Direction::Up => (-dy, dx.abs()),
            Direction::Down => (dy, dx.abs()),
        };
        if along <= 1. {
            continue;
        }
        let score = along + across * 2.;
        if best.is_none_or(|(best_score, _)| score < best_score) {
            best = Some((score, index));
        }
    }
    best.map(|(_, index)| index)
}

fn center(rect: &Bounds<Pixels>) -> Point<Pixels> {
    point(
        rect.origin.x + rect.size.width / 2.,
        rect.origin.y + rect.size.height / 2.,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use gpui::{Size, px};

    fn area(origin: Point<Pixels>, dims: Size<Pixels>) -> Bounds<Pixels> {
        Bounds { origin, size: dims }
    }

    #[test]
    fn five_tiles_fill_two_rows_and_the_last_row_stretches() {
        let rects = tiles(area(point(px(0.), px(0.)), size(px(900.), px(600.))), 5, px(10.));
        assert_eq!(rects.len(), 5);
        assert_eq!(rects[0].size.width, rects[1].size.width);
        assert!(rects[3].size.width > rects[0].size.width);
        assert_eq!(rects[3].origin.y, rects[4].origin.y);
        assert!(rects[3].origin.y > rects[0].origin.y);
    }

    #[test]
    fn neighbor_walks_the_grid() {
        let rects = tiles(area(point(px(0.), px(0.)), size(px(900.), px(600.))), 4, px(10.));
        assert_eq!(neighbor(&rects, 0, Direction::Right), Some(1));
        assert_eq!(neighbor(&rects, 0, Direction::Down), Some(2));
        assert_eq!(neighbor(&rects, 0, Direction::Left), None);
        assert_eq!(neighbor(&rects, 3, Direction::Up), Some(1));
    }
}
