use super::support::{bool_flag, client, current_session_or_exit, parse_interspersed, string_flag};
use super::{Run, Stop, USAGE_UNKNOWN_COMMAND, absolute, print_stdout};
use crate::paths::{self, os_error_text};
use attn::go::flag::{Flag, FlagDefault, FlagSet};
use attn::go::{base64, json};
use attn_protocol::{BrowserControlMessage, OpenBrowserMessage};
use serde_json::{Map, Value, json};
use std::fs::OpenOptions;
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::time::{SystemTime, UNIX_EPOCH};

pub const USAGE: &str = r"usage: attn browser <command>

commands:
  open <url> [--session <id>]                 open or navigate the browser tile
  snapshot [--session <id>]                   print a semantic page snapshot
  find --using <strategy> --value <value>      find an element and return its reference
  wait --using <strategy> --value <value>      wait for attached/visible/hidden/detached
  click --selector <css>|--element <id>        click an element
  type --selector <css>|--element <id> --text  replace an input's value
  back | forward | reload                     navigate browser history
  press --text <key>                          send a keyboard key
  scroll [--x <px>] [--y <px>]                scroll the page
  cookies                                     list cookies for the current page
  command <action> [--params <json>]           call the WebDriver-shaped API directly
  screenshot [path] [--session <id>]          save a PNG (default: attn-browser.png)
  pdf [path] [--params <json>]                 save a PDF (default: attn-browser.pdf)
";

const DEFAULT_WAIT_TIMEOUT_MS: i64 = 5000;
const PRIVATE_FILE_MODE: u32 = 0o600;
const KNOWN_COMMANDS: &[&str] = &[
    "open",
    "snapshot",
    "find",
    "wait",
    "click",
    "type",
    "reload",
    "back",
    "forward",
    "press",
    "scroll",
    "cookies",
    "command",
    "screenshot",
    "pdf",
];

fn encode_params(params: &Value) -> String {
    let encoded = json::encode(params).unwrap_or_default();
    String::from_utf8_lossy(&encoded).into_owned()
}

fn write_private_file(path: &str, data: &[u8]) -> Result<(), String> {
    let base = path.rsplit('/').next().unwrap_or(path);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |elapsed| elapsed.subsec_nanos());
    let temp = paths::join(
        &paths::dir(path),
        &format!(".{base}.tmp-{}{nanos}", std::process::id()),
    );
    let describe =
        |op: &str, error: &std::io::Error| format!("{op} {temp}: {}", os_error_text(error));
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(PRIVATE_FILE_MODE)
        .open(&temp)
        .map_err(|error| describe("open", &error))?;
    let written = file
        .write_all(data)
        .map_err(|error| describe("write", &error))
        .and_then(|()| {
            std::fs::rename(&temp, path)
                .map_err(|error| format!("rename {temp} {path}: {}", os_error_text(&error)))
        });
    if written.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    written
}

fn require(valid: bool, command: &str, message: &str) -> Result<(), Stop> {
    if valid {
        return Ok(());
    }
    eprintln!("attn browser {command}: {message}");
    Err(Stop(1))
}

fn save(command: &str, kind: &str, data: &str, path: &str) -> Result<(), Stop> {
    let fail = |message: String| {
        eprintln!("browser {command}: {message}");
        Stop(1)
    };
    let bytes = base64::decode(data).map_err(|error| fail(format!("decode {kind}: {error}")))?;
    let absolute_path =
        absolute(path).map_err(|error| fail(format!("resolve path: {}", os_error_text(&error))))?;
    write_private_file(&absolute_path, &bytes)
        .map_err(|error| fail(format!("write {absolute_path}: {error}")))?;
    print_stdout(&format!("{absolute_path}\n"));
    Ok(())
}

struct Browser<'c, 'r, 'f> {
    run: &'c mut Run<'r>,
    flags: &'f FlagSet<'f>,
    positionals: Vec<String>,
    session: Option<String>,
}

