use attn_protocol::DesktopReorderMessage;
use attn_ui::system::ListRow;

use rune::Tx;

use super::line_height;
use super::model::Line;
use crate::State;
use crate::requests::Refused;

/// Points the pointer travels before a press on a desktop becomes a drag.
const THRESHOLD: f32 = 6.0;

/// A desktop being dragged to a new place: the seam it would land at, counting the seam
/// above each desktop and the one under the last line.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Reorder {
    pub desktop: String,
    pub seam: usize,
}

/// Where a seam shows on a line of the sidebar.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Seam {
    Above,
    Below,
}

struct Span<'a> {
    id: &'a str,
    group_top: f32,
    header_top: f32,
    bottom: f32,
}

/// The line index of each desktop, in order.
fn headers(lines: &[Line]) -> Vec<(usize, &str)> {
    lines
        .iter()
        .enumerate()
        .filter_map(|(i, line)| match line {
            Line::Desktop(d) => Some((i, d.id.as_str())),
            _ => None,
        })
        .collect()
}

/// Vertical layout of each desktop group in `lines`, matching `ListRow::group` and `ListRow::well`.
fn spans(lines: &[Line]) -> Vec<Span<'_>> {
    let mut out = Vec::new();
    let mut y = 0.0;
    let mut i = 0;
    while i < lines.len() {
        let Line::Desktop(desktop) = &lines[i] else {
            i += 1;
            continue;
        };
        if !out.is_empty() {
            y += ListRow::group_gap();
        }
        let group_top = y;
        let pad = if desktop.current {
            ListRow::well_pad_y()
        } else {
            0.0
        };
        let header_top = group_top + pad;
        let mut cursor = header_top + line_height(&lines[i]);
        let mut next = i + 1;
        while next < lines.len() && !matches!(lines[next], Line::Desktop(_)) {
            cursor += line_height(&lines[next]);
            next += 1;
        }
        let bottom = cursor + pad;
        out.push(Span {
            id: &desktop.id,
            group_top,
            header_top,
            bottom,
        });
        y = bottom;
        i = next;
    }
    out
}

/// The seam nearest `y` points down the list: one above each desktop, and one under the list.
fn nearest(spans: &[Span<'_>], y: f32) -> usize {
    let end = spans.last().map_or(0.0, |s| s.bottom);
    spans
        .iter()
        .map(|s| s.group_top)
        .chain([end])
        .enumerate()
        .min_by(|(_, a), (_, b)| (a - y).abs().total_cmp(&(b - y).abs()))
        .map_or(0, |(seam, _)| seam)
}

/// The single line index and edge where the drag seam shows while a desktop is dragged.
pub fn seam_target(state: &State, lines: &[Line]) -> Option<(usize, Seam)> {
    let seam = state.window.reorder.as_ref()?.seam;
    let headers = headers(lines);
    match headers.get(seam) {
        Some((line, _)) => Some((*line, Seam::Above)),
        None => lines.len().checked_sub(1).map(|last| (last, Seam::Below)),
    }
}

/// The drag on desktop `desktop`'s row: past the threshold it follows the pointer from seam
/// to seam, and the release moves the desktop there.
pub(crate) fn drag(state: &mut State, desktop: &str, d: &rune::Drag, tx: &mut Tx) {
    let home = state.window.view == crate::shell::View::Home;
    let lines = super::model::desktop_lines(&state.facts, Some(&state.window.garden), home, &[]);
    let layout = spans(&lines);
    let Some(span) = layout.iter().find(|s| s.id == desktop) else {
        return;
    };
    match d.phase {
        rune::Phase::Down => state.window.reorder = None,
        rune::Phase::Move => {
            if state.window.reorder.is_none() && d.dx.hypot(d.dy) < THRESHOLD {
                return;
            }
            let y = span.header_top + d.start_y + d.dy;
            state.window.reorder = Some(Reorder {
                desktop: desktop.to_owned(),
                seam: nearest(&layout, y),
            });
        }
        rune::Phase::Up => {
            let Some(reorder) = state.window.reorder.take() else {
                return;
            };
            if d.cancelled() {
                return;
            }
            let order: Vec<&str> = layout.iter().map(|s| s.id).collect();
            if let Some((previous, next)) = placement(&order, &reorder.desktop, reorder.seam) {
                state.act(
                    |request_id| DesktopReorderMessage {
                        request_id,
                        desktop_id: reorder.desktop,
                        previous_desktop_id: previous,
                        next_desktop_id: next,
                        ..DesktopReorderMessage::new()
                    },
                    Refused::Bare,
                    |_, _, _| {},
                    tx,
                );
            }
        }
    }
}

/// The desktops either side of where `desktop` lands at `seam`; none when it stays put.
fn placement(
    order: &[&str],
    desktop: &str,
    seam: usize,
) -> Option<(Option<String>, Option<String>)> {
    let from = order.iter().position(|id| *id == desktop)?;
    if seam == from || seam == from + 1 {
        return None;
    }
    let rest: Vec<&str> = order.iter().copied().filter(|id| *id != desktop).collect();
    let at = if seam > from { seam - 1 } else { seam };
    let previous = at
        .checked_sub(1)
        .and_then(|i| rest.get(i))
        .map(|id| (*id).to_owned());
    let next = rest.get(at).map(|id| (*id).to_owned());
    Some((previous, next))
}

#[cfg(test)]
mod tests {
    use super::placement;

    #[test]
    fn a_desktop_lands_between_the_desktops_either_side_of_its_seam_and_stays_put_beside_itself() {
        let order = ["a", "b", "c"];
        let some =
            |p: Option<&str>, n: Option<&str>| Some((p.map(str::to_owned), n.map(str::to_owned)));
        assert_eq!(placement(&order, "b", 1), None);
        assert_eq!(placement(&order, "b", 2), None);
        assert_eq!(placement(&order, "b", 0), some(None, Some("a")));
        assert_eq!(placement(&order, "b", 3), some(Some("c"), None));
        assert_eq!(placement(&order, "a", 2), some(Some("b"), Some("c")));
        assert_eq!(placement(&order, "c", 1), some(Some("a"), Some("b")));
    }
}
