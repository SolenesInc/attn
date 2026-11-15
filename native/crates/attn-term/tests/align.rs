use attn_term::LinkRow;
use attn_term::align::{Alignment, RowRange, quotes_anchor};

/// Lays `markdown` out as an agent's TUI does: `marker` before the first row, its width of
/// indent before the rest, words wrapped at `cols`.
fn render(markdown: &str, cols: usize, marker: &str) -> Vec<String> {
    let indent = " ".repeat(marker.chars().count());
    let mut rows = Vec::new();
    for paragraph in markdown.split('\n') {
        let mut line = if rows.is_empty() {
            marker.to_owned()
        } else {
            indent.clone()
        };
        let mut empty = true;
        for word in paragraph.split_whitespace() {
            if !empty && line.chars().count() + 1 + word.chars().count() > cols {
                rows.push(std::mem::replace(&mut line, indent.clone()));
                empty = true;
            }
            if !empty {
                line.push(' ');
            }
            line.push_str(word);
            empty = false;
        }
        rows.push(line);
    }
    rows
}

fn cells(rows: &[String]) -> Vec<LinkRow> {
    rows.iter()
        .map(|row| LinkRow {
            cells: row.chars().map(String::from).collect(),
            uris: vec![None; row.chars().count()],
        })
        .collect()
}

fn align(markdown: &str, rows: &[String], base: u32) -> Alignment {
    Alignment::of(markdown, &cells(rows), base)
}

fn strings(rows: &[&str]) -> Vec<String> {
    rows.iter().map(|r| (*r).to_owned()).collect()
}

fn utf16(text: &str) -> usize {
    text.chars().map(char::len_utf16).sum()
}

/// `phrase`'s UTF-16 offsets in `markdown`.
fn anchor(markdown: &str, phrase: &str) -> (usize, usize) {
    let at = markdown
        .find(phrase)
        .expect("the phrase is in the markdown");
    let start = utf16(&markdown[..at]);
    (start, start + utf16(phrase))
}

fn slice16(markdown: &str, (start, end): (usize, usize)) -> String {
    let units: Vec<u16> = markdown.encode_utf16().collect();
    String::from_utf16(&units[start..end]).expect("whole characters")
}

fn text_at(rows: &[String], base: u32, ranges: &[RowRange]) -> Vec<String> {
    ranges
        .iter()
        .map(|r| {
            let row: Vec<char> = rows[(r.row - base) as usize].chars().collect();
            row[usize::from(r.cols.0)..usize::from(r.cols.1)]
                .iter()
                .collect()
        })
        .collect()
}

fn words(text: &[String]) -> Vec<String> {
    text.iter()
        .flat_map(|t| t.split_whitespace().map(String::from).collect::<Vec<_>>())
        .collect()
}

const MESSAGE: &str = "A soft line wrap moves overflowing text onto the next visual line without inserting a real line break. The wrap can shift when the available width changes.";
const ANCHOR: &str = "visual line without inserting a real";

fn linked_message() -> String {
    [
        "The paths below are genuine rejection evidence from the agent.",
        "",
        "Examples: [pipeline routing](/Users/tester/src/services-pilot/audiobook-ingestion/ais-pipeline-1/FileProcessingService.java:118), [audio validation](/Users/tester/src/services-pilot/audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227), [image validation](/Users/tester/src/services-pilot/audiobook-ingestion/ais-image-choreography/ImageServiceImpl.java:88), [ZIP handling](/Users/tester/src/services-pilot/audiobook-ingestion/ais-zip-choreography/UnpackerImpl.java:176).",
        "",
        "The conclusion after those rendered links must remain independently annotatable.",
    ]
    .join("\n")
}

fn linked_rows() -> Vec<String> {
    strings(&[
        "• The paths below are genuine rejection evidence from the agent.",
        "",
        "  Examples: audiobook-ingestion/ais-pipeline-1/FileProcessingService.java:118,",
        "  audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227,",
        "  audiobook-ingestion/ais-image-choreography/ImageServiceImpl.java:88,",
        "  audiobook-ingestion/ais-zip-choreography/UnpackerImpl.java:176.",
        "",
        "  The conclusion after those rendered links must remain independently annotatable.",
    ])
}

