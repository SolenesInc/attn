use std::io;

pub fn clean(path: &str) -> String {
    let rooted = path.starts_with('/');
    let mut kept: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => match kept.last() {
                Some(&last) if last != ".." => {
                    kept.pop();
                }
                _ if rooted => {}
                _ => kept.push(".."),
            },
            other => kept.push(other),
        }
    }
    let joined = kept.join("/");
    match (rooted, joined.is_empty()) {
        (true, _) => format!("/{joined}"),
        (false, true) => ".".to_string(),
        (false, false) => joined,
    }
}

pub fn join(base: &str, rest: &str) -> String {
    if base.is_empty() {
        return clean(rest);
    }
    clean(&format!("{base}/{rest}"))
}

pub fn base(path: &str) -> &str {
    if path.is_empty() {
        return ".";
    }
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        return "/";
    }
    &trimmed[trimmed.rfind('/').map_or(0, |slash| slash + 1)..]
}

pub fn extension(path: &str) -> &str {
    let name_start = path.rfind('/').map_or(0, |slash| slash + 1);
    path[name_start..]
        .rfind('.')
        .map_or("", |dot| &path[name_start + dot..])
}

pub fn dir(path: &str) -> String {
    match path.rfind('/') {
        Some(0) => "/".to_string(),
        Some(slash) => clean(&path[..slash]),
        None => ".".to_string(),
    }
}

pub fn os_error_text(error: &io::Error) -> String {
    let text = error.to_string();
    let message = text.split(" (os error").next().unwrap_or(&text);
    message.to_lowercase()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clean_matches_go_filepath_clean() {
        for (input, want) in [
            ("", "."),
            ("/", "/"),
            ("/a/../..", "/"),
            ("a/./b//c/", "a/b/c"),
            ("../a", "../a"),
            ("a/../../b", "../b"),
            ("/a/b/..", "/a"),
        ] {
            assert_eq!(clean(input), want, "clean({input:?})");
        }
    }

    #[test]
    fn extension_matches_go_filepath_ext() {
        for (input, want) in [
            ("/a/b.MD", ".MD"),
            ("/a.d/b", ""),
            ("b", ""),
            ("/a/.md", ".md"),
            ("x.tar.gz", ".gz"),
        ] {
            assert_eq!(extension(input), want, "extension({input:?})");
        }
    }
}
