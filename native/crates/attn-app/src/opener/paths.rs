//! Typed paths in the opener (pathMode.ts): a query that starts with `/`, `~`, `./` or `../`
//! browses a folder instead of searching, a relative one against the opener's root, since the
//! daemon would resolve it against its own working folder.

pub(crate) fn is_path(query: &str) -> bool {
    let q = query.trim_start();
    q.starts_with('/')
        || q.starts_with('~')
        || q == "."
        || q.starts_with("./")
        || q.starts_with("../")
}

/// The path the daemon browses for `query`; `None` when it is no path, or a relative one with
/// no root.
pub(crate) fn browse_input(query: &str, root: Option<&str>) -> Option<String> {
    let q = query.trim_start();
    if !is_path(q) {
        return None;
    }
    if q.starts_with('/') || q.starts_with('~') {
        return Some(q.to_owned());
    }
    Some(normalized(&format!("{}/{q}", root?)))
}

fn normalized(path: &str) -> String {
    let trailing = path.ends_with('/');
    let absolute = path.starts_with('/');
    let mut steps: Vec<&str> = Vec::new();
    for segment in path.split('/') {
        match segment {
            "" | "." => {}
            ".." if steps.last().is_some_and(|s| *s != "..") => {
                steps.pop();
            }
            ".." if absolute => {}
            segment => steps.push(segment),
        }
    }
    let joined = format!("{}{}", if absolute { "/" } else { "" }, steps.join("/"));
    match joined.as_str() {
        "/" | "" => {
            if absolute {
                "/".into()
            } else {
                ".".into()
            }
        }
        _ if trailing => format!("{joined}/"),
        _ => joined,
    }
}

/// The query that lists folder `shown`, as written in a row.
pub(crate) fn descend(shown: &str) -> String {
    if shown.ends_with('/') {
        shown.to_owned()
    } else {
        format!("{shown}/")
    }
}