fn decoy_above() -> Vec<String> {
    let mut rows = vec!["❯ can you please rewrite that onto the next visual".to_owned()];
    rows.extend(render(MESSAGE, 62, "⏺ ").into_iter().skip(1));
    rows
}

#[test]
fn an_anchor_resolves_onto_the_rows_showing_it() {
    let rows = render(MESSAGE, 62, "⏺ ");
    let (start, end) = anchor(MESSAGE, ANCHOR);
    let ranges = align(MESSAGE, &rows, 0).rows_for(start, end);
    assert_eq!(
        words(&text_at(&rows, 0, &ranges)),
        words(&[ANCHOR.to_owned()])
    );
}

#[test]
fn an_anchor_resolves_onto_other_rows_after_a_reflow_still_quoting_it() {
    let (start, end) = anchor(MESSAGE, ANCHOR);
    let (wide, narrow) = (render(MESSAGE, 62, "⏺ "), render(MESSAGE, 34, "⏺ "));
    let wide_ranges = align(MESSAGE, &wide, 0).rows_for(start, end);
    let narrow_ranges = align(MESSAGE, &narrow, 0).rows_for(start, end);
    assert!(narrow.len() > wide.len());
    let rows_of = |ranges: &[RowRange]| ranges.iter().map(|r| r.row).collect::<Vec<_>>();
    assert_ne!(rows_of(&narrow_ranges), rows_of(&wide_ranges));
    assert_eq!(
        words(&text_at(&wide, 0, &wide_ranges)),
        words(&[ANCHOR.into()])
    );
    assert_eq!(
        words(&text_at(&narrow, 0, &narrow_ranges)),
        words(&[ANCHOR.into()])
    );
}

#[test]
fn rows_deep_in_the_history_keep_their_screen_rows() {
    let rows = render(MESSAGE, 62, "⏺ ");
    let (start, end) = anchor(MESSAGE, ANCHOR);
    let at_zero = align(MESSAGE, &rows, 0).rows_for(start, end);
    let at_depth = align(MESSAGE, &rows, 900).rows_for(start, end);
    let shifted: Vec<u32> = at_zero.iter().map(|r| r.row + 900).collect();
    assert_eq!(at_depth.iter().map(|r| r.row).collect::<Vec<_>>(), shifted);
    assert_eq!(
        words(&text_at(&rows, 900, &at_depth)),
        words(&[ANCHOR.into()])
    );
}

#[test]
fn a_message_not_on_the_rows_resolves_nothing() {
    let rows = render(
        "Something else entirely, about unrelated matters.",
        62,
        "⏺ ",
    );
    let (start, end) = anchor(MESSAGE, ANCHOR);
    assert_eq!(align(MESSAGE, &rows, 0).rows_for(start, end), []);
}

#[test]
fn a_user_turn_above_that_shares_words_stays_out_of_the_message() {
    let alignment = align(MESSAGE, &decoy_above(), 0);
    assert_eq!(alignment.span.map(|s| s.0), Some(1));
    let (start, end) = anchor(MESSAGE, "onto the next visual");
    assert!(alignment.rows_for(start, end).iter().all(|r| r.row != 0));
}

#[test]
fn prose_on_both_sides_of_rendered_links_resolves() {
    let (markdown, rows) = (linked_message(), linked_rows());
    let alignment = align(&markdown, &rows, 0);
    for phrase in [
        "genuine rejection evidence",
        "conclusion after those rendered links",
    ] {
        let (start, end) = anchor(&markdown, phrase);
        assert_ne!(alignment.rows_for(start, end), [], "{phrase}");
    }
}

