//! Finds an agent's message, as the markdown it wrote, on the terminal rows its TUI drew it
//! on, as the Tauri terminal does (`terminalMessageAlign.ts`): words are paired between the
//! two, so a span of the markdown maps to cells and a drag over cells maps back to the markdown.
//! Markdown offsets count UTF-16 code units, as the daemon stores them; columns count cells.

use std::collections::{BTreeMap, HashMap, HashSet};

use crate::vt::LinkRow;

const CUT_CHARS: &str = "*_`~#|>┌─┬┐│├┼┤└┴┘━┃┏┓┗┛⏺✻❯⎿·•▪●○";
const ASSISTANT_MARKERS: [char; 2] = ['⏺', '•'];
const USER_MARKERS: [char; 2] = ['❯', '›'];
const RESYNC_WINDOW: usize = 4;
const SEED_LOOKAHEAD: usize = 8;

/// The share of a row's words that must pair with the message before the row counts as the
/// message's.
pub const CONFIDENT_ROW: f64 = 0.6;

#[derive(Clone, Debug, PartialEq, Eq)]
struct Token {
    norm: String,
    aliases: Vec<String>,
}

impl Token {
    fn keys(&self) -> impl Iterator<Item = &str> {
        std::iter::once(self.norm.as_str()).chain(self.aliases.iter().map(String::as_str))
    }

    fn matches(&self, other: &Token) -> bool {
        self.keys().any(|key| other.keys().any(|k| k == key))
    }
}

#[derive(Clone, Debug)]
struct SrcToken {
    token: Token,
    start: usize,
    end: usize,
}

#[derive(Clone, Debug)]
struct GridToken {
    token: Token,
    row: u32,
    col: u16,
    end_col: u16,
    assistant_lead: bool,
    explicit_user: bool,
}

fn normalize(word: &str) -> String {
    word.chars().filter(|c| !CUT_CHARS.contains(*c)).collect()
}

fn percent_decoded(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = value.get(i + 1..i + 3)?;
            if !hex.bytes().all(|b| b.is_ascii_hexdigit()) {
                return None;
            }
            out.push(u8::from_str_radix(hex, 16).ok()?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).ok()
}

fn decode_target(value: &str) -> String {
    let trimmed = value.trim();
    let trimmed = trimmed.strip_prefix('<').unwrap_or(trimmed);
    let trimmed = trimmed.strip_suffix('>').unwrap_or(trimmed);
    let lower = trimmed.to_ascii_lowercase();
    let without_scheme = if lower.starts_with("file://localhost") {
        &trimmed["file://localhost".len()..]
    } else if lower.starts_with("file://") {
        &trimmed["file://".len()..]
    } else {
        trimmed
    };
    percent_decoded(without_scheme).unwrap_or_else(|| without_scheme.to_owned())
}

fn has_scheme(target: &str) -> bool {
    let Some((scheme, _)) = target.split_once(':') else {
        return false;
    };
    let mut chars = scheme.chars();
    chars.next().is_some_and(|c| c.is_ascii_alphabetic())
        && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-'))
}

fn target_aliases(value: &str) -> Vec<String> {
    let decoded = decode_target(value);
    let target = decoded.trim_end_matches([')', ',', '.', ';', ':', '!', '?']);
    if !target.contains('/') && !has_scheme(target) {
        return Vec::new();
    }
    let target = target.replace('\\', "/");
    let target = target.trim_end_matches('/');
    if target.is_empty() {
        return Vec::new();
    }
    let mut aliases = vec![format!("target:{target}")];
    let without_query = target.split(['?', '#']).next().unwrap_or_default();
    let parts: Vec<&str> = without_query.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() >= 2 {
        let path2 = format!("path2:{}", parts[parts.len() - 2..].join("/"));
        if !aliases.contains(&path2) {
            aliases.push(path2);
        }
    }
    aliases
}

