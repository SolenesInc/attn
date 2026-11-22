use super::input::{Raw, array_items, parse_object, strict_text};
use crate::paths::{clean, extension, join};
use serde::Deserialize;
use serde_json::value::RawValue;
use std::collections::{BTreeMap, HashSet};

#[derive(Deserialize)]
struct FileEditInput<'a> {
    #[serde(borrow, default)]
    file_path: Raw<'a>,
    #[serde(borrow, default)]
    notebook_path: Raw<'a>,
}

#[derive(Deserialize)]
struct PatchInput<'a> {
    #[serde(borrow, default)]
    patch: Raw<'a>,
    #[serde(borrow, default)]
    command: Raw<'a>,
    #[serde(borrow, default)]
    input: Raw<'a>,
}

#[derive(Deserialize)]
struct SendFilesInput<'a> {
    #[serde(borrow, default)]
    files: Raw<'a>,
}

pub struct Tools {
    pub edits: &'static [&'static str],
    pub patches: &'static [&'static str],
    pub sends: &'static [&'static str],
    pub shell: fn(&str) -> bool,
}

impl Tools {
    pub fn knows(&self, tool_name: &str) -> bool {
        self.edits.contains(&tool_name)
            || self.patches.contains(&tool_name)
            || self.sends.contains(&tool_name)
            || (self.shell)(tool_name)
    }
}

pub fn markdown_edits(
    tools: &Tools,
    tool_name: &str,
    tool_input: Raw<'_>,
    cwd: &str,
    home: &str,
) -> Vec<String> {
    let paths = if tools.edits.contains(&tool_name) {
        edited_file_paths(tool_input).unwrap_or_default()
    } else if tools.patches.contains(&tool_name) {
        patched_paths(tool_input).unwrap_or_default()
    } else {
        return Vec::new();
    };
    let markdown = paths
        .iter()
        .map(|path| path.trim())
        .filter(|path| is_markdown(path))
        .filter_map(|path| expand_home_or_file_url(path, home))
        .collect();
    resolve_unique(markdown, cwd)
}

fn expand_home_or_file_url(path: &str, home: &str) -> Option<String> {
    if path == "~" || path.starts_with("~/") {
        return (!home.is_empty()).then(|| format!("{home}{}", &path[1..]));
    }
    Some(
        path.strip_prefix("file://")
            .and_then(local_file_url_path)
            .unwrap_or_else(|| path.to_string()),
    )
}

fn local_file_url_path(rest: &str) -> Option<String> {
    let slash = rest.find('/')?;
    if !matches!(&rest[..slash], "" | "localhost") {
        return None;
    }
    let path = &rest[slash..];
    let path = &path[..path.find(['?', '#']).unwrap_or(path.len())];
    percent_decoded(path)
}

fn percent_decoded(text: &str) -> Option<String> {
    let bytes = text.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut at = 0;
    while at < bytes.len() {
        if bytes[at] == b'%' {
            let hex = text.get(at + 1..at + 3)?;
            decoded.push(u8::from_str_radix(hex, 16).ok()?);
            at += 3;
        } else {
            decoded.push(bytes[at]);
            at += 1;
        }
    }
    String::from_utf8(decoded).ok()
}

fn edited_file_paths(tool_input: Raw<'_>) -> Option<Vec<String>> {
    let input: FileEditInput<'_> = parse_object(tool_input)?;
    Some(vec![
        strict_text(input.file_path)?,
        strict_text(input.notebook_path)?,
    ])
}

fn patched_paths(tool_input: Raw<'_>) -> Option<Vec<String>> {
    let input: PatchInput<'_> = parse_object(tool_input)?;
    let patch = [input.patch, input.command, input.input]
        .into_iter()
        .map(strict_text)
        .collect::<Option<Vec<_>>>()?
        .into_iter()
        .find(|text| !text.trim().is_empty())
        .unwrap_or_default();
    Some(apply_patch_targets(&patch))
}

fn apply_patch_targets(patch: &str) -> Vec<String> {
    let mut targets = Vec::new();
    for line in patch.split('\n') {
        let line = line.trim();
        if let Some(rest) = line
            .strip_prefix("*** Add File: ")
            .or_else(|| line.strip_prefix("*** Update File: "))
        {
            targets.push(rest.trim().to_string());
        } else if let Some(rest) = line.strip_prefix("*** Move to: ") {
            targets.pop();
            targets.push(rest.trim().to_string());
        }
    }
    targets
}

fn is_markdown(path: &str) -> bool {
    matches!(extension(path).to_lowercase().as_str(), ".md" | ".markdown")
}

pub fn sent_files(tools: &Tools, tool_name: &str, tool_input: Raw<'_>, cwd: &str) -> Vec<String> {
    if !tools.sends.contains(&tool_name) {
        return Vec::new();
    }
    sent_file_paths(tool_input).map_or_default(|paths| resolve_unique(paths, cwd))
}

fn sent_file_paths(tool_input: Raw<'_>) -> Option<Vec<String>> {
    let input: SendFilesInput<'_> = parse_object(tool_input)?;
    if let Some(files) = input.files
        && !files.get().starts_with('[')
        && files.get() != "null"
    {
        return None;
    }
    array_items(input.files)
        .into_iter()
        .map(|item| strict_text(Some(item)))
        .collect()
}

