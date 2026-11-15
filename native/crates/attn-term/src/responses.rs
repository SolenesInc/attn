//! Which of the terminal's replies the app forwards, from `app/src/utils/terminalQueryResponses.ts`.

const ESC: u8 = 0x1b;
const BEL: u8 = 0x07;

/// The replies left to send once those the daemon's worker answers itself are gone: cursor
/// position reports, primary device attributes and OSC 10/11/12 colours. A duplicate of one
/// reaches the shell as stray input.
pub fn without_daemon_owned(reply: &[u8]) -> Vec<u8> {
    let mut kept = Vec::with_capacity(reply.len());
    let mut at = 0;
    while at < reply.len() {
        match daemon_owned_len(&reply[at..]) {
            Some(len) => at += len,
            None => {
                kept.push(reply[at]);
                at += 1;
            }
        }
    }
    kept
}

fn daemon_owned_len(bytes: &[u8]) -> Option<usize> {
    match bytes {
        [ESC, b'[', rest @ ..] => csi_report_len(rest).map(|len| 2 + len),
        [ESC, b']', b'1', b'0' | b'1' | b'2', b';', rest @ ..] => {
            osc_body_len(rest).map(|len| 5 + len)
        }
        _ => None,
    }
}

/// `?? n;n(;n)? R` or `? [0-9;]* c` after the CSI.
fn csi_report_len(rest: &[u8]) -> Option<usize> {
    let private = rest.first() == Some(&b'?');
    let start = usize::from(private);
    let params = rest[start..]
        .iter()
        .take_while(|b| b.is_ascii_digit() || **b == b';')
        .count();
    let end = start + params;
    let fields = rest[start..end].split(|b| *b == b';');
    match rest.get(end) {
        Some(b'R') => {
            let numbers: Vec<&[u8]> = fields.collect();
            let well_formed =
                (2..=3).contains(&numbers.len()) && numbers.iter().all(|n| !n.is_empty());
            well_formed.then_some(end + 1)
        }
        Some(b'c') if private => Some(end + 1),
        _ => None,
    }
}

fn osc_body_len(rest: &[u8]) -> Option<usize> {
    for (i, b) in rest.iter().enumerate() {
        match *b {
            BEL => return Some(i + 1),
            ESC => return (rest.get(i + 1) == Some(&b'\\')).then_some(i + 2),
            _ => {}
        }
    }
    None
}
