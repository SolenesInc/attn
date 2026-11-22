use super::HookHarness;
use super::detect::Tools;

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
}
