package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

var argMention = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`\{([a-z_]+)\}`) })

var plainCommand = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`attn( [a-z_][a-z0-9_-]*)+`) })

func (c *Catalog) ToolText(text string) string {
	text = protocol.CommandMention().ReplaceAllStringFunc(text, func(mention string) string {
		line := protocol.CommandMention().FindStringSubmatch(mention)[1]
		if strings.HasPrefix(mention, "`") != strings.HasSuffix(mention, "`") {
			return protocol.CLIText(mention)
		}
		rendered, err := c.ToolMention(line)
		if err != nil {
			return shellCommand(line)
		}
		return rendered
	})
	return argMention().ReplaceAllString(text, "$1")
}

func (c *Catalog) MentionProblems(text string) []string {
	var problems []string
	if plain, ok := c.PlainCommand(text); ok {
		problems = append(problems, fmt.Sprintf("names %s in plain words; write it as {%s ...} so each surface renders it", plain, plain))
	}
	for _, mention := range protocol.CommandMention().FindAllStringSubmatch(text, -1) {
		if _, err := c.ToolMention(mention[1]); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return problems
}

func (c *Catalog) PlainCommand(text string) (string, bool) {
	text = protocol.CommandMention().ReplaceAllString(text, "")
	for _, match := range plainCommand().FindAllString(text, -1) {
		words := strings.Fields(match)[1:]
		for n := len(words); n > 0; n-- {
			if _, ok := c.Command(strings.Join(words[:n], " ")); ok {
				return "attn " + strings.Join(words[:n], " "), true
			}
		}
	}
	return "", false
}

func (c *Catalog) ToolMention(line string) (string, error) {
	words, err := mentionWords(line)
	if err != nil {
		return "", err
	}
	if len(words) < 2 || words[0] != "attn" {
		return "", fmt.Errorf("%q does not start with attn and a command", line)
	}
	cmds, rest := c.mentionedCommands(words[1:])
	if cmds == nil {
		if _, ok := c.Group(words[1]); ok {
			return shellCommand(line), nil
		}
		return "", fmt.Errorf("%q names no catalog command", line)
	}
	args, shell, err := mentionArgs(c, cmds[0], rest)
	if err != nil {
		return "", fmt.Errorf("%q: %w", line, err)
	}
	var names []string
	for _, cmd := range cmds {
		if !cmd.IsTool() {
			shell = true
		}
		names = append(names, "`"+cmd.ToolName()+"`")
	}
	if shell {
		return shellCommand(line), nil
	}
	tools := names[0]
	if len(names) > 1 {
		tools = strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
	if args == "" {
		return tools, nil
	}
	return tools + " with " + args, nil
}

func shellCommand(line string) string {
	return "the shell command `" + line + "`"
}

func (c *Catalog) mentionedCommands(words []string) ([]*Command, []string) {
	for n := len(words); n > 0; n-- {
		head := strings.Join(words[:n-1], " ")
		var cmds []*Command
		for _, verb := range strings.Split(words[n-1], "|") {
			name := strings.TrimSpace(head + " " + verb)
			cmd, ok := c.Command(name)
			if !ok {
				cmds = nil
				break
			}
			cmds = append(cmds, cmd)
		}
		if cmds != nil {
			return cmds, words[n:]
		}
	}
	return nil, nil
}

type mentionArg struct {
	name  string
	value any
}

func mentionArgs(c *Catalog, cmd *Command, words []string) (string, bool, error) {
	var positional []*Arg
	for i := range cmd.Args {
		switch cmd.Args[i].CLI {
		case "pos", "rest", "words":
			positional = append(positional, &cmd.Args[i])
		}
	}
	var out []mentionArg
	shell := false
	for i := 0; i < len(words); i++ {
		word := words[i]
		if word == "--json" && cmd.Result != "" {
			continue
		}
		if isFlag(word) {
			arg, choice := flagArg(cmd, word)
			if arg == nil {
				return "", false, fmt.Errorf("%s takes no %s", cmd.Name, word)
			}
			shell = shell || arg.CLIOnly
			var value any = "…"
			switch {
			case choice != "":
				value = choice
			case c.ArgType(*arg).Schema["type"] == "boolean":
				value = true
			case i+1 < len(words) && !isFlag(words[i+1]):
				i++
				value = mentionValue(c, *arg, words[i])
				if words[i] == "-" && arg.Stdin {
					value = "<text>"
					if i+2 < len(words) && words[i+1] == "<" {
						value = "<the contents of " + words[i+2] + ">"
						i += 2
					}
				}
			}
			out = append(out, mentionArg{arg.Name, value})
			continue
		}
		if len(positional) == 0 {
			return "", false, fmt.Errorf("%s takes no more positional words, got %q", cmd.Name, word)
		}
		arg := positional[0]
		shell = shell || arg.CLIOnly
		if arg.CLI == "pos" {
			positional = positional[1:]
			out = append(out, mentionArg{arg.Name, mentionValue(c, *arg, word)})
			continue
		}
		positional = positional[1:]
		var list []string
		for ; i < len(words) && !isFlag(words[i]); i++ {
			list = append(list, words[i])
		}
		i--
		out = append(out, mentionArg{arg.Name, list})
	}
	if len(out) == 0 {
		return "", shell, nil
	}
	var b bytes.Buffer
	b.WriteString("{")
	for i, arg := range out {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsonText(arg.name) + ": " + jsonText(arg.value))
	}
	b.WriteString("}")
	return b.String(), shell, nil
}

func isFlag(word string) bool { return strings.HasPrefix(word, "-") && word != "-" }

func flagArg(cmd *Command, flag string) (*Arg, string) {
	for i := range cmd.Args {
		arg := &cmd.Args[i]
		cli := arg.CLI
		if cli == "-m+" {
			cli = "-m"
		}
		if !strings.HasPrefix(cli, "-") {
			continue
		}
		for j, choice := range strings.Split(cli, "|") {
			if choice != flag {
				continue
			}
			if strings.Contains(cli, "|") {
				return arg, arg.Values[j]
			}
			return arg, ""
		}
	}
	return nil, ""
}

func mentionValue(c *Catalog, arg Arg, word string) any {
	if c.ArgType(arg).Schema["type"] == "integer" {
		if n, err := strconv.Atoi(word); err == nil {
			return n
		}
	}
	if arg.List {
		return []string{word}
	}
	return word
}

func jsonText(v any) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func mentionWords(line string) ([]string, error) {
	var words []string
	for i := 0; i < len(line); {
		switch {
		case line[i] == ' ':
			i++
		case line[i] == '"':
			end := strings.IndexByte(line[i+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("%q has an unclosed quote", line)
			}
			words = append(words, line[i+1:i+1+end])
			i += end + 2
		case line[i] == '<' && i+1 < len(line) && line[i+1] != ' ':
			end := strings.IndexByte(line[i:], '>')
			if end < 0 {
				return nil, fmt.Errorf("%q has an unclosed <placeholder>", line)
			}
			words = append(words, line[i:i+end+1])
			i += end + 1
		default:
			end := strings.IndexByte(line[i:], ' ')
			if end < 0 {
				end = len(line) - i
			}
			words = append(words, line[i:i+end])
			i += end
		}
	}
	return words, nil
}
