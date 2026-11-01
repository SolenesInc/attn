use std::io::{BufRead, BufReader, Write};
use std::process::{Command, Stdio};

use serde_json::Value;

#[test]
fn the_helper_greets_refuses_work_before_prepare_and_exits_when_its_lease_closes() {
    let cache = std::env::temp_dir().join(format!("attn-speech-protocol-{}", std::process::id()));
    let mut child = Command::new(env!("CARGO_BIN_EXE_attn-speech"))
        .args(["serve", "--cache"])
        .arg(&cache)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    let mut lines = BufReader::new(child.stdout.take().unwrap()).lines();
    let mut next = || serde_json::from_str::<Value>(&lines.next().unwrap().unwrap()).unwrap();

    let hello = next();
    assert_eq!(hello["event"], "hello");
    assert_eq!(hello["protocol"], 1);
    assert_eq!(hello["model"], "Parakeet Unified English");

    writeln!(stdin, "not json").unwrap();
    let unreadable = next();
    assert_eq!(unreadable["event"], "error");
    assert!(
        unreadable["message"]
            .as_str()
            .unwrap()
            .starts_with("unreadable request")
    );

    let body = b"RIFF";
    writeln!(
        stdin,
        r#"{{"op":"transcribe","id":7,"bytes":{}}}"#,
        body.len()
    )
    .unwrap();
    stdin.write_all(body).unwrap();
    writeln!(stdin, r#"{{"op":"warm","id":8}}"#).unwrap();
    for id in [7, 8] {
        let started = next();
        assert_eq!(started["event"], "started");
        assert_eq!(started["id"], id);
        let refused = next();
        assert_eq!(refused["id"], id);
        assert_eq!(refused["event"], "error");
        assert!(
            refused["message"]
                .as_str()
                .unwrap()
                .contains("not prepared"),
            "{refused}"
        );
    }

    drop(stdin);
    assert!(child.wait().unwrap().success());
    assert!(!cache.exists(), "nothing touches the cache before prepare");
}
