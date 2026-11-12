use std::path::{Path, PathBuf};

use attn_automation::Hooks;
use attn_client::Connection;
use rune::Tx;
use serde_json::{Value, json};

use crate::{Action, State, dispatch};

/// What attn adds to the automation protocol: ready once connected, its actions by shortcut
/// id, and the slices of the store and the window a test reads.
pub struct AppHooks;

impl Hooks<State> for AppHooks {
    fn ready(&self, state: &State) -> bool {
        state.facts.connection == Connection::Connected
    }

    fn run(&self, state: &mut State, name: &str, tx: &mut Tx) -> Result<(), String> {
        let action = Action::from_id(name).ok_or_else(|| {
            format!(
                "no action has this id; ids are the keymap's, such as {:?}",
                Action::SessionToggleSidebar.id()
            )
        })?;
        dispatch(state, action, tx);
        Ok(())
    }

    fn state(&self, state: &State, of: &str) -> Result<Value, String> {
        let facts = &state.facts;
        let window = &state.window;
        Ok(match of {
            "connection" => json!(format!("{:?}", facts.connection)),
            "sessions" => json!(*facts.sessions),
            "desktops" => json!(*facts.desktops),
            "profiles" => json!({"selected": facts.profiles.selected, "all": facts.profiles.all}),
            "window" => json!({
                "sidebar_hidden": window.sidebar_hidden,
                "drawer": window.drawer.map(|d| format!("{d:?}")),
                "layer": window.layer.map(|l| format!("{l:?}")),
                "keyboard": window.keyboard.as_ref().map(|h| json!({"desktop": h.desktop, "pane": h.pane})),
                "notice": window.notice.as_ref().map(|n| n.text.clone()),
            }),
            "build" => build_identity()?,
            _ => {
                return Err(
                    "slices: connection, sessions, desktops, profiles, window, build".into(),
                );
            }
        })
    }
}

/// The build stamp the install wrote beside the executable, which the scenario runner
/// compares with the checkout.
fn build_identity() -> Result<Value, String> {
    let exe = std::env::current_exe().map_err(|e| format!("locate this executable: {e}"))?;
    let dir = exe.parent().unwrap_or(Path::new("."));
    let path = [dir.join("../Resources/build-identity.json"), dir.join("../resources/build-identity.json")]
        .into_iter()
        .find(|p| p.is_file())
        .ok_or_else(|| format!("no build-identity.json beside {}; install the app with `make install INSTANCE=<name> APP=native`", exe.display()))?;
    let text =
        std::fs::read_to_string(&path).map_err(|e| format!("read {}: {e}", path.display()))?;
    serde_json::from_str(&text).map_err(|e| format!("parse {}: {e}", path.display()))
}

/// On for named instances, as the Tauri app's server is; `ATTN_AUTOMATION=1` forces it on
/// and `0` off.
pub fn enabled(instance: &str) -> bool {
    match std::env::var("ATTN_AUTOMATION").as_deref() {
        Ok("1") => true,
        Ok("0") => false,
        _ => !instance.is_empty(),
    }
}

/// Where the Tauri app writes its manifest for this bundle id, so the runner finds either
/// shell's server in one place.
pub fn manifest_path(bundle_id: &str) -> Option<PathBuf> {
    let home = std::env::home_dir()?;
    let local = if cfg!(target_os = "macos") {
        home.join("Library/Application Support")
    } else {
        std::env::var_os("XDG_DATA_HOME").map_or_else(|| home.join(".local/share"), PathBuf::from)
    };
    Some(local.join(bundle_id).join("debug/ui-automation.json"))
}