fn resolve_unique(paths: Vec<String>, cwd: &str) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut resolved = Vec::new();
    for path in paths {
        let path = path.trim();
        if path.is_empty() {
            continue;
        }
        let absolute = if path.starts_with('/') {
            clean(path)
        } else if cwd.is_empty() {
            continue;
        } else {
            join(cwd, path)
        };
        if seen.insert(absolute.clone()) {
            resolved.push(absolute);
        }
    }
    resolved
}

pub fn pull_requests_created(
    tools: &Tools,
    tool_name: &str,
    tool_input: Raw<'_>,
    tool_response: Raw<'_>,
) -> Vec<String> {
    if !(tools.shell)(tool_name) || !has_gh_pr_create(&shell_command(tool_input)) {
        return Vec::new();
    }
    let Some(response) = tool_response else {
        return Vec::new();
    };
    let mut seen = HashSet::new();
    let mut urls = Vec::new();
    let text = response.get();
    let mut from = 0;
    while let Some(found) = text[from..].find("https://") {
        let start = from + found;
        match pull_request_url_end(text.as_bytes(), start) {
            Some(end) => {
                let url = &text[start..end];
                if seen.insert(url) {
                    urls.push(url.to_string());
                }
                from = end;
            }
            None => from = start + 1,
        }
    }
    urls
}

fn shell_command(tool_input: Raw<'_>) -> String {
    let Some(object) = parse_object::<BTreeMap<String, &RawValue>>(tool_input) else {
        return String::new();
    };
    for key in ["command", "cmd"] {
        let Some(&raw) = object.get(key) else {
            continue;
        };
        if let Some(text) = strict_text(Some(raw)) {
            return text;
        }
        if raw.get().starts_with('[') {
            let parts: Option<Vec<String>> = array_items(Some(raw))
                .into_iter()
                .map(|item| strict_text(Some(item)))
                .collect();
            if let Some(parts) = parts {
                return parts.join(" ");
            }
        }
    }
    String::new()
}

fn is_word(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || byte == b'_'
}

fn is_space(byte: u8) -> bool {
    matches!(byte, b'\t' | b'\n' | 0x0c | b'\r' | b' ')
}

fn skip_spaces(bytes: &[u8], at: usize) -> Option<usize> {
    let count = bytes[at..].iter().take_while(|&&b| is_space(b)).count();
    (count > 0).then_some(at + count)
}

fn has_gh_pr_create(command: &str) -> bool {
    let bytes = command.as_bytes();
    let word_at = |at: usize, word: &[u8]| {
        bytes
            .get(at..at + word.len())
            .filter(|found| *found == word)
            .map(|_| at + word.len())
    };
    (0..bytes.len()).any(|start| {
        if start > 0 && is_word(bytes[start - 1]) {
            return false;
        }
        word_at(start, b"gh")
            .and_then(|at| skip_spaces(bytes, at))
            .and_then(|at| word_at(at, b"pr"))
            .and_then(|at| skip_spaces(bytes, at))
            .and_then(|at| word_at(at, b"create"))
            .is_some_and(|end| bytes.get(end).is_none_or(|&b| !is_word(b)))
    })
}

fn is_host_byte(b: u8) -> bool {
    b.is_ascii_alphanumeric() || b == b'.' || b == b'-'
}

fn is_segment_byte(b: u8) -> bool {
    b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-')
}

