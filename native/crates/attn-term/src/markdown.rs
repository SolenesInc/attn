//! A styled selection as Markdown, as the Tauri terminal copies one with ⇧⌘C
//! (`terminalMarkdown.ts`).

use crate::selection::clean_lines;

/// The text attributes of a cell that Markdown can carry.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Look {
    pub bold: bool,
    pub italic: bool,
    pub strikethrough: bool,
    pub underline: bool,
    /// The text is drawn in a color other than the terminal's foreground.
    pub colored: bool,
}

/// One selected row, its cells merged into runs of one look.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Line {
    pub runs: Vec<(String, Look)>,
    /// The row continues the one above, which wrapped into it.
    pub wrapped: bool,
}

impl Line {
    /// A row from its cells' text and looks, in order.
    pub fn of_cells(cells: impl IntoIterator<Item = (String, Look)>, wrapped: bool) -> Line {
        let mut runs: Vec<(String, Look)> = Vec::new();
        for (text, look) in cells {
            match runs.last_mut() {
                Some((run, last)) if *last == look => run.push_str(&text),
                _ => runs.push((text, look)),
            }
        }
        Line { runs, wrapped }
    }
}

/// The selected rows as Markdown: bold, italic and strikethrough as Markdown marks, underline as
/// `<u>`, colored text as inline code when the row is mostly prose, and wrapped rows joined
/// before the whitespace is cleaned.
pub fn styled_markdown(lines: &[Line]) -> String {
    let mut rows: Vec<String> = Vec::new();
    for line in lines {
        let code = allows_inline_code(&line.runs);
        let text: String = line
            .runs
            .iter()
            .map(|(text, look)| run_markdown(text, *look, code))
            .collect();
        match rows.last_mut() {
            Some(last) if line.wrapped => last.push_str(&text),
            _ => rows.push(text),
        }
    }
    clean_lines(&rows).join("\n")
}

/// Inline code marks colored text only where at least 0.3 of the row's visible text is in the
/// foreground color, so a fully colored row stays plain.
fn allows_inline_code(runs: &[(String, Look)]) -> bool {
    let visible = |text: &str| text.chars().filter(|c| !c.is_whitespace()).count();
    let total: usize = runs.iter().map(|(text, _)| visible(text)).sum();
    let plain: usize = runs
        .iter()
        .filter(|(_, look)| !look.colored)
        .map(|(text, _)| visible(text))
        .sum();
    total > 0 && plain as f64 / total as f64 >= 0.3
}

fn run_markdown(text: &str, look: Look, allow_code: bool) -> String {
    let semantic = look.bold || look.italic || look.strikethrough;
    let code = allow_code && look.colored && !semantic;
    if !semantic && !look.underline && !code {
        return text.to_owned();
    }
    let inner = text.trim_start();
    let leading = &text[..text.len() - inner.len()];
    let inner_trimmed = inner.trim_end();
    let trailing = &inner[inner_trimmed.len()..];
    let inner = inner_trimmed;
    if inner.is_empty() {
        return text.to_owned();
    }
    let marked = if code {
        if inner.contains('`') {
            format!("`` {inner} ``")
        } else {
            format!("`{inner}`")
        }
    } else {
        let mut marked = inner.to_owned();
        if look.underline && !look.colored {
            marked = format!("<u>{marked}</u>");
        }
        if look.strikethrough {
            marked = format!("~~{marked}~~");
        }
        match (look.bold, look.italic) {
            (true, true) => format!("***{marked}***"),
            (true, false) => format!("**{marked}**"),
            (false, true) => format!("*{marked}*"),
            (false, false) => marked,
        }
    };
    format!("{leading}{marked}{trailing}")
}

#[cfg(test)]
mod tests {
    use super::*;

    const PLAIN: Look = Look {
        bold: false,
        italic: false,
        strikethrough: false,
        underline: false,
        colored: false,
    };

    fn run(text: &str, look: Look) -> (String, Look) {
        (text.to_owned(), look)
    }

    fn line(runs: &[(&str, Look)], wrapped: bool) -> Line {
        Line {
            runs: runs.iter().map(|(text, look)| run(text, *look)).collect(),
            wrapped,
        }
    }

    #[test]
    fn a_styled_selection_becomes_markdown_as_the_tauri_terminal_copies_it() {
        let bold = Look {
            bold: true,
            ..PLAIN
        };
        let italic = Look {
            italic: true,
            ..PLAIN
        };
        let colored = Look {
            colored: true,
            ..PLAIN
        };
        let cases: Vec<(&str, Vec<Line>, &str)> = vec![
            (
                "bold and italic",
                vec![line(
                    &[("bold", bold), (" ", PLAIN), ("italic", italic)],
                    false,
                )],
                "**bold** *italic*",
            ),
            (
                "colored text inside prose is inline code",
                vec![line(
                    &[("Use ", PLAIN), ("attn", colored), (" here", PLAIN)],
                    false,
                )],
                "Use `attn` here",
            ),
            (
                "a row mostly colored stays plain",
                vec![line(&[("error: ", colored), ("it failed", colored)], false)],
                "error: it failed",
            ),
            (
                "a soft-wrapped row joins the row above",
                vec![
                    line(&[("long ", PLAIN)], false),
                    line(&[("line", bold)], true),
                ],
                "long **line**",
            ),
            (
                "bold italic marks only the text inside its spaces",
                vec![line(
                    &[
                        ("prose ", PLAIN),
                        (
                            " both ",
                            Look {
                                italic: true,
                                ..bold
                            },
                        ),
                    ],
                    false,
                )],
                "prose  ***both***",
            ),
            (
                "strikethrough",
                vec![line(
                    &[
                        ("prose ", PLAIN),
                        (
                            "gone",
                            Look {
                                strikethrough: true,
                                ..PLAIN
                            },
                        ),
                    ],
                    false,
                )],
                "prose ~~gone~~",
            ),
            (
                "underline",
                vec![line(
                    &[
                        ("prose ", PLAIN),
                        (
                            "under",
                            Look {
                                underline: true,
                                ..PLAIN
                            },
                        ),
                    ],
                    false,
                )],
                "prose <u>under</u>",
            ),
            (
                "colored underline is code, not <u>",
                vec![line(
                    &[
                        ("prose ", PLAIN),
                        (
                            "link",
                            Look {
                                underline: true,
                                ..colored
                            },
                        ),
                    ],
                    false,
                )],
                "prose `link`",
            ),
            (
                "code holding a backtick",
                vec![line(&[("prose ", PLAIN), ("a`b", colored)], false)],
                "prose `` a`b ``",
            ),
            (
                "styled blanks stay blank",
                vec![line(&[("prose ", PLAIN), ("   ", bold)], false)],
                "prose",
            ),
        ];
        for (name, lines, want) in cases {
            assert_eq!(styled_markdown(&lines), want, "{name}");
        }
    }
}