/// The words of `text`, each with its UTF-16 offsets plus `base`.
fn push_words(out: &mut Vec<SrcToken>, text: &str, base: usize) {
    let mut word = String::new();
    let mut start = base;
    let mut at = base;
    let mut flush = |word: &mut String, start: usize, end: usize| {
        if word.is_empty() {
            return;
        }
        let norm = normalize(word);
        if !norm.is_empty() {
            out.push(SrcToken {
                token: Token {
                    norm,
                    aliases: target_aliases(word),
                },
                start,
                end,
            });
        }
        word.clear();
    };
    for c in text.chars() {
        if c.is_whitespace() {
            flush(&mut word, start, at);
        } else {
            if word.is_empty() {
                start = at;
            }
            word.push(c);
        }
        at += c.len_utf16();
    }
    flush(&mut word, start, at);
}

/// A markdown link, `[label](target)` on one line: its byte ranges in the text.
struct Link {
    whole: std::ops::Range<usize>,
    label: std::ops::Range<usize>,
    target: std::ops::Range<usize>,
}

fn next_link(text: &str, from: usize) -> Option<Link> {
    let bytes = text.as_bytes();
    let mut open = from;
    loop {
        open += text[open..].find('[')?;
        let label_start = open + 1;
        let close = text[label_start..]
            .find([']', '\n'])
            .map(|i| label_start + i);
        if let Some(close) = close.filter(|&c| bytes[c] == b']' && c > label_start)
            && bytes.get(close + 1) == Some(&b'(')
        {
            let target_start = close + 2;
            let end = text[target_start..]
                .find([')', '\n'])
                .map(|i| target_start + i);
            if let Some(end) = end.filter(|&e| bytes[e] == b')' && e > target_start) {
                return Some(Link {
                    whole: open..end + 1,
                    label: label_start..close,
                    target: target_start..end,
                });
            }
        }
        open += 1;
    }
}

fn utf16_len(text: &str) -> usize {
    text.chars().map(char::len_utf16).sum()
}

fn tokenize_markdown(markdown: &str) -> Vec<SrcToken> {
    let mut out = Vec::new();
    let mut cursor = 0;
    let mut cursor16 = 0;
    while let Some(link) = next_link(markdown, cursor) {
        let start16 = cursor16 + utf16_len(&markdown[cursor..link.whole.start]);
        push_words(&mut out, &markdown[cursor..link.whole.start], cursor16);
        let label16 = start16 + 1;
        push_words(&mut out, &markdown[link.label.clone()], label16);
        let raw = &markdown[link.target.clone()];
        let trimmed = raw.trim();
        let target = trimmed
            .char_indices()
            .find(|&(i, c)| c.is_whitespace() && trimmed[i..].trim_start().starts_with(['\'', '"']))
            .map_or(trimmed, |(i, _)| &trimmed[..i]);
        let target_at = link.target.start + raw.find(target).unwrap_or(0);
        let norm = normalize(target);
        if !norm.is_empty() {
            let target16 = start16 + utf16_len(&markdown[link.whole.start..target_at]);
            out.push(SrcToken {
                token: Token {
                    norm,
                    aliases: target_aliases(target),
                },
                start: target16,
                end: target16 + utf16_len(target),
            });
        }
        cursor16 = start16 + utf16_len(&markdown[link.whole.clone()]);
        cursor = link.whole.end;
    }
    push_words(&mut out, &markdown[cursor..], cursor16);
    out
}

fn is_blank(cell: &str) -> bool {
    cell.chars().all(char::is_whitespace) && !cell.is_empty()
}

