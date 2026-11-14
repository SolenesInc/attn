//! A note's outline, as the Tauri notebook reads it (outline.ts): ATX headings outside fenced
//! code, each with the byte where it starts, so the editor can scroll to it.

#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Heading {
    pub(crate) level: usize,
    pub(crate) text: String,
    pub(crate) at: usize,
    pub(crate) line: usize,
}

pub(crate) fn outline(text: &str) -> Vec<Heading> {
    let mut headings = Vec::new();
    let mut fence: Option<char> = None;
    let mut at = 0;
    for (i, line) in text.split('\n').enumerate() {
        match (fence_char(line), fence) {
            (Some(c), None) => fence = Some(c),
            (Some(c), Some(open)) if c == open => fence = None,
            (Some(_), Some(_)) => {}
            (None, Some(_)) => {}
            (None, None) => {
                if let Some((level, text)) = atx(line) {
                    headings.push(Heading {
                        level,
                        text: text.to_owned(),
                        at,
                        line: i + 1,
                    });
                }
            }
        }
        at += line.len() + 1;
    }
    headings
}

fn fence_char(line: &str) -> Option<char> {
    let rest = line.trim_start_matches([' ', '\t']);
    let c = rest.chars().next().filter(|c| matches!(c, '`' | '~'))?;
    (rest.chars().take_while(|&x| x == c).count() >= 3).then_some(c)
}

/// `## Title ##` as `(2, "Title")`: up to three spaces, one to six `#`, whitespace, then the
/// text; a closing run of `#` goes only when whitespace separates it from the text.
fn atx(line: &str) -> Option<(usize, &str)> {
    let indent = line.len() - line.trim_start_matches(' ').len();
    if indent > 3 {
        return None;
    }
    let rest = &line[indent..];
    let level = rest.len() - rest.trim_start_matches('#').len();
    if !(1..=6).contains(&level) {
        return None;
    }
    let rest = &rest[level..];
    let body = rest.trim_start_matches([' ', '\t']);
    if body.len() == rest.len() {
        return None;
    }
    let body = body.trim_end_matches([' ', '\t']);
    let before_run = body.trim_end_matches('#');
    let text = if before_run.len() < body.len() && before_run.ends_with([' ', '\t']) {
        before_run.trim_end_matches([' ', '\t'])
    } else {
        body
    };
    let text = text.trim();
    (!text.is_empty()).then_some((level, text))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn texts(md: &str) -> Vec<String> {
        outline(md).into_iter().map(|h| h.text).collect()
    }

    #[test]
    fn headings_carry_their_level_text_line_and_where_they_start() {
        let md = "# Title\n\nIntro paragraph.\n\n## Section one\nbody\n### Detail";
        let at = |s: &str| md.find(s).unwrap();
        let heading = |level, text: &str, at, line| Heading {
            level,
            text: text.into(),
            at,
            line,
        };
        assert_eq!(
            outline(md),
            vec![
                heading(1, "Title", 0, 1),
                heading(2, "Section one", at("## Section one"), 5),
                heading(3, "Detail", at("### Detail"), 7),
            ]
        );
        let md = "preamble\n\n## Heading here\n";
        assert!(md[outline(md)[0].at..].starts_with("## Heading here"));
    }

    #[test]
    fn hashes_in_fenced_code_are_not_headings() {
        let md = "# Real heading\n\n```bash\n# not a heading, just a shell comment\necho hi\n```\n\n~~~\n## also code\n~~~\n\n## Real trailing heading";
        assert_eq!(texts(md), ["Real heading", "Real trailing heading"]);
    }

    #[test]
    fn a_closing_run_goes_only_after_whitespace_and_the_text_needs_a_space_before_it() {
        assert_eq!(
            texts("## Closed heading ##\n#hashtag is not a heading\n###### Deep"),
            ["Closed heading", "Deep"]
        );
        assert_eq!(
            texts("## C#\n### F# notes ##\n# a # b #"),
            ["C#", "F# notes", "a # b"]
        );
        assert_eq!(outline("## Closed ##")[0].level, 2);
    }

    #[test]
    fn empty_headings_and_documents_and_four_space_indents_give_nothing() {
        assert!(outline("").is_empty());
        assert!(outline("## \n#   \ntext").is_empty());
        assert_eq!(
            texts("   ### Indented three\n    # Indented four is code"),
            ["Indented three"]
        );
    }
}
