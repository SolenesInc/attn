use std::collections::{HashMap, HashSet};

use attn_client::{Connection, Facts};
use attn_protocol::session_id;
use attn_protocol::{
    ResolveSessionsMessage, ResolveSessionsResultMessage, ResolvedSession, SessionLedgerEntry,
};
use attn_term::links::{Line, Span};
use attn_term::{Overlay, OverlayKind};
use attn_ui::colors::TerminalColors;
use rune::Tx;

use super::{Term, Terminals};
use crate::State;

/// The app's accent, which an id attn knows is washed and underlined in.
const WASH: u8 = 26;
const WASH_PREVIEWING: u8 = 46;
const LINE: u8 = 184;
const LINE_PREVIEWING: u8 = 255;

const ID_BODY: &[u8] = b"0123456789abcdefghjkmnpqrstvwxyz";
const ID_BODY_LEN: usize = 6;
const SEED: &[u8] = b"s-";
const SESSION: &[u8] = b"ag-";
const HEX: &[u8] = b"xxxxxxxx";
const UUID: &[u8] = b"xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx";

/// Far past the 1,711 short session ids a 220×70 screen holds. Past it, what the daemon was
/// asked is forgotten and an id still on screen is asked about again.
const ASKED_KEPT: usize = 20_000;

/// What an id on screen names.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Subject {
    Seed(String),
    Session(String),
}

/// An id under the pointer or a click: what it names, and where its row starts and how large it
/// is in the slot, as the bits of its x, y, width and height in points.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Mark {
    pub(crate) target: Subject,
    pub(crate) at: [u32; 4],
}

/// An id on screen: what it names, and the screen rows it covers with each row's first column
/// and the column after its last.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct Found {
    target: Subject,
    rows: Vec<(u32, u16, u16)>,
}

impl Found {
    fn row_at(&self, (row, col): (u32, u16)) -> Option<(u32, u16, u16)> {
        self.rows
            .iter()
            .copied()
            .find(|&(r, start, end)| r == row && (start..end).contains(&col))
    }
}

struct Keyed {
    id: String,
    live: bool,
}

/// The seeds and sessions of the selected profile that terminals mark, and what they ask the
/// daemon about the rest.
#[derive(Default)]
pub(super) struct Ids {
    profile: String,
    seeds: HashSet<String>,
    live: Vec<String>,
    closed: HashMap<String, SessionLedgerEntry>,
    by_key: HashMap<u32, Vec<Keyed>>,
    seen_closes: u64,
    unresolved: HashSet<String>,
    asked: HashSet<String>,
    asking: bool,
    previewing: Option<(String, Subject)>,
}

impl Ids {
    fn named(&self, word: &Word<'_>) -> Option<Subject> {
        match word {
            Word::Seed(id) => self
                .seeds
                .contains(*id)
                .then(|| Subject::Seed((*id).to_owned())),
            Word::Short(id) => self.session(id, |_| true),
            Word::Uuid(id) => self.session(id, |known| known == *id),
            Word::Hex(start) => self.session(start, |known| known.starts_with(start)),
        }
    }

    /// The session `reference` names: the one live session it fits, or else, when it fits no
    /// live one, the one closed session it fits.
    fn session(&self, reference: &str, fits: impl Fn(&str) -> bool) -> Option<Subject> {
        let known = self.by_key.get(&session_id::key_of(reference)?)?;
        let fitting = |live: bool| known.iter().filter(move |k| k.live == live);
        let mut live = fitting(true).filter(|k| fits(&k.id)).peekable();
        let mut closed = fitting(false).filter(|k| fits(&k.id));
        let one = match live.peek() {
            Some(_) => live.next().filter(|_| live.next().is_none()),
            None => closed.next().filter(|_| closed.next().is_none()),
        };
        one.map(|k| Subject::Session(k.id.clone()))
    }

    fn index(&mut self) {
        self.by_key.clear();
        let live = self.live.iter().map(|id| (id, true));
        let closed = self.closed.keys().filter(|id| !self.live.contains(id));
        for (id, live) in live.chain(closed.map(|id| (id, false))) {
            let Some(key) = session_id::key_of(id) else {
                continue;
            };
            let id = id.clone();
            self.by_key.entry(key).or_default().push(Keyed { id, live });
        }
    }

