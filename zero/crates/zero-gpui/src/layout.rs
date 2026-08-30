use gpui::{Bounds, Pixels, Point, point, size};

/// Tiles `count` cards into `area`: a near-square grid whose last row stretches; three cards are a main column and a stack.
pub fn tiles(area: Bounds<Pixels>, count: usize, gap: Pixels, main: f32) -> Vec<Bounds<Pixels>> {
    if count == 0 {
        return Vec::new();
    }
    if count == 3 {
        return main_and_stack(area, gap, main);
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

/// Three tiles: the first takes a full-height column, the other two stack beside it.
fn main_and_stack(area: Bounds<Pixels>, gap: Pixels, main: f32) -> Vec<Bounds<Pixels>> {
    let main_width = (area.size.width - gap) * main;
    let stack_width = area.size.width - gap - main_width;
    let height = (area.size.height - gap) / 2.;
    let right = area.origin.x + main_width + gap;
    vec![
        Bounds { origin: area.origin, size: size(main_width, area.size.height) },
        Bounds { origin: point(right, area.origin.y), size: size(stack_width, height) },
        Bounds { origin: point(right, area.origin.y + height + gap), size: size(stack_width, height) },
    ]
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
        let rects = tiles(area(point(px(0.), px(0.)), size(px(900.), px(600.))), 5, px(10.), 0.5);
        assert_eq!(rects.len(), 5);
        assert_eq!(rects[0].size.width, rects[1].size.width);
        assert!(rects[3].size.width > rects[0].size.width);
        assert_eq!(rects[3].origin.y, rects[4].origin.y);
        assert!(rects[3].origin.y > rects[0].origin.y);
    }

    #[test]
    fn neighbor_walks_the_grid() {
        let rects = tiles(area(point(px(0.), px(0.)), size(px(900.), px(600.))), 4, px(10.), 0.5);
        assert_eq!(neighbor(&rects, 0, Direction::Right), Some(1));
        assert_eq!(neighbor(&rects, 0, Direction::Down), Some(2));
        assert_eq!(neighbor(&rects, 0, Direction::Left), None);
        assert_eq!(neighbor(&rects, 3, Direction::Up), Some(1));
    }

    #[test]
    fn three_tiles_are_a_main_column_and_a_stack() {
        let area = Bounds { origin: point(px(10.), px(20.)), size: size(px(1000.), px(600.)) };
        let tiles = tiles(area, 3, px(8.), 0.5);
        assert_eq!(tiles[0], Bounds { origin: point(px(10.), px(20.)), size: size(px(496.), px(600.)) });
        assert_eq!(tiles[1], Bounds { origin: point(px(514.), px(20.)), size: size(px(496.), px(296.)) });
        assert_eq!(tiles[2], Bounds { origin: point(px(514.), px(324.)), size: size(px(496.), px(296.)) });
    }
}

#[cfg(test)]
mod ratio_tests {
    use super::*;
    use gpui::px;

    #[test]
    fn the_main_ratio_moves_the_column_split() {
        let area = Bounds { origin: point(px(0.), px(0.)), size: size(px(1008.), px(600.)) };
        let tiles = tiles(area, 3, px(8.), 0.6);
        assert_eq!(tiles[0].size.width, px(600.));
        assert_eq!(tiles[1].size.width, px(400.));
        assert_eq!(tiles[1].origin.x, px(608.));
    }
}
