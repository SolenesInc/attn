//! Settings → Files and locations: the editor, the projects directory, the worktree sweep, the
//! Notebook folder and what attn does with a file an agent sends.

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Prose, Section, SettingRow};
use attn_ui::system::{Button, Tone, tag};
use rune::{El, Tx};

use super::controls::{SettingField, setting_switch};
use crate::{State, setting};

pub(crate) const EDITOR: &str = "editor_executable";
pub(super) const PROJECTS_DIRECTORY: &str = "projects_directory";
pub(super) const NOTEBOOK_ROOT: &str = "notebook.root";
const NOTEBOOK_ROOT_EFFECTIVE: &str = "notebook.root.effective";
const WORKTREE_SWEEP: &str = "worktree_sweep_enabled";
const OPEN_SENT_FILES: &str = "open_sent_files_enabled";

fn browse_button(key: &'static str, title: &'static str, t: &Tokens) -> El {
    Button::new("Browse")
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            crate::folders::choose(
                state,
                crate::folders::FolderFor::Setting { key, title },
                None,
                tx,
            )
        })
        .build(t)
        .key(format!("settings.browse.{key}"))
        .label("Browse")
        .into()
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let effective = state
        .facts
        .settings
        .get(NOTEBOOK_ROOT_EFFECTIVE)
        .filter(|root| !root.is_empty())
        .map(|root| {
            Prose::hint("settings.notebook.effective", "This profile uses:")
                .code(root.clone())
                .build(t)
        });
    vec![
        Section::new("settings.block.locations", "Locations")
            .description("Where attn opens files, clones repositories and keeps your Notebook.")
            .push(
                SettingField::new(EDITOR, "Editor")
                    .placeholder("$EDITOR")
                    .hint("The editor used when opening files.")
                    .build(state, t),
            )
            .push(
                SettingField::new(PROJECTS_DIRECTORY, "Projects directory")
                    .placeholder("/Users/you/projects")
                    .hint("Where Git repositories are cloned and opened in worktrees.")
                    .action(browse_button(PROJECTS_DIRECTORY, "Select Projects Directory", t))
                    .build(state, t),
            )
            .push(
                SettingField::new(NOTEBOOK_ROOT, "Notebook folder")
                    .placeholder("~/attn-notebook")
                    .hint("Your durable Notebook: dated journals, the knowledge base and seed files, as plain markdown you own. Each profile has its own folder, synced with outposts. Changing it points attn at the new folder; your existing notes are not moved, so move or sync the folder yourself to bring them along. Leave blank for ~/attn-notebook, separate per instance.")
                    .action(browse_button(NOTEBOOK_ROOT, "Select Notebook Folder", t))
                    .build(state, t),
            )
            .push_all(effective)
            .build(t),
        Section::new("settings.block.worktree-sweep", "Worktree sweep")
            .description("Reclaims worktrees whose work has landed. A worktree is only removed when it has been idle for 14 days, has no uncommitted or stashed changes, has nothing unpushed beyond what merged, holds no live session or open seed, and its branch is on the repository's integration branch — as a merged pull request, as an ancestor, or with an identical tree. Everything it keeps says why, and every removal lands in the sweep log and as a note on the seeds that worked there. On by default.")
            .push(
                SettingRow::new(
                    "settings.row.worktree-sweep",
                    "Reclaim merged worktrees in the background",
                    "Turning it off stops removals; the daemon keeps refreshing worktree state so the Worktrees list in the ledger surface stays accurate, and the keep pins you set stay set.",
                    setting_switch(
                        state,
                        WORKTREE_SWEEP,
                        "Reclaim merged worktrees in the background",
                        t,
                    ),
                )
                .build(t),
            )
            .build(t),
        Section::new("settings.block.sent-files", "Sent files")
            .description("When an agent hands you a file with its send-file tool, attn opens the ones it can show as desktop tiles. On by default.")
            .push(
                SettingRow::new(
                    "settings.row.sent-files",
                    "Open files agents send you",
                    "A markdown file an agent sends opens as a live-reloading tile beside its terminal, so you see it without hunting through the transcript. File types attn cannot show are left alone.",
                    setting_switch(state, OPEN_SENT_FILES, "Open files agents send you", t),
                )
                .build(t),
            )
            .build(t),
    ]
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
        ("sent files ignored", Tone::Neutral)
    };
    vec![
        El::from(tag(
            "settings.pill.desktop.projects",
            projects,
            projects_tone,
            t,
        ))
        .role("status"),
        El::from(tag("settings.pill.desktop.sent-files", sent, sent_tone, t)).role("status"),
    ]
}