/// The words of terminal rows, `base` the first row's screen row.
fn tokenize_rows(rows: &[LinkRow], base: u32) -> Vec<GridToken> {
    let mut out = Vec::new();
    for (r, row) in rows.iter().enumerate() {
        let row_at = base + r as u32;
        let marker = row
            .cells
            .iter()
            .find(|cell| !cell.is_empty() && !is_blank(cell))
            .and_then(|cell| cell.chars().next());
        let assistant_row = marker.is_some_and(|m| ASSISTANT_MARKERS.contains(&m));
        let user_row = marker.is_some_and(|m| USER_MARKERS.contains(&m));
        let mut emitted = 0;
        let mut col = 0;
        while col < row.cells.len() {
            if is_blank(&row.cells[col]) {
                col += 1;
                continue;
            }
            let first = col;
            let mut raw = String::new();
            while col < row.cells.len() && !is_blank(&row.cells[col]) {
                raw.push_str(&row.cells[col]);
                col += 1;
            }
            let norm = normalize(&raw);
            if norm.is_empty() {
                continue;
            }
            let mut aliases = target_aliases(&raw);
            if let Some(Some(uri)) = row.uris.get(first) {
                for alias in target_aliases(uri) {
                    if !aliases.contains(&alias) {
                        aliases.push(alias);
                    }
                }
            }
            out.push(GridToken {
                token: Token { norm, aliases },
                row: row_at,
                col: first as u16,
                end_col: col as u16,
                assistant_lead: assistant_row && emitted == 0,
                explicit_user: user_row,
            });
            emitted += 1;
        }
    }
    out
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct Pair {
    src: usize,
    grid: usize,
}

/// Each key's positions, keys in the order they first appear.
fn positions_by_key<'a>(tokens: impl Iterator<Item = &'a Token>) -> Vec<(&'a str, Vec<usize>)> {
    let mut index: HashMap<&str, usize> = HashMap::new();
    let mut out: Vec<(&str, Vec<usize>)> = Vec::new();
    for (i, token) in tokens.enumerate() {
        let mut seen = HashSet::new();
        for key in token.keys() {
            if !seen.insert(key) {
                continue;
            }
            match index.get(key) {
                Some(&at) => out[at].1.push(i),
                None => {
                    index.insert(key, out.len());
                    out.push((key, vec![i]));
                }
            }
        }
    }
    out
}

fn unique_in<'a>(positions: &'a [(&str, Vec<usize>)]) -> HashMap<&'a str, &'a [usize]> {
    positions
        .iter()
        .map(|(k, at)| (*k, at.as_slice()))
        .collect()
}

fn monotone_anchors(src: &[SrcToken], grid: &[GridToken]) -> Vec<Pair> {
    let src_positions = positions_by_key(src.iter().map(|t| &t.token));
    let grid_positions = positions_by_key(grid.iter().map(|t| &t.token));
    let grid_at = unique_in(&grid_positions);
    let mut unique: HashMap<(usize, usize), u32> = HashMap::new();
    for (key, src_at) in &src_positions {
        let ([s], Some([g])) = (src_at.as_slice(), grid_at.get(key).copied()) else {
            continue;
        };
        let strength = if src[*s].token.norm == grid[*g].token.norm {
            2
        } else {
            1
        };
        let entry = unique.entry((*s, *g)).or_insert(strength);
        *entry = (*entry).max(strength);
    }
    struct Candidate {
        pair: Pair,
        strength: u32,
        score: usize,
        previous: Option<usize>,
    }
    let mut candidates: Vec<Candidate> = unique
        .into_iter()
        .map(|((s, g), strength)| Candidate {
            pair: Pair { src: s, grid: g },
            strength,
            score: 0,
            previous: None,
        })
        .collect();
    candidates.sort_by(|a, b| {
        (a.pair.src, a.pair.grid, std::cmp::Reverse(a.strength)).cmp(&(
            b.pair.src,
            b.pair.grid,
            std::cmp::Reverse(b.strength),
        ))
    });
    if candidates.is_empty() {
        return Vec::new();
    }
    let mut fenwick: Vec<(usize, Option<usize>)> = vec![(0, None); grid.len() + 1];
    let query = |fenwick: &[(usize, Option<usize>)], exclusive: usize| {
        let mut best = (0, None);
        let mut at = exclusive;
        while at > 0 {
            if fenwick[at].0 > best.0 {
                best = fenwick[at];
            }
            at -= at.isolate_lowest_one();
        }
        best
    };
    let pair_weight = 2 * candidates.len() + 1;
    let mut best: Option<usize> = None;
    let mut batch = 0;
    while batch < candidates.len() {
        let mut end = batch + 1;
        while end < candidates.len() && candidates[end].pair.src == candidates[batch].pair.src {
            end += 1;
        }
        for i in batch..end {
            let (score, previous) = query(&fenwick, candidates[i].pair.grid);
            candidates[i].previous = previous;
            candidates[i].score = score + pair_weight + candidates[i].strength as usize;
            if best.is_none_or(|b| candidates[i].score > candidates[b].score) {
                best = Some(i);
            }
        }
        for (i, candidate) in candidates.iter().enumerate().take(end).skip(batch) {
            let value = (candidate.score, Some(i));
            let mut at = candidate.pair.grid + 1;
            while at < fenwick.len() {
                if value.0 > fenwick[at].0 {
                    fenwick[at] = value;
                }
                at += at.isolate_lowest_one();
            }
        }
        batch = end;
    }
    let mut chain = Vec::new();
    let mut at = best;
    while let Some(i) = at {
        chain.push(candidates[i].pair);
        at = candidates[i].previous;
    }
    chain.reverse();
    chain
}

