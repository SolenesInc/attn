package fakeagent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"
)

const bracketedPasteOn = "\x1b[?2004h"

type composer struct {
	prompt string
	footer string
}

type terminal struct {
	style composer
	mu    sync.Mutex
	line  []rune
}

func openTerminal(style composer) (*terminal, error) {
	raw := exec.Command("stty", "raw", "-echo")
	raw.Stdin = os.Stdin
	if out, err := raw.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stty raw -echo: %v: %s", err, out)
	}
	t := &terminal{style: style}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(bracketedPasteOn + t.composer())
	return t, nil
}

func (t *terminal) title(title string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write("\x1b]0;" + title + "\x07")
}

func (t *terminal) echo(prompt string) {
	t.print(t.style.prompt + prompt)
}

func (t *terminal) print(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write("\r\x1b[J" + onScreen(text) + "\r\n" + t.composer())
}

func (t *terminal) composer() string {
	line := t.style.prompt + string(t.line)
	if t.style.footer == "" {
		return line
	}
	return line + "\r\n" + t.style.footer + "\x1b[A\r" + line
}

func (t *terminal) write(s string) {
	_, _ = os.Stdout.WriteString(s)
}

func onScreen(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
}

func (t *terminal) readLines(submit func(string)) {
	var pending []byte
	chunk := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(chunk)
		pending = append(pending, chunk[:n]...)
		pending = t.consume(pending, submit)
		if err != nil {
			return
		}
	}
}

func (t *terminal) consume(input []byte, submit func(string)) []byte {
	for len(input) > 0 {
		switch {
		case input[0] == 0x1b:
			size := escapeLength(input)
			if size == 0 {
				return input
			}
			input = input[size:]
		case input[0] == '\r' || input[0] == '\n':
			if prompt := t.take(); strings.TrimSpace(prompt) != "" {
				submit(prompt)
			}
			input = input[1:]
		case input[0] < 0x20:
			input = input[1:]
		default:
			if !utf8.FullRune(input) {
				return input
			}
			r, size := utf8.DecodeRune(input)
			t.insert(string(r))
			input = input[size:]
		}
	}
	return input
}

func escapeLength(input []byte) int {
	if len(input) < 2 {
		return 0
	}
	switch input[1] {
	case '[':
		for i := 2; i < len(input); i++ {
			if input[i] >= 0x40 && input[i] <= 0x7e {
				return i + 1
			}
		}
		return 0
	case ']':
		for i := 2; i < len(input); i++ {
			if input[i] == 0x07 {
				return i + 1
			}
			if input[i] == '\\' && input[i-1] == 0x1b {
				return i + 1
			}
		}
		return 0
	default:
		return 2
	}
}

func (t *terminal) insert(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.line = append(t.line, []rune(text)...)
	t.write(onScreen(text))
}

func (t *terminal) take() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	prompt := string(t.line)
	t.line = nil
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	t.write("\r\x1b[J" + onScreen(t.style.prompt+prompt) + "\r\n" + t.composer())
	return prompt
}
