use rune::{KeyPlatform, PopupItem, PopupMenu, Shortcut, Tx};

use crate::State;
use crate::terminal::Part;

/// Opens the terminal's context menu at `at` in terminal `id`, as the Tauri terminal does on a
/// right-click its program does not take: the block under the pointer, its clipboard, and its
/// edges.
pub(super) fn open(state: &mut State, id: &str, at: (f32, f32), window: [f32; 2], tx: &mut Tx) {
    let Some(menu) = state.terminals.menu_at(id, at) else {
        return;
    };
    let mac = KeyPlatform::current() == KeyPlatform::Mac;
    let hint = |mac_chord: &str, chord: &str| {
        Shortcut::parse_on(if mac { mac_chord } else { chord }, KeyPlatform::current()).ok()
    };
    let item = |id: &str, title: &str, enabled: bool, shortcut: Option<Shortcut>| {
        let item = PopupItem::new(id, title).enabled(enabled);
        match shortcut {
            Some(shortcut) => item.key_equivalent(shortcut),
            None => item,
        }
    };
    let block = menu.block.is_some();
    let popup = PopupMenu::new()
        .item(item(
            "copy",
            "Copy",
            menu.copies,
            hint("cmd+c", "ctrl+shift+c"),
        ))
        .item(item(
            "copy-command",
            "Copy command",
            menu.command,
            hint("shift+cmd+c", "ctrl+alt+c"),
        ))
        .item(item("copy-output", "Copy output", block, None))
        .separator()
        .item(item("paste", "Paste", true, hint("cmd+v", "ctrl+shift+v")))
        .separator()
        .item(item("filter-block", "Filter block output", block, None))
        .item(item("find", "Find", true, hint("cmd+f", "ctrl+shift+f")))
        .separator()
        .item(item(
            "scroll-block-top",
            "Scroll to top of block",
            block,
            None,
        ))
        .item(item(
            "scroll-block-bottom",
            "Scroll to bottom of block",
            block,
            None,
        ));
    let id = id.to_owned();
    tx.popup_menu(
        popup,
        (window[0], window[1]),
        move |state: &mut State, picked, tx| {
            let Some(picked) = picked else {
                return;
            };
            let terminals = &mut state.terminals;
            let text = match (picked, menu.block) {
                ("copy", _) => terminals.copy_text(&id),
                ("copy-command", Some(block)) => terminals.block_text(&id, block, Part::Command),
                ("copy-output", Some(block)) => terminals.block_text(&id, block, Part::Output),
                ("paste", _) => {
                    tx.read_clipboard(crate::keys::on_paste);
                    None
                }
                ("filter-block", Some(block)) => {
                    terminals.open_filter(&id, block, tx);
                    None
                }
                ("find", _) => {
                    terminals.open_find(&id, tx);
                    None
                }
                ("scroll-block-top", Some(block)) => {
                    terminals.scroll_to_block(&id, block, true);
                    None
                }
                ("scroll-block-bottom", Some(block)) => {
                    terminals.scroll_to_block(&id, block, false);
                    None
                }
                _ => None,
            };
            if let Some(text) = text {
                tx.copy(text);
            }
        },
    );
}
