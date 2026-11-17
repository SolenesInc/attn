//! Settings → Files and locations: the editor, the projects directory, the worktree sweep, the
//! Notebook folder and what attn does with a file an agent sends.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::{El, FileDialogOptions, Tx, col, row, text};

use super::autosave;
use super::pages::{
    Action, Tone, block, blocks, field_label, pill, row_card, setting_switch, wide_setting_field,
};
use crate::{State, setting};

pub(crate) const EDITOR: &str = "editor_executable";
pub(super) const PROJECTS_DIRECTORY: &str = "projects_directory";
pub(super) const NOTEBOOK_ROOT: &str = "notebook.root";
const NOTEBOOK_ROOT_EFFECTIVE: &str = "notebook.root.effective";
const WORKTREE_SWEEP: &str = "worktree_sweep_enabled";
const OPEN_SENT_FILES: &str = "open_sent_files_enabled";

/// Asks for a folder and saves it as `key`, as typing it and leaving the field would.
fn browse(key: &'static str, title: &'static str, tx: &mut Tx) {
    let options = FileDialogOptions {
        title: title.into(),
        ..FileDialogOptions::default()
    };
    tx.open_folder(options, move |state: &mut State, chosen, tx| match chosen {
        Ok(Some(folder)) => autosave::apply(state, key, &folder.to_string_lossy(), tx),
        Ok(None) => {}
        Err(why) => state
            .window
            .notify(format!("Could not ask for a folder: {why}"), tx),
    });
}

fn folder_field(
    state: &State,
    key: &'static str,
    label: &str,
    placeholder: &str,
    title: &'static str,
    t: &Tokens,
) -> El {
    let browse = Action::new("Browse")
        .on_press_tx(move |_, tx| browse(key, title, tx))
        .build(t)
        .key(format!("settings.browse.{key}"))
        .label("Browse");
    wide_setting_field(state, key, label, placeholder, None, Some(browse), t)
}

/// What a field is for, under its label, as the Tauri app's `.settings-status`.
fn field_status(said: &str, t: &Tokens) -> El {
    text(said.to_owned())
        .font(type_::MONO_CAPTION)
        .color(t.text_secondary)
        .wrap()
}

fn stacked(label: &str, status: Option<&str>, field: El, t: &Tokens) -> El {
    col()
        .w_full()
        .gap(7.0)
        .child(field_label(label, t))
        .children(status.map(|said| field_status(said, t)))
        .child(field)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let effective = state
        .facts
        .settings
        .get(NOTEBOOK_ROOT_EFFECTIVE)
        .filter(|root| !root.is_empty())
        .map(|root| {
            let line = format!("This profile uses: {root}");
            row()
                .key("settings.notebook.effective")
                .label(line)
                .flex_wrap()
                .items_center()
                .gaps(2.0, 4.0)
                .child(
                    text("This profile uses:")
                        .font(type_::BODY)
                        .color(t.text_tertiary)
                        .a11y_hidden(),
                )
                .child(
                    text(root.clone())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary)
                        .wrap()
                        .shrink()
                        .a11y_hidden(),
                )
        });
    blocks(
        vec![
            block(
                "editor",
                "Editor",
                "The editor used when opening files.",
                t,
                vec![stacked(
                    "Editor",
                    Some("Used when opening files"),
                    wide_setting_field(state, EDITOR, "Editor", "$EDITOR", None, None, t),
                    t,
                )],
            ),
            block(
                "projects-directory",
                "Projects Directory",
                "Directory where Git repositories are cloned and opened in worktrees.",
                t,
                vec![stacked(
                    "Projects directory",
                    None,
                    folder_field(
                        state,
                        PROJECTS_DIRECTORY,
                        "Projects directory",
                        "/Users/you/projects",
                        "Select Projects Directory",
                        t,
                    ),
                    t,
                )],
            ),
            block(
                "worktree-sweep",
                "Worktree sweep",
                "Reclaims worktrees whose work has landed. A worktree is only removed when it has been idle for 14 days, has no uncommitted or stashed changes, has nothing unpushed beyond what merged, holds no live session or open seed, and its branch is on the repository's integration branch — as a merged pull request, as an ancestor, or with an identical tree. Everything it keeps says why, and every removal lands in the sweep log and as a note on the seeds that worked there. On by default.",
                t,
                vec![row_card(
                    "Reclaim merged worktrees in the background",
                    "Turning it off stops removals; the daemon keeps refreshing worktree state so the Worktrees list in the ledger surface stays accurate, and the keep pins you set stay set.",
                    t,
                    setting_switch(
                        state,
                        WORKTREE_SWEEP,
                        "Reclaim merged worktrees in the background",
                        t,
                    ),
                )],
            ),
            block(
                "notebook-root",
                "Notebook Folder",
                "Where attn keeps your durable Notebook — dated journals, the knowledge base and seed files — as plain markdown you own. Each profile has its own folder, synced with outposts. Changing it points attn at the new folder; your existing notes are not moved, so move or sync the folder yourself if you want the current contents to come along.",
                t,
                std::iter::once(stacked(
                    "Notebook folder",
                    Some("Leave blank for ~/attn-notebook, separate per instance."),
                    folder_field(
                        state,
                        NOTEBOOK_ROOT,
                        "Notebook folder",
                        "~/attn-notebook",
                        "Select Notebook Folder",
                        t,
                    ),
                    t,
                ))
                .chain(effective)
                .collect(),
            ),
            block(
                "sent-files",
                "Sent files",
                "When an agent hands you a file with its send-file tool, attn opens the ones it can show as desktop tiles. On by default.",
                t,
                vec![row_card(
                    "Open files agents send you",
                    "A markdown file an agent sends opens as a live-reloading tile beside its terminal, so you see it without hunting through the transcript. File types attn cannot show are left alone.",
                    t,
                    setting_switch(state, OPEN_SENT_FILES, "Open files agents send you", t),
                )],
            ),
        ],
        t,
    )
}

/// Whether the projects directory field holds a path the daemon does not have yet, and
/// whether sent files open.
pub(super) fn pills(state: &State, t: &Tokens) -> Vec<El> {
    let saved = state
        .facts
        .settings
        .get(PROJECTS_DIRECTORY)
        .map_or("", String::as_str);
    let edited = state.window.settings.autosave.draft(PROJECTS_DIRECTORY) != saved;
    let open = setting::on(&state.facts.settings, OPEN_SENT_FILES);
    let (projects, projects_tone) = if edited {
        ("project path edited", Tone::Warn)
    } else {
        ("project path saved", Tone::Good)
    };
    let (sent, sent_tone) = if open {
        ("sent files open", Tone::Good)
    } else {
        ("sent files ignored", Tone::Plain)
    };
    vec![
        pill(
            "settings.pill.desktop.projects".into(),
            projects.into(),
            projects_tone,
            t,
        ),
        pill(
            "settings.pill.desktop.sent-files".into(),
            sent.into(),
            sent_tone,
            t,
        ),
    ]
}