#[test]
fn a_list_goes_on_after_a_url_drawn_as_label_and_target() {
    let markdown = [
        "My recommendation:",
        "",
        "1. Close [ASTERISK-1797](https://spotify.atlassian.net/browse/ASTERISK-1797) as obsolete / won’t do.",
        "2. Explain that the RFC deliberately preserves the Findaway compatibility format and that migration",
        "   routing already uses an explicit feed ID.",
    ]
    .join("\n");
    let rows = strings(&[
        "• My recommendation:",
        "",
        "  1. Close ASTERISK-1797 (https://spotify.atlassian.net/browse/ASTERISK-1797) as obsolete / won’t do.",
        "  2. Explain that the RFC deliberately preserves the Findaway compatibility format and that migration",
        "     routing already uses an explicit feed ID.",
    ]);
    let alignment = align(&markdown, &rows, 0);
    let (start, end) = anchor(&markdown, "as obsolete");
    assert_ne!(alignment.rows_for(start, end), []);
    let below = anchor(&markdown, "preserves the Findaway compatibility format");
    let ranges = alignment.rows_for(below.0, below.1);
    assert_ne!(ranges, []);
    assert!(quotes_anchor(
        &slice16(&markdown, below),
        &text_at(&rows, 0, &ranges)
    ));
}

#[test]
fn a_shortened_path_on_screen_maps_back_to_the_link_target() {
    let (markdown, rows) = (linked_message(), linked_rows());
    let target = anchor(
        &markdown,
        "/Users/tester/src/services-pilot/audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227",
    );
    let ranges = align(&markdown, &rows, 0).rows_for(target.0, target.1);
    assert_eq!(ranges.len(), 1);
    let shown = text_at(&rows, 0, &ranges);
    assert!(shown[0].contains("AudioServiceImpl.java:227"));
    assert!(quotes_anchor(&slice16(&markdown, target), &shown));
}

#[test]
fn links_with_one_file_name_stay_apart_by_their_folder() {
    let markdown = "Compare [one](/repo/alpha/Shared.java:42) with [two](/repo/beta/Shared.java:42), then keep reading.";
    let rows =
        strings(&["• Compare alpha/Shared.java:42 with beta/Shared.java:42, then keep reading."]);
    let alignment = align(markdown, &rows, 0);
    for (target, shown) in [
        ("/repo/alpha/Shared.java:42", "alpha/Shared.java"),
        ("/repo/beta/Shared.java:42", "beta/Shared.java"),
    ] {
        let (start, end) = anchor(markdown, target);
        let ranges = alignment.rows_for(start, end);
        assert_eq!(ranges.len(), 1, "{target}");
        assert!(text_at(&rows, 0, &ranges)[0].contains(shown), "{target}");
    }
}

#[test]
fn a_file_name_under_another_folder_is_not_the_link() {
    let markdown = "Before [source](/repo/right/Shared.java:42), the middle and after stay mapped.";
    let rows = strings(&["• Before wrong/Shared.java:42, the middle and after stay mapped."]);
    let alignment = align(markdown, &rows, 0);
    let (start, end) = anchor(markdown, "/repo/right/Shared.java:42");
    assert_eq!(alignment.rows_for(start, end), []);
    let (start, end) = anchor(markdown, "middle and after stay mapped");
    assert_ne!(alignment.rows_for(start, end), []);
    assert!(!quotes_anchor(
        "/repo/right/Shared.java:42",
        &["wrong/Shared.java:42".into()]
    ));
}

#[test]
fn a_link_label_on_screen_is_the_label_even_when_osc_8_names_its_target() {
    let markdown = "See [implementation](/Users/tester/src/Thing.java:44) for the invariant.";
    let row = "• See implementation for the invariant.";
    let from = usize::from(col_of(row, "implementation"));
    let mut rows = cells(&strings(&[row]));
    for (col, uri) in rows[0].uris.iter_mut().enumerate() {
        if col >= from {
            *uri = Some("file:///Users/tester/src/Thing.java:44".into());
        }
    }
    let alignment = Alignment::of(markdown, &rows, 0);
    let to = from + "implementation".len();
    let span = alignment
        .offsets_for((0, from as u16), (0, to as u16))
        .expect("the drag covers the label");
    assert_eq!(slice16(markdown, span), "implementation");
}

