pub fn safe_boundary(data: &[u8]) -> usize {
    if data.is_empty() {
        return 0;
    }
    let mut boundary = utf8_boundary(data);
    if let Some(start) = incomplete_escape_start(&data[..boundary]) {
        boundary = boundary.min(start);
    }
    boundary
}

fn utf8_boundary(data: &[u8]) -> usize {
    let start = data.len().saturating_sub(4);
    for index in (start..data.len()).rev() {
        let byte = data[index];
        if byte & 0b1100_0000 == 0b1000_0000 {
            continue;
        }
        let width = match byte {
            0xc2..=0xdf => 2,
            0xe0..=0xef => 3,
            0xf0..=0xf4 => 4,
            _ => 1,
        };
        if data.len() - index < width {
            return index;
        }
        break;
    }
    data.len()
}

fn incomplete_escape_start(data: &[u8]) -> Option<usize> {
    let start = data.len().saturating_sub(64);
    for index in (start..data.len()).rev() {
        if data[index] != 0x1b {
            continue;
        }
        return (!escape_complete(&data[index..])).then_some(index);
    }
    None
}

fn escape_complete(sequence: &[u8]) -> bool {
    if sequence.first() != Some(&0x1b) {
        return true;
    }
    let Some(next) = sequence.get(1).copied() else {
        return false;
    };
    match next {
        b'[' => sequence[2..].iter().any(|byte| matches!(byte, 0x40..=0x7e)),
        b']' => sequence[2..].iter().enumerate().any(|(index, byte)| {
            *byte == 0x07 || (*byte == 0x1b && sequence.get(index + 3) == Some(&b'\\'))
        }),
        b'P' | b'^' | b'_' => sequence[2..].windows(2).any(|pair| pair == b"\x1b\\"),
        0x20..=0x2f => sequence
            .get(2)
            .is_some_and(|byte| matches!(byte, 0x30..=0x7e)),
        _ => true,
    }
}

#[cfg(test)]
mod tests {
    use super::safe_boundary;

    #[test]
    fn holds_split_utf8_and_terminal_queries() {
        assert_eq!(safe_boundary(b"a\xe2\x82"), 1);
        assert_eq!(safe_boundary(b"abc\x1b[6"), 3);
        assert_eq!(safe_boundary(b"abc\x1b[6n"), 7);
    }
}
