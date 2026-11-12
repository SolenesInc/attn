pub mod detect;
pub mod input;

use crate::client::{Client, SendError, read_json_value};
use crate::instance::Paths;
use attn_protocol::go_values::{
    CODEX_SHARED_PROFILE_ENV, CODEX_THREAD_PREFIX, STATE_IDLE, STATE_LAUNCHING,
    STATE_PENDING_APPROVAL, STATE_SCHEDULED, STATE_UNKNOWN, STATE_WAITING_INPUT, STATE_WORKING,
};
use attn_protocol::{
    FilesEditedMessage, HookCompactionMessage, HookNotificationMessage, HookStopFailureMessage,
    OpenSentFilesMessage, PullRequestCreatedMessage, SetSessionResumeIDMessage, StateMessage,
    StopBackgroundTask, StopMessage,
};
use input::{HookInput, text};
use std::io::{self, Write};

pub const COMMANDS: &[&str] = &[
    "_hook-stop",
    "_hook-session-start",
    "_hook-notification",
    "_hook-stop-failure",
    "_hook-compact",
    "_hook-state",
    "_hook-todo",
    "_hook-tool-use",
];

pub struct Failure {
    pub refusal: Option<String>,
}

pub fn terminal_from_env(getenv: &dyn Fn(&str) -> String) -> String {
    let terminal = getenv("ATTN_TERMINAL_ID");
    if !terminal.is_empty() {
        return terminal;
    }
    let profile = getenv(CODEX_SHARED_PROFILE_ENV);
    if !profile.is_empty() {
        return codex_thread_terminal(&profile, &getenv("CODEX_THREAD_ID"));
    }
    let legacy = getenv("ATTN_SESSION_ID");
    if !legacy.is_empty() {
        return legacy;
    }
    server_caller_from_env().unwrap_or_default()
}

pub fn server_caller_from_env() -> Result<String, String> {
    let mut names = Vec::new();
    let mut caller = String::new();
    for (name, value) in std::env::vars_os() {
        let name = name.to_string_lossy();
        if name.len() > "_ROOT_CONVERSATION".len()
            && name.ends_with("_ROOT_CONVERSATION")
            && !value.is_empty()
        {
            names.push(name.to_string());
            caller = format!("server-env:{name}:{}", value.to_string_lossy());
        }
    }
    if names.len() > 1 {
        names.sort();
        return Err(format!(
            "conflicting caller variables: {}; keep only the current server's root conversation",
            names.join(", ")
        ));
    }
    Ok(caller)
}

fn codex_thread_terminal(profile: &str, thread: &str) -> String {
    let thread = thread.trim();
    if thread.is_empty() {
        return String::new();
    }
    format!("{CODEX_THREAD_PREFIX}{profile}:{thread}")
}

fn say(line: &str) {
    let _ = writeln!(io::stderr(), "{line}");
}

fn usage(text: &str) -> Failure {
    say(&format!("usage: attn {text}"));
    Failure { refusal: None }
}

fn failed(what: &str, error: SendError) -> Failure {
    say(&format!("error {what}: {error}"));
    Failure {
        refusal: error.refusal,
    }
}

fn is_hook_state(value: &str) -> bool {
    [
        STATE_LAUNCHING,
        STATE_WORKING,
        STATE_PENDING_APPROVAL,
        STATE_WAITING_INPUT,
        STATE_IDLE,
        STATE_UNKNOWN,
        STATE_SCHEDULED,
        "recoverable",
    ]
    .contains(&value)
}

fn non_blank(value: &str) -> Option<String> {
    (!value.trim().is_empty()).then(|| value.to_string())
}

fn read_stdin() -> Vec<u8> {
    read_json_value(&mut io::stdin().lock()).unwrap_or_default()
}

pub struct Hook<'a> {
    pub argv: &'a [String],
    pub paths: &'a Paths<'a>,
    pub getenv: &'a dyn Fn(&str) -> String,
    pub caller: String,
}

