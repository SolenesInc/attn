use std::cell::RefCell;
use std::path::PathBuf;
use std::process::Command;

use rune::Tx;

use crate::State;

/// A shell command line the app ran to open a folder in the user's editor, and the folder it
/// ran in.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct EditorLaunch {
    pub command: String,
    pub cwd: PathBuf,
}

thread_local! {
    static RECORDED: RefCell<Option<Vec<EditorLaunch>>> = const { RefCell::new(None) };
}

/// Keeps the editors this thread would start instead of starting them, as a test run does.
pub fn record_editor_launches() {
    RECORDED.set(Some(Vec::new()));
}

/// The editors this thread kept since the last call.
pub fn take_editor_launches() -> Vec<EditorLaunch> {
    RECORDED.with_borrow_mut(|r| r.as_mut().map(std::mem::take).unwrap_or_default())
}

/// Why "Open in editor" has nothing to open for `session`, when it has nothing.
pub(crate) fn unavailable(session: Option<&attn_protocol::Session>) -> Option<&'static str> {
    match session {
        None => Some("No active session"),
        Some(s) if s.directory.trim().is_empty() => Some("No session folder"),
        Some(_) => None,
    }
}

/// Opens the folder of agent `session` in the user's editor, or tells the user why not.
pub(crate) fn open_session(state: &mut State, session: &str, tx: &mut Tx) {
    let session = state.facts.sessions.iter().find(|s| s.id == session);
    if let Some(reason) = unavailable(session) {
        state.window.notify(reason.to_owned(), tx);
        return;
    }
    let session = session.expect("unavailable without a session");
    let folder = session.directory.trim().to_owned();
    let opened = match session.host.as_deref().filter(|h| !h.is_empty()) {
        Some(host) => Err(format!(
            "{folder} is on {}; opening files on an outpost isn't supported yet",
            crate::desktop::outpost_name(host, &state.facts.outposts)
        )),
        None => launch(
            state
                .facts
                .settings
                .get(crate::settings::EDITOR)
                .map(String::as_str),
            folder,
        ),
    };
    if let Err(message) = opened {
        state.window.notify(message, tx);
    }
}

/// Runs `editor` on folder `cwd` through the login shell, as the Tauri app does: the setting,
/// else `$EDITOR`, else `$VISUAL`.
fn launch(editor: Option<&str>, cwd: String) -> Result<(), String> {
    let editor = editor
        .map(str::trim)
        .filter(|e| !e.is_empty())
        .map(str::to_owned)
        .or_else(|| std::env::var("EDITOR").ok())
        .or_else(|| std::env::var("VISUAL").ok())
        .ok_or_else(|| "EDITOR (or VISUAL) is not set".to_owned())?;
    let cwd = PathBuf::from(cwd);
    if !cwd.exists() {
        return Err(format!("Directory does not exist: {}", cwd.display()));
    }
    let launch = EditorLaunch {
        command: format!("{editor} ."),
        cwd,
    };
    if RECORDED.with_borrow_mut(|r| r.as_mut().map(|r| r.push(launch.clone())).is_some()) {
        return Ok(());
    }
    let mut editor = Command::new("sh")
        .arg("-lc")
        .arg(&launch.command)
        .current_dir(&launch.cwd)
        .spawn()
        .map_err(|e| format!("Failed to open editor: {e}"))?;
    let _ = rune::task::spawn("editor", move || drop(editor.wait()));
    Ok(())
}