impl Browser<'_, '_, '_> {
    fn string(&self, name: &str) -> String {
        self.flags.string(name).to_string()
    }

    fn trimmed(&self, name: &str) -> String {
        self.flags.string(name).trim().to_string()
    }

    fn control(
        &mut self,
        label: &str,
        action: &str,
        params: Option<String>,
        selector: &str,
        text: &str,
    ) -> Result<String, Stop> {
        let mut message = BrowserControlMessage::new();
        message.action = action.to_string();
        message.params = params;
        message.selector = (!selector.is_empty()).then(|| selector.to_string());
        message.text = (!text.is_empty() || action == "type").then(|| text.to_string());
        message.session_id.clone_from(&self.session);
        let reply = client(self.run)
            .call(&message)
            .map_err(|error| self.run.fail_send(&format!("browser {label}"), &error))?;
        Ok(reply.optional_string("data"))
    }

    fn print(&mut self, label: &str, action: &str, params: Option<String>) -> Result<(), Stop> {
        let data = self.control(label, action, params, "", "")?;
        print_stdout(&format!("{data}\n"));
        Ok(())
    }

    fn open(&mut self) -> Result<(), Stop> {
        require(
            self.positionals.len() == 1,
            "open",
            "expected exactly one <url>",
        )?;
        let url = self.positionals[0].clone();
        let mut message = OpenBrowserMessage::new();
        message.url.clone_from(&url);
        message.session_id.clone_from(&self.session);
        client(self.run)
            .send(&message)
            .map_err(|error| self.run.fail_send("browser open", &error))?;
        print_stdout(&format!("opened {url}\n"));
        Ok(())
    }

    fn locator_params(&self, command: &str) -> Result<Map<String, Value>, Stop> {
        require(
            self.positionals.is_empty()
                && !self.trimmed("using").is_empty()
                && !self.trimmed("value").is_empty(),
            command,
            "--using and --value are required",
        )?;
        let mut params = Map::new();
        params.insert("using".to_string(), json!(self.string("using")));
        params.insert("value".to_string(), json!(self.string("value")));
        let name = self.string("name");
        if !name.is_empty() {
            params.insert("name".to_string(), json!(name));
        }
        Ok(params)
    }

    fn find(&mut self) -> Result<(), Stop> {
        let params = self.locator_params("find")?;
        let action = if self.flags.bool("all") {
            "find_elements"
        } else {
            "find_element"
        };
        self.print("find", action, Some(encode_params(&Value::Object(params))))
    }

    fn wait(&mut self) -> Result<(), Stop> {
        let mut params = self.locator_params("wait")?;
        params.insert("state".to_string(), json!(self.string("state")));
        params.insert("timeout".to_string(), json!(self.flags.int("timeout")));
        self.print(
            "wait",
            "wait_for",
            Some(encode_params(&Value::Object(params))),
        )
    }

    fn has_target(&self) -> bool {
        !self.trimmed("selector").is_empty() || !self.trimmed("element").is_empty()
    }

    fn click(&mut self) -> Result<(), Stop> {
        require(
            self.positionals.is_empty() && self.has_target(),
            "click",
            "--selector <css> or --element <id> is required",
        )?;
        if self.trimmed("element").is_empty() {
            let selector = self.trimmed("selector");
            let data = self.control("click", "click", None, &selector, "")?;
            print_stdout(&format!("{data}\n"));
            return Ok(());
        }
        let params = encode_params(&json!({"element": self.string("element")}));
        self.print("click", "click_element", Some(params))
    }

    fn type_text(&mut self) -> Result<(), Stop> {
        require(
            self.positionals.is_empty() && self.has_target() && self.flags.is_set("text"),
            "type",
            "--selector <css> or --element <id>, plus --text, are required",
        )?;
        let text = self.string("text");
        if self.trimmed("element").is_empty() {
            let selector = self.trimmed("selector");
            let data = self.control("type", "type", None, &selector, &text)?;
            print_stdout(&format!("{data}\n"));
            return Ok(());
        }
        let element = self.string("element");
        let clear = encode_params(&json!({"element": element}));
        self.control("type", "clear_element", Some(clear), "", "")?;
        let send = encode_params(&json!({"element": element, "text": text}));
        self.print("type", "send_keys_to_element", Some(send))
    }

    fn no_arguments(&mut self, label: &str, action: &str) -> Result<(), Stop> {
        require(self.positionals.is_empty(), label, "unexpected arguments")?;
        self.print(label, action, None)
    }

    fn press(&mut self) -> Result<(), Stop> {
        let text = self.string("text");
        require(
            self.positionals.is_empty() && self.flags.is_set("text") && !text.is_empty(),
            "press",
            "--text <key> is required",
        )?;
        let params = json!({"actions": [{"type": "key", "id": "keyboard", "actions": [
            {"type": "keyDown", "value": text},
            {"type": "keyUp", "value": text},
        ]}]});
        self.print("press", "perform_actions", Some(encode_params(&params)))
    }

    fn scroll(&mut self) -> Result<(), Stop> {
        require(
            self.positionals.is_empty(),
            "scroll",
            "unexpected arguments",
        )?;
        let params = json!({"actions": [{"type": "wheel", "id": "wheel", "actions": [
            {"type": "scroll", "deltaX": self.flags.int("x"), "deltaY": self.flags.int("y")},
        ]}]});
        self.print("scroll", "perform_actions", Some(encode_params(&params)))
    }

    fn params_json(&self, command: &str) -> Result<String, Stop> {
        let params = self.string("params");
        require(
            serde_json::from_str::<Map<String, Value>>(&params).is_ok(),
            command,
            "--params must be a JSON object",
        )?;
        Ok(params)
    }

    fn raw_command(&mut self) -> Result<(), Stop> {
        require(
            self.positionals.len() == 1,
            "command",
            "expected exactly one <action>",
        )?;
        let params = self.params_json("command")?;
        let action = self.positionals[0].clone();
        self.print("command", &action, Some(params))
    }

    fn output_path(&self, command: &str, default: &str) -> Result<String, Stop> {
        require(
            self.positionals.len() <= 1,
            command,
            "expected at most one [path]",
        )?;
        Ok(self
            .positionals
            .first()
            .map_or_else(|| default.to_string(), Clone::clone))
    }

    fn screenshot(&mut self) -> Result<(), Stop> {
        let path = self.output_path("screenshot", "attn-browser.png")?;
        let data = self.control("screenshot", "screenshot", None, "", "")?;
        save("screenshot", "PNG", &data, &path)
    }

    fn pdf(&mut self) -> Result<(), Stop> {
        let path = self.output_path("pdf", "attn-browser.pdf")?;
        let params = self.params_json("pdf")?;
        let data = self.control("pdf", "print_page", Some(params), "", "")?;
        save("pdf", "PDF", &data, &path)
    }

    fn dispatch(&mut self, command: &str) -> Result<(), Stop> {
        match command {
            "open" => self.open(),
            "snapshot" => self.no_arguments("snapshot", "snapshot"),
            "find" => self.find(),
            "wait" => self.wait(),
            "click" => self.click(),
            "type" => self.type_text(),
            "reload" | "back" | "forward" => self.no_arguments("reload", command),
            "press" => self.press(),
            "scroll" => self.scroll(),
            "cookies" => self.print("cookies", "get_all_cookies", Some("{}".to_string())),
            "command" => self.raw_command(),
            "screenshot" => self.screenshot(),
            _ => self.pdf(),
        }
    }
}

