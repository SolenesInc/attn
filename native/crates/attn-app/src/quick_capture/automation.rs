//! What the panel's automation server reads of it and does to it: the `capture` slice, as the
//! Tauri app's `capture_state` names its fields where both have them, and `dismiss`.

use attn_automation::Hooks;
use rune::Tx;
use serde_json::{Value, json};

use super::attach::Phase;
use super::{Busy, Face, Panel};

pub struct CaptureHooks;

impl Hooks<Panel> for CaptureHooks {
    fn ready(&self, panel: &Panel) -> bool {
        panel.connected() && panel.draft.restored
    }

    fn run(&self, panel: &mut Panel, name: &str, _: &mut Tx) -> Result<(), String> {
        match name {
            "dismiss" => {
                panel.hide();
                Ok(())
            }
            _ => Err("names: dismiss".into()),
        }
    }

    fn state(&self, panel: &mut Panel, of: &str) -> Result<Option<Value>, String> {
        match of {
            "capture" => Ok(Some(capture(panel))),
            _ => Err("slices: capture".into()),
        }
    }
}

fn capture(panel: &Panel) -> Value {
    let files: Vec<Value> = panel
        .draft
        .files
        .iter()
        .map(|file| {
            let media_type = match &file.phase {
                Phase::Stored { media_type, .. } => Some(media_type.clone()),
                Phase::Copying => None,
            };
            json!({"name": file.name, "mediaType": media_type, "ready": file.ready()})
        })
        .collect();
    let images = panel
        .draft
        .files
        .iter()
        .filter(|f| matches!(&f.phase, Phase::Stored { media_type, .. } if media_type.starts_with("image/")))
        .count();
    let settled =
        panel.busy == Busy::Idle && !panel.copying() && panel.draft.files.iter().all(|f| f.ready());
    let selected = panel.history.selected.as_deref();
    let recent: Vec<Value> = panel
        .history
        .ordered()
        .into_iter()
        .map(|record| {
            json!({
                "captureId": record.id,
                "mailbox": record.mailbox.member_id,
                "text": record.content,
                "files": record.attachments.iter().map(|a| &a.name).collect::<Vec<_>>(),
                "read": panel.read_at(record).is_some(),
                "selected": selected == Some(record.id.as_str()),
            })
        })
        .collect();
    json!({
        "visible": panel.shown,
        "nativeFocused": panel.shown && panel.key_window,
        "focused": panel.shown && panel.key_window && panel.draft.text.focused,
        "text": panel.draft.text.text,
        "composing": panel.draft.text.is_composing(),
        "mailbox": panel.mailbox(),
        "view": match panel.face {
            Face::Compose => "compose",
            Face::Recent => "recent",
        },
        "files": files,
        "attachmentCount": panel.draft.files.len(),
        "imageCount": images,
        "settled": settled,
        "fontScale": crate::settings::text_scale(&panel.facts.settings),
        "error": panel.error,
        "recentRows": recent,
    })
}
