//! Choosing a folder for something the home reads: the system's dialog while the home runs on
//! this machine, and the launcher browsing the home's machine while it runs on another.

use rune::{FileDialogOptions, Tx};

use crate::State;
use crate::machine::Machines;

/// What a chosen folder becomes.
pub(crate) enum FolderFor {
    /// A setting, saved as typing it and leaving its field would.
    Setting {
        key: &'static str,
        title: &'static str,
    },
    PluginSource,
    /// The Chief's setup, its draft set aside while the launcher covers it.
    ChiefNotebook {
        draft: Option<Box<crate::chief::setup::Draft>>,
    },
    AutomationDirectory,
    EditorRoot {
        tile: String,
    },
}

impl FolderFor {
    pub(crate) fn title(&self) -> &str {
        match self {
            FolderFor::Setting { title, .. } => title,
            FolderFor::PluginSource => "Select Plugin Directory",
            FolderFor::ChiefNotebook { .. } => "Choose the Notebook folder",
            FolderFor::AutomationDirectory => "Choose a folder",
            FolderFor::EditorRoot { .. } => "Choose editor root",
        }
    }
}

/// Asks for a folder on the home's machine for `purpose`, starting at `start` when given.
pub(crate) fn choose(state: &mut State, purpose: FolderFor, start: Option<String>, tx: &mut Tx) {
    if Machines::of(&state.facts).on_app_machine(None) {
        let options = FileDialogOptions {
            title: purpose.title().into(),
            directory: start.map(Into::into),
            ..FileDialogOptions::default()
        };
        tx.open_folder(options, move |state: &mut State, folder, tx| match folder {
            Ok(Some(folder)) => {
                answered(
                    state,
                    purpose,
                    Some(folder.to_string_lossy().into_owned()),
                    tx,
                );
            }
            Ok(None) => {}
            Err(why) => state
                .window
                .notify(format!("Could not ask for a folder: {why}"), tx),
        });
    } else {
        let purpose = match purpose {
            FolderFor::ChiefNotebook { .. } => FolderFor::ChiefNotebook {
                draft: crate::chief::setup::set_aside(state),
            },
            other => other,
        };
        crate::launcher::choose_folder(state, purpose, start, tx);
    }
}

/// Answers what asked for a folder: with the folder on the home's machine, or with none when
/// the chooser closed without one.
pub(crate) fn answered(state: &mut State, purpose: FolderFor, folder: Option<String>, tx: &mut Tx) {
    if let FolderFor::ChiefNotebook { draft } = purpose {
        crate::chief::setup::notebook_chosen(state, draft, folder, tx);
        return;
    }
    let Some(folder) = folder else {
        return;
    };
    match purpose {
        FolderFor::Setting { key, .. } => crate::settings::autosave::apply(state, key, &folder, tx),
        FolderFor::PluginSource => crate::settings::plugin_source_chosen(state, folder),
        FolderFor::AutomationDirectory => {
            crate::automations::form::directory_chosen(state, folder);
        }
        FolderFor::EditorRoot { tile } => {
            crate::notebook::switch_root(state, &tile, Some(folder), tx);
        }
        FolderFor::ChiefNotebook { .. } => {}
    }
}
