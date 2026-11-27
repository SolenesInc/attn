use super::detect::Tools;
use super::input::{HookInput, Raw, array_items, parse_object, text};
use super::{HookHarness, Notification, StopFailure};
use attn_protocol::{BackgroundTask, HookTurn};
use serde::Deserialize;

pub struct Claude;

static TOOLS: Tools = Tools {
    edits: &["Edit", "Write", "MultiEdit", "NotebookEdit"],
    patches: &[],
    sends: &["SendUserFile"],
    shell: |name| name.trim().eq_ignore_ascii_case("bash"),
};

#[derive(Deserialize, Default)]
struct StopTask<'a> {
    #[serde(borrow, default)]
    id: Raw<'a>,
    #[serde(borrow, default)]
    r#type: Raw<'a>,
    #[serde(borrow, default)]
    status: Raw<'a>,
    #[serde(borrow, default)]
    description: Raw<'a>,
    #[serde(borrow, default)]
    command: Raw<'a>,
    #[serde(borrow, default)]
    agent_type: Raw<'a>,
    #[serde(borrow, default)]
    name: Raw<'a>,
    #[serde(borrow, default)]
    server: Raw<'a>,
    #[serde(borrow, default)]
    tool: Raw<'a>,
}

impl StopTask<'_> {
    fn running(&self) -> bool {
        text(self.status).trim().eq_ignore_ascii_case("running")
    }

    fn record(&self) -> BackgroundTask {
        let kind = non_blank(&text(self.r#type));
        let wakes = match kind.as_deref() {
            Some("monitor") => "each_event",
            _ => "exit",
        };
        BackgroundTask {
            id: non_blank(&text(self.id)),
            description: non_blank(&text(self.description)),
            command: non_blank(&text(self.command)),
            agent_type: non_blank(&text(self.agent_type)),
            name: non_blank(&text(self.name)),
            server: non_blank(&text(self.server)),
            tool: non_blank(&text(self.tool)),
            wakes: Some(wakes.to_string()),
            kind,
        }
    }
}

#[derive(Deserialize, Default)]
struct LaunchResponse<'a> {
    #[serde(borrow, default, rename = "backgroundTaskId")]
    background_task_id: Raw<'a>,
    #[serde(borrow, default, rename = "taskId")]
    task_id: Raw<'a>,
    #[serde(borrow, default, rename = "agentId")]
    agent_id: Raw<'a>,
    #[serde(borrow, default, rename = "isAsync")]
    is_async: Raw<'a>,
}

fn non_blank(value: &str) -> Option<String> {
    let value = value.trim();
    (!value.is_empty()).then(|| value.to_string())
}

