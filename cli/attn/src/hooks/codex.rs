use super::detect::Tools;
use super::input::{HookInput, text};
use super::{HookHarness, server_caller};

const CODEX_SHARED_PROFILE_ENV: &str = "ATTN_CODEX_SHARED_PROFILE";
const CODEX_THREAD_ENV: &str = "CODEX_THREAD_ID";

pub struct Codex;

static TOOLS: Tools = Tools {
    edits: &[],
    patches: &["apply_patch"],
    sends: &[],
    shell: |name| {
        let name = name.trim().to_lowercase();
        name == "shell" || name == "exec_command" || name.ends_with(".exec_command")
    },
};

impl HookHarness for Codex {
    fn tools(&self) -> &'static Tools {
        &TOOLS
    }

    fn caller(
        &self,
        getenv: &dyn Fn(&str) -> String,
        input: Option<&HookInput<'_>>,
    ) -> Option<String> {
        let profile = getenv(CODEX_SHARED_PROFILE_ENV);
        if profile.is_empty() {
            return None;
        }
        let thread = input.map_or_else(
            || getenv(CODEX_THREAD_ENV),
            |input| text(input.field("session_id")),
        );
        let thread = thread.trim();
        if thread.is_empty() {
            return Some(String::new());
        }
        Some(server_caller(CODEX_THREAD_ENV, &profile, thread))
    }
}
