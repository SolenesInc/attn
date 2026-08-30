use ratatui::layout::Rect;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Direction {
    Left,
    Down,
    Up,
    Right,
}

pub fn tile_layout(area: Rect, count: usize) -> Vec<Rect> {
    if count == 0 || area.is_empty() {
        return Vec::new();
    }
    let mut columns = 1usize;
    while columns * columns < count {
        columns += 1;
    }
    let rows = count.div_ceil(columns);
    let mut panes = Vec::with_capacity(count);
    for row in 0..rows {
        let row_start = row * columns;
        let row_count = columns.min(count - row_start);
        let y0 = area.y + area.height * row as u16 / rows as u16;
        let y1 = area.y + area.height * (row + 1) as u16 / rows as u16;
        for column in 0..row_count {
            let x0 = area.x + area.width * column as u16 / row_count as u16;
            let x1 = area.x + area.width * (column + 1) as u16 / row_count as u16;
            panes.push(Rect::new(x0, y0, x1 - x0, y1 - y0));
        }
    }
    panes
}

pub fn neighbor(rects: &[Rect], current: usize, direction: Direction) -> Option<usize> {
    let here = rects.get(current)?;
    let hx = here.x as i32 * 2 + here.width as i32;
    let hy = here.y as i32 * 2 + here.height as i32;
    rects
        .iter()
        .enumerate()
        .filter(|(index, _)| *index != current)
        .filter_map(|(index, candidate)| {
            let cx = candidate.x as i32 * 2 + candidate.width as i32;
            let cy = candidate.y as i32 * 2 + candidate.height as i32;
            let primary = match direction {
                Direction::Left if cx < hx => hx - cx,
                Direction::Right if cx > hx => cx - hx,
                Direction::Up if cy < hy => hy - cy,
                Direction::Down if cy > hy => cy - hy,
                _ => return None,
            };
            let secondary = match direction {
                Direction::Left | Direction::Right => (cy - hy).abs(),
                Direction::Up | Direction::Down => (cx - hx).abs(),
            };
            let aligned = match direction {
                Direction::Left | Direction::Right => {
                    candidate.y < here.bottom() && candidate.bottom() > here.y
                }
                Direction::Up | Direction::Down => {
                    candidate.x < here.right() && candidate.right() > here.x
                }
            };
            Some(((!aligned, primary, secondary), index))
        })
        .min_by_key(|(distance, _)| *distance)
        .map(|(_, index)| index)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn layout_split_covers_the_area_without_overlap() {
        let area = Rect::new(0, 0, 81, 23);
        for count in 1..=12 {
            let panes = tile_layout(area, count);
            assert_eq!(panes.len(), count);
            for (index, pane) in panes.iter().enumerate() {
                assert!(area.contains((pane.x, pane.y).into()));
                assert!(pane.right() <= area.right());
                assert!(pane.bottom() <= area.bottom());
                for other in panes.iter().skip(index + 1) {
                    assert!(!pane.intersects(*other));
                }
            }
        }
    }

    #[test]
    fn neighbor_prefers_an_aligned_pane() {
        let panes = tile_layout(Rect::new(0, 0, 120, 40), 3);

        assert_eq!(neighbor(&panes, 0, Direction::Right), Some(1));
        assert_eq!(neighbor(&panes, 0, Direction::Down), Some(2));
    }
}
