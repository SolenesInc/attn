use super::detect::Tools;
use super::input::{HookInput, Raw, array_items, parse_object, text};
use super::{HookHarness, Notification, StopFailure};
use attn::go::quote::quote;
use attn_protocol::HookTurn;
use serde::Deserialize;

pub struct Claude;

static TOOLS: Tools = Tools {
    edits: &["Edit", "Write", "MultiEdit", "NotebookEdit"],
    patches: &[],
    sends: &["SendUserFile"],
    shell: |name| name.trim().eq_ignore_ascii_case("bash"),
};

#[derive(Deserialize, Default)]
struct BackgroundTask<'a> {
    #[serde(borrow, default)]
    r#type: Raw<'a>,
    #[serde(borrow, default)]
    status: Raw<'a>,
    #[serde(borrow, default)]
    description: Raw<'a>,
}

fn background_tasks<'a>(input: &HookInput<'a>) -> Vec<BackgroundTask<'a>> {
    array_items(input.field("background_tasks"))
        .into_iter()
        .map(|item| parse_object::<BackgroundTask>(Some(item)).unwrap_or_default())
        .collect()
}

fn label(task: &BackgroundTask<'_>) -> String {
    let kind = text(task.r#type);
    let kind = kind.trim();
    let name = text(task.description);
    let name = name.trim();
    match (kind.is_empty(), name.is_empty()) {
        (true, true) => "background task".to_string(),
        (false, true) => kind.to_string(),
        (true, false) => quote(name.as_bytes()),
        (false, false) => format!("{kind} {}", quote(name.as_bytes())),
    }
}

fn non_blank(value: String) -> Option<String> {
    (!value.trim().is_empty()).then_some(value)
}

impl HookHarness for Claude {
    fn tools(&self) -> &'static Tools {
        &TOOLS
    }

    fn running_work(&self, input: &HookInput<'_>) -> Vec<String> {
        background_tasks(input)
            .iter()
            .filter(|task| text(task.status).trim().eq_ignore_ascii_case("running"))
            .map(label)
            .collect()
    }

    fn scheduled_work(&self, input: &HookInput<'_>) -> usize {
        array_items(input.field("session_crons")).len()
    }

    fn notification(&self, input: &HookInput<'_>) -> Option<Notification> {
        let kind = text(input.field("notification_type"));
        let message = non_blank(text(input.field("message")));
        match kind.trim() {
            "" => None,
            "permission_prompt" => Some(Notification {
                turn: HookTurn::from("approval"),
                detail: message,
            }),
            "idle_prompt" => Some(Notification {
                turn: HookTurn::from("prompt_idle"),
                detail: message,
            }),
            other => Some(Notification {
                turn: HookTurn::from("unknown"),
                detail: Some(other.to_string()),
            }),
        }
    }

    fn stop_failure(&self, input: &HookInput<'_>) -> Option<StopFailure> {
        let error_type = text(input.field("error_type")).trim().to_string();
        (!error_type.is_empty()).then(|| StopFailure {
            error_type,
            message: non_blank(text(input.field("error_message"))),
        })
    }

    fn compaction_trigger(&self, input: &HookInput<'_>) -> Option<String> {
        non_blank(text(input.field("trigger")))
    }

    fn used_by_subagent(&self, input: &HookInput<'_>) -> bool {
        !text(input.field("agent_id")).trim().is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stop_facts_cases() {
        struct Case {
            payload: &'static str,
            running: &'static [&'static str],
            crons: usize,
        }
        let cases = [
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[{"id":"wv9p74ip7","type":"workflow","status":"running","name":"hello-parallel"}],"session_crons":[]}"#,
                running: &["workflow"],
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"},{"type":"shell","status":"running"}]}"#,
                running: &["workflow", "shell", "shell"],
                crons: 0,
            },
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false,"background_tasks":[],"session_crons":[]}"#,
                running: &[],
                crons: 0,
            },
            Case {
                payload: r#"{"hook_event_name":"Stop","stop_hook_active":false}"#,
                running: &[],
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"type":"workflow","status":"completed"},{"type":"shell","status":"Running"}]}"#,
                running: &["shell"],
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"status":"running"}]}"#,
                running: &["background task"],
                crons: 0,
            },
            Case {
                payload: r#"{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}"#,
                running: &[],
                crons: 2,
            },
            Case {
                payload: r#"{"background_tasks":[{"id":"bzd8fe67e","type":"shell","status":"running","description":"Sleep for 90 seconds in background","command":"sleep 90"},{"id":"a2c193c118d82726c","type":"subagent","status":"running","description":"Run sleep 75 then report completion","agent_type":"general-purpose"}],"session_crons":[]}"#,
                running: &[
                    r#"shell "Sleep for 90 seconds in background""#,
                    r#"subagent "Run sleep 75 then report completion""#,
                ],
                crons: 0,
            },
        ];
        for case in &cases {
            let input = HookInput::parse(case.payload.as_bytes());
            assert_eq!(
                Claude.running_work(&input),
                case.running,
                "{}",
                case.payload
            );
            assert_eq!(
                Claude.scheduled_work(&input),
                case.crons,
                "{}",
                case.payload
            );
        }
    }

    #[test]
    fn notification_turns() {
        let cases = [
            (
                r#"{"notification_type":"permission_prompt","message":"Allow edit?"}"#,
                Some(("approval", Some("Allow edit?"))),
            ),
            (
                r#"{"notification_type":"idle_prompt","message":"waiting"}"#,
                Some(("prompt_idle", Some("waiting"))),
            ),
            (
                r#"{"notification_type":"auth_success","message":"ok"}"#,
                Some(("unknown", Some("auth_success"))),
            ),
            (r#"{"notification_type":"  ","message":"ok"}"#, None),
            (r#"{"message":"ok"}"#, None),
        ];
        for (payload, want) in cases {
            let got = Claude.notification(&HookInput::parse(payload.as_bytes()));
            let got = got.as_ref().map(|n| (n.turn.as_str(), n.detail.as_deref()));
            assert_eq!(got, want, "{payload}");
        }
    }
}
