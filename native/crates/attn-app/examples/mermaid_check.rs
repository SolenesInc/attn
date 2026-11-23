use std::io::Read;
use std::process::ExitCode;

/// Reads one Mermaid source on stdin and says whether the app draws it whole: exit 0 when it draws with every
/// statement read, 1 with the statements it leaves out printed one per line, 2 when it does not draw at all.
fn main() -> ExitCode {
    let mut source = String::new();
    if let Err(error) = std::io::stdin().read_to_string(&mut source) {
        eprintln!("reading the source on stdin: {error}");
        return ExitCode::from(3);
    }
    if !rune_kit::mermaid::supported(&source) {
        return ExitCode::from(2);
    }
    let skipped = rune_kit::mermaid::skipped(&source);
    for statement in &skipped {
        println!("{statement}");
    }
    ExitCode::from(u8::from(!skipped.is_empty()))
}
