use attn_term::links::{self, Line, Open, Target};
use attn_term::{Metrics, Overlay, OverlayKind, Theme};
use rune::{Mods, Tx};

use super::{Term, Terminals};

/// The underline's opacity over the text's color, as the Tauri terminal draws it.
const UNDERLINE_ALPHA: u8 = 204;

/// ⌘ or Ctrl, which turns a click on a link in a terminal into opening it, as the Tauri
/// terminal reads `metaKey || ctrlKey`.
pub(crate) fn accelerator(mods: Mods) -> bool {
    mods.logo || mods.ctrl
}

/// Where a terminal's session runs: its directory, which relative paths resolve against, and
/// whether it runs on an outpost, whose files this machine cannot see.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(super) struct Place {
    pub cwd: Option<String>,
    pub outpost: bool,
}

/// Where the pointer rests, the cell under it as a screen row and column, and the link over
/// it. A stale hover reads the link again where the pointer rests before it next draws.
pub(super) struct Hover {
    at: (f32, f32),
    cell: (u32, u16),
    link: Option<Found>,
    stale: bool,
}

struct Found {
    target: Target,
    /// The screen rows the link covers, each with its first column and the one after its last.
    rows: Vec<(u32, u16, u16)>,
}

impl Terminals {
    /// The pointer moved to `at` over terminal `id`, or left it: the link under it is
    /// underlined, and with ⌘ or Ctrl held the slot shows the pointing hand over it. True when
    /// the cursor changed, which the view draws.
    pub(super) fn hover(&mut self, id: &str, at: Option<(f32, f32)>, accelerator: bool) -> bool {
        let metrics = self.metrics();
        let home = home();
        let over_link = match self.panes.get_mut(id) {
            Some(term) if term.view.selecting.is_none() => {
                let at = at.and_then(|at| Some((at, screen_cell(term, &metrics, at)?)));
                let unchanged = term
                    .view
                    .hover
                    .as_ref()
                    .map(|h| (!h.stale).then_some(h.cell))
                    == Some(at.map(|(_, cell)| cell));
                if !unchanged {
                    let hover = at.map(|(at, cell)| Hover {
                        at,
                        cell,
                        link: find(term, cell, home.as_deref()),
                        stale: false,
                    });
                    let had = term.view.hover.as_ref().is_some_and(|h| h.link.is_some());
                    let has = hover.as_ref().is_some_and(|h| h.link.is_some());
                    term.view.hover = hover;
                    self.undrawn |= had || has;
                }
                term.view.hover.as_ref().is_some_and(|h| h.link.is_some())
            }
            _ => false,
        };
        let pointing = (over_link && accelerator).then(|| id.to_owned());
        self.pointing_link_in(pointing)
    }

    /// ⌘ or Ctrl went down or up: the pointing hand follows it over a hovered link.
    pub(crate) fn accelerator(&mut self, held: bool) -> bool {
        let hovered = self
            .panes
            .iter()
            .find(|(_, t)| t.view.hover.as_ref().is_some_and(|h| h.link.is_some()))
            .map(|(id, _)| id.clone());
        self.pointing_link_in(hovered.filter(|_| held))
    }

    fn pointing_link_in(&mut self, id: Option<String>) -> bool {
        if self.pointing == id {
            return false;
        }
        self.pointing = id;
        true
    }

    /// The terminal whose slot shows the pointing hand over a link.
    pub(crate) fn pointing_link(&self) -> Option<&str> {
        self.pointing.as_deref()
    }

    /// A ⌘- or Ctrl-click at `at` in terminal `id` ended: the link under it opens. True when
    /// one did.
    pub(crate) fn open_link_at(&mut self, id: &str, at: (f32, f32), tx: &mut Tx) -> bool {
        let Some(target) = self.link_at(id, at) else {
            return false;
        };
        open(&target, tx);
        true
    }

    /// A press at `at` in terminal `id` with ⌘ or Ctrl held lands on a link, which it opens
    /// instead of telling the program.
    pub(crate) fn presses_link(&mut self, id: &str, at: (f32, f32)) -> bool {
        self.link_at(id, at).is_some()
    }

