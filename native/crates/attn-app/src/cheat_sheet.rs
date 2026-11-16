//! The keyboard shortcuts sheet, row for row as the Tauri app's `buildCheatsheet` lists it:
//! paired actions share a row, a digit or arrow family shows its modifiers once, and some rows
//! carry a note. The keys come from the user's keymap.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, KeyPlatform, col, el, row, text};
use rune_kit::Keybind;

use crate::Action;
use crate::keymap::{Binding, Keymap};

/// What a combo on a row presses: an action's own keys, or an action's modifiers with a
/// family of keys such as the digits.
enum Keys {
    Of(Action),
    ModifiersOf(Action, &'static str),
}

use Keys::{ModifiersOf, Of};

struct Line {
    label: &'static str,
    keys: &'static [Keys],
    note: Option<fn(&Keymap) -> String>,
}

const fn line(label: &'static str, keys: &'static [Keys]) -> Line {
    Line {
        label,
        keys,
        note: None,
    }
}

const fn noted(label: &'static str, keys: &'static [Keys], note: fn(&Keymap) -> String) -> Line {
    Line {
        label,
        keys,
        note: Some(note),
    }
}

struct Section {
    key: &'static str,
    title: &'static str,
    lines: &'static [Line],
}

const SECTIONS: &[Section] = &[
    Section {
        key: "Sessions",
        title: "Desktops & Sessions",
        lines: &[
            line(
                "New terminal on this desktop",
                &[Of(Action::SessionNewTerminal)],
            ),
            line("New session on this desktop", &[Of(Action::SessionNew)]),
            line("New session below", &[Of(Action::SessionNewHorizontal)]),
            line(
                "Close session, focused pane or empty desktop",
                &[Of(Action::SessionClose)],
            ),
            line(
                "Previous / next desktop (queue mode: agent in the queue)",
                &[Of(Action::SessionPrev), Of(Action::SessionNext)],
            ),
            line(
                "Back / forward through agent history",
                &[
                    Of(Action::SessionHistoryBack),
                    Of(Action::SessionHistoryForward),
                ],
            ),
            line(
                "Jump to this session's dispatcher",
                &[Of(Action::SessionOrchestrator)],
            ),
            noted(
                "Switch to desktop 1–9",
                &[ModifiersOf(Action::DesktopSelect1, "1–9")],
                |_| "The digit of the current desktop goes back to the previous one.".into(),
            ),
            noted(
                "Move this to desktop 1–9 and follow",
                &[ModifiersOf(Action::DesktopSend1, "1–9")],
                |_| "A digit with no desktop yet gets a new one.".into(),
            ),
            noted(
                "Move this to desktop 1–9",
                &[ModifiersOf(Action::DesktopSendStay1, "1–9")],
                |_| "A digit with no desktop yet gets a new one.".into(),
            ),
            line("Desktop overview", &[Of(Action::DesktopOverview)]),
            line("Switch profile", &[Of(Action::ProfileSwitch)]),
            line(
                "Go to dashboard (home)",
                &[Of(Action::SessionGoToDashboard)],
            ),
            line(
                "Jump to next waiting session",
                &[Of(Action::SessionJumpToWaiting)],
            ),
            line(
                "Next automation run needing you",
                &[Of(Action::SessionNextRun)],
            ),
            line("All agents", &[Of(Action::SidebarAgentList)]),
            line("Settle turn", &[Of(Action::SessionSettle)]),
            line("Asks drawer", &[Of(Action::DockAsks)]),
            line("Toggle session priority", &[Of(Action::SessionPriority)]),
            line("Snooze this agent", &[Of(Action::SessionSnooze)]),
            line(
                "Stop the countdown, or keep the next turn",
                &[Of(Action::SessionCancelCountdown)],
            ),
            line("Toggle sidebar", &[Of(Action::SessionToggleSidebar)]),
            line(
                "Widen / narrow the sidebar",
                &[Of(Action::SidebarWider), Of(Action::SidebarNarrower)],
            ),
            noted(
                "Sessions list, live and closed",
                &[Of(Action::SessionsOpen)],
                |_| "Reopen a closed session from here.".into(),
            ),
        ],
    },
    Section {
        key: "Panes",
        title: "Panes & Terminals",
        lines: &[
            line("Find in terminal", &[Of(Action::TerminalFind)]),
            line("New shell beside", &[Of(Action::TerminalSplitVertical)]),
            line("New shell below", &[Of(Action::TerminalSplitHorizontal)]),
            noted(
                "Move focus between panes",
                &[ModifiersOf(Action::TerminalFocusLeft, "←↑→↓")],
                |_| "Steps to the next desktop at an edge.".into(),
            ),
            line(
                "Carry tile along the row",
                &[Of(Action::ShelfCarryLeft), Of(Action::ShelfCarryRight)],
            ),
            noted(
                "Send tile to the shelf",
                &[Of(Action::ShelfSendToShelf)],
                |keymap| {
                    format!(
                        "A held tile returns with a chip, {} or a restart.",
                        label(keymap, &Action::SessionJumpToWaiting)
                    )
                },
            ),
            line("Zoom active pane", &[Of(Action::TerminalToggleZoom)]),
            line("Focus active pane", &[Of(Action::TerminalToggleMaximize)]),
            line("Focus utility terminal", &[Of(Action::TerminalOpen)]),
            line(
                "Fewer, wider / more, narrower tiles",
                &[Of(Action::ShelfFewerTiles), Of(Action::ShelfMoreTiles)],
            ),
        ],
    },
    Section {
        key: "Markdown",
        title: "Markdown & Annotations",
        lines: &[
            noted(
                "Send annotations to session",
                &[Of(Action::MarkdownSendAnnotations)],
                |_| "When a markdown tile has focus and annotations exist.".into(),
            ),
            noted(
                "Send terminal annotations to session",
                &[Of(Action::TerminalSendAnnotations)],
                |_| "When the annotated pane has focus and marks are waiting.".into(),
            ),
        ],
    },
    Section {
        key: "Review",
        title: "Review & Git",
        lines: &[
            line("PRs drawer", &[Of(Action::DockAttention)]),
            line("Refresh PRs", &[Of(Action::SessionRefreshPrs)]),
        ],
    },
    Section {
        key: "App",
        title: "App",
        lines: &[
            line("Agent palette", &[Of(Action::UiActionMenu)]),
            line("Command palette", &[Of(Action::UiCommandPalette)]),
            line("Settings", &[Of(Action::UiOpenSettings)]),
            line(
                "Font size up / down / reset",
                &[
                    Of(Action::UiIncreaseFontSize),
                    Of(Action::UiDecreaseFontSize),
                    Of(Action::UiResetFontSize),
                ],
            ),
            line("Keyboard shortcuts", &[Of(Action::UiShowShortcuts)]),
            line("Quit attn", &[Of(Action::AppQuit)]),
        ],
    },
];

fn label(keymap: &Keymap, action: &Action) -> String {
    keymap
        .binding(action)
        .map(|b| keymap.label(b))
        .unwrap_or_default()
}

/// The keycaps a combo draws, or none when its action has no keys.
fn keycaps(keymap: &Keymap, keys: &Keys) -> Option<Vec<String>> {
    match keys {
        Of(action) => Some(keymap.keycaps(keymap.binding(action)?)),
        ModifiersOf(action, family) => match keymap.binding(action)? {
            Binding::Chord(chord) => {
                let mut caps = chord.keycaps_on(keymap.platform());
                caps.pop();
                caps.push((*family).to_owned());
                Some(caps)
            }
            Binding::Sequence { .. } => None,
        },
    }
}

fn spoken(keymap: &Keymap, caps: &[String]) -> String {
    caps.join(if keymap.platform() == KeyPlatform::Mac {
        ""
    } else {
        "+"
    })
}

pub(crate) fn sections(keymap: &Keymap, t: &Tokens) -> Vec<El> {
    SECTIONS
        .iter()
        .map(|section| {
            col()
                .key(format!("shortcuts.{}", section.key))
                .role("list")
                .label(section.title)
                .w_full()
                .child(crate::shell::section_title(section.title, t))
                .children(section.lines.iter().enumerate().flat_map(|(i, line)| {
                    let rule =
                        (i > 0).then(|| el().w_full().h(1.0).fill(t.border_subtle).a11y_hidden());
                    rule.into_iter().chain([row_of(keymap, line, t)])
                }))
        })
        .collect()
}

fn row_of(keymap: &Keymap, line: &Line, t: &Tokens) -> El {
    let combos: Vec<Vec<String>> = line
        .keys
        .iter()
        .filter_map(|keys| keycaps(keymap, keys))
        .collect();
    let first = line.keys.first().map(|keys| match keys {
        Of(action) | ModifiersOf(action, _) => action.id(),
    });
    let said = combos
        .iter()
        .map(|caps| spoken(keymap, caps))
        .collect::<Vec<_>>()
        .join(" / ");
    let note = line.note.map(|note| note(keymap));
    let kit = attn_ui::kit(t);
    let mut label = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(2.0)
        .child(text(line.label).font(type_::BODY).color(t.text_primary));
    if let Some(note) = &note {
        label = label.child(text(note.clone()).font(type_::LABEL).color(t.text_tertiary));
    }
    let item = row()
        .key(format!("shortcut.{}", first.unwrap_or_default()))
        .role("listitem")
        .label(format!("{}, {said}", line.label))
        .w_full()
        .items_center()
        .gap(16.0)
        .py(7.0)
        .child(label)
        .child(
            row().gap(space::S1).items_center().children(
                combos
                    .into_iter()
                    .map(|caps| Keybind::new(&caps).build(kit)),
            ),
        );
    match note {
        Some(note) => item.description(note),
        None => item,
    }
}
