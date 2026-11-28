mod help_generated;
mod table_generated;

use super::{Run, Stop, print_json, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use crate::paths::os_error_text;
use attn_protocol::{
    ViewDataAddResult, ViewDataListResult, ViewDataRemoveResult, ViewDataSetResult,
    ViewDeleteResult, ViewEditResult, ViewExportResult, ViewForkResult, ViewHideResult,
    ViewKeepResult, ViewListResult, ViewReadResult, ViewRestoreResult, ViewShowResult,
};
use std::fmt::Write as _;
use std::path::{Path, PathBuf};

const VIEW_FILE: &str = "view.md";

struct View;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &View, args)
}

impl Family for View {
    fn group(&self) -> &'static str {
        "view"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        help_generated::GROUP_HELP
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        if cmd.name != "export" {
            return command::print_result(run, self, cmd, &command::result(reply));
        }
        let exported = written_export(run, cmd, parsed, reply)?;
        let encoded = attn::go::json::encode(&exported).expect("a plain struct always encodes");
        print_json(&encoded);
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let out = match cmd.name {
            "list" => list(&command::decode(run, self, cmd, &result)?),
            "read" => read(&command::decode(run, self, cmd, &result)?),
            "export" => {
                let exported = written_export(run, cmd, parsed, reply)?;
                let folder = exported.folder.unwrap_or_default();
                format!(
                    "wrote version {} of {} to {folder}; edit it, keep the first line of view.md, then: attn view edit {} --from {folder}\n",
                    exported.version, exported.view, exported.view
                )
            }
            "keep" => {
                let kept: ViewKeepResult = command::decode(run, self, cmd, &result)?;
                format!("kept {} as version {}\n", kept.view, kept.version)
            }
            "edit" => {
                let edited: ViewEditResult = command::decode(run, self, cmd, &result)?;
                format!(
                    "saved {} as version {} (was {})\n",
                    edited.view, edited.version, edited.previous_version
                )
            }
            "restore" => {
                let restored: ViewRestoreResult = command::decode(run, self, cmd, &result)?;
                match (restored.version, restored.restored_from) {
                    (Some(version), Some(from)) => format!(
                        "saved {} as version {version}, restored from version {from}\n",
                        restored.view
                    ),
                    _ => format!("{} shows what attn ships again\n", restored.view),
                }
            }
            "fork" => {
                let forked: ViewForkResult = command::decode(run, self, cmd, &result)?;
                format!(
                    "kept {} as version {}, a copy of {}\n",
                    forked.view, forked.version, forked.from
                )
            }
            "delete" => {
                let deleted: ViewDeleteResult = command::decode(run, self, cmd, &result)?;
                let mut out = format!(
                    "deleted {} and closed {} tile(s)\n",
                    deleted.view,
                    deleted.closed.len()
                );
                for store in &deleted.stores {
                    let _ = writeln!(
                        out,
                        "deleted its store {} ({} item(s))",
                        store.set, store.items
                    );
                }
                out
            }
            name if name.starts_with("data ") => items(run, self, cmd, &result)?,
            "show" => {
                let shown: ViewShowResult = command::decode(run, self, cmd, &result)?;
                if shown.added {
                    format!("put {} on desktop {}\n", shown.view, shown.desktop_id)
                } else {
                    format!(
                        "{} is already on desktop {}\n",
                        shown.view, shown.desktop_id
                    )
                }
            }
            "hide" => {
                let hidden: ViewHideResult = command::decode(run, self, cmd, &result)?;
                format!(
                    "took {} off desktop {} ({} tile(s))\n",
                    hidden.view,
                    hidden.desktop_id,
                    hidden.removed.len()
                )
            }
            _ => return command::print_result(run, self, cmd, &result),
        };
        print_stdout(&out);
        Ok(())
    }
}

fn list(listed: &ViewListResult) -> String {
    let mut out = String::new();
    for view in &listed.views {
        let version = view
            .version
            .map_or_else(|| "attn's own".to_string(), |v| format!("version {v}"));
        let shown: Vec<&str> = view.shown.iter().map(|s| s.desktop.as_str()).collect();
        let shown = if shown.is_empty() {
            "not shown".to_string()
        } else {
            format!("on {}", shown.join(", "))
        };
        let _ = writeln!(out, "{}  {version}  {shown}", view.name);
        for v in view.versions.iter().flatten() {
            let mut line = format!("  {}  {}", v.version, v.saved_at);
            if let Some(from) = v.restored_from {
                let _ = write!(line, "  restored from {from}");
            }
            if let Some(note) = v.note.as_deref().filter(|n| !n.is_empty()) {
                let _ = write!(line, "  {note}");
            }
            out.push_str(&line);
            out.push('\n');
        }
    }
    out
}