    /// Takes in the live sessions of the selected profile and the ones that closed. Returns
    /// whether that changed what a terminal marks.
    fn follow(&mut self, facts: &Facts) -> bool {
        let switched = self.profile != facts.profiles.selected;
        if switched {
            *self = Ids {
                profile: facts.profiles.selected.clone(),
                seeds: std::mem::take(&mut self.seeds),
                asking: self.asking,
                ..Ids::default()
            };
        }
        if facts.connection != Connection::Connected {
            self.asked.clear();
        }
        let closes = &facts.closes;
        let mut changed = switched;
        for entry in closes.since(self.seen_closes).unwrap_or(&closes.recent) {
            changed |= self.take_closed(entry.clone());
        }
        self.seen_closes = closes.seq;
        let of_profile = |s: &&attn_protocol::Session| s.profile_id == facts.profiles.selected;
        let live = || facts.sessions.iter().filter(of_profile).map(|s| &s.id);
        if !live().eq(self.live.iter()) {
            let before = std::mem::replace(&mut self.live, live().cloned().collect());
            let live = &self.live;
            self.closed
                .retain(|id, _| before.contains(id) || !live.contains(id));
            changed = true;
        }
        if changed {
            self.index();
        }
        changed
    }

    fn take_closed(&mut self, entry: SessionLedgerEntry) -> bool {
        let here = entry.profile_id == self.profile;
        if here {
            self.closed.insert(entry.id.clone(), entry);
        }
        here
    }

    fn learn(&mut self, refs: Vec<String>, found: Vec<ResolvedSession>) {
        if self.asked.len() + refs.len() > ASKED_KEPT {
            self.asked.clear();
        }
        for reference in refs {
            self.unresolved.remove(&reference);
            self.asked.insert(reference);
        }
        for session in found {
            self.take_closed(session.closed);
        }
        self.index();
    }
}

impl Terminals {
    /// The garden's seed ids changed: each terminal marks the ones it shows again.
    pub(crate) fn know_seeds(&mut self, known: HashSet<String>) {
        if self.ids.seeds == known {
            return;
        }
        self.ids.seeds = known;
        self.mark_again();
    }

    /// The sessions attn knows changed: each terminal marks the ones it shows again.
    pub(crate) fn know_sessions(&mut self, facts: &Facts) {
        if self.ids.follow(facts) {
            self.mark_again();
        }
    }

    fn mark_again(&mut self) {
        for term in self.panes.values_mut() {
            term.view.ids = None;
        }
        self.undrawn = true;
    }

    pub(crate) fn knows(&self, target: &Subject) -> bool {
        match target {
            Subject::Seed(id) => self.ids.seeds.contains(id),
            Subject::Session(id) => self.ids.live.contains(id) || self.ids.closed.contains_key(id),
        }
    }

    /// The closed session `id`, as the ledger holds it.
    pub(crate) fn closed_session(&self, id: &str) -> Option<&SessionLedgerEntry> {
        let live = self.ids.live.iter().any(|known| known == id);
        self.ids.closed.get(id).filter(|_| !live)
    }

    pub(crate) fn draws(&self, runtime: &str) -> bool {
        self.panes.contains_key(runtime)
    }

    /// Terminal `runtime` shows the card of `target`, so it brightens the ids that name it.
    pub(crate) fn preview(&mut self, card: Option<(&str, &Subject)>) {
        let previewing = self.ids.previewing.as_ref().map(|(r, t)| (r.as_str(), t));
        if previewing != card {
            self.ids.previewing = card.map(|(r, t)| (r.to_owned(), t.clone()));
            self.undrawn = true;
        }
    }

    /// The id under the pointer resting on terminal `runtime`.
    pub(crate) fn mark_under_pointer(&mut self, runtime: &str) -> Option<Mark> {
        self.current(runtime)?;
        let cell = self.panes.get(runtime)?.view.hover.as_ref()?.cell;
        self.mark_in_cell(runtime, cell)
    }

    pub(super) fn points_at_mark(&mut self, runtime: &str) -> bool {
        self.mark_under_pointer(runtime).is_some()
    }

    /// A press at `at` in terminal `runtime` with ⌘ or Ctrl held lands on a link or an id, which
    /// it opens instead of telling the program.
    pub(crate) fn presses_mark(&mut self, runtime: &str, at: (f32, f32)) -> bool {
        self.presses_link(runtime, at) || self.mark_at(runtime, at).is_some()
    }

    /// The id at `at` in terminal `runtime` that no link covers, as a click with ⌘ or
    /// Ctrl finds it.
    pub(crate) fn mark_at(&mut self, runtime: &str, at: (f32, f32)) -> Option<Mark> {
        if self.presses_link(runtime, at) {
            return None;
        }
        self.current(runtime)?;
        let metrics = self.metrics();
        let cell = super::links::screen_cell(self.panes.get_mut(runtime)?, &metrics, at)?;
        self.mark_in_cell(runtime, cell)
    }

