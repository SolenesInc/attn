use crate::client::Client;
use crate::instance::Paths;
use serde_json::Value;
use std::collections::BTreeMap;

const ERROR_REFUSED: &str = "refused";
const ERROR_FAILED: &str = "failed";

pub struct Invocation<'a> {
    pub path: &'a str,
    pub caller: String,
    pub refusal: Option<String>,
}

pub fn record(paths: &Paths<'_>, invocation: Invocation<'_>, code: u8, terminal_from_env: &str) {
    let mut props = BTreeMap::new();
    props.insert("ok".to_string(), Value::Bool(code == 0));
    props.insert("exit_code".to_string(), Value::from(code));
    if code != 0 {
        match invocation.refusal {
            Some(reason) => {
                props.insert("error_kind".to_string(), ERROR_REFUSED.into());
                props.insert("refusal".to_string(), reason.into());
            }
            None => {
                props.insert("error_kind".to_string(), ERROR_FAILED.into());
            }
        }
    }
    let caller = if invocation.caller.is_empty() {
        terminal_from_env
    } else {
        invocation.caller.as_str()
    };
    let _ = Client::new(paths, "", caller).record_analytics(invocation.path, props);
}
