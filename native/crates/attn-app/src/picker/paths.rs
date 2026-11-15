/// The last segment of a path, `/` for the root.
pub(crate) fn basename(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return if path.is_empty() { "" } else { "/" };
    }
    trimmed.rsplit('/').next().unwrap_or(trimmed)
}

/// `path` with the home folder written as `~`.
pub(crate) fn display(path: &str, home: &str) -> String {
    if !home.is_empty() {
        if path == home {
            return "~".into();
        }
        if let Some(rest) = path.strip_prefix(home).filter(|rest| rest.starts_with('/')) {
            return format!("~{rest}");
        }
    }
    path.into()
}

/// `path` with a leading `~` written as the home folder.
pub(crate) fn expand(path: &str, home: &str) -> String {
    if home.is_empty() || !path.starts_with('~') {
        return path.into();
    }
    match &path[1..] {
        "" => home.into(),
        rest if rest.starts_with('/') => format!("{home}{rest}"),
        rest => format!("{home}/{rest}"),
    }
}

/// What the picker asks the daemon to inspect for a typed path: trimmed, expanded and without
/// trailing slashes, except for the root and the home folder themselves.
pub(crate) fn normalize(path: &str, home: &str) -> String {
    let expanded = expand(path.trim(), home);
    if expanded == "/" || expanded == home {
        return expanded;
    }
    expanded.trim_end_matches('/').into()
}

/// The path field's text when the picker opens: the projects folder, ready to browse.
pub(crate) fn initial_input(projects: &str, home: &str) -> String {
    if projects.is_empty() {
        return String::new();
    }
    let shown = display(projects, home);
    if shown.ends_with('/') {
        shown
    } else {
        format!("{shown}/")
    }
}

/// A repository chooser's own way of shortening a path: the home it can see in the path.
pub(crate) fn tildify(path: &str) -> String {
    for root in ["/Users/", "/home/"] {
        if let Some(rest) = path.strip_prefix(root) {
            let user = rest.split('/').next().unwrap_or_default();
            if !user.is_empty() {
                return format!("~{}", &rest[user.len()..]);
            }
        }
    }
    path.into()
}

pub(crate) fn starts_with_ignoring_case(candidate: &str, input: &str) -> bool {
    candidate.to_lowercase().starts_with(&input.to_lowercase())
}