const fn int_flag(name: &'static str, default: i64) -> Flag<'static> {
    Flag {
        name,
        default: FlagDefault::Int(default),
        usage: "",
    }
}

const fn text_flag(name: &'static str, default: &'static str) -> Flag<'static> {
    Flag {
        name,
        default: FlagDefault::String(default),
        usage: "",
    }
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(command) = args.first() else {
        eprint!("{USAGE}");
        return Err(Stop(1));
    };
    let definitions = [
        string_flag("session"),
        string_flag("selector"),
        string_flag("text"),
        text_flag("params", "{}"),
        string_flag("using"),
        string_flag("value"),
        string_flag("name"),
        string_flag("element"),
        text_flag("state", "attached"),
        int_flag("timeout", DEFAULT_WAIT_TIMEOUT_MS),
        bool_flag("all"),
        int_flag("x", 0),
        int_flag("y", 0),
    ];
    let mut flags = FlagSet::new("browser", &definitions);
    let positionals = parse_interspersed(run, &mut flags, &args[1..]).map_err(|error| {
        eprint!("attn browser {command}: {error}\n{USAGE}");
        Stop(1)
    })?;
    let mut session = flags.string("session").trim().to_string();
    if session.is_empty() {
        session = current_session_or_exit(run)?;
    }
    if !KNOWN_COMMANDS.contains(&command.as_str()) {
        run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
        eprint!("unknown browser command: {command}\n{USAGE}");
        return Err(Stop(1));
    }
    run.path = format!("browser {command}");
    let mut browser = Browser {
        run,
        flags: &flags,
        positionals,
        session: (!session.is_empty()).then_some(session),
    };
    browser.dispatch(command)
}
