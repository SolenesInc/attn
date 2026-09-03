use crate::ghostty::{Terminal, Theme};

const DEFAULT_FOREGROUND: &str = "#d4d4d4";
const DEFAULT_BACKGROUND: &str = "#1e1e1e";
const DEFAULT_CURSOR: &str = "#d4d4d4";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ColorScheme {
    Dark,
    Light,
}

pub struct TerminalQueries {
    da1: bool,
    cpr: bool,
    osc_order: Vec<u8>,
    color_scheme: usize,
    da1_before_cpr: bool,
}

impl TerminalQueries {
    pub fn detect(data: &[u8]) -> Self {
        let da1_index = index_da1(data);
        let cpr_index = find(data, b"\x1b[6n");
        Self {
            da1: da1_index.is_some(),
            cpr: cpr_index.is_some(),
            osc_order: scan_osc_colors(data),
            color_scheme: count(data, b"\x1b[?996n"),
            da1_before_cpr: da1_index.zip(cpr_index).is_some_and(|(da1, cpr)| da1 < cpr),
        }
    }

    pub fn replies_before_feed(&self, theme: &Theme) -> Vec<u8> {
        let mut replies = Vec::with_capacity(self.osc_order.len() * 30 + self.color_scheme * 10);
        for code in &self.osc_order {
            let color = match code {
                10 => osc_color(&theme.foreground, DEFAULT_FOREGROUND),
                11 => osc_color(&theme.background, DEFAULT_BACKGROUND),
                12 => osc_color(&theme.cursor, DEFAULT_CURSOR),
                _ => continue,
            };
            replies.extend_from_slice(format!("\x1b]{code};{color}\x1b\\").as_bytes());
        }
        for _ in 0..self.color_scheme {
            replies.extend_from_slice(color_scheme_report(theme_color_scheme(theme)));
        }
        replies
    }

    pub fn replies_after_feed(&self, terminal: &Terminal, drained: &[u8]) -> Vec<u8> {
        let mut replies = strip_scanner_owned(drained);
        let da1 = b"\x1b[?1;2c";
        let (col, row) = terminal.cursor_pos();
        let cpr = format!("\x1b[{};{}R", row + 1, col + 1);
        if self.da1_before_cpr {
            replies.extend_from_slice(da1);
            replies.extend_from_slice(cpr.as_bytes());
        } else {
            if self.cpr {
                replies.extend_from_slice(cpr.as_bytes());
            }
            if self.da1 {
                replies.extend_from_slice(da1);
            }
        }
        replies
    }
}

pub fn track_color_scheme_reports(data: &[u8], enabled: &mut bool) {
    let set = rfind(data, b"\x1b[?2031h");
    let reset = rfind(data, b"\x1b[?2031l");
    if set.is_some() || reset.is_some() {
        *enabled = match (set, reset) {
            (Some(set), Some(reset)) => set > reset,
            (Some(_), None) => true,
            (None, Some(_)) => false,
            (None, None) => *enabled,
        };
    }
}

pub fn theme_color_scheme(theme: &Theme) -> ColorScheme {
    let background = if valid_hex(&theme.background) {
        &theme.background
    } else {
        DEFAULT_BACKGROUND
    };
    let channel = |value: &str| {
        let linear = f64::from(u8::from_str_radix(value, 16).unwrap_or_default()) / 255.0;
        if linear <= 0.039_28 {
            linear / 12.92
        } else {
            ((linear + 0.055) / 1.055).powf(2.4)
        }
    };
    let luminance = 0.2126 * channel(&background[1..3])
        + 0.7152 * channel(&background[3..5])
        + 0.0722 * channel(&background[5..7]);
    if luminance >= 0.5 {
        ColorScheme::Light
    } else {
        ColorScheme::Dark
    }
}

pub fn color_scheme_report(scheme: ColorScheme) -> &'static [u8] {
    match scheme {
        ColorScheme::Dark => b"\x1b[?997;1n",
        ColorScheme::Light => b"\x1b[?997;2n",
    }
}

fn index_da1(data: &[u8]) -> Option<usize> {
    for index in 0..data.len().saturating_sub(2) {
        if data.get(index..index + 2) != Some(b"\x1b[") {
            continue;
        }
        let mut cursor = index + 2;
        while data
            .get(cursor)
            .is_some_and(|byte| byte.is_ascii_digit() || *byte == b';')
        {
            cursor += 1;
        }
        if data.get(cursor) == Some(&b'c') {
            return Some(index);
        }
    }
    None
}

fn scan_osc_colors(data: &[u8]) -> Vec<u8> {
    let mut result = Vec::new();
    let mut index = 0;
    while index < data.len() {
        let matched = [
            (10, b"\x1b]10;?".as_slice()),
            (11, b"\x1b]11;?"),
            (12, b"\x1b]12;?"),
        ]
        .into_iter()
        .find(|(_, prefix)| data.get(index..index + prefix.len()) == Some(*prefix));
        if let Some((code, prefix)) = matched {
            result.push(code);
            index += prefix.len();
        } else {
            index += 1;
        }
    }
    result
}