fn seed_score(src: &[SrcToken], grid: &[GridToken], gi: usize) -> usize {
    (0..SEED_LOOKAHEAD)
        .take_while(|&d| d < src.len() && gi + d < grid.len())
        .filter(|&d| src[d].token.matches(&grid[gi + d].token))
        .count()
}

fn find_seed(src: &[SrcToken], grid: &[GridToken]) -> Option<Pair> {
    let src_positions = positions_by_key(src.iter().map(|t| &t.token));
    let grid_positions = positions_by_key(grid.iter().map(|t| &t.token));
    let grid_at = unique_in(&grid_positions);
    let mut anchors = Vec::new();
    let mut votes: Vec<(isize, usize)> = Vec::new();
    for (key, src_at) in &src_positions {
        let ([s], Some([g])) = (src_at.as_slice(), grid_at.get(key).copied()) else {
            continue;
        };
        anchors.push(Pair { src: *s, grid: *g });
        let offset = *g as isize - *s as isize;
        match votes.iter_mut().find(|(o, _)| *o == offset) {
            Some((_, n)) => *n += 1,
            None => votes.push((offset, 1)),
        }
    }
    if !anchors.is_empty() {
        let mut best = (0, 0);
        for &(offset, n) in &votes {
            if n > best.1 {
                best = (offset, n);
            }
        }
        if let Some(first) = anchors
            .iter()
            .filter(|a| a.grid as isize - a.src as isize == best.0)
            .min_by_key(|a| a.src)
        {
            return Some(*first);
        }
    }
    let mut found = None;
    let mut best_score = 0;
    let mut best_lead = false;
    for (j, token) in grid.iter().enumerate() {
        if !src[0].token.matches(&token.token) {
            continue;
        }
        let score = seed_score(src, grid, j);
        if score > best_score || (score == best_score && token.assistant_lead && !best_lead) {
            best_score = score;
            best_lead = token.assistant_lead;
            found = Some(j);
        }
    }
    found.map(|gi| Pair { src: 0, grid: gi })
}

fn resync_forward(
    src: &[SrcToken],
    (si, src_end): (usize, usize),
    grid: &[GridToken],
    (gi, grid_end): (usize, usize),
) -> Option<(usize, usize, usize)> {
    for d in 1..=RESYNC_WINDOW {
        if si + d < src_end && src[si + d].token.matches(&grid[gi].token) {
            return Some((si + d, gi, d));
        }
        if gi + d < grid_end && src[si].token.matches(&grid[gi + d].token) {
            return Some((si, gi + d, d));
        }
        if si + d < src_end && gi + d < grid_end && src[si + d].token.matches(&grid[gi + d].token) {
            return Some((si + d, gi + d, d));
        }
    }
    None
}

