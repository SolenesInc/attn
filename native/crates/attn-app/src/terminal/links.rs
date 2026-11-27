use attn_term::links::{self, Line, Open, Target};
use attn_term::{Metrics, Overlay, OverlayKind};
use attn_ui::colors::TerminalColors;
use rune::{Mods, Tx};

use super::{Term, Terminals};

/// The underline's opacity over the text's color.
const UNDERLINE_ALPHA: u8 = 204;

/// ⌘ or Ctrl, which turns a click on a link in a terminal into opening it.
pub(crate) fn accelerator(mods: Mods) -> bool {
    mods.logo || mods.ctrl
}

/// Where a terminal's session runs: its directory, which relative paths resolve against, and
/// the outpost it runs on, whose files this machine cannot see.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(super) struct Place {
    pub cwd: Option<String>,
    pub host: Option<String>,
    /// The machine the terminal runs on, when it is not this app's.
    pub away: Option<String>,
}

/// Where the pointer rests, the cell under it as a screen row and column, and the link over
/// it. A stale hover reads the link again where the pointer rests before it next draws.
pub(super) struct Hover {
    at: (f32, f32),
    pub(super) cell: (u32, u16),
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
    /// underlined, and with ⌘ or Ctrl held the slot shows the pointing hand over it or over a
    /// session id. True when the cursor changed, which the view draws.
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
        let points = accelerator && (over_link || self.points_at_mark(id));
        let pointing = points.then(|| id.to_owned());
        self.pointing_link_in(pointing)
    }

    /// ⌘ or Ctrl went down or up: the pointing hand follows it over a hovered link or id.
    pub(crate) fn accelerator(&mut self, held: bool) -> bool {
        let hovered: Vec<(String, bool)> = self
            .panes
            .iter()
            .filter(|_| held)
            .filter_map(|(id, t)| Some((id.clone(), t.view.hover.as_ref()?.link.is_some())))
            .collect();
        let pointing = hovered
            .into_iter()
            .find(|(id, link)| *link || self.points_at_mark(id))
            .map(|(id, _)| id);
        self.pointing_link_in(pointing)
    }

    fn pointing_link_in(&mut self, id: Option<String>) -> bool {
        if self.pointing == id {
            return false;
        }
        self.pointing = id;
        true
    }

    /// The terminal whose slot shows the pointing hand over a link or a session id.
    pub(crate) fn pointing_link(&self) -> Option<&str> {
        self.pointing.as_deref()
    }

    /// A ⌘- or Ctrl-click at `at` in terminal `id` ended: the link under it opens, except a
    /// markdown file, which comes back for the app to show. A file on another machine stays
    /// shut, and the refusal is what to tell the person.
    pub(crate) fn open_link_at(
        &mut self,
        id: &str,
        at: (f32, f32),
        tx: &mut Tx,
    ) -> Result<Option<String>, String> {
        let Some(target) = self.link_at(id, at) else {
            return Ok(None);
        };
        let away = self.panes.get(id).and_then(|t| t.place.away.as_deref());
        match (target.open(), away) {
            (Open::Path(path) | Open::Markdown(path), Some(machine)) => Err(format!(
                "{path} is on {machine}; opening files on another machine isn't supported yet"
            )),
            (Open::Markdown(path), None) => Ok(Some(path)),
            (opening, _) => {
                open(opening, tx);
                Ok(None)
            }
        }
    }

    pub(super) fn presses_link(&mut self, id: &str, at: (f32, f32)) -> bool {
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

    /// Output, a scroll or a resize moved the text: the hover reads the link under the pointer
    /// again, and the ids on screen are found again.
    pub(super) fn hover_moved(term: &mut Term) {
        if let Some(hover) = &mut term.view.hover {
            hover.stale = true;
        }
        term.view.ids = None;
    }

    /// Reads each stale hover's link again where its pointer rests, before the terminal draws.
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
    pub(super) fn link_overlays(term: &mut Term, theme: &TerminalColors) -> Vec<Overlay> {
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

/// Opens `target`: a web, mail or phone link in its handler, a path with the system's opener.
fn open(opening: Open, tx: &mut Tx) {
    let _ = match opening {
        Open::Url(uri) if uri.starts_with("tel:") => tx.open_uri(&uri),
        Open::Url(uri) => tx.open_url(&uri),
        Open::Path(path) | Open::Markdown(path) => tx.open_uri(&links::file_url(&path)),
    };
}

fn home() -> Option<String> {
    std::env::var("HOME").ok().filter(|h| !h.is_empty())
}

pub(super) fn screen_cell(
    term: &mut Term,
    metrics: &Metrics,
    at: (f32, f32),
) -> Option<(u32, u16)> {
    let (col, row) = super::cell_at(term, metrics, at)?;
    Some((term.vt.view_top() + u32::from(row), col))
}

/// The link over screen cell `cell`: an OSC 8 hyperlink first, since its label may hold spaces,
/// then a URL, then a path that exists.
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
    if term.place.away.is_some() {
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