    /// The link at `at` in terminal `id`, read afresh, as a press or release finds it.
    fn link_at(&mut self, id: &str, at: (f32, f32)) -> Option<Target> {
        let metrics = self.metrics();
        let home = home();
        let term = self.panes.get_mut(id)?;
        let cell = screen_cell(term, &metrics, at)?;
        find(term, cell, home.as_deref()).map(|f| f.target)
    }

    /// Output, a scroll or a resize moved the text under the pointer.
    pub(super) fn hover_moved(term: &mut Term) {
        if let Some(hover) = &mut term.view.hover {
            hover.stale = true;
        }
    }

    /// Reads each stale hover's link again where its pointer rests, as the Tauri terminal does
    /// before it draws.
    pub(super) fn refresh_hovers(&mut self) {
        let metrics = self.metrics();
        let home = home();
        for term in self.panes.values_mut() {
            let Some(at) = term.view.hover.as_ref().filter(|h| h.stale).map(|h| h.at) else {
                continue;
            };
            term.view.hover = screen_cell(term, &metrics, at).map(|cell| Hover {
                at,
                cell,
                link: find(term, cell, home.as_deref()),
                stale: false,
            });
        }
    }

    /// The underline under the hovered link of a terminal, in its view's rows.
    pub(super) fn link_overlays(term: &mut Term, theme: &Theme) -> Vec<Overlay> {
        let Some(found) = term.view.hover.as_ref().and_then(|h| h.link.as_ref()) else {
            return Vec::new();
        };
        let rows = u32::from(term.vt.size().1);
        let top = term.vt.view_top();
        found
            .rows
            .iter()
            .filter(|(row, ..)| (top..top + rows).contains(row))
            .map(|&(row, start, end)| Overlay {
                row: (row - top) as u16,
                cols: (start, end),
                color: theme.fg,
                alpha: UNDERLINE_ALPHA,
                kind: OverlayKind::Underline,
            })
            .collect()
    }
}

/// Opens `target` as the Tauri terminal's opener allows: a web, mail or phone link in its
/// handler, a path with the system's opener. A markdown path opens there too until the app
/// shows markdown itself.
fn open(target: &Target, tx: &mut Tx) {
    let _ = match target.open() {
        Open::Url(uri) if uri.starts_with("tel:") => tx.open_uri(&uri),
        Open::Url(uri) => tx.open_url(&uri),
        Open::Path(path) | Open::Markdown(path) => tx.open_uri(&links::file_url(&path)),
    };
}

fn home() -> Option<String> {
    std::env::var("HOME").ok().filter(|h| !h.is_empty())
}

fn screen_cell(term: &mut Term, metrics: &Metrics, at: (f32, f32)) -> Option<(u32, u16)> {
    let (slot, scale) = term.slot?;
    let size = term.vt.size();
    let (col, row) = metrics.cell_in(size, slot, scale, at)?;
    (col < size.0).then(|| (term.vt.view_top() + u32::from(row), col))
}

/// The link over screen cell `cell`, as the Tauri terminal's hover finds it: an OSC 8
/// hyperlink first, since its label may hold spaces, then a URL, then a path that exists.
fn find(term: &mut Term, (row, col): (u32, u16), home: Option<&str>) -> Option<Found> {
    let (cols, rows) = term.vt.size();
    let top = term.vt.view_top();
    let vt = &mut term.vt;
    let (first, last) = links::extent(row - top, u32::from(rows), |r| vt.row_continues(top + r));
    let line = Line::over(first, last, cols, |r| vt.link_row(top + r));
    let index = line.index(row - top, col)?;
    let found = |link: links::Link| Found {
        rows: line
            .rows(link.span)
            .into_iter()
            .map(|(r, start, end)| (top + r, start, end))
            .collect(),
        target: link.target,
    };
    if let Some(link) = links::hyperlink_at(&line, index).or_else(|| links::url_at(&line, index)) {
        return Some(found(link));
    }
    if term.place.outpost {
        return None;
    }
    let fragment = links::fragment_at(&line, index)?;
    let cwd = term.place.cwd.as_deref();
    links::path_candidates(&line, fragment)
        .into_iter()
        .find_map(|mut link| {
            let Target::Path { path, .. } = &mut link.target else {
                return None;
            };
            *path = links::resolve(path, cwd, home)?;
            std::path::Path::new(path).exists().then_some(link)
        })
        .map(found)
}