impl HookHarness for Claude {
    fn tools(&self) -> &'static Tools {
        &TOOLS
    }

    fn running_work(&self, input: &HookInput<'_>) -> Vec<BackgroundTask> {
        array_items(input.field("background_tasks"))
            .into_iter()
            .map(|item| parse_object::<StopTask>(Some(item)).unwrap_or_default())
            .filter(StopTask::running)
            .map(|task| task.record())
            .collect()
    }

    fn launched_task(&self, input: &HookInput<'_>) -> Option<String> {
        let response = parse_object::<LaunchResponse>(input.field("tool_response"))?;
        non_blank(&text(response.background_task_id))
            .or_else(|| non_blank(&text(response.task_id)))
            .or_else(|| {
                let launched = response.is_async.is_some_and(|raw| raw.get() == "true");
                launched
                    .then(|| non_blank(&text(response.agent_id)))
                    .flatten()
            })
    }

    fn scheduled_work(&self, input: &HookInput<'_>) -> usize {
        array_items(input.field("session_crons")).len()
    }

    fn notification(&self, input: &HookInput<'_>) -> Option<Notification> {
        let kind = text(input.field("notification_type"));
        let message = non_blank(&text(input.field("message")));
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
            message: non_blank(&text(input.field("error_message"))),
        })
    }

    fn compaction_trigger(&self, input: &HookInput<'_>) -> Option<String> {
        non_blank(&text(input.field("trigger")))
    }

    fn used_by_subagent(&self, input: &HookInput<'_>) -> bool {
        !text(input.field("agent_id")).trim().is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn record(task: &BackgroundTask) -> String {
        let field =
            |name: &str, value: &Option<String>| value.as_ref().map(|v| format!("{name}={v}"));
        [
            field("id", &task.id),
            field("kind", &task.kind),
            field("description", &task.description),
            field("command", &task.command),
            field("agent_type", &task.agent_type),
            field("name", &task.name),
            field("server", &task.server),
            field("tool", &task.tool),
            field("wakes", &task.wakes),
        ]
        .into_iter()
        .flatten()
        .collect::<Vec<_>>()
        .join(" ")
    }

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
                running: &["id=wv9p74ip7 kind=workflow name=hello-parallel wakes=exit"],
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"type":"workflow","status":"running"},{"type":"shell","status":"running"}]}"#,
                running: &["kind=workflow wakes=exit", "kind=shell wakes=exit"],
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
                running: &["kind=shell wakes=exit"],
                crons: 0,
            },
            Case {
                payload: r#"{"background_tasks":[{"status":"running"}]}"#,
                running: &["wakes=exit"],
                crons: 0,
            },
            Case {
                payload: r#"{"session_crons":[{"id":"43f0809f","schedule":"*/30 * * * *","recurring":true,"prompt":"echo recurring-probe"},{"id":"2b1dec68","schedule":"15 9 20 6 *","recurring":false,"prompt":"echo oneshot-probe"}]}"#,
                running: &[],
                crons: 2,
            },
            Case {
                payload: r#"{"background_tasks":[{"id":"bzd8fe67e","type":"shell","status":"running","description":"Sleep for 90 seconds in background","command":"sleep 90"},{"id":"a2c193c118d82726c","type":"subagent","status":"running","description":"Run sleep 75 then report completion","agent_type":"general-purpose"},{"id":"m1","type":"monitor","status":"running","server":"ci","tool":"watch"}],"session_crons":[]}"#,
                running: &[
                    "id=bzd8fe67e kind=shell description=Sleep for 90 seconds in background command=sleep 90 wakes=exit",
                    "id=a2c193c118d82726c kind=subagent description=Run sleep 75 then report completion agent_type=general-purpose wakes=exit",
                    "id=m1 kind=monitor server=ci tool=watch wakes=each_event",
                ],
                crons: 0,
            },
        ];
        for case in &cases {
            let input = HookInput::parse(case.payload.as_bytes());
            let running: Vec<String> = Claude.running_work(&input).iter().map(record).collect();
            assert_eq!(running, case.running, "{}", case.payload);
            assert_eq!(
                Claude.scheduled_work(&input),
                case.crons,
                "{}",
                case.payload
            );
        }
    }

    #[test]
    fn launched_tasks() {
        let cases = [
            (
                r#"{"tool_name":"Bash","tool_response":{"stdout":"","stderr":"","interrupted":false,"isImage":false,"noOutputExpected":false,"backgroundTaskId":"bqccrduyv"}}"#,
                Some("bqccrduyv"),
            ),
            (
                r#"{"tool_name":"Monitor","tool_response":{"taskId":"b1na7550g","timeoutMs":900000,"persistent":false}}"#,
                Some("b1na7550g"),
            ),
            (
                r#"{"tool_name":"Agent","tool_response":{"isAsync":true,"status":"async_launched","agentId":"a5c329f4628e0e0ce"}}"#,
                Some("a5c329f4628e0e0ce"),
            ),
            (
                r#"{"tool_name":"Agent","tool_response":{"agentId":"a5c329f4628e0e0ce","content":[]}}"#,
                None,
            ),
            (
                r#"{"tool_name":"Bash","tool_response":{"stdout":"ok","stderr":"","interrupted":false}}"#,
                None,
            ),
            (r#"{"tool_name":"Read","tool_response":"text"}"#, None),
        ];
        for (payload, want) in cases {
            let got = Claude.launched_task(&HookInput::parse(payload.as_bytes()));
            assert_eq!(got.as_deref(), want, "{payload}");
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
