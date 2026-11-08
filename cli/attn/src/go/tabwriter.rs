const PADDING: usize = 2;

struct Cell<'a> {
    text: &'a str,
    width: usize,
}

impl<'a> Cell<'a> {
    fn new(text: &'a str) -> Self {
        Self {
            text,
            width: text.chars().count(),
        }
    }
}

pub fn format(input: &str) -> String {
    let mut out = String::with_capacity(input.len());
    let mut lines: Vec<Vec<Cell>> = vec![Vec::new()];
    let mut cell_start = 0;
    for (index, byte) in input.bytes().enumerate() {
        if !matches!(byte, b'\t' | 0x0b | b'\n' | 0x0c) {
            continue;
        }
        let line = lines.last_mut().expect("lines is never empty");
        line.push(Cell::new(&input[cell_start..index]));
        let cells_on_line = line.len();
        cell_start = index + 1;
        if matches!(byte, b'\n' | 0x0c) {
            lines.push(Vec::new());
            if byte == 0x0c || cells_on_line == 1 {
                write_block(&mut out, &lines);
                lines = vec![Vec::new()];
            }
        }
    }
    if cell_start < input.len() {
        lines
            .last_mut()
            .expect("lines is never empty")
            .push(Cell::new(&input[cell_start..]));
    }
    write_block(&mut out, &lines);
    out
}

fn write_block(out: &mut String, lines: &[Vec<Cell>]) {
    write_columns(out, lines, &mut Vec::new(), 0, lines.len());
}

fn write_columns(
    out: &mut String,
    lines: &[Vec<Cell>],
    widths: &mut Vec<usize>,
    mut first: usize,
    end: usize,
) {
    let column = widths.len();
    let has_cell_to_align = |line: usize| column + 1 < lines[line].len();
    let mut current = first;
    while current < end {
        if !has_cell_to_align(current) {
            current += 1;
            continue;
        }
        write_lines(out, lines, widths, first, current);
        first = current;
        let mut width = 0;
        while current < end && has_cell_to_align(current) {
            width = width.max(lines[current][column].width + PADDING);
            current += 1;
        }
        widths.push(width);
        write_columns(out, lines, widths, first, current);
        widths.pop();
        first = current;
        current += 1;
    }
    write_lines(out, lines, widths, first, end);
}

fn write_lines(out: &mut String, lines: &[Vec<Cell>], widths: &[usize], first: usize, end: usize) {
    for (index, line) in lines.iter().enumerate().take(end).skip(first) {
        for (column, cell) in line.iter().enumerate() {
            out.push_str(cell.text);
            if let Some(width) = widths.get(column) {
                out.extend(std::iter::repeat_n(' ', width.saturating_sub(cell.width)));
            }
        }
        if index + 1 != lines.len() {
            out.push('\n');
        }
    }
}
