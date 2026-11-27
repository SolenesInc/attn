package document

import (
	"fmt"
	"strings"
)

type LineError struct {
	Line    int
	Message string
}

func (e *LineError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Message) }

type Block struct {
	Line   int
	Opener string
	Lines  []string
}

func (b Block) IsPart() bool { return b.Opener != "" }

func (b Block) Text() string { return strings.Trim(strings.Join(b.Lines, "\n"), "\n") }

const (
	partFence      = ":::"
	containerFence = "::::"
)

type Syntax struct {
	Opens      func(trimmed string) bool
	Containers bool
}

type Refusal int

const (
	Unclosed Refusal = iota + 1
	StrayCloser
	StrayDirective
)

type LexError struct {
	Line      int
	Refusal   Refusal
	Directive string
}

func (s Syntax) Blocks(markdown string) ([]Block, *LexError) {
	var blocks []Block
	var prose Block
	flushProse := func() {
		if strings.TrimSpace(prose.Text()) != "" {
			blocks = append(blocks, prose)
		}
		prose = Block{}
	}
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var fence Fence
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		opens := s.Opens(trimmed)
		if !fence.Open() && !opens && isDirective(trimmed) {
			return blocks, s.stray(trimmed, i+1)
		}
		if fence.Open() || !opens {
			fence.Track(line)
			if len(prose.Lines) == 0 {
				prose.Line = i + 1
			}
			prose.Lines = append(prose.Lines, line)
			continue
		}
		end, err := s.partEnd(lines, i)
		if err != nil {
			return blocks, err
		}
		flushProse()
		blocks = append(blocks, Block{Line: i + 1, Opener: trimmed, Lines: lines[i+1 : end]})
		i = end
	}
	flushProse()
	return blocks, nil
}

func (s Syntax) isContainer(opener string) bool {
	return s.Containers && strings.HasPrefix(opener, containerFence)
}

func (s Syntax) partEnd(lines []string, open int) (int, *LexError) {
	opener := strings.TrimSpace(lines[open])
	closer, container := partFence, s.isContainer(opener)
	if container {
		closer = containerFence
	}
	var fence Fence
	for j := open + 1; j < len(lines); j++ {
		if !fence.Open() {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == closer {
				return j, nil
			}
			if s.Opens(trimmed) && (!container || s.isContainer(trimmed)) {
				break
			}
		}
		fence.Track(lines[j])
	}
	return 0, &LexError{Line: open + 1, Refusal: Unclosed, Directive: opener}
}

func (s Syntax) stray(directive string, line int) *LexError {
	if directive == partFence || (s.Containers && directive == containerFence) {
		return &LexError{Line: line, Refusal: StrayCloser, Directive: directive}
	}
	return &LexError{Line: line, Refusal: StrayDirective, Directive: directive}
}

func isDirective(trimmed string) bool {
	return strings.HasPrefix(trimmed, partFence)
}

var questionSyntax = Syntax{Opens: func(trimmed string) bool {
	_, ok := questionKinds[trimmed]
	return ok
}}

func QuestionBlocks(markdown string) ([]Block, error) {
	blocks, refused := questionSyntax.Blocks(markdown)
	if refused == nil {
		return blocks, nil
	}
	if refused.Refusal == Unclosed {
		return blocks, &LineError{Line: refused.Line, Message: "question block has no closing :::"}
	}
	return blocks, strayDirective(refused.Directive, refused.Line)
}

func strayDirective(directive string, line int) *LineError {
	if directive == partFence {
		return &LineError{Line: line, Message: "::: closes no question block"}
	}
	return &LineError{Line: line, Message: fmt.Sprintf("%q opens no question block; use :::question, :::question-multi, :::question-text or :::question-file", directive)}
}

type Fence struct {
	marker string
}

func (f *Fence) Open() bool { return f.marker != "" }

func (f *Fence) Track(line string) {
	trimmed := strings.TrimSpace(line)
	if f.marker != "" {
		if strings.HasPrefix(trimmed, f.marker) && strings.Trim(trimmed, f.marker[:1]) == "" {
			f.marker = ""
		}
		return
	}
	for _, char := range []string{"`", "~"} {
		run := len(trimmed) - len(strings.TrimLeft(trimmed, char))
		if run >= 3 {
			f.marker = strings.Repeat(char, run)
			return
		}
	}
}