    fn mark_in_cell(&mut self, runtime: &str, cell: (u32, u16)) -> Option<Mark> {
        let metrics = self.metrics();
        let term = self.panes.get_mut(runtime)?;
        let (slot, scale) = term.slot?;
        let (top, size) = (term.vt.view_top(), term.vt.size());
        let (found, (row, start, end)) = marks(term, &mut self.ids)
            .iter()
            .find_map(|found| Some((found, found.row_at(cell)?)))?;
        let row = row.checked_sub(top)? as u16;
        let [x, y, w, h] = metrics.cell_rect(size, slot, scale, (start, row));
        let wide = w * f32::from(end - start);
        Some(Mark {
            target: found.target.clone(),
            at: [x, y, wide, h].map(f32::to_bits),
        })
    }

    pub(crate) fn has_unresolved_ids(&self) -> bool {
        !self.ids.unresolved.is_empty() && !self.ids.asking
    }

    /// Washes and underlines the ids on terminal `term`'s view that attn knows, brighter on the
    /// ones that name what the open card shows.
    pub(super) fn id_overlays(
        id: &str,
        term: &mut Term,
        ids: &mut Ids,
        theme: &TerminalColors,
    ) -> Vec<Overlay> {
        let rows = u32::from(term.vt.size().1);
        let top = term.vt.view_top();
        let mut overlays = Vec::new();
        marks(term, ids);
        let previewing = ids.previewing.as_ref().filter(|(r, _)| r == id);
        for found in term.view.ids.iter().flatten() {
            let bright = previewing.is_some_and(|(_, target)| *target == found.target);
            for &(row, start, end) in &found.rows {
                if !(top..top + rows).contains(&row) {
                    continue;
                }
                let row = (row - top) as u16;
                overlays.push(Overlay {
                    row,
                    cols: (start, end),
                    color: theme.seed,
                    alpha: if bright { WASH_PREVIEWING } else { WASH },
                    kind: OverlayKind::Wash,
                });
                overlays.push(Overlay {
                    row,
                    cols: (start, end),
                    color: theme.seed,
                    alpha: if bright { LINE_PREVIEWING } else { LINE },
                    kind: OverlayKind::Underline,
                });
            }
        }
        overlays
    }
}

/// Asks the daemon, in one request at a time, about the session ids on screen that name no
/// session the app holds. What it does not know it is not asked again, and a request it never
/// answered leaves its ids to be asked about once the text changes.
pub(crate) fn ask_about_unresolved(state: &mut State, tx: &mut Tx) {
    if !state.terminals.has_unresolved_ids() {
        return;
    }
    let ids = &mut state.terminals.ids;
    ids.asking = true;
    let mut refs: Vec<String> = std::mem::take(&mut ids.unresolved).into_iter().collect();
    refs.sort();
    let profile = ids.profile.clone();
    let asked = refs.clone();
    state.ask(
        |request_id| ResolveSessionsMessage {
            request_id: Some(request_id),
            refs,
            ..ResolveSessionsMessage::new()
        },
        move |state, answer, tx| {
            let terminals = &mut state.terminals;
            terminals.ids.asking = false;
            let reply = answer
                .parse::<ResolveSessionsResultMessage>()
                .filter(|r| r.success && terminals.ids.profile == profile);
            if let Some(reply) = reply {
                let learns = !reply.found.is_empty();
                terminals.ids.learn(asked, reply.found);
                if learns {
                    terminals.mark_again();
                }
            }
            ask_about_unresolved(state, tx);
        },
        tx,
    );
}

/// The known ids on the rows terminal `term` shows, found once per screen of text.
fn marks<'a>(term: &'a mut Term, ids: &mut Ids) -> &'a [Found] {
    if term.view.ids.is_none() {
        term.view.ids = Some(find(term, ids));
    }
    term.view.ids.as_deref().unwrap_or_default()
}

/// A cell's first and last characters, none for an empty cell.
type Ends = Option<(char, char)>;

/// A cell holding more than one character: whether the first and the last belong to a word.
const CLUSTER_STARTS_AS_WORD: u8 = 1;
const CLUSTER_ENDS_AS_WORD: u8 = 2;

/// A cell as the scan reads it: its character when it holds one ASCII character, and otherwise
/// what it is to a word beside it.
fn scanned(ends: Ends) -> u8 {
    let is_word = |c: char| c.is_ascii_alphanumeric() || c == '_';
    match ends {
        Some((first, last)) if first == last && first.is_ascii_graphic() => first as u8,
        Some((first, last)) => {
            let starts = u8::from(is_word(first)) * CLUSTER_STARTS_AS_WORD;
            starts | (u8::from(is_word(last)) * CLUSTER_ENDS_AS_WORD)
        }
        None => 0,
    }
}

fn in_word(cell: u8, cluster: u8) -> bool {
    match cell {
        0..=3 => cell & cluster != 0,
        cell => cell.is_ascii_alphanumeric() || cell == b'_',
    }
}

