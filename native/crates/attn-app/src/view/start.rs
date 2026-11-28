//! The notice that names the commands a view would run and waits for the user's key, and the
//! key that starts them.

use attn_protocol::{ViewDocument, ViewRunCommand, ViewStartMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Notice};
use attn_ui::system::{Button, Part, Said, Size};

use crate::State;

/// Whether the view has a command the user has not started, or that changed since they did.
pub(crate) fn waits(view: &ViewDocument) -> bool {
    view.commands.iter().any(|c| c.state != "started")
}

/// Whether a command stopped, so the values the view shows are its last ones.
pub(crate) fn stopped(view: &ViewDocument) -> bool {
    view.sources.iter().any(|s| s.state == "out_of_date")
}

/// Starts the commands of the view the tile `tile` on `desktop` shows, as the tile shows them.
pub(crate) fn start(state: &mut State, desktop: &str, tile: &str) {
    let _ = state.daemon.send(&ViewStartMessage {
        desktop_id: desktop.to_owned(),
        tile_id: tile.to_owned(),
        ..ViewStartMessage::new()
    });
}

fn how_often(command: &ViewRunCommand) -> String {
    match &command.every {
        Some(every) => format!(" every {every}"),
        None => " once each time the view is shown".to_owned(),
    }
}

/// The notice over a view whose commands wait for the user: each command, how often it runs,
/// what changed in one that stopped, and the machine they run on.
pub(crate) fn notice(
    view: &ViewDocument,
    (desktop, tile): (&str, &str),
    machine: &str,
    t: &Tokens,
) -> Option<Block> {
    if !waits(view) {
        return None;
    }
    let changed = view.commands.iter().any(|c| c.state == "changed");
    let count = view.commands.len();
    let mut parts = vec![Part::Text(if changed {
        "attn stopped running a command that changed. It runs again once you start it.".to_owned()
    } else if count == 1 {
        format!("This view runs a command on {machine}. It runs once you start it.")
    } else {
        format!("This view runs {count} commands on {machine}. They run once you start them.")
    })];
    for command in &view.commands {
        parts.push(Part::Text("\n".to_owned()));
        parts.push(Part::Code(command.line.clone()));
        parts.push(Part::Text(match &command.change {
            Some(change) if command.state == "changed" => format!(" {change}"),
            _ => how_often(command),
        }));
    }
    let label = match (changed, count) {
        (true, _) => "Run the new one",
        (false, 1) => "Run it",
        (false, _) => "Run them",
    };
    let (desktop, tile) = (desktop.to_owned(), tile.to_owned());
    let key = format!("tile.{tile}.view.start");
    let run = Button::new(label)
        .size(Size::Compact)
        .shortcut("r")
        .on_press_tx(move |state: &mut State, _| start(state, &desktop, &tile))
        .build(t)
        .key(format!("{key}.run"));
    let said = Said::parts(parts);
    let shown = if changed {
        Notice::warn(key, said)
    } else {
        Notice::info(key, said)
    };
    Some(shown.action(run).build(t))
}