fn item_id(item: &serde_json::Value) -> &str {
    item.get("id")
        .and_then(serde_json::Value::as_str)
        .unwrap_or_default()
}

fn read(view: &ViewReadResult) -> String {
    let mut out = view.text.clone();
    if !out.ends_with('\n') {
        out.push('\n');
    }
    for problem in &view.problems {
        let _ = writeln!(
            out,
            "problem: line {}: {} {}",
            problem.line, problem.text, problem.fix
        );
    }
    for command in &view.commands {
        let _ = writeln!(out, "command: {} ({:?})", command.line, command.state);
    }
    out
}

fn written_export(
    run: &mut Run<'_>,
    cmd: &Command,
    parsed: &Parsed,
    reply: &Reply,
) -> Result<ViewExportResult, Stop> {
    let mut exported: ViewExportResult = command::decode(run, &View, cmd, &command::result(reply))?;
    let to = parsed.text("to").trim().to_string();
    let folder = PathBuf::from(if to.is_empty() {
        exported.view.clone()
    } else {
        to
    });
    let replaced: Vec<String> = exported
        .files
        .iter()
        .filter(|(name, text)| !holds_export(&folder.join(name), name, text))
        .map(|(name, _)| folder.join(name).display().to_string())
        .collect();
    if !replaced.is_empty() {
        return Err(View.fail(
            "export",
            &format!(
                "wrote nothing: writing would lose the other text these files hold: {}; export into another folder with --to <dir>",
                replaced.join(", ")
            ),
        ));
    }
    for (name, text) in &exported.files {
        write_file(&folder.join(name), text.as_bytes()).map_err(|message| {
            View.fail(
                "export",
                &format!("writing {}: {message}", folder.join(name).display()),
            )
        })?;
    }
    let folder = std::path::absolute(&folder).unwrap_or(folder);
    exported.folder = Some(folder.display().to_string());
    Ok(exported)
}

fn holds_export(path: &Path, name: &str, text: &str) -> bool {
    let Ok(held) = std::fs::read(path) else {
        return !path.exists();
    };
    let Ok(held) = String::from_utf8(held) else {
        return false;
    };
    held == text
        || (name == VIEW_FILE
            && below_stamp(&held).is_some_and(|rest| below_stamp(text) == Some(rest)))
}

fn below_stamp(view: &str) -> Option<&str> {
    let (first, rest) = view.split_once('\n')?;
    let words = first.trim().strip_prefix("<!--")?.strip_suffix("-->")?;
    let mut words = words.split_whitespace();
    (words.next() == Some("attn-view")).then_some(rest)
}

fn write_file(path: &Path, contents: &[u8]) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).map_err(|error| os_error_text(&error))?;
    }
    std::fs::write(path, contents).map_err(|error| os_error_text(&error))
}

/// What a command on a kept view's items prints.
fn items(run: &mut Run<'_>, view: &View, cmd: &Command, result: &Reply) -> Result<String, Stop> {
    Ok(match cmd.name {
        "data list" => {
            let listed: ViewDataListResult = command::decode(run, view, cmd, result)?;
            let mut out = String::new();
            for item in &listed.items {
                let encoded = attn::go::json::encode(item).expect("a decoded value always encodes");
                out.push_str(&String::from_utf8_lossy(&encoded));
                out.push('\n');
            }
            out
        }
        "data add" => {
            let added: ViewDataAddResult = command::decode(run, view, cmd, result)?;
            format!(
                "added {} to {}/{}\n",
                item_id(&added.item),
                added.view,
                added.set
            )
        }
        "data set" => {
            let set: ViewDataSetResult = command::decode(run, view, cmd, result)?;
            format!(
                "changed {} in {}/{}\n",
                item_id(&set.item),
                set.view,
                set.set
            )
        }
        "data remove" => {
            let removed: ViewDataRemoveResult = command::decode(run, view, cmd, result)?;
            format!(
                "removed {} from {}/{}\n",
                removed.id, removed.view, removed.set
            )
        }
        _ => unreachable!("{} is not a command on a view's items", cmd.name),
    })
}
