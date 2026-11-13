//! Which files the opener lists for a query, best first, as the Tauri opener ranks them
//! (openerRank.ts, rank.ts): recent files and the root's index in one list, each file once,
//! scored as a subsequence of its path or name with a bonus for files opened before.

/// A file the opener can list: by its absolute path, labelled relative to the root when it
/// is under it, on outpost `host` when it has one.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct File {
    pub(crate) path: String,
    pub(crate) label: String,
    pub(crate) recent_at: Option<String>,
    pub(crate) host: Option<String>,
}

/// A recently opened file: its absolute path, when, and the outpost that holds it.
pub(crate) struct Recent {
    pub(crate) path: String,
    pub(crate) at: String,
    pub(crate) host: Option<String>,
}

/// Enough to win ties and near-ties against an equally good match never opened, not enough to
/// keep a weak match above a clearly better one.
const RECENT_BONUS: f64 = 1.4;
const LIMIT: usize = 50;

/// The recents, then the index's files under `root` that are not among them.
pub(crate) fn merged(recents: &[Recent], root: Option<&str>, index: &[String]) -> Vec<File> {
    let prefix = root.map(|r| {
        if r.ends_with('/') {
            r.to_owned()
        } else {
            format!("{r}/")
        }
    });
    let mut files: Vec<File> = recents
        .iter()
        .map(|r| File {
            path: r.path.clone(),
            label: match (&r.host, &prefix) {
                (None, Some(prefix)) => r
                    .path
                    .strip_prefix(prefix.as_str())
                    .unwrap_or(&r.path)
                    .to_owned(),
                _ => r.path.clone(),
            },
            recent_at: Some(r.at.clone()),
            host: r.host.clone(),
        })
        .collect();
    let Some(prefix) = prefix else {
        return files;
    };
    let mut seen: std::collections::HashSet<String> = files
        .iter()
        .filter(|f| f.host.is_none())
        .map(|f| f.path.clone())
        .collect();
    for relative in index {
        let path = format!("{prefix}{relative}");
        if seen.insert(path.clone()) {
            files.push(File {
                path,
                label: relative.clone(),
                recent_at: None,
                host: None,
            });
        }
    }
    files
}

/// The files `query` matches, best first; the recents alone for an empty query.
pub(crate) fn ranked(files: &[File], query: &str) -> Vec<File> {
    if query.trim().is_empty() {
        return files
            .iter()
            .filter(|f| f.recent_at.is_some())
            .take(LIMIT)
            .cloned()
            .collect();
    }
    let mut scored: Vec<(f64, &File)> = files
        .iter()
        .map(|f| {
            let bonus = if f.recent_at.is_some() {
                RECENT_BONUS
            } else {
                1.0
            };
            (score(&f.label, query) * bonus, f)
        })
        .filter(|(score, _)| *score > 0.0)
        .collect();
    scored.sort_by(|(a, left), (b, right)| {
        b.total_cmp(a)
            .then_with(|| right.recent_at.cmp(&left.recent_at))
            .then_with(|| left.label.cmp(&right.label))
    });
    scored
        .into_iter()
        .take(LIMIT)
        .map(|(_, f)| f.clone())
        .collect()
}

pub(crate) fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

fn score(path: &str, query: &str) -> f64 {
    let query = query.trim().to_lowercase();
    let path = path.to_lowercase();
    let whole = subsequence(&path, &query);
    let base = subsequence(basename(&path), &query);
    whole.max(base * 1.5)
}

/// How well `query`'s characters appear in order in `text`: one point each, more for runs and
/// for a match at a word's start; 0 when they do not all appear.
fn subsequence(text: &str, query: &str) -> f64 {
    let text: Vec<char> = text.chars().collect();
    let mut score = 0.0;
    let mut from = 0;
    let mut previous: Option<usize> = None;
    let mut run = 0.0;
    for wanted in query.chars() {
        let Some(found) = (from..text.len()).find(|&i| text[i] == wanted) else {
            return 0.0;
        };
        score += 1.0;
        if previous.is_some_and(|p| found == p + 1) {
            run += 1.0;
            score += run * 2.0;
        } else {
            run = 0.0;
        }
        if found == 0 || matches!(text[found - 1], '/' | '-' | '_' | '.' | ' ') {
            score += 3.0;
        }
        previous = Some(found);
        from = found + 1;
    }
    score
}
