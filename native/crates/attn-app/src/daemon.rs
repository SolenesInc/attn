use std::path::PathBuf;
use std::process::{Command, Stdio};

/// Starts or reuses the instance's daemon with the `attnd` bundled beside this executable.
/// `attnd daemon ensure` returns once the daemon answers health, or says why it could not.
pub fn ensure() -> Result<(), String> {
    let attnd = bundled_attnd()?;
    let output = Command::new(&attnd)
        .args(["daemon", "ensure"])
        .stdin(Stdio::null())
        .output()
        .map_err(|error| format!("run {} daemon ensure: {error}", attnd.display()))?;
    if output.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&output.stderr);
    Err(format!(
        "{} daemon ensure failed ({}): {}",
        attnd.display(),
        output.status,
        stderr.trim()
    ))
}

fn bundled_attnd() -> Result<PathBuf, String> {
    let exe =
        std::env::current_exe().map_err(|error| format!("locate this executable: {error}"))?;
    let attnd = exe.with_file_name("attnd");
    if attnd.is_file() {
        Ok(attnd)
    } else {
        Err(format!(
            "no attnd beside {}; install the app with `make install INSTANCE=<name> APP=native`",
            exe.display()
        ))
    }
}
