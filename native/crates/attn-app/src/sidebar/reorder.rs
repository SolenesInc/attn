use attn_ui::system::ListRow;

use rune::Tx;

use super::line_height;
use super::model::{Line, ordered_desktops};
use crate::{Action, State};

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

/// Whether a desktop sits in the vertical desktop stack (folded empty desktops sit in the strip
/// at the foot of the list instead).
pub(super) fn stacked(desktop: &super::model::DesktopHeader) -> bool {
    !desktop.empty || desktop.current
}

/// The line index of each vertically stacked desktop, in order.
fn headers(lines: &[Line]) -> Vec<(usize, &str)> {
    lines
        .iter()
        .enumerate()
        .filter_map(|(i, line)| match line {
            Line::Desktop(d) if stacked(d) => Some((i, d.id.as_str())),
            _ => None,
        })
        .collect()
}

/// Vertical layout of each stacked desktop group in `lines`, matching `ListRow::group` and
/// `ListRow::well`.
fn spans(lines: &[Line]) -> Vec<Span<'_>> {
    let mut out = Vec::new();
    let mut y = 0.0;
    let mut i = 0;
    while i < lines.len() {
        let Line::Desktop(desktop) = &lines[i] else {
            i += 1;
            continue;
        };
        let mut next = i + 1;
        while next < lines.len() && !matches!(lines[next], Line::Desktop(_)) {
            next += 1;
        }
        if !stacked(desktop) {
            i = next;
            continue;
        }
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
        for line in &lines[i + 1..next] {
            cursor += line_height(line);
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
        None => {
            let &(start, _) = headers.last()?;
            let mut end = start + 1;
            while end < lines.len() && !matches!(lines[end], Line::Desktop(_)) {
                end += 1;
            }
            Some((end - 1, Seam::Below))
        }
    }
}

/// The drag on desktop `desktop`'s row: past the threshold it follows the pointer from seam
/// to seam, and the release moves the desktop there.
pub(crate) fn drag(state: &mut State, desktop: &str, d: &rune::Drag, tx: &mut Tx) {
    let lines = super::lines(state);
    let layout = spans(&lines);
    let Some(from) = layout.iter().position(|span| span.id == desktop) else {
        return;
    };
    match d.phase {
        rune::Phase::Down => state.window.reorder = None,
        rune::Phase::Move => {
            if state.window.reorder.is_none() && d.dx.hypot(d.dy) < THRESHOLD {
                return;
            }
            let y = layout[from].header_top + d.start_y + d.dy;
            state.window.reorder = Some(Reorder {
                desktop: desktop.to_owned(),
                seam: nearest(&layout, y),
            });
        }
        rune::Phase::Up => {
            let Some(reorder) = state.window.reorder.take() else {
                return;
            };
            let seam = reorder.seam;
            if d.cancelled() || seam == from || seam == from + 1 {
                return;
            }
            let (anchor, after) = match layout.get(seam) {
                Some(next) => (next.id, false),
                None => (
                    layout.last().expect("the dragged desktop has a span").id,
                    true,
                ),
            };
            let profile = state.facts.profiles.selected.clone();
            let mut desktops: Vec<String> = ordered_desktops(&state.facts.desktops)
                .into_iter()
                .filter(|d| d.profile_id == profile && d.id != reorder.desktop)
                .map(|d| d.id.clone())
                .collect();
            let Some(at) = desktops.iter().position(|id| id == anchor) else {
                return;
            };
            desktops.insert(at + usize::from(after), reorder.desktop);
            crate::desktop::run(state, &Action::SetDesktopOrder { profile, desktops }, tx);
        }
    }
}
