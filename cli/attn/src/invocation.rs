use crate::client::Client;
use crate::instance::Paths;
use serde_json::Value;
use std::collections::BTreeMap;

const ERROR_REFUSED: &str = "refused";
const ERROR_FAILED: &str = "failed";
const ERROR_USAGE: &str = "usage";

pub struct Invocation<'a> {
    pub path: &'a str,
    pub caller: String,
    pub refusal: Option<String>,
    pub usage_error: Option<&'a str>,
    pub session: Option<String>,
}

pub fn record(paths: &Paths<'_>, invocation: Invocation<'_>, code: u8, terminal_from_env: &str) {
    let mut props = BTreeMap::new();
    props.insert("ok".to_string(), Value::Bool(code == 0));
    props.insert("exit_code".to_string(), Value::from(code));
    let kind = match (invocation.usage_error, invocation.refusal) {
        (Some(usage), _) => Some(usage),
        (None, Some(reason)) if code != 0 => {
            props.insert("refusal".to_string(), reason.into());
            Some(ERROR_REFUSED)
        }
        (None, _) if code == 2 => Some(ERROR_USAGE),
        (None, _) if code != 0 => Some(ERROR_FAILED),
        (None, _) => None,
    };
    if let Some(kind) = kind {
        props.insert("error_kind".to_string(), kind.into());
    }
    let caller = if invocation.caller.is_empty() {
        terminal_from_env
    } else {
        invocation.caller.as_str()
    };
    let _ =
        Client::new(paths, "", caller).record_analytics(invocation.path, invocation.session, props);
}