fn pull_request_url_end(bytes: &[u8], start: usize) -> Option<usize> {
    let run = |at: usize, allowed: fn(u8) -> bool| {
        bytes[at..].iter().take_while(|&&b| allowed(b)).count()
    };
    let slash = |at: usize, literal: &[u8]| {
        bytes
            .get(at..at + literal.len())
            .filter(|found| *found == literal)
            .map(|_| at + literal.len())
    };
    let host_start = start + "https://".len();
    let first = *bytes.get(host_start)?;
    if !first.is_ascii_alphanumeric() {
        return None;
    }
    let mut at = slash(host_start + 1 + run(host_start + 1, is_host_byte), b"/")?;
    for _ in 0..2 {
        let length = run(at, is_segment_byte);
        if length == 0 {
            return None;
        }
        at = slash(at + length, b"/")?;
    }
    at = slash(at, b"pull/")?;
    let digits = bytes[at..]
        .iter()
        .take_while(|b| b.is_ascii_digit())
        .count();
    (digits > 0).then_some(at + digits)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::path::Path;

    fn raw(json: &str) -> Box<RawValue> {
        RawValue::from_string(json.to_string()).unwrap()
    }

    fn fixture(name: &str) -> String {
        fs::read_to_string(
            Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("testdata/hooks")
                .join(name),
        )
        .unwrap()
    }

    fn tools(tool_name: &str) -> &'static Tools {
        crate::hooks::tools_for(tool_name).unwrap_or(&NO_TOOLS)
    }

    static NO_TOOLS: Tools = Tools {
        edits: &[],
        patches: &[],
        sends: &[],
        shell: |_| false,
    };

    #[derive(Deserialize)]
    struct MarkdownEditCase<'a> {
        name: String,
        #[serde(default = "seen_by_hooks")]
        hooks: bool,
        tool_name: String,
        #[serde(borrow)]
        tool_input: &'a RawValue,
        cwd: String,
        #[serde(default)]
        home: String,
        want: Vec<String>,
    }

    fn seen_by_hooks() -> bool {
        true
    }

    #[test]
    fn markdown_edits_against_the_shared_corpus() {
        let corpus = fixture("markdown-edits.json");
        let cases: Vec<MarkdownEditCase<'_>> = serde_json::from_str(&corpus).unwrap();
        assert!(!cases.is_empty());
        for case in cases {
            let got = markdown_edits(
                tools(&case.tool_name),
                &case.tool_name,
                Some(case.tool_input),
                &case.cwd,
                &case.home,
            );
            let want: &[String] = if case.hooks { &case.want } else { &[] };
            assert_eq!(got, want, "{}", case.name);
        }
    }

    #[test]
    fn sent_files_cases() {
        let cases: [(&str, &str, &str, &str, &[&str]); 7] = [
            (
                "absolute",
                "SendUserFile",
                r#"{"files": ["/repo/docs/plan.md", "/repo/report.html"]}"#,
                "/repo",
                &["/repo/docs/plan.md", "/repo/report.html"],
            ),
            (
                "relative resolves",
                "SendUserFile",
                r#"{"files": ["docs/plan.md"]}"#,
                "/repo",
                &["/repo/docs/plan.md"],
            ),
            (
                "relative without cwd",
                "SendUserFile",
                r#"{"files": ["docs/plan.md"]}"#,
                "",
                &[],
            ),
            (
                "duplicates and blanks",
                "SendUserFile",
                r#"{"files": ["/a.md", "  ", "/a.md", "./a.md"]}"#,
                "/",
                &["/a.md"],
            ),
            (
                "other tool",
                "Write",
                r#"{"files": ["/a.md"]}"#,
                "/repo",
                &[],
            ),
            (
                "malformed",
                "SendUserFile",
                r#"{"files": "not-an-array"}"#,
                "/repo",
                &[],
            ),
            ("empty", "SendUserFile", r#"{"files": []}"#, "/repo", &[]),
        ];
        for (name, tool, input, cwd, want) in cases {
            let input = raw(input);
            assert_eq!(
                sent_files(tools(tool), tool, Some(&input), cwd),
                want,
                "{name}"
            );
        }
    }

    #[derive(Deserialize)]
    struct PullRequestCase<'a> {
        name: String,
        tool_name: String,
        #[serde(borrow)]
        tool_input: &'a RawValue,
        #[serde(borrow)]
        tool_output: &'a RawValue,
        want: Vec<String>,
    }

    #[test]
    fn pull_requests_against_the_shared_corpus() {
        let corpus = fixture("pull-request-extraction.json");
        let cases: Vec<PullRequestCase<'_>> = serde_json::from_str(&corpus).unwrap();
        assert!(!cases.is_empty());
        for case in cases {
            let got = pull_requests_created(
                tools(&case.tool_name),
                &case.tool_name,
                Some(case.tool_input),
                Some(case.tool_output),
            );
            assert_eq!(got, case.want, "{}", case.name);
        }
    }

    #[test]
    fn pull_request_without_a_tool_response() {
        let input = raw(r#"{"command":"gh pr create --fill"}"#);
        assert_eq!(
            pull_requests_created(tools("Bash"), "Bash", Some(&input), None),
            Vec::<String>::new()
        );
    }

    #[derive(Deserialize)]
    #[allow(
        clippy::struct_field_names,
        reason = "named after the hook's JSON fields"
    )]
    struct CapturedPayload<'a> {
        tool_name: String,
        #[serde(borrow)]
        tool_input: &'a RawValue,
        #[serde(borrow)]
        tool_response: &'a RawValue,
    }

    #[test]
    fn pull_requests_against_captured_codex_payloads() {
        let captured = fixture("codex-post-tool-use.jsonl");
        let lines: Vec<&str> = captured.lines().collect();
        assert_eq!(lines.len(), 2);
        for line in lines {
            let payload: CapturedPayload<'_> = serde_json::from_str(line).unwrap();
            assert_eq!(payload.tool_name, "Bash");
            assert_eq!(
                pull_requests_created(
                    tools(&payload.tool_name),
                    &payload.tool_name,
                    Some(payload.tool_input),
                    Some(payload.tool_response)
                ),
                Vec::<String>::new()
            );

            let gh_create = raw(r#"{"command":"gh pr create --fill"}"#);
            let created = pull_requests_created(
                tools(&payload.tool_name),
                &payload.tool_name,
                Some(&gh_create),
                Some(payload.tool_response),
            );
            if payload.tool_response.get().contains("/pull/71") {
                assert_eq!(created, ["https://github.com/victorarias/attn/pull/71"]);
            } else {
                assert_eq!(created, Vec::<String>::new());
            }
        }
    }
}