fn osc_color(value: &str, fallback: &str) -> String {
    let value = if valid_hex(value) { value } else { fallback };
    format!(
        "rgb:{0}{0}/{1}{1}/{2}{2}",
        &value[1..3],
        &value[3..5],
        &value[5..7]
    )
}

fn valid_hex(value: &str) -> bool {
    value.len() == 7
        && value.starts_with('#')
        && value.as_bytes()[1..].iter().all(u8::is_ascii_hexdigit)
}

fn strip_scanner_owned(responses: &[u8]) -> Vec<u8> {
    let mut result = Vec::with_capacity(responses.len());
    let mut index = 0;
    while index < responses.len() {
        if responses[index] != 0x1b || index + 1 >= responses.len() {
            result.push(responses[index]);
            index += 1;
            continue;
        }
        match responses[index + 1] {
            b'[' => {
                let mut end = index + 2;
                while end < responses.len() && !matches!(responses[end], 0x40..=0x7e) {
                    end += 1;
                }
                if end >= responses.len() {
                    result.extend_from_slice(&responses[index..]);
                    break;
                }
                if !matches!(responses[end], b'R' | b'c') {
                    result.extend_from_slice(&responses[index..=end]);
                }
                index = end + 1;
            }
            b']' => {
                let mut end = index + 2;
                while end < responses.len() {
                    if responses[end] == 0x07 {
                        end += 1;
                        break;
                    }
                    if responses[end] == 0x1b && responses.get(end + 1) == Some(&b'\\') {
                        end += 2;
                        break;
                    }
                    end += 1;
                }
                if !is_osc_color_report(&responses[index..end]) {
                    result.extend_from_slice(&responses[index..end]);
                }
                index = end;
            }
            _ => {
                result.extend_from_slice(&responses[index..index + 2]);
                index += 2;
            }
        }
    }
    result
}

fn is_osc_color_report(sequence: &[u8]) -> bool {
    sequence.len() >= 5
        && sequence.starts_with(b"\x1b]1")
        && matches!(sequence[3], b'0' | b'1' | b'2')
        && sequence[4] == b';'
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

fn rfind(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .rposition(|window| window == needle)
}

fn count(haystack: &[u8], needle: &[u8]) -> usize {
    haystack
        .windows(needle.len())
        .filter(|window| *window == needle)
        .count()
}

#[cfg(test)]
mod tests {
    use super::{ColorScheme, TerminalQueries, theme_color_scheme, track_color_scheme_reports};
    use crate::ghostty::{Terminal, Theme};

    #[test]
    fn preserves_terminal_query_order() {
        let mut terminal = Terminal::new(80, 24).expect("terminal");
        let queries = TerminalQueries::detect(b"\x1b]11;?\x07\x1b]10;?\x1b\\\x1b[6n\x1b[0c");
        let before = String::from_utf8(queries.replies_before_feed(&Theme::default())).unwrap();
        assert!(before.starts_with("\x1b]11;rgb:1e1e/1e1e/1e1e"));
        terminal.write(b"abc");
        let after = String::from_utf8(queries.replies_after_feed(&terminal, &[])).unwrap();
        assert_eq!(after, "\x1b[1;4R\x1b[?1;2c");
    }

    #[test]
    fn classifies_background_luminance() {
        let mut theme = Theme::default();
        assert_eq!(theme_color_scheme(&theme), ColorScheme::Dark);
        theme.background = "#ffffff".to_owned();
        assert_eq!(theme_color_scheme(&theme), ColorScheme::Light);
    }

    #[test]
    fn plain_text_preserves_native_replies_and_color_subscription() {
        let terminal = Terminal::new(80, 24).expect("terminal");
        let queries = TerminalQueries::detect(b"ordinary terminal output\r\n");
        assert!(queries.replies_before_feed(&Theme::default()).is_empty());
        assert_eq!(
            queries.replies_after_feed(&terminal, b"\x1b[0n"),
            b"\x1b[0n"
        );
        for initial in [false, true] {
            let mut enabled = initial;
            track_color_scheme_reports(b"ordinary output", &mut enabled);
            assert_eq!(enabled, initial);
        }
    }

    #[test]
    fn answers_da1_without_cursor_query() {
        let terminal = Terminal::new(80, 24).expect("terminal");
        let queries = TerminalQueries::detect(b"\x1b[c");
        assert_eq!(queries.replies_after_feed(&terminal, &[]), b"\x1b[?1;2c");
        let queries = TerminalQueries::detect(b"\x1b[c\x1b[6n");
        assert_eq!(
            queries.replies_after_feed(&terminal, &[]),
            b"\x1b[?1;2c\x1b[1;1R"
        );
    }
}
