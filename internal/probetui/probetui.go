// No wall-clock time, randomness, or PID may leak into the output, or a captured
// transcript stops being reproducible.
package probetui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type Style string

const (
	StyleCodex  Style = "codex"
	StyleClaude Style = "claude"
)

func ParseStyle(s string) (Style, error) {
	switch Style(s) {
	case StyleCodex:
		return StyleCodex, nil
	case StyleClaude:
		return StyleClaude, nil
	default:
		return "", fmt.Errorf("probetui: unknown style %q (want %q or %q)", s, StyleCodex, StyleClaude)
	}
}

func Startup(style Style, cols, rows int) []byte {
	switch style {
	case StyleCodex:
		return startupCodex(cols, rows)
	case StyleClaude:
		return startupClaude(cols, rows)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

func Frame(style Style, cols, rows int, seq int) []byte {
	switch style {
	case StyleCodex:
		return frameCodex(cols, rows, seq)
	case StyleClaude:
		return frameClaude(cols, rows, seq)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

func OnResize(style Style, cols, rows int) []byte {
	switch style {
	case StyleCodex:
		return onResizeCodex(cols, rows)
	case StyleClaude:
		return onResizeClaude(cols, rows)
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

func Teardown(style Style) []byte {
	switch style {
	case StyleCodex:
		return teardownCodex()
	case StyleClaude:
		return teardownClaude()
	default:
		panic(fmt.Sprintf("probetui: unknown style %q", style))
	}
}

func Run(
	ctx context.Context,
	w io.Writer,
	style Style,
	size func() (cols int, rows int, err error),
	winch <-chan os.Signal,
	interval time.Duration,
) error {
	cols, rows, err := size()
	if err != nil {
		return fmt.Errorf("probetui: initial size: %w", err)
	}

	if _, err := w.Write(Startup(style, cols, rows)); err != nil {
		return err
	}

	seq := 0
	writeFrame := func() error {
		seq++
		_, err := w.Write(Frame(style, cols, rows, seq))
		return err
	}
	if err := writeFrame(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_, err := w.Write(Teardown(style))
			return err

		case <-winch:
			newCols, newRows, err := size()
			if err != nil {
				continue
			}
			if newCols == cols && newRows == rows {
				continue
			}
			cols, rows = newCols, newRows
			if _, err := w.Write(OnResize(style, cols, rows)); err != nil {
				return err
			}
			if err := writeFrame(); err != nil {
				return err
			}

		case <-ticker.C:
			if err := writeFrame(); err != nil {
				return err
			}
		}
	}
}

func csi(params string, final byte) []byte {
	return []byte("\x1b[" + params + string(final))
}

func privateMode(set bool, modes ...string) []byte {
	final := byte('l')
	if set {
		final = 'h'
	}
	params := "?"
	for i, m := range modes {
		if i > 0 {
			params += ";"
		}
		params += m
	}
	return csi(params, final)
}

func cup(row, col int) []byte { return csi(fmt.Sprintf("%d;%d", row, col), 'H') }
func el() []byte              { return csi("", 'K') }
func ed(n int) []byte {
	if n == 0 {
		return csi("", 'J')
	}
	return csi(fmt.Sprintf("%d", n), 'J')
}
func sgr(params string) []byte { return csi(params, 'm') }
func decstbm(top, bottom int) []byte {
	if top == 0 && bottom == 0 {
		return csi("", 'r')
	}
	return csi(fmt.Sprintf("%d;%d", top, bottom), 'r')
}
func da1Query() []byte { return csi("", 'c') }
func cprQuery() []byte { return csi("6", 'n') }

func osc(code, data string) []byte {
	return []byte("\x1b]" + code + ";" + data + "\x1b\\")
}

func oscColorQuery(code string) []byte { return osc(code, "?") }

func hyperlink(url, text string) []byte {
	var b bytes.Buffer
	b.WriteString("\x1b]8;;" + url + "\x1b\\")
	b.WriteString(text)
	b.WriteString("\x1b]8;;\x1b\\")
	return b.Bytes()
}

func bannerGeometryRow(cols, rows int) string {
	return fmt.Sprintf("ATTN-PROBE %dx%d", cols, rows)
}

func bannerStyleRow(style Style, seq int) string {
	return fmt.Sprintf("style=%s seq=%d READY", style, seq)
}

func fillRow(cols, rows, seq, row int) string {
	ch := byte('#')
	if (row+seq+rows)%2 == 1 {
		ch = '.'
	}
	if cols <= 0 {
		return ""
	}
	b := make([]byte, cols)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// Assumes single-width ASCII, the only content probetui renders.
func truncateToWidth(s string, cols int) string {
	if cols <= 0 {
		return ""
	}
	if len(s) <= cols {
		return s
	}
	return s[:cols]
}