/// Reads the rows the view draws from the grid the renderer took, and the row past either
/// edge of the view from the screen only when a line wraps across that edge.
fn find(term: &mut Term, ids: &mut Ids) -> Vec<Found> {
    let grid = &term.grid;
    let vt = &mut term.vt;
    let (cols, rows) = (grid.cols, u32::from(grid.rows));
    if cols == 0 || rows == 0 {
        return Vec::new();
    }
    let top = vt.view_top();
    let mut outside: Vec<(u32, Vec<Ends>)> = Vec::new();
    let mut first = top;
    if top > 0 && vt.row_continues(top) {
        first = top - 1;
        outside.push((first, screen_row(vt, first)));
    }
    let mut last = top + rows - 1;
    if vt.scrolled_back() > 0 && vt.row_continues(last + 1) {
        last += 1;
        outside.push((last, screen_row(vt, last)));
    }
    let width = usize::from(cols);
    let mut found = Vec::new();
    let mut cells: Vec<u8> = Vec::new();
    let mut start = first;
    while start <= last {
        let mut end = start;
        while end < last && vt.row_continues(end + 1) {
            end += 1;
        }
        cells.clear();
        for row in start..=end {
            match row.checked_sub(top).filter(|r| *r < rows) {
                Some(r) => cells.extend(
                    grid.row(r as usize)
                        .iter()
                        .map(|cell| scanned(grid.ends(cell.grapheme))),
                ),
                None => cells.extend(
                    outside
                        .iter()
                        .filter(|(r, _)| *r == row)
                        .flat_map(|(_, cells)| cells.iter().copied().map(scanned)),
                ),
            }
            cells.resize((row - start + 1) as usize * width, 0);
        }
        let line = Line {
            first_row: start,
            row_count: end - start + 1,
            cols,
            ..Line::default()
        };
        words(&cells, |span, word| match ids.named(&word) {
            Some(target) => found.push(Found {
                target,
                rows: line.rows(span),
            }),
            None => {
                if let Word::Short(id) | Word::Uuid(id) = word
                    && !ids.asked.contains(id)
                {
                    ids.unresolved.insert(id.to_owned());
                }
            }
        });
        start = end + 1;
    }
    found
}

fn screen_row(vt: &mut attn_term::Vt, row: u32) -> Vec<Ends> {
    vt.row_cells(row)
        .iter()
        .map(|t| Some((t.chars().next()?, t.chars().next_back()?)))
        .collect()
}

/// A word shaped like an id.
enum Word<'a> {
    Seed(&'a str),
    Short(&'a str),
    Uuid(&'a str),
    Hex(&'a str),
}

/// Hands `each` the words of a line's `cells` shaped like ids: no letter, digit or underscore
/// right before or after.
fn words(cells: &[u8], mut each: impl FnMut(Span, Word<'_>)) {
    let mut i = 0;
    let mut inside = false;
    while i < cells.len() {
        let word = (!inside)
            .then(|| word_at(&cells[i..]))
            .flatten()
            .filter(|(len, _)| {
                cells
                    .get(i + len)
                    .is_none_or(|after| !in_word(*after, CLUSTER_STARTS_AS_WORD))
            });
        let len = match word {
            Some((len, word)) => {
                let (start, end) = (i, i + len);
                each(Span { start, end }, word);
                len
            }
            None => 1,
        };
        i += len;
        inside = in_word(cells[i - 1], CLUSTER_ENDS_AS_WORD);
    }
}

fn word_at(cells: &[u8]) -> Option<(usize, Word<'_>)> {
    let body = |prefix: &[u8]| {
        let len = prefix.len() + ID_BODY_LEN;
        let id = cells.get(..len)?;
        let body = id.strip_prefix(prefix)?;
        let whole = body.iter().all(|c| ID_BODY.contains(c));
        whole.then(|| std::str::from_utf8(id).ok()).flatten()
    };
    let hex = |shape: &[u8]| {
        let id = cells.get(..shape.len())?;
        let fits = id.iter().zip(shape).all(|(c, shape)| match shape {
            b'-' => *c == b'-',
            _ => matches!(c, b'0'..=b'9' | b'a'..=b'f'),
        });
        fits.then(|| std::str::from_utf8(id).ok()).flatten()
    };
    let word = match cells.first()? {
        b's' => Word::Seed(body(SEED)?),
        b'0'..=b'9' | b'a'..=b'f' => match (body(SESSION), hex(UUID), hex(HEX)) {
            (Some(id), ..) => Word::Short(id),
            (_, Some(id), _) => Word::Uuid(id),
            (.., Some(id)) => Word::Hex(id),
            _ => return None,
        },
        _ => return None,
    };
    let (Word::Seed(id) | Word::Short(id) | Word::Uuid(id) | Word::Hex(id)) = &word;
    Some((id.len(), word))
}
