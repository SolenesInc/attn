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
    command: Raw<'a>,
    #[serde(borrow, default)]
    input: Raw<'a>,
}

#[derive(Deserialize)]
struct SendFilesInput<'a> {
    #[serde(borrow, default)]
    files: Raw<'a>,
}

pub fn markdown_edits(tool_name: &str, tool_input: Raw<'_>, cwd: &str) -> Vec<String> {
    let paths = match tool_name {
        "Edit" | "Write" | "MultiEdit" | "NotebookEdit" => {
            edited_file_paths(tool_input).unwrap_or_default()
        }
        "apply_patch" => patched_paths(tool_input).unwrap_or_default(),
        _ => return Vec::new(),
    };
    let markdown = paths
        .into_iter()
        .filter(|path| is_markdown(path.trim()))
        .collect();
    resolve_unique(markdown, cwd)
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
    let command = strict_text(input.command)?;
    let input = strict_text(input.input)?;
    let patch = if command.trim().is_empty() {
        input
    } else {
        command
    };
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

pub fn sent_files(tool_name: &str, tool_input: Raw<'_>, cwd: &str) -> Vec<String> {
    if tool_name != "SendUserFile" {
        return Vec::new();
    }
    sent_file_paths(tool_input)
        .map(|paths| resolve_unique(paths, cwd))
        .unwrap_or_default()
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
    tool_name: &str,
    tool_input: Raw<'_>,
    tool_response: Raw<'_>,
) -> Vec<String> {
    if !is_shell_tool(tool_name) || !has_gh_pr_create(&shell_command(tool_input)) {
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

fn is_shell_tool(name: &str) -> bool {
    let name = name.trim().to_lowercase();
    name == "bash" || name == "shell" || name == "exec_command" || name.ends_with(".exec_command")
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

    #[test]
    fn markdown_edits_cases() {
        let cases: [(&str, &str, &str, &str, &[&str]); 13] = [
            (
                "claude edit",
                "Edit",
                r#"{"file_path":"/repo/notes.md","old_string":"old"}"#,
                "",
                &["/repo/notes.md"],
            ),
            (
                "claude write",
                "Write",
                r##"{"file_path":"/repo/extra.md","content":"# Extra\n"}"##,
                "",
                &["/repo/extra.md"],
            ),
            (
                "notebook",
                "NotebookEdit",
                r#"{"notebook_path":"/repo/analysis.md","new_source":"x"}"#,
                "",
                &["/repo/analysis.md"],
            ),
            (
                "several patched files",
                "apply_patch",
                r#"{"command":"*** Begin Patch\n*** Update File: /repo/notes.md\n@@\n-old line\n+new line\n*** Add File: /repo/extra.md\n+# Extra\n*** End Patch"}"#,
                "",
                &["/repo/notes.md", "/repo/extra.md"],
            ),
            (
                "rename at destination",
                "apply_patch",
                r#"{"command":"*** Begin Patch\n*** Update File: /repo/old.md\n*** Move to: /repo/new.md\n@@\n-a\n+b\n*** End Patch"}"#,
                "",
                &["/repo/new.md"],
            ),
            (
                "deletion",
                "apply_patch",
                r#"{"command":"*** Begin Patch\n*** Delete File: /repo/gone.md\n*** End Patch"}"#,
                "",
                &[],
            ),
            (
                "relative resolves",
                "Write",
                r#"{"file_path":"docs/plan.md"}"#,
                "/repo",
                &["/repo/docs/plan.md"],
            ),
            (
                "relative without cwd",
                "Write",
                r#"{"file_path":"docs/plan.md"}"#,
                "",
                &[],
            ),
            (
                "source file",
                "Edit",
                r#"{"file_path":"/repo/main.go"}"#,
                "",
                &[],
            ),
            (
                "shell rewrite",
                "Bash",
                r#"{"command":"sed -i '' s/a/b/ notes.md"}"#,
                "",
                &[],
            ),
            ("read", "Read", r#"{"file_path":"/repo/notes.md"}"#, "", &[]),
            (
                "same file twice",
                "apply_patch",
                r#"{"command":"*** Update File: /repo/notes.md\n*** Update File: /repo/notes.md"}"#,
                "",
                &["/repo/notes.md"],
            ),
            ("malformed", "Edit", r#""not an object""#, "", &[]),
        ];
        for (name, tool, input, cwd, want) in cases {
            let input = raw(input);
            assert_eq!(markdown_edits(tool, Some(&input), cwd), want, "{name}");
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
            assert_eq!(sent_files(tool, Some(&input), cwd), want, "{name}");
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
        assert!(pull_requests_created("Bash", Some(&input), None).is_empty());
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
            assert!(
                pull_requests_created(
                    &payload.tool_name,
                    Some(payload.tool_input),
                    Some(payload.tool_response)
                )
                .is_empty()
            );

            let gh_create = raw(r#"{"command":"gh pr create --fill"}"#);
            let created = pull_requests_created(
                &payload.tool_name,
                Some(&gh_create),
                Some(payload.tool_response),
            );
            if payload.tool_response.get().contains("/pull/71") {
                assert_eq!(created, ["https://github.com/victorarias/attn/pull/71"]);
            } else {
                assert!(created.is_empty());
            }
        }
    }
}