fn greedy_segment(
    src: &[SrcToken],
    grid: &[GridToken],
    (src_start, src_end): (usize, usize),
    (grid_start, grid_end): (usize, usize),
) -> Vec<Pair> {
    let mut pairs = Vec::new();
    let (mut si, mut gi, mut gaps) = (src_start, grid_start, 0);
    let budget = RESYNC_WINDOW.max((src_end - src_start).div_ceil(2));
    while si < src_end && gi < grid_end {
        if src[si].token.matches(&grid[gi].token) {
            pairs.push(Pair { src: si, grid: gi });
            si += 1;
            gi += 1;
            continue;
        }
        let Some((s, g, cost)) = resync_forward(src, (si, src_end), grid, (gi, grid_end)) else {
            break;
        };
        gaps += cost;
        if gaps > budget {
            break;
        }
        (si, gi) = (s, g);
    }
    pairs
}

/// Greedy, not a longest common subsequence: a repeated word can pair wrongly, which is safe
/// only because [`quotes_anchor`] reads the rows again before anything paints.
fn pair_tokens(src: &[SrcToken], grid: &[GridToken]) -> Vec<Pair> {
    if src.is_empty() || grid.is_empty() {
        return Vec::new();
    }
    let anchors = monotone_anchors(src, grid);
    if !anchors.is_empty() {
        let mut pairs = Vec::new();
        let (mut s, mut g) = (0, 0);
        for anchor in anchors {
            pairs.extend(greedy_segment(src, grid, (s, anchor.src), (g, anchor.grid)));
            pairs.push(anchor);
            (s, g) = (anchor.src + 1, anchor.grid + 1);
        }
        pairs.extend(greedy_segment(src, grid, (s, src.len()), (g, grid.len())));
        return pairs;
    }
    let Some(seed) = find_seed(src, grid) else {
        return Vec::new();
    };
    let budget = RESYNC_WINDOW.max(src.len().div_ceil(2));
    let mut before = Vec::new();
    let (mut si, mut gi, mut gaps) = (seed.src as isize, seed.grid as isize, 0);
    while si >= 0 && gi >= 0 {
        let (s, g) = (si as usize, gi as usize);
        if src[s].token.matches(&grid[g].token) {
            before.push(Pair { src: s, grid: g });
            si -= 1;
            gi -= 1;
            continue;
        }
        let mut resynced = false;
        for d in 1..=RESYNC_WINDOW as isize {
            let (ds, dg) = (si - d, gi - d);
            if ds >= 0 && src[ds as usize].token.matches(&grid[g].token) {
                si = ds;
            } else if dg >= 0 && src[s].token.matches(&grid[dg as usize].token) {
                gi = dg;
            } else if ds >= 0 && dg >= 0 && src[ds as usize].token.matches(&grid[dg as usize].token)
            {
                (si, gi) = (ds, dg);
            } else {
                continue;
            }
            gaps += d as usize;
            resynced = true;
            break;
        }
        if !resynced || gaps > budget {
            break;
        }
    }
    before.reverse();
    let (mut si, mut gi, mut gaps) = (seed.src + 1, seed.grid + 1, 0);
    while si < src.len() && gi < grid.len() {
        if src[si].token.matches(&grid[gi].token) {
            before.push(Pair { src: si, grid: gi });
            si += 1;
            gi += 1;
            continue;
        }
        let Some((s, g, cost)) = resync_forward(src, (si, src.len()), grid, (gi, grid.len()))
        else {
            break;
        };
        gaps += cost;
        if gaps > budget {
            break;
        }
        (si, gi) = (s, g);
    }
    before
}

/// One markdown word placed on a row.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct Placed {
    start: usize,
    end: usize,
    col: u16,
    end_col: u16,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct RowAlignment {
    words: Vec<Placed>,
    explicit_user: bool,
    matched: usize,
    total: usize,
}

impl RowAlignment {
    fn confident(&self) -> bool {
        !self.explicit_user
            && self.total > 0
            && self.matched as f64 / self.total as f64 >= CONFIDENT_ROW
    }
}

/// Where one message sits on the terminal: the rows it was found on, by screen row.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Alignment {
    rows: BTreeMap<u32, RowAlignment>,
    /// The first and last rows that confidently hold the message.
    pub span: Option<(u32, u32)>,
}

