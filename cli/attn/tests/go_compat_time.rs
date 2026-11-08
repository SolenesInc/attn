use attn::go::time::Time;
use serde_json::Value;
use std::fs;

unsafe extern "C" {
    fn tzset();
}

#[test]
fn local_time_matches_go() {
    let path = format!(
        "{}/../testdata/local_time.jsonl",
        env!("CARGO_MANIFEST_DIR")
    );
    let body = fs::read_to_string(path).expect("corpus");
    let mut total = 0;
    let mut mismatches = Vec::new();
    for line in body.lines() {
        let case: Value = serde_json::from_str(line).expect("JSON");
        unsafe {
            std::env::set_var("TZ", case["zone"].as_str().expect("zone"));
            tzset();
        }
        total += 1;
        let input = case["input"].as_str().expect("input");
        let expected_error = case["error"].as_str().expect("error");
        let rfc3339_error = Time::parse_rfc3339(input).err().unwrap_or_default();
        if rfc3339_error != case["rfc3339_error"].as_str().expect("rfc3339_error") {
            mismatches.push(format!("{line}\n  RFC3339 got error {rfc3339_error}"));
        }
        match Time::parse_rfc3339_nano(input) {
            Err(error) if error != expected_error => {
                mismatches.push(format!("{line}\n  got error {error}"));
            }
            Err(_) => {}
            Ok(time) => {
                if !expected_error.is_empty() {
                    mismatches.push(format!("{line}\n  parsed, Go refused"));
                }
                for (layout, expected) in case["formats"].as_object().expect("formats") {
                    let actual = time.format_local(layout);
                    if actual != expected.as_str().expect("string") {
                        mismatches.push(format!("{line}\n  {layout} got {actual}"));
                    }
                }
                for (layout, expected) in case["own_formats"].as_object().expect("own_formats") {
                    let actual = time.format(layout);
                    if actual != expected.as_str().expect("string") {
                        mismatches.push(format!("{line}\n  own {layout} got {actual}"));
                    }
                }
            }
        }
    }
    assert!(total > 0, "local_time has no cases");
    assert!(
        mismatches.is_empty(),
        "{} of {total} cases differ from Go. First:\n{}",
        mismatches.len(),
        mismatches
            .iter()
            .take(5)
            .cloned()
            .collect::<Vec<_>>()
            .join("\n")
    );
}
