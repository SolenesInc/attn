use serde::Deserialize;
use serde_json::value::RawValue;

pub type Raw<'a> = Option<&'a RawValue>;

#[derive(Deserialize, Default)]
pub struct HookInput<'a> {
    #[serde(borrow, default)]
    pub session_id: Raw<'a>,
    #[serde(borrow, default)]
    pub transcript_path: Raw<'a>,
    #[serde(borrow, default)]
    pub prompt: Raw<'a>,
    #[serde(borrow, default)]
    pub tool_name: Raw<'a>,
    #[serde(borrow, default)]
    pub tool_input: Raw<'a>,
    #[serde(borrow, default)]
    pub tool_response: Raw<'a>,
    #[serde(borrow, default)]
    pub cwd: Raw<'a>,
    #[serde(borrow, default)]
    pub background_tasks: Raw<'a>,
    #[serde(borrow, default)]
    pub session_crons: Raw<'a>,
    #[serde(borrow, default)]
    pub permission_mode: Raw<'a>,
    #[serde(borrow, default)]
    pub message: Raw<'a>,
    #[serde(borrow, default)]
    pub notification_type: Raw<'a>,
    #[serde(borrow, default)]
    pub agent_id: Raw<'a>,
    #[serde(borrow, default)]
    pub error_type: Raw<'a>,
    #[serde(borrow, default)]
    pub error_message: Raw<'a>,
    #[serde(borrow, default)]
    pub trigger: Raw<'a>,
}

#[derive(Deserialize, Default)]
pub struct BackgroundTask<'a> {
    #[serde(borrow, default)]
    pub r#type: Raw<'a>,
    #[serde(borrow, default)]
    pub status: Raw<'a>,
    #[serde(borrow, default)]
    pub description: Raw<'a>,
}

impl<'a> HookInput<'a> {
    pub fn parse(bytes: &'a [u8]) -> Self {
        let mut deserializer = serde_json::Deserializer::from_slice(bytes);
        HookInput::deserialize(&mut deserializer).unwrap_or_default()
    }

    pub fn background_tasks(&self) -> Vec<BackgroundTask<'a>> {
        array_items(self.background_tasks)
            .into_iter()
            .map(|item| parse_object::<BackgroundTask>(Some(item)).unwrap_or_default())
            .collect()
    }

    pub fn session_cron_count(&self) -> usize {
        array_items(self.session_crons).len()
    }
}

pub fn array_items(raw: Raw<'_>) -> Vec<&RawValue> {
    let Some(raw) = raw else {
        return Vec::new();
    };
    if !raw.get().starts_with('[') {
        return Vec::new();
    }
    serde_json::from_str(raw.get()).unwrap_or_default()
}

pub fn parse_object<'a, T: Deserialize<'a>>(raw: Raw<'a>) -> Option<T> {
    let raw = raw?;
    if !raw.get().starts_with('{') {
        return None;
    }
    serde_json::from_str(raw.get()).ok()
}

pub fn text(raw: Raw<'_>) -> String {
    string_value(raw).unwrap_or_default()
}

pub fn string_value(raw: Raw<'_>) -> Option<String> {
    let body = raw?.get().strip_prefix('"')?.strip_suffix('"')?;
    Some(unquote(body))
}

pub fn strict_text(raw: Raw<'_>) -> Option<String> {
    match raw {
        None => Some(String::new()),
        Some(value) if value.get() == "null" => Some(String::new()),
        Some(_) => string_value(raw),
    }
}

fn unquote(body: &str) -> String {
    let mut out = String::with_capacity(body.len());
    let mut chars = body.chars();
    while let Some(ch) = chars.next() {
        if ch != '\\' {
            out.push(ch);
            continue;
        }
        match chars.next() {
            Some('n') => out.push('\n'),
            Some('t') => out.push('\t'),
            Some('r') => out.push('\r'),
            Some('b') => out.push('\u{8}'),
            Some('f') => out.push('\u{c}'),
            Some('u') => out.push(unicode_escape(&mut chars)),
            Some(other) => out.push(other),
            None => {}
        }
    }
    out
}

fn hex4(chars: &mut std::str::Chars<'_>) -> u32 {
    (0..4).fold(0, |value, _| {
        value * 16 + chars.next().and_then(|c| c.to_digit(16)).unwrap_or(0)
    })
}

fn unicode_escape(chars: &mut std::str::Chars<'_>) -> char {
    let first = hex4(chars);
    if !(0xD800..0xDC00).contains(&first) {
        return char::from_u32(first).unwrap_or('\u{FFFD}');
    }
    let mut lookahead = chars.clone();
    if lookahead.next() == Some('\\') && lookahead.next() == Some('u') {
        let second = hex4(&mut lookahead);
        if (0xDC00..0xE000).contains(&second) {
            *chars = lookahead;
            let combined = 0x10000 + ((first - 0xD800) << 10) + (second - 0xDC00);
            return char::from_u32(combined).unwrap_or('\u{FFFD}');
        }
    }
    '\u{FFFD}'
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stop_facts_cases() {
        struct Case {
            payload: &'static str,
            statuses: &'static [&'static str],
            names: Option<&'static [&'static str]>,
            crons: usize,
        }
        let cases = [
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}"#,
                statuses: &["running"],
                names: None,
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}"#,
                statuses: &["running", "running", "running"],
                names: None,
                crons: 0,
            },
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}"#,
                statuses: &[],
                names: None,
                crons: 0,
            },
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false}"#,
                statuses: &[],
                names: None,
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"type":"workflow","status":"completed"}]}"#,
                statuses: &["completed"],
                names: None,
                crons: 0,
            },
            Case {
                payload: r#"{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}"#,
                statuses: &[],
                names: None,
                crons: 2,
            },
            Case {
                payload: r#"{"background_tasks":[{"id":"bzd8fe67e","type":"shell","status":"running","description":"Sleep for 90 seconds in background","command":"sleep 90"},{"id":"a2c193c118d82726c","type":"subagent","status":"running","description":"Run sleep 75 then report completion","agent_type":"general-purpose"}],"session_crons":[]}"#,
                statuses: &["running", "running"],
                names: Some(&[
                    "Sleep for 90 seconds in background",
                    "Run sleep 75 then report completion",
                ]),
                crons: 0,
            },
        ];
        for case in &cases {
            let input = HookInput::parse(case.payload.as_bytes());
            let tasks = input.background_tasks();
            let statuses: Vec<String> = tasks.iter().map(|task| text(task.status)).collect();
            assert_eq!(statuses, case.statuses, "{}", case.payload);
            if let Some(names) = case.names {
                let got: Vec<String> = tasks.iter().map(|task| text(task.description)).collect();
                assert_eq!(got, names, "{}", case.payload);
            }
            assert_eq!(input.session_cron_count(), case.crons, "{}", case.payload);
        }
    }

    #[test]
    fn a_lone_surrogate_keeps_the_rest_of_the_input() {
        let input = HookInput::parse(br#"{"prompt":"bad \ud83d end","session_id":"s1"}"#);
        assert_eq!(text(input.prompt), "bad \u{FFFD} end");
        assert_eq!(text(input.session_id), "s1");
    }

    #[test]
    fn a_wrong_field_type_keeps_the_other_fields() {
        let input = HookInput::parse(br#"{"prompt":7,"session_id":"s1"}"#);
        assert_eq!(text(input.prompt), "");
        assert_eq!(text(input.session_id), "s1");
    }
}
