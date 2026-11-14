//! Which files the notebook opens as markdown, as plain text, or not at all (fileKind.ts). An
//! unknown kind stays closed, since saving an opaque file as text could change its bytes.

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum FileKind {
    Markdown,
    Text,
    Binary,
}

const TEXT_EXTENSIONS: &[&str] = &[
    "txt", "text", "html", "htm", "css", "scss", "sass", "less", "js", "mjs", "cjs", "ts", "tsx",
    "jsx", "json", "jsonc", "jsonl", "yaml", "yml", "toml", "xml", "csv", "tsv", "ini", "cfg",
    "conf", "env", "log", "sh", "bash", "zsh", "fish", "ps1", "go", "rs", "py", "rb", "php",
    "java", "kt", "kts", "c", "cc", "cpp", "cxx", "h", "hpp", "cs", "swift", "scala", "sql",
];

const TEXT_NAMES: &[&str] = &["readme", "license", "makefile", "dockerfile", "brewfile"];

pub(crate) fn file_kind(path: &str) -> FileKind {
    let name = path.rsplit('/').next().unwrap_or(path);
    let extension = name
        .rfind('.')
        .filter(|&dot| dot > 0)
        .map(|dot| name[dot + 1..].to_ascii_lowercase())
        .unwrap_or_default();
    if extension == "md" || extension == "markdown" {
        FileKind::Markdown
    } else if TEXT_EXTENSIONS.contains(&extension.as_str())
        || TEXT_NAMES.contains(&name.to_ascii_lowercase().as_str())
    {
        FileKind::Text
    } else {
        FileKind::Binary
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn markdown_opens_as_a_note_known_text_as_text_and_everything_else_stays_closed() {
        for (path, kind) in [
            ("notes/NOTES.MARKDOWN", FileKind::Markdown),
            ("a.md", FileKind::Markdown),
            ("config.json", FileKind::Text),
            ("Makefile", FileKind::Text),
            ("docs/README", FileKind::Text),
            ("clip.MP4", FileKind::Binary),
            ("fonts.woff2", FileKind::Binary),
            ("installer.pkg", FileKind::Binary),
            ("attachment", FileKind::Binary),
            (".gitignore", FileKind::Binary),
        ] {
            assert_eq!(file_kind(path), kind, "{path}");
        }
    }
}
