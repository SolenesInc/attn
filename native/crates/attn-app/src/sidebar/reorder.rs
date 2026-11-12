use attn_protocol::DesktopReorderMessage;

use super::model::Line;
use super::{ROW_HEIGHT, request_id};
use crate::State;

/// Points the pointer travels before a press on a desktop becomes a drag, as in the Tauri app.
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

/// The seam nearest `y` points down the list: one above each desktop, and one under the list.
fn nearest(lines: &[Line], y: f32) -> usize {
    let tops = headers(lines)
        .into_iter()
        .map(|(i, _)| i as f32 * ROW_HEIGHT)
        .chain([lines.len() as f32 * ROW_HEIGHT]);
    tops.enumerate()
        .min_by(|(_, a), (_, b)| (a - y).abs().total_cmp(&(b - y).abs()))
        .map_or(0, |(seam, _)| seam)
}

/// The seam line `index` shows while a desktop is dragged, if any.
pub fn seam_on(state: &State, lines: &[Line], index: usize) -> Option<Seam> {
    let seam = state.window.reorder.as_ref()?.seam;
    let headers = headers(lines);
    match headers.get(seam) {
        Some((line, _)) if *line == index => Some(Seam::Above),
        None if index + 1 == lines.len() => Some(Seam::Below),
        _ => None,
    }
}

/// The drag on desktop `desktop`'s row: past the threshold it follows the pointer from seam
/// to seam, and the release moves the desktop there.
pub(crate) fn drag(state: &mut State, desktop: &str, d: &rune::Drag) {
    let home = state.window.view == crate::shell::View::Home;
    let lines = super::model::desktop_lines(&state.facts, home, &[]);
    let Some(index) = headers(&lines)
        .into_iter()
        .find_map(|(line, id)| (id == desktop).then_some(line))
    else {
        return;
    };
    match d.phase {
        rune::Phase::Down => state.window.reorder = None,
        rune::Phase::Move => {
            if state.window.reorder.is_none() && d.dx.hypot(d.dy) < THRESHOLD {
                return;
            }
            let y = index as f32 * ROW_HEIGHT + d.start_y + d.dy;
            state.window.reorder = Some(Reorder {
                desktop: desktop.to_owned(),
                seam: nearest(&lines, y),
            });
        }
        rune::Phase::Up => {
            let Some(reorder) = state.window.reorder.take() else {
                return;
            };
            if d.cancelled() {
                return;
            }
            let order: Vec<&str> = headers(&lines).into_iter().map(|(_, id)| id).collect();
            if let Some((previous, next)) = placement(&order, &reorder.desktop, reorder.seam) {
                let _ = state.daemon.send(&DesktopReorderMessage {
                    request_id: request_id("desktop_reorder"),
                    desktop_id: reorder.desktop,
                    previous_desktop_id: previous,
                    next_desktop_id: next,
                    ..DesktopReorderMessage::new()
                });
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
