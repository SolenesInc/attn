use std::path::{Path, PathBuf};

use attn_automation::Hooks;
use attn_client::Connection;
use rune::Tx;
use serde_json::{Value, json};

use crate::{Action, State, dispatch};

/// The `run` name that binds Quick Capture's system-wide shortcut to what follows it, as
/// `capture.shortcut=Control+Alt+KeyB`, or turns it off when nothing does.
const CAPTURE_SHORTCUT: &str = "capture.shortcut=";

/// What attn adds to the automation protocol: ready once connected, its actions by shortcut
/// id, and the slices of the store and the window a test reads.
pub struct AppHooks;

impl Hooks<State> for AppHooks {
    fn ready(&self, state: &State) -> bool {
        state.facts.connection == Connection::Connected
    }

    fn run(&self, state: &mut State, name: &str, tx: &mut Tx) -> Result<(), String> {
        if let Some(binding) = name.strip_prefix(CAPTURE_SHORTCUT) {
            let binding = (!binding.is_empty()).then(|| binding.to_owned());
            return crate::quick_capture::shortcut::choose(state, binding);
        }
        let action = Action::from_id(name).ok_or_else(|| {
            format!(
                "no action has this id; ids are the keymap's, such as {:?}",
                Action::SessionToggleSidebar.id()
            )
        })?;
        dispatch(state, action, tx);
        Ok(())
    }

    fn state(&self, state: &mut State, of: &str) -> Result<Option<Value>, String> {
        let facts = &state.facts;
        let window = &state.window;
        if let Some(id) = of.strip_prefix("terminal:") {
            return Ok(state.terminals.drawn_text(id).map(|rows| json!(rows)));
        }
        if let Some(id) = of.strip_prefix("terminal-view:") {
            return Ok(state.terminals.view_state(id));
        }
        Ok(Some(match of {
            "connection" => json!(format!("{:?}", facts.connection)),
            "sessions" => json!(*facts.sessions),
            "desktops" => json!(*facts.desktops),
            "profiles" => json!({"selected": facts.profiles.selected, "all": facts.profiles.all}),
            "window" => json!({
                "view": format!("{:?}", window.view),
                "on_screen": window.on_screen(facts),
                "sidebar_hidden": window.sidebar_hidden,
                "drawers": window.drawers.iter().map(|d| format!("{d:?}")).collect::<Vec<_>>(),
                "layer": window.layer.map(|l| format!("{l:?}")),
                "keyboard": window.keyboard.as_ref().map(|h| json!({"desktop": h.desktop, "pane": h.pane})),
                "notice": window.notice.as_ref().map(|n| n.text.clone()),
            }),
            "settings" => crate::settings::automation_state(state),
            "capture-shortcut" => json!({
                "binding": state.capture_shortcut.active.clone().unwrap_or_default(),
                "error": state.capture_shortcut.error,
            }),
            "build" => build_identity()?,
            _ => {
                return Err("slices: connection, sessions, desktops, profiles, window, settings, capture-shortcut, build, terminal:<runtime id>, terminal-view:<runtime id>".into());
            }
        }))
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

/// Where the Quick Capture panel's automation server writes its manifest, beside the app's.
pub fn capture_manifest_path(bundle_id: &str) -> Option<PathBuf> {
    manifest_path(bundle_id).map(|main| main.with_file_name("ui-automation-capture.json"))
}

/// Where the Tauri app writes its manifest for this bundle id, so the runner finds either
/// shell's server in one place.
pub fn manifest_path(bundle_id: &str) -> Option<PathBuf> {
    crate::instance::debug_dir(bundle_id).map(|dir| dir.join("ui-automation.json"))
}