#[test]
fn the_message_starts_on_its_own_row_when_the_row_above_echoes_its_first_words() {
    let full = render(MESSAGE, 62, "⏺ ");
    let mut rows =
        vec!["❯ hmm can you redo that A soft line wrap explanation please thanks".to_owned()];
    rows.extend(full.into_iter().skip(1));
    let last = rows.len() as u32 - 1;
    assert_eq!(align(MESSAGE, &rows, 0).span, Some((1, last)));
}

fn col_of(row: &str, word: &str) -> u16 {
    row[..row.find(word).expect("the word is on the row")]
        .chars()
        .count() as u16
}

#[test]
fn a_drag_on_codex_answer_row_maps_to_the_answer_not_the_prompt_quoting_it() {
    let message = "A retry wrapper protects idempotent operations from duplicate network effects.";
    let mut rows = strings(&[
        "› Before using a tool, write exactly this sentence as",
        "  ordinary prose: A retry wrapper protects idempotent",
        "  operations from duplicate network effects.",
        "",
    ]);
    rows.extend(render(message, 54, "• "));
    let at = rows.iter().position(|r| r.starts_with("• ")).unwrap();
    let span = align(message, &rows, 0)
        .offsets_for(
            (at as u32, col_of(&rows[at], "retry")),
            (
                at as u32,
                col_of(&rows[at], "idempotent") + "idempotent".len() as u16,
            ),
        )
        .expect("the drag covers the answer");
    assert_eq!(slice16(message, span), "retry wrapper protects idempotent");
}

#[test]
fn a_drag_over_the_rows_maps_back_to_the_markdown_the_agent_wrote() {
    let rows = render(MESSAGE, 62, "⏺ ");
    let alignment = align(MESSAGE, &rows, 0);
    let at = rows.iter().position(|r| r.contains("visual")).unwrap();
    let span = alignment
        .offsets_for(
            (at as u32, col_of(&rows[at], "visual")),
            (
                at as u32 + 1,
                col_of(&rows[at + 1], "real") + "real".len() as u16,
            ),
        )
        .unwrap();
    assert_eq!(slice16(MESSAGE, span), ANCHOR);

    let start = col_of(&rows[at], "visual");
    let span = alignment
        .offsets_for((at as u32, start + 2), (at as u32, start + 4))
        .unwrap();
    assert_eq!(
        slice16(MESSAGE, span),
        "visual",
        "a drag mid-word takes the word"
    );
}

#[test]
fn a_drag_over_none_of_the_message_maps_to_nothing() {
    let mut rows = strings(&["› Use /skills to list available skills"]);
    rows.extend(render(MESSAGE, 62, "⏺ "));
    assert_eq!(align(MESSAGE, &rows, 0).offsets_for((0, 0), (0, 40)), None);
}

#[test]
fn a_mark_paints_only_while_the_rows_still_read_as_its_quote() {
    let cases: [(&str, &str, &[&str], bool); 5] = [
        (
            "the anchored words read back off the rows",
            ANCHOR,
            &["  visual line", "  without inserting a real"],
            true,
        ),
        (
            "markdown syntax the TUI strips",
            "a **real** line `break`",
            &["a real line break"],
            true,
        ),
        (
            "a wash the view clips short",
            ANCHOR,
            &["line without inserting a real"],
            true,
        ),
        (
            "rows holding what the agent did not write",
            ANCHOR,
            &["› Use /skills to list available skills"],
            false,
        ),
        ("blank rows a repaint leaves", ANCHOR, &["     "], false),
    ];
    for (name, quote, painted, want) in cases {
        assert_eq!(quotes_anchor(quote, &strings(painted)), want, "{name}");
    }
}