/// The cells of one row that show part of a message.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct RowRange {
    pub row: u32,
    pub cols: (u16, u16),
}

impl Alignment {
    /// Finds `markdown` on `rows`, the first of them screen row `base`.
    pub fn of(markdown: &str, rows: &[LinkRow], base: u32) -> Alignment {
        let src = tokenize_markdown(markdown);
        let grid = tokenize_rows(rows, base);
        let mut totals: HashMap<u32, usize> = HashMap::new();
        for token in &grid {
            *totals.entry(token.row).or_default() += 1;
        }
        let mut aligned: BTreeMap<u32, RowAlignment> = BTreeMap::new();
        for pair in pair_tokens(&src, &grid) {
            let (g, s) = (&grid[pair.grid], &src[pair.src]);
            let row = aligned.entry(g.row).or_insert_with(|| RowAlignment {
                words: Vec::new(),
                explicit_user: g.explicit_user,
                matched: 0,
                total: totals[&g.row],
            });
            row.words.push(Placed {
                start: s.start,
                end: s.end,
                col: g.col,
                end_col: g.end_col,
            });
            row.matched += 1;
        }
        let confident: Vec<u32> = aligned
            .iter()
            .filter(|(_, r)| r.confident())
            .map(|(row, _)| *row)
            .collect();
        Alignment {
            span: confident
                .first()
                .zip(confident.last())
                .map(|(a, b)| (*a, *b)),
            rows: aligned,
        }
    }

    /// The cells that show the markdown between UTF-16 offsets `start` and `end`, by row.
    pub fn rows_for(&self, start: usize, end: usize) -> Vec<RowRange> {
        self.rows
            .iter()
            .filter(|(_, r)| r.confident())
            .filter_map(|(row, r)| {
                let words = r.words.iter().filter(|w| w.end > start && w.start < end);
                let low = words.clone().map(|w| w.col).min()?;
                let high = words.map(|w| w.end_col).max()?;
                Some(RowRange {
                    row: *row,
                    cols: (low, high),
                })
            })
            .collect()
    }

    /// The UTF-16 offsets of the markdown a drag from (`from` row, col) to (`to` row, col)
    /// covers, whole words, or None when it covers none of the message.
    pub fn offsets_for(&self, from: (u32, u16), to: (u32, u16)) -> Option<(usize, usize)> {
        let mut span: Option<(usize, usize)> = None;
        for (row, r) in self.rows.range(from.0..=to.0) {
            if !r.confident() {
                continue;
            }
            let low = if *row == from.0 { from.1 } else { 0 };
            let high = if *row == to.0 { to.1 } else { u16::MAX };
            for w in r.words.iter().filter(|w| w.end_col > low && w.col < high) {
                span = Some(span.map_or((w.start, w.end), |(s, e)| (s.min(w.start), e.max(w.end))));
            }
        }
        span
    }
}

/// Whether `painted`, the rows a span covers as the terminal shows them now, still reads as
/// `quoted`, the markdown it was anchored to. It does not ask the aligner, so a wrong alignment
/// can only hide a mark.
pub fn quotes_anchor(quoted: &str, painted: &[String]) -> bool {
    let want: Vec<Token> = tokenize_markdown(quoted)
        .into_iter()
        .map(|t| t.token)
        .collect();
    let rows: Vec<LinkRow> = painted
        .iter()
        .map(|text| LinkRow {
            cells: text.chars().map(String::from).collect(),
            uris: Vec::new(),
        })
        .collect();
    let got: Vec<Token> = tokenize_rows(&rows, 0)
        .into_iter()
        .map(|t| t.token)
        .collect();
    if want.is_empty() || got.is_empty() {
        return false;
    }
    let contains = |whole: &[Token], part: &[Token]| {
        part.len() <= whole.len()
            && whole
                .windows(part.len())
                .any(|w| w.iter().zip(part).all(|(a, b)| b.matches(a)))
    };
    contains(&got, &want) || contains(&want, &got)
}