impl Hook<'_> {
    pub fn run(&mut self, command: &str) -> Result<(), Failure> {
        match command {
            "_hook-stop" => self.stop(),
            "_hook-session-start" => self.session_start(),
            "_hook-notification" => self.notification(),
            "_hook-stop-failure" => self.stop_failure(),
            "_hook-compact" => self.compact(),
            "_hook-state" => self.state(),
            "_hook-tool-use" => self.tool_use(),
            _ => Ok(()),
        }
    }

    fn own_terminal(&self) -> String {
        terminal_from_env(self.getenv).trim().to_string()
    }

    fn terminal_arg_or_env(&self, index: usize) -> String {
        self.argv
            .get(index)
            .map_or_else(|| self.own_terminal(), |arg| arg.trim().to_string())
    }

    fn caller_from(&self, id: String, input: &HookInput<'_>) -> String {
        if !(self.getenv)("ATTN_TERMINAL_ID").is_empty() {
            return id;
        }
        let profile = (self.getenv)(CODEX_SHARED_PROFILE_ENV);
        if profile.is_empty() {
            return id;
        }
        codex_thread_terminal(&profile, &text(input.session_id))
    }

    fn client(&mut self, caller: &str) -> Client<'_> {
        self.caller = caller.trim().to_string();
        let socket = (self.getenv)("ATTN_SOCKET_PATH");
        Client::new(self.paths, socket.trim(), caller)
    }

    fn stop(&mut self) -> Result<(), Failure> {
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let id = self.caller_from(self.terminal_arg_or_env(2), &input);
        if id.is_empty() {
            return Err(usage("_hook-stop [session_id]"));
        }
        let mut message = StopMessage::new();
        message.id.clone_from(&id);
        message.transcript_path = text(input.transcript_path);
        message.pending_session_crons = i64::try_from(input.session_cron_count()).ok();
        let tasks: Vec<StopBackgroundTask> = input
            .background_tasks()
            .into_iter()
            .map(|task| StopBackgroundTask {
                r#type: text(task.r#type),
                status: text(task.status),
                name: Some(text(task.description)),
            })
            .collect();
        message.background_tasks = (!tasks.is_empty()).then_some(tasks);
        self.client(&id)
            .send(&message)
            .map_err(|error| failed("sending stop", error))
    }

    fn observe_conversation(&mut self, id: &str, input: &HookInput<'_>) {
        let session = text(input.session_id);
        let session = session.trim();
        let transcript = text(input.transcript_path);
        let transcript = transcript.trim();
        if session.is_empty() || transcript.is_empty() {
            return;
        }
        let mut message = SetSessionResumeIDMessage::new();
        message.id = id.to_string();
        message.resume_session_id = session.to_string();
        message.transcript_path = Some(transcript.to_string());
        let lifecycle = (self.getenv)("ATTN_RUNTIME_LIFECYCLE");
        if !lifecycle.is_empty() {
            message.lifecycle_id = Some(lifecycle);
        }
        if let Err(error) = self.client(id).send(&message) {
            say(&format!(
                "warning: could not observe agent conversation: {error}"
            ));
        }
    }

    fn session_start(&mut self) -> Result<(), Failure> {
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let id = self.caller_from(self.terminal_arg_or_env(2), &input);
        if id.is_empty() {
            return Err(usage("_hook-session-start [session_id]"));
        }
        self.caller = id.trim().to_string();
        self.observe_conversation(&id, &input);
        Ok(())
    }

    fn state_args(&self) -> (String, String, String) {
        let Some(first) = self.argv.get(2).map(|arg| arg.trim().to_string()) else {
            return (String::new(), String::new(), String::new());
        };
        let word = |index: usize| {
            self.argv
                .get(index)
                .map_or_else(String::new, |arg| arg.trim().to_string())
        };
        if is_hook_state(&first) {
            return (self.own_terminal(), first, word(3));
        }
        if self.argv.len() < 4 {
            return (String::new(), String::new(), String::new());
        }
        (first, word(3), word(4))
    }

    fn state(&mut self) -> Result<(), Failure> {
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let (id, state, hook_event) = self.state_args();
        let id = self.caller_from(id, &input);
        if id.is_empty() || state.is_empty() {
            return Err(usage("_hook-state [session_id] <state>"));
        }
        if hook_event.eq_ignore_ascii_case("user_prompt_submit") {
            self.observe_conversation(&id, &input);
        }
        let mut message = StateMessage::new();
        message.id.clone_from(&id);
        message.state = state;
        message.permission_mode = non_blank(&text(input.permission_mode));
        message.hook_event = non_blank(&hook_event);
        message.prompt = non_blank(&text(input.prompt));
        self.client(&id)
            .send(&message)
            .map_err(|error| failed("updating state", error))
    }

    fn notification(&mut self) -> Result<(), Failure> {
        let id = self.terminal_arg_or_env(2);
        if id.is_empty() {
            return Err(usage("_hook-notification [session_id]"));
        }
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let notification_type = text(input.notification_type);
        if notification_type.trim().is_empty() {
            return Ok(());
        }
        let mut message = HookNotificationMessage::new();
        message.id.clone_from(&id);
        message.notification_type = notification_type;
        message.message = non_blank(&text(input.message));
        self.client(&id)
            .send(&message)
            .map_err(|error| failed("recording notification", error))
    }

    fn stop_failure(&mut self) -> Result<(), Failure> {
        let id = self.terminal_arg_or_env(2);
        if id.is_empty() {
            return Err(usage("_hook-stop-failure [session_id]"));
        }
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let error_type = text(input.error_type);
        let error_type = error_type.trim();
        if error_type.is_empty() {
            return Ok(());
        }
        let mut message = HookStopFailureMessage::new();
        message.id.clone_from(&id);
        message.error_type = error_type.to_string();
        message.error_message = non_blank(&text(input.error_message));
        self.client(&id)
            .send(&message)
            .map_err(|error| failed("recording stop failure", error))
    }

    fn compact(&mut self) -> Result<(), Failure> {
        let id = self.terminal_arg_or_env(2);
        if id.is_empty() || self.argv.len() < 4 {
            return Err(usage("_hook-compact <session_id> <start|end>"));
        }
        let active = self.argv[3].trim() == "start";
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let mut message = HookCompactionMessage::new();
        message.id.clone_from(&id);
        message.active = active;
        message.trigger = non_blank(&text(input.trigger));
        self.client(&id)
            .send(&message)
            .map_err(|error| failed("recording compaction", error))
    }

    fn tool_use(&mut self) -> Result<(), Failure> {
        let stdin = read_stdin();
        let input = HookInput::parse(&stdin);
        let id = self.caller_from(self.terminal_arg_or_env(2), &input);
        if id.is_empty() {
            return Err(usage("_hook-tool-use [session_id]"));
        }
        let client = self.client(&id);
        if text(input.agent_id).trim().is_empty() {
            let mut message = StateMessage::new();
            message.id.clone_from(&id);
            message.state = STATE_WORKING.to_string();
            client
                .send(&message)
                .map_err(|error| failed("updating state", error))?;
        }
        let tool_name = text(input.tool_name);
        let cwd = text(input.cwd);
        let edited = detect::markdown_edits(&tool_name, input.tool_input, &cwd);
        if !edited.is_empty() {
            let mut message = FilesEditedMessage::new();
            message.id.clone_from(&id);
            message.paths = edited;
            if let Err(error) = client.send(&message) {
                say(&format!("warning: could not record edited files: {error}"));
            }
        }
        let sent = detect::sent_files(&tool_name, input.tool_input, &cwd);
        if !sent.is_empty() {
            let mut message = OpenSentFilesMessage::new();
            message.terminal_id = Some(id.clone());
            message.paths = sent;
            if let Err(error) = client.send(&message) {
                say(&format!("warning: could not open sent files: {error}"));
            }
        }
        for url in detect::pull_requests_created(&tool_name, input.tool_input, input.tool_response)
        {
            let mut message = PullRequestCreatedMessage::new();
            message.terminal_id = Some(id.clone());
            message.url.clone_from(&url);
            if let Err(error) = client.send(&message) {
                say(&format!(
                    "warning: could not record pull request {url}: {error}"
                ));
            }
        }
        Ok(())
    }
}
