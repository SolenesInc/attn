package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
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
	args, err := mentionArgs(c, cmds[0], rest)
	if err != nil {
		return "", fmt.Errorf("%q: %w", line, err)
	}
	shell := slices.ContainsFunc(args, func(arg mentionArg) bool { return arg.cliOnly })
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
	if len(args) == 0 {
		return tools, nil
	}
	return tools + " with " + jsonArgs(args), nil
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
	name    string
	value   any
	choice  string
	written string
	cliOnly bool
}

func mentionArgs(c *Catalog, cmd *Command, words []string) ([]mentionArg, error) {
	var positional []*Arg
	for i := range cmd.Args {
		switch cmd.Args[i].CLI {
		case "pos", "rest", "words":
			positional = append(positional, &cmd.Args[i])
		}
	}
	var out []mentionArg
	add := func(arg *Arg, value any, choice string, written []string) error {
		given := mentionArg{name: arg.Name, value: value, choice: choice, written: strings.Join(written, " "), cliOnly: arg.CLIOnly}
		for i := range out {
			if out[i].name == arg.Name {
				return out[i].addRepeat(cmd, given)
			}
		}
		out = append(out, given)
		return nil
	}
	words, stdin := withoutRedirect(words)
	for i := 0; i < len(words); i++ {
		start, word := i, words[i]
		if word == "--json" && cmd.Result != "" {
			continue
		}
		if isFlag(word) {
			arg, choice := flagArg(cmd, word)
			if arg == nil {
				return nil, fmt.Errorf("%s takes no %s", cmd.Name, word)
			}
			var value any = "…"
			if c.takesObject(*arg) {
				value = objectSlot("{…}")
			}
			switch {
			case choice != "":
				value = choice
			case c.ArgType(*arg).Schema["type"] == "boolean":
				value = true
			case arg.FlagWords() != "":
				var list []string
				for i+1 < len(words) && !isFlag(words[i+1]) {
					i++
					list = append(list, words[i])
				}
				value = list
			case i+1 < len(words) && !isFlag(words[i+1]):
				i++
				value = mentionValue(c, *arg, words[i])
				if words[i] == "-" && arg.Stdin {
					value = c.slot(*arg, "text")
					if stdin != "" {
						value = c.slot(*arg, contentsOf(stdin))
					}
				}
			}
			if err := add(arg, value, choice, words[start:i+1]); err != nil {
				return nil, err
			}
			continue
		}
		if len(positional) == 0 {
			return nil, fmt.Errorf("%s takes no more positional words, got %q", cmd.Name, word)
		}
		positional = skipUnfilled(c, cmd, positional, word, positionalWords(words[i:]))
		arg := positional[0]
		if arg.CLI == "pos" {
			positional = positional[1:]
			if err := add(arg, mentionValue(c, *arg, word), "", words[start:i+1]); err != nil {
				return nil, err
			}
			continue
		}
		positional = positional[1:]
		var list []string
		for ; i < len(words) && !isFlag(words[i]); i++ {
			list = append(list, words[i])
		}
		i--
		var value any = list
		if !arg.List && c.ArgType(*arg).Schema["type"] != "array" {
			value = strings.Join(list, " ")
		}
		if err := add(arg, value, "", words[start:i+1]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (arg *mentionArg) addRepeat(cmd *Command, again mentionArg) error {
	list, isList := arg.value.([]string)
	more, moreIsList := again.value.([]string)
	if !isList || !moreIsList {
		return fmt.Errorf("%s takes %s once", cmd.Name, arg.name)
	}
	arg.value = append(list, more...)
	arg.written += " " + again.written
	return nil
}

func jsonArgs(args []mentionArg) string {
	if len(args) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("{")
	for i, arg := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsonText(arg.name) + ": " + jsonText(arg.value))
	}
	b.WriteString("}")
	return b.String()
}

func withoutRedirect(words []string) ([]string, string) {
	if n := len(words); n > 1 && words[n-2] == "<" {
		return words[:n-2], words[n-1]
	}
	return words, ""
}

func positionalWords(words []string) int {
	count := 0
	for _, word := range words {
		if isFlag(word) {
			break
		}
		count++
	}
	return count
}

func skipUnfilled(c *Catalog, cmd *Command, positional []*Arg, word string, words int) []*Arg {
	first := positional[0]
	if partner, prefix, shared := c.SharedPositional(cmd, *first); shared && !namesSeed(word, prefix) {
		return positionalNamed(positional, partner)
	} else if len(positional) > 1 && !shared {
		if _, prefix, seedSide := c.SharedPositional(cmd, *positional[1]); seedSide && namesSeed(word, prefix) {
			return positional[1:]
		}
	}
	required := 0
	for _, arg := range positional {
		if arg.Required {
			required++
		}
	}
	if !first.Required && required > 0 && words <= required {
		return positional[1:]
	}
	return positional
}

func positionalNamed(positional []*Arg, name string) []*Arg {
	for i, arg := range positional {
		if arg.Name == name {
			return positional[i:]
		}
	}
	return positional
}

func namesSeed(word, prefix string) bool {
	return strings.HasPrefix(word, prefix) || word == "<plot>" || word == "<seed>" || word == "<seed-id>"
}

type objectSlot string

func (c *Catalog) takesObject(arg Arg) bool { return c.ArgType(arg).Schema["type"] == "object" }

func (c *Catalog) slot(arg Arg, content string) any {
	if c.takesObject(arg) {
		return objectSlot("<" + content + ", a JSON object>")
	}
	return "<" + content + ">"
}

func contentsOf(file any) string { return fmt.Sprintf("the contents of %v", file) }

func isFlag(word string) bool { return strings.HasPrefix(word, "-") && word != "-" }

func flagArg(cmd *Command, flag string) (*Arg, string) {
	for i := range cmd.Args {
		arg := &cmd.Args[i]
		cli := strings.TrimSuffix(arg.CLI, "...")
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
	if output := shellOutput().FindStringSubmatch(word); output != nil {
		return c.slot(arg, "the output of "+output[1])
	}
	if arg.File && word != "-" {
		return c.slot(arg, contentsOf(word))
	}
	if c.takesObject(arg) {
		return c.slot(arg, "text")
	}
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
	if slot, ok := v.(objectSlot); ok {
		return string(slot)
	}
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
		case line[i] == '"' || line[i] == '\'':
			end := strings.IndexByte(line[i+1:], line[i])
			if end < 0 {
				return nil, fmt.Errorf("%q has an unclosed quote", line)
			}
			words = append(words, line[i+1:i+1+end])
			i += end + 2
		case line[i] == '<' && i+1 < len(line) && line[i+1] != ' ':
			start := i
			for i < len(line) && line[i] != ' ' {
				if line[i] != '<' {
					i++
					continue
				}
				end := strings.IndexByte(line[i:], '>')
				if end < 0 {
					return nil, fmt.Errorf("%q has an unclosed <placeholder>", line)
				}
				i += end + 1
			}
			words = append(words, line[start:i])
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
